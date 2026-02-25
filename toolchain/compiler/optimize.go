package compiler

// optimizeLockUnlock walks a statement list, tracks execution mode, and
// removes redundant LockStmt nodes. It returns the optimized list and the
// mode after the last statement. The input mode is the known mode at entry
// (modeLocked for behavior top level).
func (p *parser) optimizeLockUnlock(stmts []Stmt, mode execMode) ([]Stmt, execMode) {
	out := make([]Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *LockStmt:
			targetMode := modeLocked
			if s.Unlock {
				targetMode = modeUnlocked
			}
			if mode == targetMode {
				continue // redundant — skip
			}
			mode = targetMode
			out = append(out, s)

		case *IfStmt:
			optimized, modeAfter := p.optimizeLockIf(s, mode)
			mode = modeAfter
			out = append(out, optimized)

		case *WhileStmt:
			optimized := p.optimizeLockWhile(s)
			mode = modeUnknown
			out = append(out, optimized)

		case *LoopStmt:
			optimized := p.optimizeLockLoop(s)
			mode = modeUnknown
			out = append(out, optimized)

		default:
			mode = p.stmtModeEffect(stmt, mode)
			out = append(out, stmt)
		}
	}
	return out, mode
}

// optimizeLockIf optimizes an IfStmt's branches and computes the mode after.
// Each branch starts at the input mode. The mode after the if is:
//   - If there's an else and all branches agree → that mode
//   - If there's no else and all branches match the input mode → input mode
//   - Otherwise → modeUnknown
func (p *parser) optimizeLockIf(s *IfStmt, mode execMode) (*IfStmt, execMode) {
	result := &IfStmt{
		Cond:    s.Cond,
		Comment: s.Comment,
	}

	// Optimize the main body.
	body, bodyMode := p.optimizeLockUnlock(s.Body, mode)
	result.Body = body

	// Optimize else-if branches.
	branchModes := []execMode{bodyMode}
	if len(s.ElseIfs) > 0 {
		result.ElseIfs = make([]ElseIfClause, len(s.ElseIfs))
		for i, ei := range s.ElseIfs {
			eiBody, eiMode := p.optimizeLockUnlock(ei.Body, mode)
			result.ElseIfs[i] = ElseIfClause{Cond: ei.Cond, Body: eiBody}
			branchModes = append(branchModes, eiMode)
		}
	}

	// Optimize else branch.
	if s.Else != nil {
		elseBody, elseMode := p.optimizeLockUnlock(s.Else, mode)
		result.Else = elseBody
		branchModes = append(branchModes, elseMode)
	}

	// Compute mode after.
	modeAfter := computeModeAfterIf(s.Else != nil, mode, branchModes)
	return result, modeAfter
}

// computeModeAfterIf determines the execution mode after an if statement
// based on whether all branches agree.
func computeModeAfterIf(hasElse bool, inputMode execMode, branchModes []execMode) execMode {
	if hasElse {
		// All branches (including else) must agree.
		agreed := branchModes[0]
		for _, m := range branchModes[1:] {
			if m != agreed {
				return modeUnknown
			}
		}
		return agreed
	}
	// No else: the only safe answer is the input mode if all branches
	// end at the input mode (the "didn't change" case). Otherwise unknown.
	for _, m := range branchModes {
		if m != inputMode {
			return modeUnknown
		}
	}
	return inputMode
}

// optimizeLockWhile optimizes the body of a while loop. The body starts
// at modeUnknown because the loop may iterate multiple times.
func (p *parser) optimizeLockWhile(s *WhileStmt) *WhileStmt {
	body, _ := p.optimizeLockUnlock(s.Body, modeUnknown)
	return &WhileStmt{
		Cond:    s.Cond,
		Body:    body,
		Comment: s.Comment,
	}
}

// optimizeLockLoop optimizes the body of an unconditional loop. The body
// starts at modeUnknown because the loop may iterate multiple times.
func (p *parser) optimizeLockLoop(s *LoopStmt) *LoopStmt {
	body, _ := p.optimizeLockUnlock(s.Body, modeUnknown)
	return &LoopStmt{
		Body:    body,
		Comment: s.Comment,
	}
}

// stmtModeEffect computes the mode after a non-control-flow statement.
// It checks whether the statement contains a function call and, if so,
// walks the function's AST body to determine mode effects.
func (p *parser) stmtModeEffect(stmt Stmt, mode execMode) execMode {
	var fnName string
	switch s := stmt.(type) {
	case *CallStmt:
		fnName = s.Name
	case *LetStmt:
		if ce, ok := s.Value.(*CallExpr); ok {
			fnName = ce.Name
		}
	case *AssignStmt:
		if ce, ok := s.Value.(*CallExpr); ok {
			fnName = ce.Name
		}
	case *MultiReturnStmt:
		if ce, ok := s.Value.(*CallExpr); ok {
			fnName = ce.Name
		}
	}
	if fnName == "" {
		return mode
	}
	fn := p.fns[fnName]
	if fn == nil || fn.astBody == nil {
		return mode
	}
	return p.fnModeEffect(fnName, mode, nil)
}

// fnModeEffect walks a function's AST body and computes the final mode.
// The visiting map prevents infinite recursion for (hypothetical) cycles.
func (p *parser) fnModeEffect(fnName string, mode execMode, visiting map[string]bool) execMode {
	if visiting != nil && visiting[fnName] {
		return modeUnknown // cycle guard
	}
	fn := p.fns[fnName]
	if fn == nil || fn.astBody == nil {
		return mode
	}
	if visiting == nil {
		visiting = map[string]bool{}
	}
	visiting[fnName] = true
	mode = p.stmtsModeEffect(fn.astBody, mode, visiting)
	delete(visiting, fnName)
	return mode
}

// stmtsModeEffect walks a statement list computing the final mode,
// accounting for lock/unlock and nested function calls.
func (p *parser) stmtsModeEffect(stmts []Stmt, mode execMode, visiting map[string]bool) execMode {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *LockStmt:
			if s.Unlock {
				mode = modeUnlocked
			} else {
				mode = modeLocked
			}
		case *IfStmt, *WhileStmt, *LoopStmt:
			mode = modeUnknown
		default:
			mode = p.stmtModeEffectWithVisiting(stmt, mode, visiting)
		}
	}
	return mode
}

// stmtModeEffectWithVisiting is like stmtModeEffect but threads the
// visiting map for recursion prevention.
func (p *parser) stmtModeEffectWithVisiting(stmt Stmt, mode execMode, visiting map[string]bool) execMode {
	var fnName string
	switch s := stmt.(type) {
	case *CallStmt:
		fnName = s.Name
	case *LetStmt:
		if ce, ok := s.Value.(*CallExpr); ok {
			fnName = ce.Name
		}
	case *AssignStmt:
		if ce, ok := s.Value.(*CallExpr); ok {
			fnName = ce.Name
		}
	case *MultiReturnStmt:
		if ce, ok := s.Value.(*CallExpr); ok {
			fnName = ce.Name
		}
	}
	if fnName == "" {
		return mode
	}
	fn := p.fns[fnName]
	if fn == nil || fn.astBody == nil {
		return mode
	}
	return p.fnModeEffect(fnName, mode, visiting)
}
