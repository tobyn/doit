package compiler

import (
	"fmt"
	"strconv"
)

// patchIterDoneSlot sets the "done" slot on an iterator instruction frame
// to point to target.
func patchIterDoneSlot(b *frameBuilder, instrIdx int, doneSlot string, target frameRef) {
	b.frames[instrIdx][doneSlot] = target
}

// patchUnlabeledBreakToLast replaces unlabeled @break placeholder frames
// in b.frames[from:] with a "last" instruction that stops an iterator.
// Used by iterator-backed for-loop emitters where unlabeled break means
// "stop iterating" rather than "jump past the loop".
func patchUnlabeledBreakToLast(b *frameBuilder, from int) {
	for j := from; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@break" {
			if label, _ := f["label"].(string); label == "" {
				b.frames[j] = map[string]any{"op": "last"}
			}
		}
	}
}

// emitForStmt emits a for-in loop.
func (p *parser) emitForStmt(s *ForStmt, ctx *emitContext, comment string) error {
	// Inline iterator instruction form
	if s.IterInstrFrame != nil {
		return p.emitInlineIterInstruction(s, ctx, comment)
	}

	// Iterator form: for vars in iter_name(args) { body }
	if s.IterName != "" {
		return p.emitForIterStmt(s, ctx, comment)
	}

	// Range form
	ctx.pushScope()
	iterVar := ctx.declareIterVar(s.IterVars[0])

	var err error
	ctor, isCtor := s.Range.(*ConstructorExpr)
	if isCtor && ctor.TypeName == "Range" {
		err = p.emitForStmtRange(s, ctor, iterVar, ctx, comment)
	} else {
		err = p.emitForStmtRuntime(s, iterVar, ctx, comment)
	}
	ctx.popScope()
	return err
}

// emitForIterStmt emits a for-in loop with an iterator source.
func (p *parser) emitForIterStmt(s *ForStmt, ctx *emitContext, comment string) error {
	it := p.iters[s.IterName]
	if it == nil {
		return p.errorf(s.Pos, "unknown iterator %q%s", s.IterName, suggest(s.IterName, collectKeys(p.iters)))
	}

	if it.frame != nil {
		return p.emitInstructionIter(s, it, ctx, comment)
	}
	if isStaticSequence(it.astBody) {
		return p.emitStateMachineIter(s, it, ctx, comment)
	}
	return p.emitYieldIter(s, it, ctx, comment)
}

// buildIterParamMap builds the parameter substitution map for an iterator's
// instruction frame. Resolves positional and keyword arguments from the
// for-loop call site, and maps output names to the caller's iter var
// registers (or allocates throwaway registers for extra outputs).
func (ctx *emitContext) buildIterParamMap(it *iterDef, iterArgs []Expr, iterKwArgs map[string]Expr, iterVarRegs []string) (map[string]any, error) {
	paramMap := map[string]any{}
	posIdx := 0
	for _, pd := range it.params {
		if pd.keyword == "" {
			if posIdx < len(iterArgs) {
				val, err := ctx.exprGetValue(iterArgs[posIdx], "")
				if err != nil {
					return nil, err
				}
				paramMap[pd.name] = val
			}
			posIdx++
		} else if iterKwArgs != nil {
			if expr, ok := iterKwArgs[pd.keyword]; ok {
				val, err := ctx.exprGetValue(expr, "")
				if err != nil {
					return nil, err
				}
				paramMap[pd.name] = val
			}
		}
	}
	for i, outName := range it.outputs {
		if i < len(iterVarRegs) {
			paramMap[outName] = iterVarRegs[i]
		} else {
			paramMap[outName] = allocUniqueVar("@out", ctx.usedVars)
		}
	}
	return paramMap, nil
}

// isStaticSequence reports whether every statement in stmts is a *YieldStmt.
func isStaticSequence(stmts []Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	for _, s := range stmts {
		if _, ok := s.(*YieldStmt); !ok {
			return false
		}
	}
	return true
}

// emitStateMachineIter emits a for-in loop for a static sequence iterator
// (body consists entirely of yield statements). Uses for_number to drive a
// state machine that dispatches to the correct yield block each tick.
func (p *parser) emitStateMachineIter(s *ForStmt, it *iterDef, ctx *emitContext, comment string) error {
	ctx.pushScope()

	// Declare iter vars
	iterVarRegs := make([]string, len(s.IterVars))
	for i, v := range s.IterVars {
		iterVarRegs[i] = ctx.declareIterVar(v)
	}

	iterParamMap, err := ctx.buildIterParamMap(it, s.IterArgs, s.IterKwArgs, iterVarRegs)
	if err != nil {
		ctx.popScope()
		return err
	}

	// Temporarily merge the iterator's scope into p.fns and p.iters so
	// that transitive dependencies are available during yield expression
	// emission.
	var scopeAdded []string
	if it.scope != nil {
		for k, v := range it.scope {
			if _, exists := p.fns[k]; !exists {
				p.fns[k] = v
				scopeAdded = append(scopeAdded, k)
			}
		}
	}
	var iterScopeAdded []string
	if it.iterScope != nil {
		for k, v := range it.iterScope {
			if _, exists := p.iters[k]; !exists {
				p.iters[k] = v
				iterScopeAdded = append(iterScopeAdded, k)
			}
		}
	}

	N := len(it.astBody)

	// Allocate state variable
	stateVar := allocUniqueVar("@sm", ctx.usedVars)

	// Look up the for_number iter to get its instruction frame
	eachNumberIter := p.iters["each_number"]

	// Build paramMap for for_number: from=0, to=N-1, step=1, i=stateVar
	// for_number is inclusive of the stop value, so N yields need 0..N-1
	fnParamMap := map[string]any{
		"from": map[string]any{"num": 0},
		"to":   map[string]any{"num": N - 1},
		"step": map[string]any{"num": 1},
		"i":    stateVar,
	}

	// Resolve and emit for_number instruction frame
	resolved := resolveInstructionFrame(eachNumberIter.frame, nil, fnParamMap, nil, comment)
	instrIdx := ctx.b.emit(resolved)

	// Record body start for break patching
	origLen := len(ctx.b.frames)

	if N == 1 {
		// Single yield — no dispatch needed, emit directly
		yield := it.astBody[0].(*YieldStmt)
		for j, expr := range yield.Values {
			if j < len(iterVarRegs) {
				if err := p.emitExprTo(expr, iterVarRegs[j], ctx.b, iterParamMap, ctx.usedVars, "", 0); err != nil {
					ctx.popScope()
					return err
				}
			}
		}
		if err := ctx.emitBody(s.Body); err != nil {
			ctx.popScope()
			return err
		}
		lastIdx := ctx.b.pos() - 1
		ctx.b.frames[lastIdx]["next"] = false
	} else {
		// N > 1: shared body with check_number dispatch
		// Each yield's assignments route to a shared body via frameRef.
		// After the body, next=false re-dispatches to for_number.
		type yieldEnd struct {
			frameIdx int  // last frame of this yield's assignments
			isCheck  bool // true if the carrier is a check_number (zero-value yield)
		}
		var yieldEnds []yieldEnd

		for i := 0; i < N; i++ {
			yield := it.astBody[i].(*YieldStmt)
			checkIdx := -1

			// Emit check_number for all but the last yield (catch-all)
			if i < N-1 {
				check := map[string]any{
					"op":        "check_number",
					checkValue:  stateVar,
					checkTarget: map[string]any{"num": i},
				}
				checkIdx = ctx.b.emit(check)
			}

			// Emit yield value assignments
			beforeAssign := ctx.b.pos()
			for j, expr := range yield.Values {
				if j < len(iterVarRegs) {
					if err := p.emitExprTo(expr, iterVarRegs[j], ctx.b, iterParamMap, ctx.usedVars, "", 0); err != nil {
						ctx.popScope()
						return err
					}
				}
			}
			hasAssignments := ctx.b.pos() > beforeAssign

			// Record end frame for non-last yields (last falls through to body)
			if i < N-1 {
				if hasAssignments {
					yieldEnds = append(yieldEnds, yieldEnd{ctx.b.pos() - 1, false})
				} else {
					// Zero-value yield: use check_number's next (equal) slot
					yieldEnds = append(yieldEnds, yieldEnd{checkIdx, true})
				}
			}

			// Patch checkLarger to point to the next frame
			if checkIdx >= 0 {
				ctx.b.frames[checkIdx][checkLarger] = frameRef(ctx.b.pos())
			}
		}

		// Emit body ONCE
		bodyStart := frameRef(ctx.b.pos())
		if err := ctx.emitBody(s.Body); err != nil {
			ctx.popScope()
			return err
		}
		lastIdx := ctx.b.pos() - 1
		ctx.b.frames[lastIdx]["next"] = false

		// Patch non-last yield ends to jump to shared body
		for _, ye := range yieldEnds {
			if ye.isCheck {
				// Zero-value yield: route check_number's equal branch to body
				ctx.b.frames[ye.frameIdx]["next"] = bodyStart
			} else {
				ctx.b.frames[ye.frameIdx]["next"] = bodyStart
			}
		}
	}

	// Remove temporarily added scope entries
	for _, k := range scopeAdded {
		delete(p.fns, k)
	}
	for _, k := range iterScopeAdded {
		delete(p.iters, k)
	}

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())

	patchIterDoneSlot(ctx.b, instrIdx, eachNumberIter.doneSlot, afterLoop)

	// Patch @continue in the body — re-dispatch to for_number
	patchContinuePlaceholders(ctx.b, origLen, false)

	// Patch unlabeled @break → "last"; labeled @break → jump past loop
	patchUnlabeledBreakToLast(ctx.b, origLen)
	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	ctx.popScope()
	return nil
}

// emitInstructionIter emits a for-in loop backed by an instruction-based iterator.
func (p *parser) emitInstructionIter(s *ForStmt, it *iterDef, ctx *emitContext, comment string) error {
	ctx.pushScope()

	// Declare iter vars and allocate registers
	iterVarRegs := make([]string, len(s.IterVars))
	for i, v := range s.IterVars {
		iterVarRegs[i] = ctx.declareIterVar(v)
	}

	paramMap, err := ctx.buildIterParamMap(it, s.IterArgs, s.IterKwArgs, iterVarRegs)
	if err != nil {
		ctx.popScope()
		return err
	}

	// Resolve the instruction frame with paramMap
	resolved := resolveInstructionFrame(it.frame, nil, paramMap, nil, comment)

	// Emit the instruction frame
	instrIdx := ctx.b.emit(resolved)

	// Record where the body starts
	origLen := len(ctx.b.frames)

	// Emit the loop body
	if err := ctx.emitBody(s.Body); err != nil {
		ctx.popScope()
		return err
	}

	// Set "next": false on the last body frame (detached — VM re-dispatches)
	lastIdx := ctx.b.pos() - 1
	if lastIdx >= origLen {
		ctx.b.frames[lastIdx]["next"] = false
	}

	// Patch @continue in the body — re-dispatch to iterator instruction
	patchContinuePlaceholders(ctx.b, origLen, false)

	// Patch unlabeled @break → "last"; labeled @break → jump past loop
	patchUnlabeledBreakToLast(ctx.b, origLen)

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())

	patchIterDoneSlot(ctx.b, instrIdx, it.doneSlot, afterLoop)

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	ctx.popScope()
	return nil
}

// parseIteratorInstruction parses `iterator_instruction "op" { fields } { body }`
// in the for...in position. Uses parseInstruction for the instruction block,
// then validates and stores on ForStmt for emission.
func (p *parser) parseIteratorInstruction(iterVars []string, label, comment string, ctx *parseContext, kwTok token) (*ForStmt, error) {
	frame, err := p.parseInstruction()
	if err != nil {
		return nil, err
	}

	// Extract and remove "done" key
	doneVal, hasDone := frame["done"]
	if !hasDone {
		return nil, p.errorf(kwTok.pos, "iterator_instruction requires a 'done:' slot")
	}
	doneSlot, ok := doneVal.(int)
	if !ok {
		return nil, p.errorf(kwTok.pos, "iterator_instruction 'done:' value must be a number")
	}
	delete(frame, "done")
	doneKey := strconv.Itoa(doneSlot)

	// Validate: no exec bindings allowed
	for k, v := range frame {
		if _, isEB := v.(execBinding); isEB {
			return nil, p.errorf(kwTok.pos, "iterator_instruction does not support exec bindings (key %q); use instruction with ' blocks for branching", k)
		}
	}

	// Count @N return slots and validate against iter var count
	retCount := frameReturnCount(frame)
	if retCount > len(iterVars) {
		return nil, p.errorf(kwTok.pos, "iterator_instruction has @%d but only %d iteration variable(s) bound", retCount, len(iterVars))
	}

	// Parse loop body
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	ctx.pushScope()
	for _, v := range iterVars {
		ctx.declareIterVar(v)
	}

	p.enterLoop(label)
	body, err := ctx.parseBody(false)
	p.exitLoop(label)

	ctx.popScope()

	if err != nil {
		return nil, err
	}

	return &ForStmt{
		Pos:            kwTok.pos,
		Label:          label,
		IterVars:       iterVars,
		IterInstrFrame: frame,
		IterInstrDone:  doneKey,
		Body:           body,
		Comment:        comment,
	}, nil
}

// emitInlineIterInstruction emits a for-in loop backed by an inline
// iterator_instruction. Similar to emitInstructionIter but resolves the
// frame directly rather than through an iterDef.
func (p *parser) emitInlineIterInstruction(s *ForStmt, ctx *emitContext, comment string) error {
	ctx.pushScope()

	// Declare iter vars and allocate registers
	iterVarRegs := make([]string, len(s.IterVars))
	for i, v := range s.IterVars {
		iterVarRegs[i] = ctx.declareIterVar(v)
	}

	// Build retVals from iter var regs (for @N substitution)
	retVals := make([]any, len(iterVarRegs))
	for i, r := range iterVarRegs {
		retVals[i] = r
	}

	// Resolve the instruction frame
	resolved := ctx.resolveInstrFrame(s.IterInstrFrame, retVals, comment)

	// Emit the instruction frame
	instrIdx := ctx.b.emit(resolved)

	// Record where the body starts
	origLen := len(ctx.b.frames)

	// Emit the loop body
	if err := ctx.emitBody(s.Body); err != nil {
		ctx.popScope()
		return err
	}

	// Set "next": false on the last body frame (detached — VM re-dispatches)
	lastIdx := ctx.b.pos() - 1
	if lastIdx >= origLen {
		ctx.b.frames[lastIdx]["next"] = false
	}

	// Patch @continue in the body — re-dispatch to iterator instruction
	patchContinuePlaceholders(ctx.b, origLen, false)

	// Patch unlabeled @break → "last"; labeled @break → jump past loop
	patchUnlabeledBreakToLast(ctx.b, origLen)

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())

	patchIterDoneSlot(ctx.b, instrIdx, s.IterInstrDone, afterLoop)

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	ctx.popScope()
	return nil
}

// emitYieldIter emits a for-in loop backed by a yield-based (user-defined) iterator.
// The iter body is inlined (like expandCall for fn bodies). At each yield point,
// the caller's for-loop body is expanded inline with output bindings.
func (p *parser) emitYieldIter(s *ForStmt, it *iterDef, ctx *emitContext, comment string) error {
	ctx.pushScope()

	// Declare iter vars
	iterVarRegs := make([]string, len(s.IterVars))
	for i, v := range s.IterVars {
		iterVarRegs[i] = ctx.declareIterVar(v)
	}

	paramMap, err := ctx.buildIterParamMap(it, s.IterArgs, s.IterKwArgs, iterVarRegs)
	if err != nil {
		ctx.popScope()
		return err
	}

	// Temporarily merge the iterator's scope into p.fns and p.iters so
	// that transitive dependencies are available during body expansion.
	var scopeAdded []string
	if it.scope != nil {
		for k, v := range it.scope {
			if _, exists := p.fns[k]; !exists {
				p.fns[k] = v
				scopeAdded = append(scopeAdded, k)
			}
		}
	}
	var iterScopeAdded []string
	if it.iterScope != nil {
		for k, v := range it.iterScope {
			if _, exists := p.iters[k]; !exists {
				p.iters[k] = v
				iterScopeAdded = append(iterScopeAdded, k)
			}
		}
	}

	// Pre-scan for output variables (like expandCall does)
	collectASTOutputVars(it.astBody, paramMap, ctx.usedVars)

	origLen := len(ctx.b.frames)

	// Emit the iter body through emitFnBody, but intercept YieldStmt.
	// We rewrite the AST to replace yield statements with the caller's body.
	rewritten := p.rewriteYieldToBody(it.astBody, s.Body, it.outputs, iterVarRegs)
	if err := p.emitFnBody(rewritten, ctx.b, paramMap, ctx.usedVars, comment, 0); err != nil {
		ctx.popScope()
		return err
	}

	// Remove temporarily added scope entries
	for _, k := range scopeAdded {
		delete(p.fns, k)
	}
	for _, k := range iterScopeAdded {
		delete(p.iters, k)
	}

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())

	// Patch labeled break placeholders for the entire expansion
	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	ctx.popScope()
	return nil
}

// rewriteYieldToBody creates a rewritten copy of the iter body where each
// YieldStmt is replaced with: assignments from yield values to iter var
// registers, followed by the caller's for-loop body.
func (p *parser) rewriteYieldToBody(iterBody []Stmt, callerBody []Stmt, outputs []string, iterVarRegs []string) []Stmt {
	var result []Stmt
	for _, stmt := range iterBody {
		switch st := stmt.(type) {
		case *YieldStmt:
			// Insert assignments: iter_output = yield_value
			for i, expr := range st.Values {
				if i < len(iterVarRegs) {
					result = append(result, &AssignStmt{
						Target:   outputs[i],
						Value:    expr,
						Internal: true,
					})
				}
			}
			// Insert the caller's body wrapped in YieldBodyStmt
			// so @continue is patched to jump past it
			result = append(result, &YieldBodyStmt{Body: callerBody})
		case *IfStmt:
			rewritten := *st
			rewritten.Body = p.rewriteYieldToBody(st.Body, callerBody, outputs, iterVarRegs)
			for i, elif := range st.ElseIfs {
				rewritten.ElseIfs[i].Body = p.rewriteYieldToBody(elif.Body, callerBody, outputs, iterVarRegs)
			}
			if st.Else != nil {
				rewritten.Else = p.rewriteYieldToBody(st.Else, callerBody, outputs, iterVarRegs)
			}
			result = append(result, &rewritten)
		case *ForStmt:
			rewritten := *st
			rewritten.Body = p.rewriteYieldToBody(st.Body, callerBody, outputs, iterVarRegs)
			result = append(result, &rewritten)
		case *WhileStmt:
			rewritten := *st
			rewritten.Body = p.rewriteYieldToBody(st.Body, callerBody, outputs, iterVarRegs)
			result = append(result, &rewritten)
		case *LoopStmt:
			rewritten := *st
			rewritten.Body = p.rewriteYieldToBody(st.Body, callerBody, outputs, iterVarRegs)
			result = append(result, &rewritten)
		case *ModeBlockStmt:
			rewritten := *st
			rewritten.Body = p.rewriteYieldToBody(st.Body, callerBody, outputs, iterVarRegs)
			result = append(result, &rewritten)
		default:
			result = append(result, stmt)
		}
	}
	return result
}

// emitForStmtRange emits a for loop when the Range constructor is directly visible.
// Compiles to a single for_number instruction frame.
func (p *parser) emitForStmtRange(s *ForStmt, ctor *ConstructorExpr, iterVar string, ctx *emitContext, comment string) error {
	// If we're inside another for_number loop, compile as a manual counter
	// loop instead. Nested for_number instructions don't work correctly
	// because the inner body's next:false re-dispatch hits the outer
	// for_number first (lower frame number), advancing both counters on
	// every inner iteration.
	if p.forNumberDepth > 0 {
		return p.emitForStmtRangeManual(s, ctor, iterVar, ctx, comment)
	}

	startVal, err := ctx.exprGetValue(ctor.Args[0], "")
	if err != nil {
		return err
	}
	stopVal, err := ctx.exprGetValue(ctor.Args[1], "")
	if err != nil {
		return err
	}
	stepVal, err := ctx.exprGetValue(ctor.Args[2], "")
	if err != nil {
		return err
	}

	eachNumberIter := p.iters["each_number"]
	paramMap := map[string]any{
		"from": startVal,
		"to":   stopVal,
		"step": stepVal,
		"i":    iterVar,
	}

	resolved := resolveInstructionFrame(eachNumberIter.frame, nil, paramMap, nil, comment)
	instrIdx := ctx.b.emit(resolved)

	origLen := len(ctx.b.frames)

	p.forNumberDepth++
	if err := ctx.emitBody(s.Body); err != nil {
		p.forNumberDepth--
		return err
	}
	p.forNumberDepth--

	// Set "next": false on the last body frame (detached — VM re-dispatches)
	lastIdx := ctx.b.pos() - 1
	if lastIdx >= origLen {
		ctx.b.frames[lastIdx]["next"] = false
	}

	// Patch @continue in the body — re-dispatch to for_number
	patchContinuePlaceholders(ctx.b, origLen, false)

	// Patch unlabeled @break → "last"; labeled @break → jump past loop
	patchUnlabeledBreakToLast(ctx.b, origLen)

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())

	patchIterDoneSlot(ctx.b, instrIdx, eachNumberIter.doneSlot, afterLoop)

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// emitForStmtRangeManual emits a Range-based for loop using manual counter
// instructions (set_number + check_number + add) instead of for_number.
// Used for nested Range loops where the VM's re-dispatch model would cause
// the outer for_number to advance on every inner iteration.
func (p *parser) emitForStmtRangeManual(s *ForStmt, ctor *ConstructorExpr, iterVar string, ctx *emitContext, comment string) error {
	startVal, err := ctx.exprGetValue(ctor.Args[0], "")
	if err != nil {
		return err
	}
	stopVal, err := ctx.exprGetValue(ctor.Args[1], "")
	if err != nil {
		return err
	}
	stepVal, err := ctx.exprGetValue(ctor.Args[2], "")
	if err != nil {
		return err
	}

	// Determine step direction for check_number branching.
	stepPositive := false
	if m, ok := stepVal.(map[string]any); ok {
		if n, ok := m["num"].(int); ok {
			if n == 0 {
				return fmt.Errorf("Range step cannot be zero")
			}
			stepPositive = n > 0
		} else {
			return fmt.Errorf("nested Range loops require a compile-time constant step")
		}
	} else {
		return fmt.Errorf("nested Range loops require a compile-time constant step")
	}

	// Initialize counter: set_number iterVar = startVal
	ctx.b.emit(map[string]any{
		"op": "set_number",
		"1":  startVal,
		"2":  iterVar,
	})

	// Check: check_number iterVar, stopVal
	checkIdx := ctx.b.pos()
	bodyRef := frameRef(checkIdx + 1)
	donePlaceholder := frameRef(0)

	checkFrame := map[string]any{
		"op":        "check_number",
		checkValue:  iterVar,
		checkTarget: stopVal,
		"next":      bodyRef, // equal → body (inclusive range)
	}
	if stepPositive {
		// Done when i > stop → larger branch
		checkFrame[checkLarger] = donePlaceholder
	} else {
		// Done when i < stop → smaller branch
		checkFrame[checkSmaller] = donePlaceholder
	}
	ctx.b.emit(checkFrame)

	origLen := len(ctx.b.frames)

	// Emit body (keep depth tracking for further nesting)
	p.forNumberDepth++
	if err := ctx.emitBody(s.Body); err != nil {
		p.forNumberDepth--
		return err
	}
	p.forNumberDepth--

	// Increment: add iterVar += stepVal, then back-edge to check
	incrIdx := ctx.b.pos()
	ctx.b.emit(map[string]any{
		"op":   "add",
		"0":    iterVar,
		"1":    stepVal,
		"2":    iterVar,
		"next": frameRef(checkIdx),
	})

	// Detach noop: absorbs outer for_number's "next: false" on the last
	// body frame so the add frame's back-edge is preserved. Emitted without
	// "next" — natural fall-through. When this IS the outer for_number's
	// last body frame, the outer loop sets "next": false for re-dispatch.
	// When it's NOT the last, it falls through to the next statement.
	// Eliminated by eliminateNoopBridges.
	detachIdx := ctx.b.pos()
	ctx.b.emit(map[string]any{"op": "set_reg", "0": false, "1": false})

	// Patch @continue → increment frame
	patchContinuePlaceholders(ctx.b, origLen, frameRef(incrIdx))

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)

	// afterLoop points to the detach noop. After noop elimination,
	// references resolve to the noop's target: false (re-dispatch) when
	// this is the last body frame of the outer for_number, or the next
	// statement otherwise.
	afterLoop := frameRef(detachIdx)

	// Patch done placeholder to afterLoop
	if stepPositive {
		ctx.b.frames[checkIdx][checkLarger] = afterLoop
	} else {
		ctx.b.frames[checkIdx][checkSmaller] = afterLoop
	}

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// emitForStmtRuntime emits a for loop where the range is a runtime value (Path C).
// Decomposes the range with separate_register, then uses for_number.
func (p *parser) emitForStmtRuntime(s *ForStmt, iterVar string, ctx *emitContext, comment string) error {
	if p.forNumberDepth > 0 {
		return fmt.Errorf("nested Range loops with runtime range values are not supported; use Range(...) constructor")
	}

	rangeVal, err := ctx.exprGetValue(s.Range, "")
	if err != nil {
		return err
	}

	stepVar := allocUniqueVar("@step", ctx.usedVars)
	startVar := allocUniqueVar("@start", ctx.usedVars)
	stopVar := allocUniqueVar("@stop", ctx.usedVars)
	retVals := []any{stepVar, false, false, startVar, stopVar}
	if err := p.expandCall("separate_register", []any{rangeVal}, nil, retVals, ctx.b, 0, "", ctx.usedVars); err != nil {
		return err
	}

	eachNumberIter := p.iters["each_number"]
	paramMap := map[string]any{
		"from": startVar,
		"to":   stopVar,
		"step": stepVar,
		"i":    iterVar,
	}

	resolved := resolveInstructionFrame(eachNumberIter.frame, nil, paramMap, nil, comment)
	instrIdx := ctx.b.emit(resolved)

	origLen := len(ctx.b.frames)

	p.forNumberDepth++
	if err := ctx.emitBody(s.Body); err != nil {
		p.forNumberDepth--
		return err
	}
	p.forNumberDepth--

	// Set "next": false on the last body frame (detached — VM re-dispatches)
	lastIdx := ctx.b.pos() - 1
	if lastIdx >= origLen {
		ctx.b.frames[lastIdx]["next"] = false
	}

	// Patch @continue in the body — re-dispatch to for_number
	patchContinuePlaceholders(ctx.b, origLen, false)

	// Patch unlabeled @break → "last"; labeled @break → jump past loop
	patchUnlabeledBreakToLast(ctx.b, origLen)

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())

	patchIterDoneSlot(ctx.b, instrIdx, eachNumberIter.doneSlot, afterLoop)

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// parseForStmt parses a for-in loop. Two forms:
//
//	Range form:    for i in Range(5) { ... }
//	Iterator form: for comp, idx in for_component() { ... }
func (p *parser) parseForStmt(ctx *parseContext, comment string, label ...string) (*ForStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}

	// Parse iteration variable(s): single var or comma-separated list
	var iterVars []string
	iterTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if err := p.checkVarName(iterTok.val, iterTok.pos); err != nil {
		return nil, err
	}
	iterVars = append(iterVars, iterTok.val)

	// Check for additional comma-separated variables
	for {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind != tokComma {
			p.unget(peek)
			break
		}
		varTok, err := p.expect(tokIdent)
		if err != nil {
			return nil, err
		}
		if err := p.checkVarName(varTok.val, varTok.pos); err != nil {
			return nil, err
		}
		iterVars = append(iterVars, varTok.val)
	}

	// Expect 'in'
	inTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if inTok.val != "in" {
		return nil, p.errorf(inTok.pos, "expected 'in' after for variable, got %q", inTok.val)
	}

	// Parse the source: Range constructor, iterator call, or variable
	rangeTok, err := p.next()
	if err != nil {
		return nil, err
	}

	// Check for iterator_instruction
	if rangeTok.kind == tokIdent && rangeTok.val == "iterator_instruction" {
		return p.parseIteratorInstruction(iterVars, lbl, comment, ctx, rangeTok)
	}

	// Check for iterator call (including namespace-qualified: lib.my_iter)
	if rangeTok.kind == tokIdent {
		resolvedName, fn, resolveErr := p.resolveFnName(rangeTok)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if it := p.iters[resolvedName]; it != nil {
			// Validate iter var count
			if len(iterVars) > len(it.outputs) {
				return nil, p.errorf(iterTok.pos, "too many variables: %s yields %d value(s), but %d variable(s) bound",
					resolvedName, len(it.outputs), len(iterVars))
			}

			// Parse iterator call args (use resolved name token for error messages)
			resolvedTok := token{kind: tokIdent, val: resolvedName, pos: rangeTok.pos}
			iterArgs, iterKwArgs, err := p.parseIterCallArgs(it, resolvedTok, ctx)
			if err != nil {
				return nil, err
			}

			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}

			ctx.pushScope()
			for _, v := range iterVars {
				ctx.declareIterVar(v)
			}

			p.enterLoop(lbl)
			body, err := ctx.parseBody(false)
			p.exitLoop(lbl)

			ctx.popScope()

			if err != nil {
				return nil, err
			}

			return &ForStmt{
				Pos:        rangeTok.pos,
				Label:      lbl,
				IterVars:   iterVars,
				IterName:   resolvedName,
				IterArgs:   iterArgs,
				IterKwArgs: iterKwArgs,
				Body:       body,
				Comment:    comment,
			}, nil
		}
		// If resolveFnName consumed namespace tokens (qualified name), but it
		// wasn't an iterator, error now — for...in only accepts iterators, Range,
		// or variables holding Range values.
		if resolvedName != rangeTok.val {
			if fn != nil {
				return nil, p.errorf(rangeTok.pos, "%q is a function, not an iterator; use 'iter' to declare iterators", resolvedName)
			}
			return nil, p.errorf(rangeTok.pos, "%q is not an iterator", resolvedName)
		}
		// Not a namespace-qualified name — check fn-as-iterator error
		if fn != nil && fn.hasExec() {
			return nil, p.errorf(rangeTok.pos, "%q is a function with exec blocks, not an iterator; use 'iter' to declare iterators", rangeTok.val)
		}
	}

	// Range form — must be a single variable
	if len(iterVars) > 1 {
		return nil, p.errorf(iterTok.pos, "Range for-loop binds exactly one variable, got %d", len(iterVars))
	}

	var rangeExpr Expr
	if rangeTok.kind == tokIdent && rangeTok.val == "Range" {
		rangeExpr, err = ctx.parseConstructor(rangeTok)
		if err != nil {
			return nil, err
		}
	} else if rangeTok.kind == tokIdent {
		resolved, err := ctx.resolve(rangeTok)
		if err != nil {
			return nil, err
		}
		rangeExpr = resolved
	} else {
		return nil, p.errorf(rangeTok.pos, "expected Range constructor, iterator, or variable after 'in', got %s", rangeTok.describe())
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	ctx.pushScope()
	ctx.declareIterVar(iterTok.val)

	p.enterLoop(lbl)
	body, err := ctx.parseBody(false)
	p.exitLoop(lbl)

	ctx.popScope()

	if err != nil {
		return nil, err
	}

	return &ForStmt{Pos: rangeTok.pos, Label: lbl, IterVars: []string{iterTok.val}, Range: rangeExpr, Body: body, Comment: comment}, nil
}

// parseIterCallArgs parses positional and keyword arguments for an iterator
// call in a for...in statement. Mirrors parseFnBodyCallArgs but uses the
// parseContext's expression parser.
func (p *parser) parseIterCallArgs(it *iterDef, calleeTok token, ctx *parseContext) ([]Expr, map[string]Expr, error) {
	// Detect parenthesized call syntax
	paren := false
	peek, err := p.next()
	if err != nil {
		return nil, nil, err
	}
	if peek.kind == tokLParen {
		paren = true
	} else {
		p.unget(peek)
	}

	posCount := 0
	for _, pd := range it.params {
		if pd.keyword == "" {
			posCount++
		}
	}

	var args []Expr
	for i := 0; i < posCount; i++ {
		if i > 0 {
			if _, err := p.expect(tokComma); err != nil {
				return nil, nil, err
			}
		}
		tok, err := p.next()
		if err != nil {
			return nil, nil, err
		}
		// Skip direction annotations
		if tok.kind == tokIdent && isDirection(tok.val) {
			tok, err = p.next()
			if err != nil {
				return nil, nil, err
			}
		}
		p.unget(tok)
		arg, err := p.parseBhvOrFnExpr(ctx)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, arg)
	}

	// Parse optional keyword args
	var kwArgs map[string]Expr
	peek, err = p.next()
	if err != nil {
		return nil, nil, err
	}
	if peek.kind == tokComma && posCount < len(it.params) {
		kwArgs = map[string]Expr{}
		for {
			kwTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, nil, err
			}
			// Skip direction annotations
			if isDirection(kwTok.val) {
				kwTok, err = p.expect(tokIdent)
				if err != nil {
					return nil, nil, err
				}
			}
			kw := iterKeywordByName(it, kwTok.val)
			if kw == nil {
				return nil, nil, p.errorf(kwTok.pos, "unknown keyword argument %q for iter %q", kwTok.val, calleeTok.val)
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, nil, err
			}
			val, err := p.parseBhvOrFnExpr(ctx)
			if err != nil {
				return nil, nil, err
			}
			kwArgs[kwTok.val] = val

			next, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			if next.kind != tokComma {
				p.unget(next)
				break
			}
		}
	} else {
		p.unget(peek)
	}

	if paren {
		if _, err := p.expect(tokRParen); err != nil {
			return nil, nil, err
		}
	}

	return args, kwArgs, nil
}

// parseBhvOrFnExpr parses a single expression using the parseContext's resolver.
// Used for iterator call arguments in for...in statements.
func (p *parser) parseBhvOrFnExpr(ctx *parseContext) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind == tokIdent && isConstructor(tok.val) {
		return ctx.parseConstructor(tok)
	}
	p.unget(tok)
	return p.parseArithExpr(ctx.resolve)
}

// iterKeywordByName returns the paramDef for the given keyword, or nil.
func iterKeywordByName(it *iterDef, keyword string) *paramDef {
	for i := range it.params {
		if it.params[i].keyword == keyword {
			return &it.params[i]
		}
	}
	return nil
}
