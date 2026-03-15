package compiler

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
)

// --- AST-based fn body emission ---

// collectASTOutputVars pre-scans AST statements for output variables
// and adds them to paramMap with unique names (for inline renaming).
func collectASTOutputVars(stmts []Stmt, paramMap map[string]any, usedVars map[string]bool) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *LetStmt:
			if _, mapped := paramMap[s.Name]; !mapped {
				paramMap[s.Name] = allocUniqueVar(s.Name, usedVars)
			}
			collectExprOutputVars(s.Value, paramMap, usedVars)
		case *AssignStmt:
			if !strings.HasPrefix(s.Target, "%") {
				if _, mapped := paramMap[s.Target]; !mapped {
					paramMap[s.Target] = allocUniqueVar(s.Target, usedVars)
				}
			}
			collectExprOutputVars(s.Value, paramMap, usedVars)
		case *CompoundAssignStmt:
			if !strings.HasPrefix(s.Target, "%") {
				if _, mapped := paramMap[s.Target]; !mapped {
					paramMap[s.Target] = allocUniqueVar(s.Target, usedVars)
				}
			}
		case *IncrDecrStmt:
			if !strings.HasPrefix(s.Target, "%") {
				if _, mapped := paramMap[s.Target]; !mapped {
					paramMap[s.Target] = allocUniqueVar(s.Target, usedVars)
				}
			}
		case *MultiReturnStmt:
			for _, bind := range s.Bindings {
				if !bind.Discard {
					if _, mapped := paramMap[bind.Name]; !mapped {
						paramMap[bind.Name] = allocUniqueVar(bind.Name, usedVars)
					}
				}
			}
			collectExprOutputVars(s.Value, paramMap, usedVars)
		case *IfStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
			for _, elif := range s.ElseIfs {
				collectASTOutputVars(elif.Body, paramMap, usedVars)
			}
			collectASTOutputVars(s.Else, paramMap, usedVars)
		case *WhileStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *LoopStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *ForStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *ModeBlockStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *WaitStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *OnEventStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		case *AssertStmt:
			collectASTOutputVars(s.Body, paramMap, usedVars)
		}
	}
}

// collectExprOutputVars recursively scans an expression for nested statement
// bodies (IfExpr, ModeBlockExpr) that may declare output variables.
func collectExprOutputVars(expr Expr, paramMap map[string]any, usedVars map[string]bool) {
	switch e := expr.(type) {
	case *IfExpr:
		collectASTOutputVars(e.Body, paramMap, usedVars)
		collectExprOutputVars(e.Tail, paramMap, usedVars)
		for _, elif := range e.ElseIfs {
			collectASTOutputVars(elif.Body, paramMap, usedVars)
			collectExprOutputVars(elif.Tail, paramMap, usedVars)
		}
		collectASTOutputVars(e.ElsBody, paramMap, usedVars)
		collectExprOutputVars(e.ElsTail, paramMap, usedVars)
	case *ModeBlockExpr:
		collectASTOutputVars(e.Body, paramMap, usedVars)
		collectExprOutputVars(e.Tail, paramMap, usedVars)
	case *ExprListExpr:
		for _, item := range e.Exprs {
			collectExprOutputVars(item, paramMap, usedVars)
		}
	}
}

// resolveVarName resolves a variable name through paramMap.
func resolveVarName(name string, paramMap map[string]any) any {
	if strings.HasPrefix(name, "%") {
		return map[string]any{"fr": name[1:]}
	}
	if val, ok := paramMap[name]; ok {
		return val
	}
	return name
}

// emitCallExprArgs resolves AST call args through paramMap, emitting
// complex expressions (constructors, &) to temp variables as needed.
func (p *parser) emitCallExprArgs(args []Expr, kwArgs map[string]Expr, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, pos int) ([]any, map[string]any, error) {
	resolvedArgs := make([]any, len(args))
	for i, arg := range args {
		val, err := p.emitExprGetValue(arg, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, nil, err
		}
		resolvedArgs[i] = val
	}
	resolvedKwArgs := map[string]any{}
	for kw, arg := range kwArgs {
		val, err := p.emitExprGetValue(arg, b, paramMap, usedVars, "", pos)
		if err != nil {
			return nil, nil, err
		}
		resolvedKwArgs[kw] = val
	}
	return resolvedArgs, resolvedKwArgs, nil
}

// tryResolveConstructorLiteral attempts to resolve a ConstructorExpr to a
// compile-time literal. Returns (nil, false) if any argument is not a literal.
func tryResolveConstructorLiteral(ctor *ConstructorExpr) (any, bool) {
	prefix := ""
	switch ctor.TypeName {
	case "Component":
		prefix = "c_"
	case "Technology":
		prefix = "t_"
	case "Value":
		prefix = "v_"
	}
	switch ctor.TypeName {
	case "Item", "Component", "Technology", "Value":
		lit, ok := ctor.Args[0].(*LiteralExpr)
		if !ok {
			return nil, false
		}
		s, ok := lit.Value.(string)
		if !ok {
			return nil, false
		}
		return map[string]any{"id": prefix + s}, true
	case "Coordinate":
		xLit, xOk := ctor.Args[0].(*LiteralExpr)
		yLit, yOk := ctor.Args[1].(*LiteralExpr)
		if !xOk || !yOk {
			return nil, false
		}
		xMap, xIsMap := xLit.Value.(map[string]any)
		yMap, yIsMap := yLit.Value.(map[string]any)
		if !xIsMap || !yIsMap {
			return nil, false
		}
		xNum, xHas := xMap["num"]
		yNum, yHas := yMap["num"]
		if !xHas || !yHas || len(xMap) != 1 || len(yMap) != 1 {
			return nil, false
		}
		return map[string]any{"coord": map[string]any{"x": xNum, "y": yNum}}, true
	case "Range":
		// Range has 3 args: start, stop, step (all normalized during parsing)
		startLit, sOk := ctor.Args[0].(*LiteralExpr)
		stopLit, eOk := ctor.Args[1].(*LiteralExpr)
		stepLit, tOk := ctor.Args[2].(*LiteralExpr)
		if !sOk || !eOk || !tOk {
			return nil, false
		}
		startMap, sIsMap := startLit.Value.(map[string]any)
		stopMap, eIsMap := stopLit.Value.(map[string]any)
		stepMap, tIsMap := stepLit.Value.(map[string]any)
		if !sIsMap || !eIsMap || !tIsMap {
			return nil, false
		}
		startNum, sHas := startMap["num"]
		stopNum, eHas := stopMap["num"]
		stepNum, tHas := stepMap["num"]
		if !sHas || !eHas || !tHas || len(startMap) != 1 || len(stopMap) != 1 || len(stepMap) != 1 {
			return nil, false
		}
		return map[string]any{
			"coord": map[string]any{"x": startNum, "y": stopNum},
			"num":   stepNum,
		}, true
	}
	return nil, false
}

// tryResolveAmpersandLiteral attempts to resolve an AmpersandExpr to a
// compile-time literal. Returns (nil, false) if any operand is not compile-time.
func tryResolveAmpersandLiteral(amp *AmpersandExpr) (any, bool) {
	// Resolve LHS
	var baseVal map[string]any
	switch lhs := amp.Value.(type) {
	case *LiteralExpr:
		if m, ok := lhs.Value.(map[string]any); ok {
			baseVal = m
		}
	case *ConstructorExpr:
		if v, ok := tryResolveConstructorLiteral(lhs); ok {
			if m, ok := v.(map[string]any); ok {
				baseVal = m
			}
		}
	}
	if baseVal == nil {
		return nil, false
	}
	// Resolve RHS
	numLit, ok := amp.Num.(*LiteralExpr)
	if !ok {
		return nil, false
	}
	numMap, ok := numLit.Value.(map[string]any)
	if !ok {
		return nil, false
	}
	numN, ok := numMap["num"]
	if !ok || len(numMap) != 1 {
		return nil, false
	}
	result := make(map[string]any, len(baseVal)+1)
	for k, v := range baseVal {
		result[k] = v
	}
	result["num"] = numN
	return result, true
}

// tryResolveDotAccessLiteral attempts to resolve a DotAccessExpr to a
// compile-time literal. Returns (nil, false) if the operand is not compile-time.
func tryResolveDotAccessLiteral(dot *DotAccessExpr) (any, bool) {
	var m map[string]any
	switch v := dot.Value.(type) {
	case *LiteralExpr:
		switch val := v.Value.(type) {
		case map[string]any:
			m = val
		case bool:
			// null/false literal — both .number and .value return null
			if !val {
				return false, true
			}
			return nil, false
		default:
			return nil, false
		}
	case *ConstructorExpr:
		resolved, ok := tryResolveConstructorLiteral(v)
		if !ok {
			return nil, false
		}
		if rm, ok := resolved.(map[string]any); ok {
			m = rm
		} else {
			return nil, false
		}
	default:
		return nil, false
	}
	if dot.Field == "number" {
		if num, has := m["num"]; has {
			return map[string]any{"num": num}, true
		}
		return false, true // no numeric component → null
	}
	// "value": strip the numeric component
	result := make(map[string]any, len(m))
	for k, v := range m {
		if k != "num" {
			result[k] = v
		}
	}
	if len(result) == 0 {
		return false, true // pure number → value is null
	}
	return result, true
}

// emitDotAccessFrame emits a separate_register instruction extracting
// the requested field (.number → slot 1, .value → slot 2) into target.
func emitDotAccessFrame(field string, src, target any, b *frameBuilder, comment string) error {
	f := map[string]any{"op": "separate_register", "0": src}
	if field == "number" {
		f["1"] = target
	} else {
		f["2"] = target
	}
	setComment(f, comment)
	b.emit(f)
	return nil
}

// emitExprGetValue emits an expression and returns the resolved value.
// For simple literals/idents, returns without emitting. For compile-time
// constructors/ampersands, returns the literal. For runtime expressions,
// emits to a temp variable and returns the temp name.
func (p *parser) emitExprGetValue(expr Expr, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) (any, error) {
	switch e := expr.(type) {
	case *LiteralExpr:
		return e.Value, nil
	case *IdentExpr:
		return resolveVarName(e.Name, paramMap), nil
	case *ConstructorExpr:
		if val, ok := tryResolveConstructorLiteral(e); ok {
			return val, nil
		}
		tempName := allocUniqueVar("@ctor", usedVars)
		if err := p.emitConstructorTo(e, tempName, b, paramMap, usedVars, comment, pos); err != nil {
			return nil, err
		}
		return tempName, nil
	case *AmpersandExpr:
		if val, ok := tryResolveAmpersandLiteral(e); ok {
			return val, nil
		}
		tempName := allocUniqueVar("@ctor", usedVars)
		if err := p.emitAmpersandTo(e, tempName, b, paramMap, usedVars, comment, pos); err != nil {
			return nil, err
		}
		return tempName, nil
	case *DotAccessExpr:
		if val, ok := tryResolveDotAccessLiteral(e); ok {
			return val, nil
		}
		tempName := allocUniqueVar("@dot", usedVars)
		if err := p.emitDotAccessTo(e, tempName, b, paramMap, usedVars, comment, pos); err != nil {
			return nil, err
		}
		return tempName, nil
	default:
		tempName := allocUniqueVar("@ctor", usedVars)
		if err := p.emitExprTo(e, tempName, b, paramMap, usedVars, comment, pos); err != nil {
			return nil, err
		}
		return tempName, nil
	}
}

// emitExprTo emits an expression writing the result to target.
func (p *parser) emitExprTo(expr Expr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	switch e := expr.(type) {
	case *CallExpr:
		resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(e.Args, e.KwArgs, b, paramMap, usedVars, pos)
		if err != nil {
			return err
		}
		if e.Blocks != nil {
			opts := expandCallOpts{
				blocks: e.Blocks,
				emitBlockBody: func(stmts []Stmt, bindings map[string]any) error {
					pm := maps.Clone(paramMap)
					for k, v := range bindings {
						pm[k] = v
					}
					return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
				},
				emitTail: func(tail Expr) error {
					return p.emitExprTo(tail, target, b, paramMap, usedVars, comment, pos)
				},
				breakRetVals: []any{target},
			}
			return p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, []any{target}, b, pos, comment, usedVars, opts)
		}
		return p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, []any{target}, b, pos, comment, usedVars)
	case *InstructionExpr:
		if e.Blocks != nil {
			return p.emitFnBodyInstructionExprWithBlocks(e, []any{target}, b, paramMap, usedVars, comment, pos)
		}
		resolved := resolveInstructionFrame(e.Frame, []any{target}, paramMap, nil, comment)
		b.emit(resolved)
		return nil
	case *ConstructorExpr:
		return p.emitConstructorTo(e, target, b, paramMap, usedVars, comment, pos)
	case *AmpersandExpr:
		return p.emitAmpersandTo(e, target, b, paramMap, usedVars, comment, pos)
	case *DotAccessExpr:
		return p.emitDotAccessTo(e, target, b, paramMap, usedVars, comment, pos)
	case *LiteralExpr:
		f := map[string]any{"op": "set_reg", "0": e.Value, "1": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	case *IdentExpr:
		val := resolveVarName(e.Name, paramMap)
		f := map[string]any{"op": "set_reg", "0": val, "1": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	case *ArithExpr:
		return p.emitFnArithTo(e, target, b, paramMap, usedVars, comment, pos)
	case *CompareExpr, *TypeCheckExpr, *TruthyExpr, *BoolChainExpr, *NotExpr:
		return p.emitFnBoolExprTo(expr, target, b, paramMap, usedVars, comment, pos)
	case *ModeBlockExpr:
		ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
		return p.emitModeBlockExpr(e, []any{target}, ctx, comment)
	case *IfExpr:
		ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
		return p.emitIfExpr(e, []any{target}, ctx, comment)
	case *CallBehaviorExpr:
		return p.emitFnBodyCallBehaviorExpr(e, []any{target}, b, paramMap, usedVars, comment)
	}
	return fmt.Errorf("unsupported expression type %T in emitExprTo", expr)
}

// emitConstructorTo emits a type constructor writing the result to target.
func (p *parser) emitConstructorTo(ctor *ConstructorExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	// Try compile-time resolution first (based on AST types, not resolved values)
	if val, ok := tryResolveConstructorLiteral(ctor); ok {
		f := map[string]any{"op": "set_reg", "0": val, "1": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	// Runtime path — Coordinate and Range can be runtime
	if ctor.TypeName == "Coordinate" {
		xVal, err := p.emitExprGetValue(ctor.Args[0], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		yVal, err := p.emitExprGetValue(ctor.Args[1], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		return p.expandCall("combine_coordinate", []any{xVal, yVal}, nil, []any{target}, b, pos, comment, usedVars)
	}
	if ctor.TypeName == "Range" {
		// combine_register step, false, x: start, y: stop
		stepVal, err := p.emitExprGetValue(ctor.Args[2], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		startVal, err := p.emitExprGetValue(ctor.Args[0], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		stopVal, err := p.emitExprGetValue(ctor.Args[1], b, paramMap, usedVars, "", pos)
		if err != nil {
			return err
		}
		kwArgs := map[string]any{"x": startVal, "y": stopVal}
		return p.expandCall("combine_register", []any{stepVal, false}, kwArgs, []any{target}, b, pos, comment, usedVars)
	}
	return fmt.Errorf("unknown constructor %q; valid constructors are Item, Component, Technology, Value, Coordinate, Range", ctor.TypeName)
}

// emitAmpersandTo emits an & expression writing the result to target.
func (p *parser) emitAmpersandTo(amp *AmpersandExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	// Try compile-time resolution (based on AST types)
	if val, ok := tryResolveAmpersandLiteral(amp); ok {
		f := map[string]any{"op": "set_reg", "0": val, "1": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	// Runtime: emit set_number
	baseVal, err := p.emitExprGetValue(amp.Value, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}
	numVal, err := p.emitExprGetValue(amp.Num, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}
	return p.expandCall("set_number", []any{baseVal, numVal}, nil, []any{target}, b, pos, comment, usedVars)
}

// emitDotAccessTo emits a .number or .value access writing the result to target.
func (p *parser) emitDotAccessTo(dot *DotAccessExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	if val, ok := tryResolveDotAccessLiteral(dot); ok {
		f := map[string]any{"op": "set_reg", "0": val, "1": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}
	srcVal, err := p.emitExprGetValue(dot.Value, b, paramMap, usedVars, "", pos)
	if err != nil {
		return err
	}
	return emitDotAccessFrame(dot.Field, srcVal, target, b, comment)
}

// emitFnArithTo emits an arithmetic expression writing the result to target.
func (p *parser) emitFnArithTo(expr *ArithExpr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	ac := &arithCounter{}
	_, err := p.emitArithNode(expr, target, b, usedVars, comment, ac, func(e Expr) (any, error) {
		return p.emitExprGetValue(e, b, paramMap, usedVars, "", pos)
	})
	return err
}

// emitFnBoolExprTo emits a boolean expression (comparison/typecheck/truthy/chain)
// writing the result to target. Mirrors emitBhvBoolExprTo but resolves operands
// through paramMap.
func (p *parser) emitFnBoolExprTo(expr Expr, target any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	resolved, err := p.resolveFnBoolTree(expr, b, paramMap, usedVars, pos)
	if err != nil {
		return err
	}
	p.emitResolvedBoolExprTo(resolved, target, b, comment)
	return nil
}

// resolveFnBoolTree walks an Expr tree, resolving operands through paramMap
// and emitting arithmetic frames. Produces a resolvedBoolExpr tree.
// resolveFnBoolTree delegates to the unified resolveBoolTree with
// fn body operand resolution.
func (p *parser) resolveFnBoolTree(expr Expr, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, pos int) (*resolvedBoolExpr, error) {
	return p.resolveBoolTree(expr, func(e Expr) (any, error) {
		return p.emitExprGetValue(e, b, paramMap, usedVars, "", pos)
	})
}

// fnParseCtx constructs a parseContext for fn body parsing.
func (p *parser) fnParseCtx(fnCtx *fnBodyContext) *parseContext {
	type fnScopeState struct {
		info  map[string]fnVarInfo
		depth int
	}
	var scopeStack []fnScopeState
	mode := modeFunction
	if fnCtx.inIter {
		mode = modeIterator
	}
	pctx := &parseContext{
		mode:    mode,
		resolve: fnCtx.resolve,
		pushScope: func() {
			info, depth := fnCtx.pushFnScope()
			scopeStack = append(scopeStack, fnScopeState{info, depth})
		},
		popScope: func() {
			n := len(scopeStack) - 1
			s := scopeStack[n]
			fnCtx.popFnScope(s.info, s.depth)
			scopeStack = scopeStack[:n]
		},
		declareIterVar: func(name string) {
			fnCtx.declareFnVarWarn(name, false, p, 0)
		},
		parseConstructor: func(nameTok token) (Expr, error) {
			return p.parseFnBodyConstructorExpr(nameTok)
		},
		parseLetVar: func(mutable bool, comment string) ([]Stmt, error) {
			return p.parseFnBodyLetVar(fnCtx, mutable, comment)
		},
		parseOnEvent: func(comment string) (*OnEventStmt, error) {
			return p.parseFnBodyOnEvent(fnCtx, comment)
		},
		parseDefaultStmt: func(tok token, comment string, exprTail bool) ([]Stmt, bool, error) {
			return p.parseFnDefaultStmtUnified(tok, fnCtx, comment, exprTail)
		},
		checkInstrDirs: func(frame map[string]any, pos int) error {
			return p.checkFnBodyInstructionDirections(frame, fnCtx.paramDirs, pos)
		},
		parseLocalBlocks: func(frame map[string]any) ([]*ContinuationBlock, error) {
			return p.maybeParseFnBodyLocalBlocks(frame, fnCtx)
		},
		parseValueExpr: func() (Expr, error) {
			return p.parseFnBodyReturnItem(fnCtx)
		},
		fnCtx: fnCtx,
	}
	pctx.parseBody = func(exprTail bool) ([]Stmt, error) {
		return p.parseStmtBlock(pctx, exprTail)
	}
	return pctx
}

// fnEmitCtx constructs an emitContext for fn body emission.
func (p *parser) fnEmitCtx(b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) *emitContext {
	return &emitContext{
		b:        b,
		usedVars: usedVars,
		resolveBool: func(expr Expr) (*resolvedBoolExpr, error) {
			return p.resolveFnBoolTree(expr, b, paramMap, usedVars, pos)
		},
		emitBody: func(stmts []Stmt) error {
			return p.emitFnBody(stmts, b, paramMap, usedVars, comment, pos)
		},
		exprGetValue: func(expr Expr, cmt string) (any, error) {
			return p.emitExprGetValue(expr, b, paramMap, usedVars, cmt, pos)
		},
		exprTo: func(expr Expr, target any, cmt string) error {
			return p.emitExprTo(expr, target, b, paramMap, usedVars, cmt, pos)
		},
		expandCallExpr: func(ce *CallExpr, retVals []any, cmt string) error {
			resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(ce.Args, ce.KwArgs, b, paramMap, usedVars, pos)
			if err != nil {
				return err
			}
			return p.expandCall(ce.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, cmt, usedVars)
		},
		resolveInstrFrame: func(frame map[string]any, retVals []any, cmt string) map[string]any {
			return resolveInstructionFrame(frame, retVals, paramMap, nil, cmt)
		},
		pushScope:      func() {},
		popScope:       func() {},
		declareIterVar: func(name string) string {
			counterVar := allocUniqueVar(name, usedVars)
			paramMap[name] = counterVar
			return counterVar
		},
	}
}

// inheritComment returns stmtComment if non-empty, otherwise falls back to
// the caller's comment. Used in emitFnBody to propagate doc comments.
func inheritComment(stmtComment, callerComment string) string {
	if stmtComment != "" {
		return stmtComment
	}
	return callerComment
}

// emitFnBodyInstructionWithBlocks emits an instruction with local ' blocks
// inside a fn body expansion.
func (p *parser) emitFnBodyInstructionWithBlocks(s *InstructionStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	localNames, localDetached := extractLocalExecInfo(s.Frame)
	execOutputRegs := allocLocalExecOutputRegs(s.Frame, s.Blocks, usedVars)

	maxSlot := 0
	for slot := range execOutputRegs {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	retVals := make([]any, maxSlot)
	for slot, reg := range execOutputRegs {
		retVals[slot-1] = reg
	}

	resolved := resolveInstructionFrame(s.Frame, retVals, paramMap, nil, comment)
	instrIdx := b.emit(resolved)

	return p.expandInstructionBlocks(instrIdx, s.Blocks, b, localNames, localDetached,
		func(stmts []Stmt, bindings map[string]any) error {
			pm := maps.Clone(paramMap)
			for k, v := range bindings {
				pm[k] = v
			}
			return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
		},
		execOutputRegs, nil)
}

// emitFnBodyInstructionExprWithBlocks emits an instruction expression with
// local ' blocks inside a fn body expansion. Each block has a Tail expression
// that produces the block's value.
func (p *parser) emitFnBodyInstructionExprWithBlocks(e *InstructionExpr, retVals []any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	localNames, localDetached := extractLocalExecInfo(e.Frame)
	execOutputRegs := allocLocalExecOutputRegs(e.Frame, e.Blocks, usedVars)

	// Merge retVals and execOutputRegs for @N substitution
	maxSlot := 0
	for slot := range execOutputRegs {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	instrRetVals := make([]any, maxSlot)
	for i := 0; i < maxSlot; i++ {
		if i < len(retVals) {
			instrRetVals[i] = retVals[i]
		}
		if reg, ok := execOutputRegs[i+1]; ok {
			instrRetVals[i] = reg
		}
	}

	resolved := resolveInstructionFrame(e.Frame, instrRetVals, paramMap, nil, comment)
	instrIdx := b.emit(resolved)

	return p.expandInstructionBlocks(instrIdx, e.Blocks, b, localNames, localDetached,
		func(stmts []Stmt, bindings map[string]any) error {
			pm := maps.Clone(paramMap)
			for k, v := range bindings {
				pm[k] = v
			}
			return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
		},
		execOutputRegs,
		func(tail Expr) error {
			if len(retVals) > 0 {
				return p.emitExprTo(tail, retVals[0], b, paramMap, usedVars, "", pos)
			}
			return nil
		})
}

// emitFnBody emits frames for an AST body during call expansion.
func (p *parser) emitFnBody(stmts []Stmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	collectASTOutputVars(stmts, paramMap, usedVars)
	return p.emitFnBodyCore(stmts, b, paramMap, usedVars, comment, pos)
}

// emitFnBodyCore emits fn body statements without pre-scanning for output
// variables. Used by YieldBodyStmt to emit the caller's loop body without
// renaming caller-scope variables.
func (p *parser) emitFnBodyCore(stmts []Stmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string, pos int) error {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *InstructionStmt:
			callComment := inheritComment(s.Comment, comment)
			if s.Blocks != nil {
				if err := p.emitFnBodyInstructionWithBlocks(s, b, paramMap, usedVars, callComment, pos); err != nil {
					return err
				}
			} else {
				resolved := resolveInstructionFrame(s.Frame, nil, paramMap, nil, callComment)
				b.emit(resolved)
			}

		case *ModeBlockStmt:
			callComment := inheritComment(s.Comment, comment)
			origLen := len(b.frames)
			saved := emitModeEntry(b, s.Unlock, callComment)

			// Statement-form mode block: clear breakRetVals so break-with-value is rejected
			savedBreakRetVals := p.breakRetVals
			p.breakRetVals = nil

			if err := p.emitFnBody(s.Body, b, paramMap, usedVars, comment, pos); err != nil {
				p.breakRetVals = savedBreakRetVals
				return err
			}
			p.breakRetVals = savedBreakRetVals
			modeExitTarget := frameRef(b.pos())
			emitModeExit(b, saved)
			patchBreakPlaceholders(b, origLen, "", modeExitTarget)

		case *CallStmt:
			resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(s.Args, s.KwArgs, b, paramMap, usedVars, pos)
			if err != nil {
				return err
			}
			callComment := inheritComment(s.Comment, comment)
			if s.Blocks != nil {
				if err := p.expandCall(s.Name, resolvedArgs, resolvedKwArgs, nil, b, pos, callComment, usedVars, expandCallOpts{
					blocks: s.Blocks,
					emitBlockBody: func(stmts []Stmt, bindings map[string]any) error {
						pm := maps.Clone(paramMap)
						for k, v := range bindings {
							pm[k] = v
						}
						return p.emitFnBody(stmts, b, pm, usedVars, comment, pos)
					},
				}); err != nil {
					return err
				}
			} else {
				if err := p.expandCall(s.Name, resolvedArgs, resolvedKwArgs, nil, b, pos, callComment, usedVars); err != nil {
					return err
				}
			}

		case *LetStmt:
			target := resolveVarName(s.Name, paramMap)
			callComment := inheritComment(s.Comment, comment)
			if err := p.emitExprTo(s.Value, target, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *MultiReturnStmt:
			retVals := make([]any, len(s.Bindings))
			for i, bind := range s.Bindings {
				if bind.Discard {
					retVals[i] = false
				} else {
					retVals[i] = resolveVarName(bind.Name, paramMap)
				}
			}
			callComment := inheritComment(s.Comment, comment)
			switch v := s.Value.(type) {
			case *CallExpr:
				resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(v.Args, v.KwArgs, b, paramMap, usedVars, pos)
				if err != nil {
					return err
				}
				if err := p.expandCall(v.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, callComment, usedVars); err != nil {
					return err
				}
			case *ModeBlockExpr:
				ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
				if err := p.emitModeBlockExpr(v, retVals, ctx, callComment); err != nil {
					return err
				}
			case *IfExpr:
				ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
				if err := p.emitIfExpr(v, retVals, ctx, callComment); err != nil {
					return err
				}
			case *InstructionExpr:
				if v.Blocks != nil {
					if err := p.emitFnBodyInstructionExprWithBlocks(v, retVals, b, paramMap, usedVars, callComment, pos); err != nil {
						return err
					}
				} else {
					resolved := resolveInstructionFrame(v.Frame, retVals, paramMap, nil, callComment)
					b.emit(resolved)
				}
			case *ExprListExpr:
				ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
				bindIdx := 0
				for _, expr := range v.Exprs {
					switch e := expr.(type) {
					case *CallExpr:
						fn := p.fns[e.Name]
						arity := fn.returnCount()
						remaining := len(s.Bindings) - bindIdx
						callArity := arity
						if callArity > remaining {
							callArity = remaining
						}
						callRetVals := retVals[bindIdx : bindIdx+callArity]
						resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(e.Args, e.KwArgs, b, paramMap, usedVars, pos)
						if err != nil {
							return err
						}
						if err := p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, callRetVals, b, pos, callComment, usedVars); err != nil {
							return err
						}
						bindIdx += callArity
					case *ModeBlockExpr:
						arity := p.exprArity(e.Tail)
						remaining := len(s.Bindings) - bindIdx
						mbeArity := arity
						if mbeArity > remaining {
							mbeArity = remaining
						}
						if mbeArity == 1 {
							if !s.Bindings[bindIdx].Discard {
								if err := p.emitModeBlockExpr(e, []any{retVals[bindIdx]}, ctx, callComment); err != nil {
									return err
								}
							}
						} else {
							mbeRetVals := retVals[bindIdx : bindIdx+mbeArity]
							if err := p.emitModeBlockExpr(e, mbeRetVals, ctx, callComment); err != nil {
								return err
							}
						}
						bindIdx += mbeArity
					case *IfExpr:
						arity := p.ifExprArity(e)
						remaining := len(s.Bindings) - bindIdx
						ifArity := arity
						if ifArity > remaining {
							ifArity = remaining
						}
						if ifArity == 1 {
							if !s.Bindings[bindIdx].Discard {
								if err := p.emitIfExpr(e, []any{retVals[bindIdx]}, ctx, callComment); err != nil {
									return err
								}
							}
						} else {
							ifRetVals := retVals[bindIdx : bindIdx+ifArity]
							if err := p.emitIfExpr(e, ifRetVals, ctx, callComment); err != nil {
								return err
							}
						}
						bindIdx += ifArity
					default:
						if !s.Bindings[bindIdx].Discard {
							if err := p.emitExprTo(expr, retVals[bindIdx], b, paramMap, usedVars, callComment, pos); err != nil {
								return err
							}
						}
						bindIdx++
					}
				}
			}

		case *AssignStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := inheritComment(s.Comment, comment)
			if err := p.emitExprTo(s.Value, target, b, paramMap, usedVars, callComment, pos); err != nil {
				return err
			}

		case *CompoundAssignStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := inheritComment(s.Comment, comment)
			rhs, err := p.emitExprGetValue(s.Value, b, paramMap, usedVars, "", pos)
			if err != nil {
				return err
			}
			f := map[string]any{
				"op": arithOpName(s.Op),
				"0":  target,
				"1":  rhs,
				"2":  target,
			}
			setComment(f, callComment)
			b.emit(f)

		case *IncrDecrStmt:
			target := resolveVarName(s.Target, paramMap)
			callComment := inheritComment(s.Comment, comment)
			op := "add"
			if s.Op == tokMinusMinus {
				op = "sub"
			}
			f := map[string]any{
				"op": op,
				"0":  target,
				"1":  map[string]any{"num": 1},
				"2":  target,
			}
			setComment(f, callComment)
			b.emit(f)

		case *IfStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitIfStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *WhileStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitWhileStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *LoopStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitLoopStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *ForStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitForStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *WaitStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitWaitStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *AssertStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitAssertStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *BreakStmt:
			if len(s.Values) > 0 {
				if p.breakRetVals == nil {
					return fmt.Errorf("break with value outside of expression block")
				}
				if len(s.Values) != len(p.breakRetVals) {
					return fmt.Errorf("break has %d value(s) but expression block expects %d", len(s.Values), len(p.breakRetVals))
				}
				for i, val := range s.Values {
					if err := p.emitExprTo(val, p.breakRetVals[i], b, paramMap, usedVars, s.Comment, pos); err != nil {
						return err
					}
				}
			}
			// Emit placeholder frame that the enclosing loop emitter will patch
			if s.CrossBoundary {
				b.emit(map[string]any{"op": "@jumpbreak", "label": s.Label})
			} else {
				f := map[string]any{"op": "@break"}
				if s.Label != "" {
					f["label"] = s.Label
				}
				b.emit(f)
			}

		case *YieldBodyStmt:
			bodyStart := b.pos()
			// Use emitFnBodyCore (no pre-scan) — the caller body references
			// behavior-level variables that must not be renamed.
			if err := p.emitFnBodyCore(s.Body, b, paramMap, usedVars, comment, pos); err != nil {
				return err
			}
			// If the body contains @continue placeholders, emit a bridge noop
			// and patch them to jump to it. Inside a loop, the loop emitter
			// will set next:false on the bridge (as the last body frame),
			// giving correct re-dispatch. At top level, the bridge falls
			// through sequentially to the next iterator statement.
			if hasContinuePlaceholder(b, bodyStart) {
				bridge := b.emit(map[string]any{"op": "set_reg", "0": false, "1": false})
				patchContinuePlaceholders(b, bodyStart, frameRef(bridge))
			}

		case *ContinueStmt:
			b.emit(map[string]any{"op": "@continue"})

		case *ExitStmt:
			callComment := inheritComment(s.Comment, comment)
			f := map[string]any{"op": "exit"}
			setComment(f, callComment)
			b.emit(f)

		case *RestartStmt:
			callComment := inheritComment(s.Comment, comment)
			f := map[string]any{"op": "restart"}
			setComment(f, callComment)
			b.emit(f)

		case *LabelStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitLabelStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *JumpStmt:
			callComment := inheritComment(s.Comment, comment)
			ctx := p.fnEmitCtx(b, paramMap, usedVars, comment, pos)
			if err := p.emitJumpStmt(s, ctx, callComment); err != nil {
				return err
			}

		case *LastStmt:
			callComment := inheritComment(s.Comment, comment)
			f := map[string]any{"op": "last"}
			setComment(f, callComment)
			b.emit(f)

		case *CallBehaviorStmt:
			callComment := inheritComment(s.Comment, comment)
			if err := p.emitFnBodyCallBehavior(s, b, paramMap, usedVars, callComment); err != nil {
				return err
			}

		case *OnEventStmt:
			// Defer event emission until after main flow.
			// Resolve the param through paramMap for inlined function context.
			b.deferredEvents = append(b.deferredEvents, deferredEvent{
				stmt:            s,
				paramMap:        maps.Clone(paramMap),
				frameAtDeferral: frameRef(b.pos()),
			})

		case *ReturnStmt:
			callComment := inheritComment(s.Comment, comment)

			if s.Continuation != "" {
				// Emit data args to @carg registers before the jump
				for i, arg := range s.ContinuationArgs {
					target := resolveVarName("@carg"+strconv.Itoa(i+1), paramMap)
					if err := p.emitExprTo(arg, target, b, paramMap, usedVars, callComment, pos); err != nil {
						return err
					}
				}
				// Continuation dispatch: emit @exec_<name> placeholder
				b.emit(map[string]any{
					"op":   "set_reg",
					"0":    false,
					"1":    false,
					"next": "@exec_" + s.Continuation,
				})
			} else {
				// Emit values to @retK targets, then emit @return jump placeholder
				retOffset := 0
				for _, val := range s.Values {
					switch e := val.(type) {
					case *CallExpr:
						callee := p.fns[e.Name]
						rc := callee.returnCount()
						retVals := make([]any, rc)
						for j := 0; j < rc; j++ {
							retVals[j] = resolveVarName("@ret"+strconv.Itoa(retOffset+j+1), paramMap)
						}
						resolvedArgs, resolvedKwArgs, err := p.emitCallExprArgs(e.Args, e.KwArgs, b, paramMap, usedVars, pos)
						if err != nil {
							return err
						}
						if err := p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, retVals, b, pos, callComment, usedVars); err != nil {
							return err
						}
						retOffset += rc
					case *InstructionExpr:
						rc := frameReturnCount(e.Frame)
						retVals := make([]any, rc)
						for j := 0; j < rc; j++ {
							retVals[j] = resolveVarName("@ret"+strconv.Itoa(retOffset+j+1), paramMap)
						}
						if e.Blocks != nil {
							if err := p.emitFnBodyInstructionExprWithBlocks(e, retVals, b, paramMap, usedVars, callComment, pos); err != nil {
								return err
							}
						} else {
							resolved := resolveInstructionFrame(e.Frame, retVals, paramMap, nil, callComment)
							b.emit(resolved)
						}
						retOffset += rc
					default:
						target := resolveVarName("@ret"+strconv.Itoa(retOffset+1), paramMap)
						if err := p.emitExprTo(val, target, b, paramMap, usedVars, callComment, pos); err != nil {
							return err
						}
						retOffset++
					}
				}
				// Zero remaining @retK slots
				totalRets := 0
				for k := range paramMap {
					if strings.HasPrefix(k, "@ret") {
						n, err := strconv.Atoi(k[4:])
						if err == nil && n > totalRets {
							totalRets = n
						}
					}
				}
				for i := retOffset + 1; i <= totalRets; i++ {
					target := resolveVarName("@ret"+strconv.Itoa(i), paramMap)
					f := map[string]any{"op": "set_reg", "0": false, "1": target}
					b.emit(f)
				}
				// Emit @return jump placeholder
				b.emit(map[string]any{
					"op": "@return",
				})
			}
		}
	}
	return nil
}
