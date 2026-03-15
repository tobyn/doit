package compiler

import (
	"reflect"
	"strconv"
	"strings"
)

// --- Compile-time constants ---

// parseConstDecl parses a const declaration after the `const` keyword.
// Syntax: const <name> = <expr>
// Returns the constant name.
func (p *parser) parseConstDecl(private bool) (string, error) {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return "", err
	}
	name := nameTok.val
	if Keywords[name] {
		return "", p.errorf(nameTok.pos, "%q is a reserved keyword and cannot be used as a constant name", name)
	}
	if name == "_" {
		return "", p.errorf(nameTok.pos, "'_' cannot be used as a constant name")
	}
	if err := p.checkDeclName(name, "constant", nameTok.pos); err != nil {
		return "", err
	}
	if _, err := p.expect(tokEquals); err != nil {
		return "", err
	}

	// Peek to see if the RHS is a string or localize block (these can't go through parseBoolExpr)
	peek, err := p.next()
	if err != nil {
		return "", err
	}

	var val any
	if peek.kind == tokString {
		val = peek.val
	} else if peek.kind == tokIdent && peek.val == "localize" {
		resolved, err := p.parseLocalize()
		if err != nil {
			return "", err
		}
		val = resolved
	} else {
		p.unget(peek)

		// Parse expression using a const-only resolver
		constResolver := func(tok token) (Expr, error) {
			if strings.HasPrefix(tok.val, "$") {
				if reg, ok := unitRegisters[tok.val]; ok {
					return &LiteralExpr{Value: reg}, nil
				}
				return nil, p.errorf(tok.pos, "unknown unit register %q", tok.val)
			}
			if c, ok := p.consts[tok.val]; ok {
				return &LiteralExpr{Value: c.value}, nil
			}
			return nil, p.errorf(tok.pos, "%q is not a compile-time constant", tok.val)
		}
		// Enable function calls in const expressions
		savedCallExprParser := p.callExprParser
		p.callExprParser = func(callee *fnDef, calleeTok token) (Expr, error) {
			args, kwArgs, err := p.parseConstCallArgs(callee, calleeTok, constResolver)
			if err != nil {
				return nil, err
			}
			return &CallExpr{Name: calleeTok.val, Args: args, KwArgs: kwArgs}, nil
		}
		defer func() { p.callExprParser = savedCallExprParser }()

		expr, err := p.parseBoolExpr(constResolver)
		if err != nil {
			return "", err
		}

		// Unwrap TruthyExpr for plain arithmetic results (same pattern as
		// parseBhvVarInit and parseFnBodyRHSExpr)
		if te, ok := expr.(*TruthyExpr); ok {
			expr = te.Value
		}

		// Check for & operator after the expression
		ampTok, err := p.next()
		if err != nil {
			return "", err
		}
		if ampTok.kind == tokAmpersand {
			numExpr, err := p.parseBoolExpr(constResolver)
			if err != nil {
				return "", err
			}
			if te, ok := numExpr.(*TruthyExpr); ok {
				numExpr = te.Value
			}
			expr = &AmpersandExpr{Value: expr, Num: numExpr}
		} else {
			p.unget(ampTok)
		}

		// Evaluate the expression using the compile-time evaluator
		p.evalStepLimit = 10000
		result, ok := p.tryEvalExpr(expr, nil)
		if !ok {
			return "", p.errorf(nameTok.pos, "expression is not compile-time evaluable")
		}
		val = result
	}

	p.consts[name] = &constDef{value: val, private: private}
	return name, nil
}

// parseEnumDecl parses an enum declaration: enum Name { Member1; Member2 = 5; Member3 }
func (p *parser) parseEnumDecl(private bool) (string, error) {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return "", err
	}
	name := nameTok.val
	if Keywords[name] {
		return "", p.errorf(nameTok.pos, "%q is a reserved keyword and cannot be used as an enum name", name)
	}
	if name == "_" {
		return "", p.errorf(nameTok.pos, "'_' cannot be used as an enum name")
	}
	if err := p.checkDeclName(name, "enum", nameTok.pos); err != nil {
		return "", err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return "", err
	}

	values := map[string]int{}
	usedValues := map[int]string{} // value -> member name (for duplicate value detection)
	var members []string
	nextVal := 0

	for {
		tok, err := p.next()
		if err != nil {
			return "", err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind != tokIdent {
			return "", p.errorf(tok.pos, "expected enum member name, got %s", tok.describe())
		}
		memberName := tok.val
		if Keywords[memberName] {
			return "", p.errorf(tok.pos, "%q is a reserved keyword and cannot be used as an enum member name", memberName)
		}
		if _, exists := values[memberName]; exists {
			return "", p.errorf(tok.pos, "duplicate enum member %q in %s", memberName, name)
		}

		// Check for explicit value assignment
		peek, err := p.next()
		if err != nil {
			return "", err
		}
		if peek.kind == tokEquals {
			// Explicit value: Member = N or Member = -N
			valTok, err := p.next()
			if err != nil {
				return "", err
			}
			negative := false
			if valTok.kind == tokMinus {
				negative = true
				valTok, err = p.next()
				if err != nil {
					return "", err
				}
			}
			if valTok.kind != tokNumber {
				return "", p.errorf(valTok.pos, "expected number for enum member value, got %s", valTok.describe())
			}
			num, _ := strconv.Atoi(valTok.val)
			if negative {
				num = -num
			}
			nextVal = num
		} else {
			p.unget(peek)
		}

		// Check for duplicate value
		if prevMember, exists := usedValues[nextVal]; exists {
			return "", p.errorf(tok.pos, "enum member %q has the same value (%d) as %q in %s", memberName, nextVal, prevMember, name)
		}

		values[memberName] = nextVal
		usedValues[nextVal] = memberName
		members = append(members, memberName)
		nextVal++

		// Optional comma separator between members.
		sep, err := p.next()
		if err != nil {
			return "", err
		}
		if sep.kind != tokComma {
			p.unget(sep)
		}
	}

	if len(members) == 0 {
		return "", p.errorf(nameTok.pos, "enum %q has no members", name)
	}

	p.enums[name] = &enumDef{values: values, members: members, private: private}
	return name, nil
}

// --- Compile-time evaluator ---
//
// tryEvalExpr, tryEvalCall, and tryEvalStmts form a compile-time evaluator
// that can trace through function calls. The evaluator bails (returns false)
// when it encounters runtime-only constructs like the instruction intrinsic
// or wait statements. It uses p.evalStepLimit as a safety limit.

// constEvalStatus signals non-normal completion from tryEvalStmts.
type constEvalStatus struct {
	returned   bool
	retVals    []any
	broke      bool
	breakLabel string
	continued  bool
}

// tryEvalExpr evaluates an expression at compile time.
// env holds local variable bindings (nil for top-level const expressions).
// Returns (value, true) on success, (nil, false) on bail.
func (p *parser) tryEvalExpr(expr Expr, env map[string]any) (any, bool) {
	switch e := expr.(type) {
	case *LiteralExpr:
		return e.Value, true
	case *IdentExpr:
		if env != nil {
			if val, ok := env[e.Name]; ok {
				return val, true
			}
		}
		return nil, false
	case *ArithExpr:
		lhs, ok := p.tryEvalExpr(e.LHS, env)
		if !ok {
			return nil, false
		}
		rhs, ok := p.tryEvalExpr(e.RHS, env)
		if !ok {
			return nil, false
		}
		lNum, lOk := extractNum(lhs)
		rNum, rOk := extractNum(rhs)
		if !lOk || !rOk {
			return nil, false
		}
		var result int
		switch e.Op {
		case tokPlus:
			result = lNum + rNum
		case tokMinus:
			result = lNum - rNum
		case tokStar:
			result = lNum * rNum
		case tokSlash:
			if rNum == 0 {
				return nil, false
			}
			result = lNum / rNum
		case tokPercent:
			if rNum == 0 {
				return nil, false
			}
			result = lNum % rNum
		default:
			return nil, false
		}
		return map[string]any{"num": result}, true
	case *ConstructorExpr:
		// Evaluate constructor args first (they may reference env vars)
		resolved := &ConstructorExpr{TypeName: e.TypeName, Args: make([]Expr, len(e.Args))}
		for i, arg := range e.Args {
			val, ok := p.tryEvalExpr(arg, env)
			if !ok {
				return nil, false
			}
			resolved.Args[i] = &LiteralExpr{Value: val}
		}
		val, ok := tryResolveConstructorLiteral(resolved)
		if !ok {
			return nil, false
		}
		return val, true
	case *AmpersandExpr:
		// Evaluate both sides first
		lhs, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		rhs, ok := p.tryEvalExpr(e.Num, env)
		if !ok {
			return nil, false
		}
		resolved := &AmpersandExpr{
			Value: &LiteralExpr{Value: lhs},
			Num:   &LiteralExpr{Value: rhs},
		}
		val, ok := tryResolveAmpersandLiteral(resolved)
		if !ok {
			return nil, false
		}
		return val, true
	case *DotAccessExpr:
		inner, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		resolved := &DotAccessExpr{
			Value: &LiteralExpr{Value: inner},
			Field: e.Field,
		}
		val, ok := tryResolveDotAccessLiteral(resolved)
		if !ok {
			return nil, false
		}
		return val, true
	case *CompareExpr:
		lhs, ok := p.tryEvalExpr(e.LHS, env)
		if !ok {
			return nil, false
		}
		rhs, ok := p.tryEvalExpr(e.RHS, env)
		if !ok {
			return nil, false
		}
		if evalCompare(e.Op, lhs, rhs) {
			return map[string]any{"num": 1}, true
		}
		return false, true
	case *BoolChainExpr:
		if e.Op == tokDoubleAmpersand {
			for _, child := range e.Children {
				val, ok := p.tryEvalExpr(child, env)
				if !ok {
					return nil, false
				}
				if !isTruthy(val) {
					return false, true
				}
			}
			return map[string]any{"num": 1}, true
		}
		// ||
		for _, child := range e.Children {
			val, ok := p.tryEvalExpr(child, env)
			if !ok {
				return nil, false
			}
			if isTruthy(val) {
				return map[string]any{"num": 1}, true
			}
		}
		return false, true
	case *NotExpr:
		inner, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		if isTruthy(inner) {
			return false, true
		}
		return map[string]any{"num": 1}, true
	case *TruthyExpr:
		inner, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		if isTruthy(inner) {
			return map[string]any{"num": 1}, true
		}
		return false, true
	case *TypeCheckExpr:
		val, ok := p.tryEvalExpr(e.Value, env)
		if !ok {
			return nil, false
		}
		if evalTypeCheck(val, e.TypeSlot) {
			return map[string]any{"num": 1}, true
		}
		return false, true
	case *CallExpr:
		fn := p.fns[e.Name]
		if fn == nil {
			return nil, false
		}
		// Evaluate positional args
		posArgs, kwArgs, ok := p.tryEvalCallArgs(e.Args, e.KwArgs, env)
		if !ok {
			return nil, false
		}
		retVals, ok := p.tryEvalCall(fn, posArgs, kwArgs)
		if !ok {
			return nil, false
		}
		if len(retVals) == 0 {
			return false, true
		}
		return retVals[0], true
	case *ModeBlockExpr:
		// Mode is irrelevant at compile time; just eval body + tail
		status, ok := p.tryEvalStmts(e.Body, env)
		if !ok {
			return nil, false
		}
		if status != nil {
			return nil, false // unexpected return/break in mode block body
		}
		return p.tryEvalExpr(e.Tail, env)
	case *IfExpr:
		cond, ok := p.tryEvalExpr(e.Cond, env)
		if !ok {
			return nil, false
		}
		if isTruthy(cond) {
			status, ok := p.tryEvalStmts(e.Body, env)
			if !ok {
				return nil, false
			}
			if status != nil {
				return nil, false
			}
			return p.tryEvalExpr(e.Tail, env)
		}
		for _, elif := range e.ElseIfs {
			cond, ok := p.tryEvalExpr(elif.Cond, env)
			if !ok {
				return nil, false
			}
			if isTruthy(cond) {
				status, ok := p.tryEvalStmts(elif.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					return nil, false
				}
				return p.tryEvalExpr(elif.Tail, env)
			}
		}
		if e.ElsTail != nil {
			status, ok := p.tryEvalStmts(e.ElsBody, env)
			if !ok {
				return nil, false
			}
			if status != nil {
				return nil, false
			}
			return p.tryEvalExpr(e.ElsTail, env)
		}
		return false, true // no else -> null
	case *ExprListExpr:
		// Evaluate each expression; collect results
		var results []any
		for _, sub := range e.Exprs {
			val, ok := p.tryEvalExpr(sub, env)
			if !ok {
				return nil, false
			}
			results = append(results, val)
		}
		if len(results) == 1 {
			return results[0], true
		}
		return results, true
	case *InstructionExpr:
		return nil, false // bail: runtime-only
	default:
		return nil, false
	}
}

// tryEvalCallArgs evaluates positional and keyword arguments at compile time.
// Returns (posArgs, kwArgs, true) on success, (nil, nil, false) if any arg bails.
func (p *parser) tryEvalCallArgs(args []Expr, kwArgExprs map[string]Expr, env map[string]any) ([]any, map[string]any, bool) {
	posArgs := make([]any, len(args))
	for i, arg := range args {
		val, ok := p.tryEvalExpr(arg, env)
		if !ok {
			return nil, nil, false
		}
		posArgs[i] = val
	}
	var kwArgs map[string]any
	if len(kwArgExprs) > 0 {
		kwArgs = make(map[string]any, len(kwArgExprs))
		for k, v := range kwArgExprs {
			val, ok := p.tryEvalExpr(v, env)
			if !ok {
				return nil, nil, false
			}
			kwArgs[k] = val
		}
	}
	return posArgs, kwArgs, true
}

// tryEvalCall evaluates a function call at compile time.
// Returns (returnValues, true) on success, (nil, false) on bail.
func (p *parser) tryEvalCall(fn *fnDef, posArgs []any, kwArgs map[string]any) ([]any, bool) {
	if fn.frame != nil {
		return nil, false // instruction-based function -> bail
	}
	if fn.astBody == nil {
		return nil, false // no body -> bail
	}

	// Build environment from params + args
	env := map[string]any{}
	posIdx := 0
	for _, param := range fn.params {
		if param.keyword == "" {
			if posIdx < len(posArgs) {
				env[param.name] = posArgs[posIdx]
			} else {
				env[param.name] = false
			}
			posIdx++
		} else {
			if kwArgs != nil {
				if val, ok := kwArgs[param.keyword]; ok {
					env[param.name] = val
				} else {
					env[param.name] = false
				}
			} else {
				env[param.name] = false
			}
		}
	}

	// Merge transitive function and iterator scope (same pattern as expandCall)
	var savedFns map[string]*fnDef
	if fn.scope != nil {
		savedFns = map[string]*fnDef{}
		for name, def := range fn.scope {
			if _, exists := p.fns[name]; !exists {
				p.fns[name] = def
				savedFns[name] = def
			}
		}
	}
	var savedIters map[string]*iterDef
	if fn.iterScope != nil {
		savedIters = map[string]*iterDef{}
		for name, def := range fn.iterScope {
			if _, exists := p.iters[name]; !exists {
				p.iters[name] = def
				savedIters[name] = def
			}
		}
	}

	status, ok := p.tryEvalStmts(fn.astBody, env)

	// Restore function and iterator scope
	if savedFns != nil {
		for name := range savedFns {
			delete(p.fns, name)
		}
	}
	if savedIters != nil {
		for name := range savedIters {
			delete(p.iters, name)
		}
	}

	if !ok {
		return nil, false
	}

	// Extract return values
	if status != nil && status.returned {
		return status.retVals, true
	}

	// No explicit return -- extract from rets
	if fn.rets != nil {
		retVals := make([]any, len(fn.rets))
		for i, name := range fn.rets {
			if val, ok := env[name]; ok {
				retVals[i] = val
			} else {
				retVals[i] = false
			}
		}
		return retVals, true
	}

	return nil, true
}

// tryEvalStmts evaluates a list of statements at compile time.
// Returns (nil, true) for normal completion, (*constEvalStatus, true) for
// return/break, and (nil, false) for bail.
func (p *parser) tryEvalStmts(stmts []Stmt, env map[string]any) (*constEvalStatus, bool) {
	for _, stmt := range stmts {
		p.evalStepLimit--
		if p.evalStepLimit <= 0 {
			return nil, false // step limit exceeded
		}
		switch s := stmt.(type) {
		case *LetStmt:
			val, ok := p.tryEvalExpr(s.Value, env)
			if !ok {
				return nil, false
			}
			env[s.Name] = val
		case *AssignStmt:
			val, ok := p.tryEvalExpr(s.Value, env)
			if !ok {
				return nil, false
			}
			env[s.Target] = val
		case *CompoundAssignStmt:
			rhs, ok := p.tryEvalExpr(s.Value, env)
			if !ok {
				return nil, false
			}
			lhs, ok := env[s.Target]
			if !ok {
				return nil, false
			}
			lNum, lOk := extractNum(lhs)
			rNum, rOk := extractNum(rhs)
			if !lOk || !rOk {
				return nil, false
			}
			var result int
			switch s.Op {
			case tokPlusEquals:
				result = lNum + rNum
			case tokMinusEquals:
				result = lNum - rNum
			case tokStarEquals:
				result = lNum * rNum
			case tokSlashEquals:
				if rNum == 0 {
					return nil, false
				}
				result = lNum / rNum
			case tokPercentEquals:
				if rNum == 0 {
					return nil, false
				}
				result = lNum % rNum
			default:
				return nil, false
			}
			env[s.Target] = map[string]any{"num": result}
		case *IncrDecrStmt:
			lhs, ok := env[s.Target]
			if !ok {
				return nil, false
			}
			lNum, lOk := extractNum(lhs)
			if !lOk {
				return nil, false
			}
			if s.Op == tokPlusPlus {
				env[s.Target] = map[string]any{"num": lNum + 1}
			} else {
				env[s.Target] = map[string]any{"num": lNum - 1}
			}
		case *MultiReturnStmt:
			// Evaluate RHS
			switch rv := s.Value.(type) {
			case *CallExpr:
				fn := p.fns[rv.Name]
				if fn == nil {
					return nil, false
				}
				posArgs, kwArgs, ok := p.tryEvalCallArgs(rv.Args, rv.KwArgs, env)
				if !ok {
					return nil, false
				}
				retVals, ok := p.tryEvalCall(fn, posArgs, kwArgs)
				if !ok {
					return nil, false
				}
				for i, b := range s.Bindings {
					if b.Discard {
						continue
					}
					if i < len(retVals) {
						env[b.Name] = retVals[i]
					} else {
						env[b.Name] = false
					}
				}
			case *ExprListExpr:
				idx := 0
				for _, e := range rv.Exprs {
					val, ok := p.tryEvalExpr(e, env)
					if !ok {
						return nil, false
					}
					if idx < len(s.Bindings) {
						b := s.Bindings[idx]
						if !b.Discard {
							env[b.Name] = val
						}
						idx++
					}
				}
			default:
				return nil, false
			}
		case *CallStmt:
			fn := p.fns[s.Name]
			if fn == nil {
				return nil, false
			}
			posArgs, kwArgs, ok := p.tryEvalCallArgs(s.Args, s.KwArgs, env)
			if !ok {
				return nil, false
			}
			_, ok = p.tryEvalCall(fn, posArgs, kwArgs)
			if !ok {
				return nil, false
			}
		case *ReturnStmt:
			var retVals []any
			for _, v := range s.Values {
				val, ok := p.tryEvalExpr(v, env)
				if !ok {
					return nil, false
				}
				retVals = append(retVals, val)
			}
			return &constEvalStatus{returned: true, retVals: retVals}, true
		case *IfStmt:
			cond, ok := p.tryEvalExpr(s.Cond, env)
			if !ok {
				return nil, false
			}
			if isTruthy(cond) {
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					return status, true
				}
				continue
			}
			matched := false
			for _, elif := range s.ElseIfs {
				cond, ok := p.tryEvalExpr(elif.Cond, env)
				if !ok {
					return nil, false
				}
				if isTruthy(cond) {
					status, ok := p.tryEvalStmts(elif.Body, env)
					if !ok {
						return nil, false
					}
					if status != nil {
						return status, true
					}
					matched = true
					break
				}
			}
			if !matched && s.Else != nil {
				status, ok := p.tryEvalStmts(s.Else, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					return status, true
				}
			}
		case *LoopStmt:
			if s.Count == nil {
				return nil, false // infinite loop -> bail
			}
			countVal, ok := p.tryEvalExpr(s.Count, env)
			if !ok {
				return nil, false
			}
			countNum, cOk := extractNum(countVal)
			if !cOk || countNum < 0 {
				return nil, false
			}
			for i := 0; i < countNum; i++ {
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					if status.broke {
						if status.breakLabel == "" || status.breakLabel == s.Label {
							break // break consumed
						}
						return status, true // propagate labeled break
					}
					if status.continued {
						continue // restart iteration
					}
					if status.returned {
						return status, true
					}
				}
			}
		case *WhileStmt:
			for {
				p.evalStepLimit--
				if p.evalStepLimit <= 0 {
					return nil, false
				}
				cond, ok := p.tryEvalExpr(s.Cond, env)
				if !ok {
					return nil, false
				}
				if !isTruthy(cond) {
					break
				}
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					if status.broke {
						if status.breakLabel == "" || status.breakLabel == s.Label {
							break
						}
						return status, true
					}
					if status.continued {
						continue // restart iteration (re-evaluate condition)
					}
					if status.returned {
						return status, true
					}
				}
			}
		case *ForStmt:
			rangeVal, ok := p.tryEvalExpr(s.Range, env)
			if !ok {
				return nil, false
			}
			// Extract range parts from coordinate+number composite
			rm, ok := rangeVal.(map[string]any)
			if !ok {
				return nil, false
			}
			coord, ok := rm["coord"].(map[string]any)
			if !ok {
				return nil, false
			}
			start, ok := coord["x"].(int)
			if !ok {
				return nil, false
			}
			stop, ok := coord["y"].(int)
			if !ok {
				return nil, false
			}
			step := 1
			if n, ok := rm["num"]; ok {
				if s, ok := n.(int); ok && s != 0 {
					step = s
				}
			}
			for i := start; (step > 0 && i < stop) || (step < 0 && i > stop); i += step {
				p.evalStepLimit--
				if p.evalStepLimit <= 0 {
					return nil, false
				}
				env[s.IterVars[0]] = map[string]any{"num": i}
				status, ok := p.tryEvalStmts(s.Body, env)
				if !ok {
					return nil, false
				}
				if status != nil {
					if status.broke {
						if status.breakLabel == "" || status.breakLabel == s.Label {
							break
						}
						return status, true
					}
					if status.continued {
						continue // restart iteration
					}
					if status.returned {
						return status, true
					}
				}
			}
		case *BreakStmt:
			return &constEvalStatus{broke: true, breakLabel: s.Label}, true
		case *ContinueStmt:
			return &constEvalStatus{continued: true}, true
		case *ExitStmt:
			return nil, false // bail: runtime-only
		case *RestartStmt:
			return nil, false // bail: runtime-only
		case *LabelStmt:
			return nil, false // bail: runtime-only
		case *JumpStmt:
			return nil, false // bail: runtime-only
		case *LastStmt:
			return nil, false // bail: runtime-only
		case *ModeBlockStmt:
			// Mode is irrelevant at compile time
			status, ok := p.tryEvalStmts(s.Body, env)
			if !ok {
				return nil, false
			}
			if status != nil {
				return status, true
			}
		case *InstructionStmt:
			return nil, false // bail: runtime-only
		case *WaitStmt:
			return nil, false // bail: runtime-only
		case *AssertStmt:
			return nil, false // bail: runtime-only
		default:
			return nil, false
		}
	}
	return nil, true // normal completion
}

// parseConstCallArgs parses function call arguments in a const expression context.
func (p *parser) parseConstCallArgs(callee *fnDef, calleeTok token, constResolver operandResolver) ([]Expr, map[string]Expr, error) {
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

	posCount := callee.positionalCount()
	args := make([]Expr, posCount)
	for i := 0; i < posCount; i++ {
		if i > 0 {
			if paren {
				if _, err := p.expect(tokComma); err != nil {
					return nil, nil, err
				}
			} else {
				sep, err := p.next()
				if err != nil {
					return nil, nil, err
				}
				if sep.kind != tokComma {
					p.unget(sep)
				}
			}
		}

		// Skip direction annotations (irrelevant for compile-time eval)
		dirTok, err := p.next()
		if err != nil {
			return nil, nil, err
		}
		if !(dirTok.kind == tokIdent && isDirection(dirTok.val)) {
			p.unget(dirTok)
		}

		val, err := p.parseConstArgExpr(constResolver)
		if err != nil {
			return nil, nil, err
		}
		args[i] = val
	}

	// Parse optional keyword args
	var kwArgs map[string]Expr
	peek, err = p.next()
	if err != nil {
		return nil, nil, err
	}
	if peek.kind == tokComma && callee.positionalCount() < len(callee.params) {
		kwArgs = map[string]Expr{}
		for {
			dirOrKw, err := p.expect(tokIdent)
			if err != nil {
				return nil, nil, err
			}
			if isDirection(dirOrKw.val) {
				dirOrKw, err = p.expect(tokIdent)
				if err != nil {
					return nil, nil, err
				}
			}
			kw := callee.keywordByName(dirOrKw.val)
			if kw == nil {
				return nil, nil, p.errorf(dirOrKw.pos, "unknown keyword argument %q", dirOrKw.val)
			}
			if _, exists := kwArgs[dirOrKw.val]; exists {
				return nil, nil, p.errorf(dirOrKw.pos, "duplicate keyword argument %q", dirOrKw.val)
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, nil, err
			}
			val, err := p.parseConstArgExpr(constResolver)
			if err != nil {
				return nil, nil, err
			}
			kwArgs[dirOrKw.val] = val

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

// parseConstArgExpr parses a single argument in a const expression context.
func (p *parser) parseConstArgExpr(constResolver operandResolver) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind == tokString {
		return &LiteralExpr{Value: tok.val}, nil
	}
	if tok.kind == tokIdent && tok.val == "localize" {
		resolved, err := p.parseLocalize()
		if err != nil {
			return nil, err
		}
		return &LiteralExpr{Value: resolved}, nil
	}
	p.unget(tok)
	expr, err := p.parseArithExpr(constResolver)
	if err != nil {
		return nil, err
	}
	// Check for & operator
	ampTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if ampTok.kind == tokAmpersand {
		numExpr, err := p.parseArithExpr(constResolver)
		if err != nil {
			return nil, err
		}
		return &AmpersandExpr{Value: expr, Num: numExpr}, nil
	}
	p.unget(ampTok)
	return expr, nil
}

// extractNum extracts the integer from a compile-time value.
func extractNum(val any) (int, bool) {
	if m, ok := val.(map[string]any); ok {
		if n, ok := m["num"]; ok {
			if i, ok := n.(int); ok {
				return i, true
			}
		}
	}
	return 0, false
}

// isTruthy checks if a compile-time value is truthy (non-false/non-null).
func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return true // maps, ints, strings are truthy
}

// evalCompare evaluates a comparison between two compile-time values.
func evalCompare(op tokenKind, lhs, rhs any) bool {
	if isEqualityOp(op) {
		eq := reflect.DeepEqual(lhs, rhs)
		if op == tokDoubleEquals {
			return eq
		}
		return !eq
	}
	lNum, lOk := extractNum(lhs)
	rNum, rOk := extractNum(rhs)
	if !lOk || !rOk {
		return false
	}
	switch op {
	case tokGreater:
		return lNum > rNum
	case tokGreaterEquals:
		return lNum >= rNum
	case tokLess:
		return lNum < rNum
	case tokLessEquals:
		return lNum <= rNum
	}
	return false
}

// evalTypeCheck checks whether a compile-time value matches a type slot.
func evalTypeCheck(val any, typeSlot string) bool {
	m, ok := val.(map[string]any)
	if !ok {
		return false
	}
	if _, hasID := m["id"]; hasID {
		id := m["id"].(string)
		switch typeSlot {
		case valueTypeItem:
			return !strings.HasPrefix(id, "c_") && !strings.HasPrefix(id, "t_") && !strings.HasPrefix(id, "v_")
		case valueTypeComp:
			return strings.HasPrefix(id, "c_")
		case valueTypeTech:
			return strings.HasPrefix(id, "t_")
		case valueTypeValue:
			return strings.HasPrefix(id, "v_")
		}
		return false
	}
	if _, hasCoord := m["coord"]; hasCoord {
		return typeSlot == valueTypeCoord
	}
	return false
}
