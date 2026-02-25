package compiler

// bhvast.go — Behavior-level AST parsing and emission (Phase 2).
//
// Expression parsers return Expr nodes (no frame emission).
// Statement parsers return Stmt/[]Stmt nodes.
// The emitter (emitBehaviorStmts) walks []Stmt and emits frames.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// -----------------------------------------------------------------------
// Expression parsers → Expr nodes
// -----------------------------------------------------------------------

// parseBhvArithPrimary parses an arithmetic atom: number literal, null,
// variable, $register, or a parenthesized sub-expression.
func (p *parser) parseBhvArithPrimary(syms *symbolTable) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	switch tok.kind {
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		return &LiteralExpr{Value: map[string]any{"num": num}}, nil
	case tokLParen:
		val, err := p.parseBhvArithExpr(syms)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return val, nil
	case tokIdent:
		if tok.val == "null" {
			return &LiteralExpr{Value: false}, nil
		}
		return p.resolveBhvOperand(tok, syms)
	default:
		return nil, p.errorf(tok.pos, "expected number, variable, or '(' in arithmetic expression, got %s", tok.describe())
	}
}

// parseBhvArithTerm parses `primary (* | / primary)*`.
func (p *parser) parseBhvArithTerm(syms *symbolTable) (Expr, error) {
	lhs, err := p.parseBhvArithPrimary(syms)
	if err != nil {
		return nil, err
	}
	return p.parseBhvArithTermFrom(lhs, syms)
}

// parseBhvArithTermFrom parses `(* | / primary)*` from an already-parsed first.
func (p *parser) parseBhvArithTermFrom(first Expr, syms *symbolTable) (Expr, error) {
	result := first
	for {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if !isHighPriorityArithOp(peek.kind) {
			p.unget(peek)
			return result, nil
		}
		rhs, err := p.parseBhvArithPrimary(syms)
		if err != nil {
			return nil, err
		}
		result = &ArithExpr{Op: peek.kind, LHS: result, RHS: rhs}
	}
}

// parseBhvArithExpr parses `term (+ | - term)*`.
func (p *parser) parseBhvArithExpr(syms *symbolTable) (Expr, error) {
	lhs, err := p.parseBhvArithTerm(syms)
	if err != nil {
		return nil, err
	}
	return p.parseBhvArithExprFrom(lhs, syms)
}

// parseBhvArithExprFrom parses `(+ | - term)*` from an already-parsed first.
func (p *parser) parseBhvArithExprFrom(first Expr, syms *symbolTable) (Expr, error) {
	result := first
	for {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if !isLowPriorityArithOp(peek.kind) {
			p.unget(peek)
			return result, nil
		}
		rhs, err := p.parseBhvArithTerm(syms)
		if err != nil {
			return nil, err
		}
		result = &ArithExpr{Op: peek.kind, LHS: result, RHS: rhs}
	}
}

// parseBhvArithExprFromFull parses a full PEMDAS expression from an
// already-parsed first value.
func (p *parser) parseBhvArithExprFromFull(first Expr, syms *symbolTable) (Expr, error) {
	termResult, err := p.parseBhvArithTermFrom(first, syms)
	if err != nil {
		return nil, err
	}
	return p.parseBhvArithExprFrom(termResult, syms)
}

// resolveBhvOperand validates an identifier as readable and resolves it:
// $register → LiteralExpr{int}, $param → LiteralExpr{int}, else → IdentExpr.
func (p *parser) resolveBhvOperand(tok token, syms *symbolTable) (Expr, error) {
	if err := p.checkReadable(tok.val, syms, tok.pos); err != nil {
		return nil, err
	}
	if strings.HasPrefix(tok.val, "$") {
		if reg, ok := unitRegisters[tok.val]; ok {
			return &LiteralExpr{Value: reg}, nil
		}
		if idx, ok := syms.paramMap[tok.val]; ok {
			return &LiteralExpr{Value: idx}, nil
		}
		return nil, p.errorf(tok.pos, "unknown register %q", tok.val)
	}
	return &IdentExpr{Name: tok.val}, nil
}

// parseBhvBoolPrimary parses a single boolean term: parenthesized
// sub-expression, or value (with optional arithmetic) followed by
// comparison operator, 'is', or nothing (truthy check).
func (p *parser) parseBhvBoolPrimary(syms *symbolTable) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}

	if tok.kind == tokLParen {
		inner, err := p.parseBhvBoolExpr(syms)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return inner, nil
	}

	var lhs Expr
	if tok.kind == tokNumber {
		num, _ := strconv.Atoi(tok.val)
		val := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
		lhs, err = p.parseBhvArithExprFromFull(val, syms)
		if err != nil {
			return nil, err
		}
	} else if tok.kind == tokIdent {
		if tok.val == "null" {
			lhs = &LiteralExpr{Value: false}
		} else {
			resolved, err := p.resolveBhvOperand(tok, syms)
			if err != nil {
				return nil, err
			}
			lhs, err = p.parseBhvArithExprFromFull(resolved, syms)
			if err != nil {
				return nil, err
			}
		}
	} else {
		return nil, p.errorf(tok.pos, "expected identifier, number, or '(' in boolean expression, got %s", tok.describe())
	}

	cmpTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if cmpTok.kind == tokIdent && cmpTok.val == "is" {
		slot, err := p.parseIsRHS()
		if err != nil {
			return nil, err
		}
		return &TypeCheckExpr{Value: lhs, TypeSlot: slot}, nil
	}
	if isComparisonOp(cmpTok.kind) {
		rhs, err := p.parseBhvArithExpr(syms)
		if err != nil {
			return nil, err
		}
		return &CompareExpr{Op: cmpTok.kind, LHS: lhs, RHS: rhs}, nil
	}

	p.unget(cmpTok)
	return &TruthyExpr{Value: lhs}, nil
}

// parseBhvBoolExpr parses a complete boolean expression.
func (p *parser) parseBhvBoolExpr(syms *symbolTable) (Expr, error) {
	first, err := p.parseBhvBoolPrimary(syms)
	if err != nil {
		return nil, err
	}
	return p.parseBhvBoolChain(first, syms)
}

// parseBhvBoolChain peeks for &&/||. If absent, returns first unchanged.
func (p *parser) parseBhvBoolChain(first Expr, syms *symbolTable) (Expr, error) {
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek.kind != tokDoubleAmpersand && peek.kind != tokDoublePipe {
		p.unget(peek)
		return first, nil
	}

	chainOp := peek.kind
	children := []Expr{first}
	for {
		next, err := p.parseBhvBoolPrimary(syms)
		if err != nil {
			return nil, err
		}
		children = append(children, next)

		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind != tokDoubleAmpersand && tok.kind != tokDoublePipe {
			p.unget(tok)
			break
		}
		if tok.kind != chainOp {
			return nil, p.errorf(tok.pos, "cannot mix '&&' and '||' without parentheses; use '(' and ')' to group sub-expressions")
		}
	}

	return &BoolChainExpr{Op: chainOp, Children: children}, nil
}

// parseBhvArgExpr parses a single argument value into an Expr.
func (p *parser) parseBhvArgExpr(syms *symbolTable) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	var base Expr
	switch tok.kind {
	case tokString:
		base = &LiteralExpr{Value: tok.val}
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		val := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
		result, err := p.parseBhvArithExprFromFull(val, syms)
		if err != nil {
			return nil, err
		}
		base = result
	case tokIdent:
		if tok.val == "localize" {
			resolved, err := p.parseLocalize()
			if err != nil {
				return nil, err
			}
			base = &LiteralExpr{Value: resolved}
		} else if tok.val == "null" {
			base = &LiteralExpr{Value: false}
		} else if isConstructor(tok.val) {
			ctor, err := p.parseBhvConstructorExpr(tok, syms)
			if err != nil {
				return nil, err
			}
			base = ctor
		} else if strings.HasPrefix(tok.val, "$") {
			// Resolve $ without readability check — direction checks
			// happen later via checkCallDirections during emission.
			var resolved Expr
			if reg, ok := unitRegisters[tok.val]; ok {
				resolved = &LiteralExpr{Value: reg}
			} else if idx, ok := syms.paramMap[tok.val]; ok {
				resolved = &LiteralExpr{Value: idx}
			} else {
				return nil, p.errorf(tok.pos, "unknown register %q", tok.val)
			}
			result, err := p.parseBhvArithExprFromFull(resolved, syms)
			if err != nil {
				return nil, err
			}
			base = result
		} else {
			resolved := Expr(&IdentExpr{Name: tok.val})
			result, err := p.parseBhvArithExprFromFull(resolved, syms)
			if err != nil {
				return nil, err
			}
			base = result
		}
	default:
		return nil, p.errorf(tok.pos, "expected argument value, got %s", tok.describe())
	}

	// Check for & operator
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek.kind == tokAmpersand {
		numExpr, err := p.parseBhvArgExpr(syms)
		if err != nil {
			return nil, err
		}
		return &AmpersandExpr{Value: base, Num: numExpr}, nil
	}
	p.unget(peek)
	return base, nil
}

// parseBhvConstructorExpr parses a type constructor call into an Expr.
func (p *parser) parseBhvConstructorExpr(nameTok token, syms *symbolTable) (Expr, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return nil, p.errorf(nameTok.pos, "expected '(' after %s", nameTok.val)
	}
	switch nameTok.val {
	case "Item", "Component", "Technology", "Value":
		argTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if argTok.kind != tokString {
			return nil, p.errorf(argTok.pos, "expected string argument, got %s", argTok.describe())
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		base := Expr(&ConstructorExpr{
			TypeName: nameTok.val,
			Args:     []Expr{&LiteralExpr{Value: argTok.val}},
		})
		// Check for & operator
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokAmpersand {
			numExpr, err := p.parseBhvArgExpr(syms)
			if err != nil {
				return nil, err
			}
			return &AmpersandExpr{Value: base, Num: numExpr}, nil
		}
		p.unget(peek)
		return base, nil
	case "Coordinate":
		x, err := p.parseBhvArgExpr(syms)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokComma); err != nil {
			return nil, err
		}
		y, err := p.parseBhvArgExpr(syms)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		base := Expr(&ConstructorExpr{
			TypeName: "Coordinate",
			Args:     []Expr{x, y},
		})
		// Check for & operator
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokAmpersand {
			numExpr, err := p.parseBhvArgExpr(syms)
			if err != nil {
				return nil, err
			}
			return &AmpersandExpr{Value: base, Num: numExpr}, nil
		}
		p.unget(peek)
		return base, nil
	}
	return nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
}

// parseBhvCallArgs parses a function call's argument list into AST Exprs.
func (p *parser) parseBhvCallArgs(fn *fnDef, nameTok token, syms *symbolTable) ([]Expr, map[string]Expr, error) {
	posCount := fn.positionalCount()
	args := make([]Expr, posCount)
	for i := 0; i < posCount; i++ {
		if i > 0 {
			sep, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			if sep.kind != tokComma {
				p.unget(sep)
			}
		}

		dirTok, err := p.next()
		if err != nil {
			return nil, nil, err
		}
		annotation := ""
		annotationPos := dirTok.pos
		if dirTok.kind == tokIdent && isDirection(dirTok.val) {
			annotation = dirTok.val
		} else {
			p.unget(dirTok)
		}

		pd := fn.positionalParam(i)
		if err := p.checkCallAnnotation(annotation, pd, nameTok.val, annotationPos); err != nil {
			return nil, nil, err
		}

		val, err := p.parseBhvArgExpr(syms)
		if err != nil {
			return nil, nil, err
		}
		args[i] = val
	}

	// Parse optional keyword args
	var kwArgs map[string]Expr
	peek, err := p.next()
	if err != nil {
		return nil, nil, err
	}
	if (peek.kind == tokString || peek.kind == tokNumber) && fn.positionalCount() < len(fn.params) {
		return nil, nil, p.errorf(peek.pos,
			"too many positional arguments for %s (remaining parameters are keyword-only)", nameTok.val)
	}
	if peek.kind == tokComma {
		kwArgs = map[string]Expr{}
		for {
			dirOrKw, err := p.expect(tokIdent)
			if err != nil {
				return nil, nil, err
			}
			annotation := ""
			annotationPos := dirOrKw.pos
			if isDirection(dirOrKw.val) {
				annotation = dirOrKw.val
				dirOrKw, err = p.expect(tokIdent)
				if err != nil {
					return nil, nil, err
				}
			}

			kw := fn.keywordByName(dirOrKw.val)
			if kw == nil {
				return nil, nil, p.errorf(dirOrKw.pos, "unknown keyword argument %q", dirOrKw.val)
			}
			if _, exists := kwArgs[dirOrKw.val]; exists {
				return nil, nil, p.errorf(dirOrKw.pos, "duplicate keyword argument %q", dirOrKw.val)
			}
			if err := p.checkCallAnnotation(annotation, kw, nameTok.val, annotationPos); err != nil {
				return nil, nil, err
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, nil, err
			}
			val, err := p.parseBhvArgExpr(syms)
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

	return args, kwArgs, nil
}

// maybeBhvExprContinuation peeks for comparison/is/&&/|| after a value.
// Returns (expr, true) if continuation found, (original, false) otherwise.
func (p *parser) maybeBhvExprContinuation(valueExpr Expr, syms *symbolTable) (Expr, bool, error) {
	peek, err := p.next()
	if err != nil {
		return nil, false, err
	}
	if isComparisonOp(peek.kind) {
		rhs, err := p.parseBhvArithExpr(syms)
		if err != nil {
			return nil, false, err
		}
		cmp := Expr(&CompareExpr{Op: peek.kind, LHS: valueExpr, RHS: rhs})
		chained, err := p.parseBhvBoolChain(cmp, syms)
		if err != nil {
			return nil, false, err
		}
		return chained, true, nil
	}
	if peek.kind == tokIdent && peek.val == "is" {
		slot, err := p.parseIsRHS()
		if err != nil {
			return nil, false, err
		}
		tc := Expr(&TypeCheckExpr{Value: valueExpr, TypeSlot: slot})
		chained, err := p.parseBhvBoolChain(tc, syms)
		if err != nil {
			return nil, false, err
		}
		return chained, true, nil
	}
	if peek.kind == tokDoubleAmpersand || peek.kind == tokDoublePipe {
		p.unget(peek)
		truthy := Expr(&TruthyExpr{Value: valueExpr})
		chained, err := p.parseBhvBoolChain(truthy, syms)
		if err != nil {
			return nil, false, err
		}
		return chained, true, nil
	}
	p.unget(peek)
	return valueExpr, false, nil
}

// -----------------------------------------------------------------------
// Statement parsers → Stmt nodes
// -----------------------------------------------------------------------

// parseBhvVarInit parses the RHS of a var/let declaration after '='.
// May return multiple statements (e.g., fn call + boolean continuation).
func (p *parser) parseBhvVarInit(nameTok token, mutable bool, syms *symbolTable) ([]Stmt, error) {
	comment := p.docComment
	rhsTok, err := p.next()
	if err != nil {
		return nil, err
	}

	if rhsTok.kind == tokNumber {
		num, _ := strconv.Atoi(rhsTok.val)
		// Check for & after number (error)
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokAmpersand {
			return nil, p.errorf(peek.pos, "number literal cannot be left side of '&' (use a type constructor)")
		}
		p.unget(peek)

		numExpr := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
		result, err := p.parseBhvArithExprFromFull(numExpr, syms)
		if err != nil {
			return nil, err
		}

		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true

		final, handled, err := p.maybeBhvExprContinuation(result, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
		}

		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
	}

	if rhsTok.kind == tokIdent && isConstructor(rhsTok.val) {
		ctor, err := p.parseBhvConstructorExpr(rhsTok, syms)
		if err != nil {
			return nil, err
		}
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true
		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: ctor, Comment: comment}}, nil
	}

	if rhsTok.kind == tokIdent && rhsTok.val == "instruction" {
		rawFrame, err := p.parseInstruction()
		if err != nil {
			return nil, err
		}
		if !frameHasReturnSlot(rawFrame) {
			return nil, p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
		}
		if err := p.checkInstructionDirections(rawFrame, syms, rhsTok.pos); err != nil {
			return nil, err
		}
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true
		return []Stmt{&LetStmt{
			Name:    nameTok.val,
			Mutable: mutable,
			Value:   &InstructionExpr{Frame: rawFrame},
			Comment: comment,
		}}, nil
	}

	if rhsTok.kind == tokIdent {
		fn := p.fns[rhsTok.val]
		if fn == nil {
			// Not a function — parse as value with arithmetic/comparison/boolean
			resolved, err := p.resolveBhvOperand(rhsTok, syms)
			if err != nil {
				return nil, err
			}

			result, err := p.parseBhvArithExprFromFull(resolved, syms)
			if err != nil {
				return nil, err
			}

			syms.vars[nameTok.val] = varInfo{mutable: mutable}
			syms.usedVars[nameTok.val] = true

			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
			}

			// Check if any arithmetic or continuation happened
			if _, isIdent := result.(*IdentExpr); isIdent {
				return nil, p.errorf(rhsTok.pos, "unknown function %q", rhsTok.val)
			}
			if _, isLit := result.(*LiteralExpr); isLit {
				return nil, p.errorf(rhsTok.pos, "unknown function %q", rhsTok.val)
			}

			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
		}
		if !fn.hasReturn() {
			return nil, p.errorf(rhsTok.pos, "function %q has no return value", rhsTok.val)
		}
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true
		args, kwArgs, err := p.parseBhvCallArgs(fn, rhsTok, syms)
		if err != nil {
			return nil, err
		}

		callExpr := &CallExpr{Name: rhsTok.val, Args: args, KwArgs: kwArgs}

		// Check for comparison/boolean continuation after fn call
		contExpr, handled, err := p.maybeBhvExprContinuation(&IdentExpr{Name: nameTok.val}, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			// Two stmts: call writes to var, then boolean overwrites it
			return []Stmt{
				&LetStmt{Name: nameTok.val, Mutable: mutable, Value: callExpr, Comment: comment},
				&AssignStmt{Target: nameTok.val, Value: contExpr, Comment: "", Internal: true},
			}, nil
		}

		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: callExpr, Comment: comment}}, nil
	}

	if rhsTok.kind == tokLParen {
		p.unget(rhsTok)
		expr, err := p.parseBhvBoolExpr(syms)
		if err != nil {
			return nil, err
		}
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true

		// Single truthy = parenthesized value — check for arithmetic continuation
		if truthy, ok := expr.(*TruthyExpr); ok {
			innerVal := truthy.Value
			result, err := p.parseBhvArithExprFromFull(innerVal, syms)
			if err != nil {
				return nil, err
			}

			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
			}
			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
		}

		// Check for continuation after parenthesized bool expression
		contExpr, handled, err := p.maybeBhvExprContinuation(&IdentExpr{Name: nameTok.val}, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			return []Stmt{
				&LetStmt{Name: nameTok.val, Mutable: mutable, Value: expr, Comment: comment},
				&AssignStmt{Target: nameTok.val, Value: contExpr, Comment: "", Internal: true},
			}, nil
		}

		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: expr, Comment: comment}}, nil
	}

	return nil, p.errorf(rhsTok.pos, "expected number, function call, or constructor after '=', got %s", rhsTok.describe())
}

// parseBhvDefaultStmt parses a function call, assignment, compound assignment,
// or increment/decrement. Returns one or more statements.
func (p *parser) parseBhvDefaultStmt(tok token, syms *symbolTable) ([]Stmt, error) {
	comment := p.docComment
	tok2, err := p.next()
	if err != nil {
		return nil, err
	}

	if tok2.kind == tokPlusPlus {
		return []Stmt{&IncrDecrStmt{Target: tok.val, Op: tokPlusPlus, Comment: comment}}, nil
	}
	if tok2.kind == tokMinusMinus {
		return []Stmt{&IncrDecrStmt{Target: tok.val, Op: tokMinusMinus, Comment: comment}}, nil
	}

	if tok2.kind == tokEquals {
		rhsTok, err := p.next()
		if err != nil {
			return nil, err
		}

		if rhsTok.kind == tokNumber {
			num, _ := strconv.Atoi(rhsTok.val)
			numExpr := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
			result, err := p.parseBhvArithExprFromFull(numExpr, syms)
			if err != nil {
				return nil, err
			}

			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment}}, nil
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment}}, nil
		}

		if rhsTok.kind == tokIdent && isConstructor(rhsTok.val) {
			p.unget(rhsTok)
			ctor, err := p.parseBhvArgExpr(syms)
			if err != nil {
				return nil, err
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: ctor, Comment: comment}}, nil
		}

		if rhsTok.kind == tokIdent && rhsTok.val == "instruction" {
			rawFrame, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			if !frameHasReturnSlot(rawFrame) {
				return nil, p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
			}
			if err := p.checkInstructionDirections(rawFrame, syms, rhsTok.pos); err != nil {
				return nil, err
			}
			return []Stmt{&AssignStmt{
				Target:  tok.val,
				Value:   &InstructionExpr{Frame: rawFrame},
				Comment: comment,
			}}, nil
		}

		if rhsTok.kind == tokIdent {
			fn := p.fns[rhsTok.val]
			if fn == nil {
				// Not a function — value + arithmetic/comparison/boolean
				resolved, err := p.resolveBhvOperand(rhsTok, syms)
				if err != nil {
					return nil, err
				}
				result, err := p.parseBhvArithExprFromFull(resolved, syms)
				if err != nil {
					return nil, err
				}

				final, handled, err := p.maybeBhvExprContinuation(result, syms)
				if err != nil {
					return nil, err
				}
				if handled {
					return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment}}, nil
				}

				if result == resolved {
					return nil, p.errorf(rhsTok.pos, "unknown function %q", rhsTok.val)
				}
				return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment}}, nil
			}
			if !fn.hasReturn() {
				return nil, p.errorf(rhsTok.pos, "function %q has no return value", rhsTok.val)
			}
			args, kwArgs, err := p.parseBhvCallArgs(fn, rhsTok, syms)
			if err != nil {
				return nil, err
			}

			callExpr := &CallExpr{Name: rhsTok.val, Args: args, KwArgs: kwArgs}

			// Check for continuation after fn call
			contExpr, handled, err := p.maybeBhvExprContinuation(&IdentExpr{Name: tok.val}, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{
					&AssignStmt{Target: tok.val, Value: callExpr, Comment: comment},
					&AssignStmt{Target: tok.val, Value: contExpr, Comment: ""},
				}, nil
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: callExpr, Comment: comment}}, nil
		}

		if rhsTok.kind == tokLParen {
			p.unget(rhsTok)
			expr, err := p.parseBhvBoolExpr(syms)
			if err != nil {
				return nil, err
			}

			if truthy, ok := expr.(*TruthyExpr); ok {
				innerVal := truthy.Value
				result, err := p.parseBhvArithExprFromFull(innerVal, syms)
				if err != nil {
					return nil, err
				}
				final, handled, err := p.maybeBhvExprContinuation(result, syms)
				if err != nil {
					return nil, err
				}
				if handled {
					return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment}}, nil
				}
				return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment}}, nil
			}

			// Check for continuation after parenthesized expression
			contExpr, handled, err := p.maybeBhvExprContinuation(&IdentExpr{Name: tok.val}, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{
					&AssignStmt{Target: tok.val, Value: expr, Comment: comment},
					&AssignStmt{Target: tok.val, Value: contExpr, Comment: ""},
				}, nil
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: expr, Comment: comment}}, nil
		}

		return nil, p.errorf(rhsTok.pos, "expected number, function call, constructor, or instruction after '=', got %s", rhsTok.describe())
	}

	if isCompoundAssignOp(tok2.kind) {
		rhs, err := p.parseBhvArithExpr(syms)
		if err != nil {
			return nil, err
		}
		return []Stmt{&CompoundAssignStmt{Target: tok.val, Op: tok2.kind, Value: rhs, Comment: comment}}, nil
	}

	// Function call
	p.unget(tok2)
	fn := p.fns[tok.val]
	if fn == nil {
		return nil, p.errorf(tok.pos, "unknown statement %q", tok.val)
	}
	args, kwArgs, err := p.parseBhvCallArgs(fn, tok, syms)
	if err != nil {
		return nil, err
	}
	return []Stmt{&CallStmt{Name: tok.val, Args: args, KwArgs: kwArgs, Comment: comment}}, nil
}

// parseBhvIfStmt parses an if/else-if/else statement.
func (p *parser) parseBhvIfStmt(syms *symbolTable) (*IfStmt, error) {
	comment := p.docComment
	lhsTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if err := p.checkReadable(lhsTok.val, syms, lhsTok.pos); err != nil {
		return nil, err
	}
	opTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if !isComparisonOp(opTok.kind) {
		return nil, p.errorf(opTok.pos, "unsupported comparison operator %s", opTok.describe())
	}
	rhsTok, err := p.expect(tokNumber)
	if err != nil {
		return nil, err
	}
	rhsNum, _ := strconv.Atoi(rhsTok.val)

	cond := &CompareExpr{
		Op:  opTok.kind,
		LHS: &IdentExpr{Name: lhsTok.val},
		RHS: &LiteralExpr{Value: map[string]any{"num": rhsNum}},
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	body, err := p.parseBhvStmtBlock(syms)
	if err != nil {
		return nil, err
	}

	stmt := &IfStmt{
		Cond:    cond,
		Body:    body,
		Comment: comment,
	}

	// Parse optional else / else-if chains
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	if tok.kind == tokIdent && tok.val == "else" {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent && peek.val == "if" {
			// else if
			err := p.parseBhvElseIfChain(stmt, syms)
			if err != nil {
				return nil, err
			}
		} else {
			// plain else
			p.unget(peek)
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			elseBody, err := p.parseBhvStmtBlock(syms)
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBody
		}
	} else {
		p.unget(tok)
	}

	return stmt, nil
}

// parseBhvElseIfChain parses the else-if / else chain and attaches them
// to the given IfStmt.
func (p *parser) parseBhvElseIfChain(stmt *IfStmt, syms *symbolTable) error {
	// Parse condition for "else if"
	lhsTok, err := p.expect(tokIdent)
	if err != nil {
		return err
	}
	if err := p.checkReadable(lhsTok.val, syms, lhsTok.pos); err != nil {
		return err
	}
	opTok, err := p.next()
	if err != nil {
		return err
	}
	if !isComparisonOp(opTok.kind) {
		return p.errorf(opTok.pos, "unsupported comparison operator %s", opTok.describe())
	}
	rhsTok, err := p.expect(tokNumber)
	if err != nil {
		return err
	}
	rhsNum, _ := strconv.Atoi(rhsTok.val)

	cond := &CompareExpr{
		Op:  opTok.kind,
		LHS: &IdentExpr{Name: lhsTok.val},
		RHS: &LiteralExpr{Value: map[string]any{"num": rhsNum}},
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	body, err := p.parseBhvStmtBlock(syms)
	if err != nil {
		return err
	}

	stmt.ElseIfs = append(stmt.ElseIfs, ElseIfClause{Cond: cond, Body: body})

	// Check for trailing else
	tok, err := p.next()
	if err != nil {
		return err
	}
	if tok.kind == tokIdent && tok.val == "else" {
		peek, err := p.next()
		if err != nil {
			return err
		}
		if peek.kind == tokIdent && peek.val == "if" {
			return p.parseBhvElseIfChain(stmt, syms)
		}
		// Plain else
		p.unget(peek)
		if _, err := p.expect(tokLBrace); err != nil {
			return err
		}
		elseBody, err := p.parseBhvStmtBlock(syms)
		if err != nil {
			return err
		}
		stmt.Else = elseBody
	} else {
		p.unget(tok)
	}

	return nil
}

// parseBhvWhileStmt parses a while loop.
func (p *parser) parseBhvWhileStmt(syms *symbolTable) (*WhileStmt, error) {
	comment := p.docComment
	varTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if err := p.checkReadable(varTok.val, syms, varTok.pos); err != nil {
		return nil, err
	}
	if _, err := p.expect(tokLessEquals); err != nil {
		return nil, err
	}
	limitTok, err := p.expect(tokNumber)
	if err != nil {
		return nil, err
	}
	limitNum, _ := strconv.Atoi(limitTok.val)

	cond := &CompareExpr{
		Op:  tokLessEquals,
		LHS: &IdentExpr{Name: varTok.val},
		RHS: &LiteralExpr{Value: map[string]any{"num": limitNum}},
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	body, err := p.parseBhvStmtBlock(syms)
	if err != nil {
		return nil, err
	}

	return &WhileStmt{Cond: cond, Body: body, Comment: comment}, nil
}

// parseBhvLoopStmt parses a loop { ... } block.
func (p *parser) parseBhvLoopStmt(syms *symbolTable) (*LoopStmt, error) {
	comment := p.docComment
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	body, err := p.parseBhvLoopBody(syms)
	if err != nil {
		return nil, err
	}
	return &LoopStmt{Body: body, Comment: comment}, nil
}

// parseBhvLoopBody parses loop body statements. Handles 'if' as if/break
// (the only form currently supported in loop bodies).
func (p *parser) parseBhvLoopBody(syms *symbolTable) ([]Stmt, error) {
	var stmts []Stmt
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind == tokEOF {
			return nil, p.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected statement, got %s", tok.describe())
		}
		comment := p.docComment

		switch tok.val {
		case "if":
			// Parse if/break pattern inside loop
			ifStmt, err := p.parseBhvIfBreak(syms)
			if err != nil {
				return nil, err
			}
			ifStmt.Comment = comment
			stmts = append(stmts, ifStmt)
		case "lock":
			stmts = append(stmts, &LockStmt{Unlock: false, Comment: comment})
		case "unlock":
			stmts = append(stmts, &LockStmt{Unlock: true, Comment: comment})
		default:
			parsed, err := p.parseBhvDefaultStmt(tok, syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, parsed...)
		}
	}
	return stmts, nil
}

// parseBhvIfBreak parses `if ident >= number { break }` inside a loop.
func (p *parser) parseBhvIfBreak(syms *symbolTable) (*IfStmt, error) {
	lhsTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if err := p.checkReadable(lhsTok.val, syms, lhsTok.pos); err != nil {
		return nil, err
	}
	if _, err := p.expect(tokGreaterEquals); err != nil {
		return nil, err
	}
	rhsTok, err := p.expect(tokNumber)
	if err != nil {
		return nil, err
	}
	rhsNum, _ := strconv.Atoi(rhsTok.val)

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	breakTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if breakTok.val != "break" {
		return nil, p.errorf(breakTok.pos, "expected 'break', got %q", breakTok.val)
	}
	if _, err := p.expect(tokRBrace); err != nil {
		return nil, err
	}

	return &IfStmt{
		Cond: &CompareExpr{
			Op:  tokGreaterEquals,
			LHS: &IdentExpr{Name: lhsTok.val},
			RHS: &LiteralExpr{Value: map[string]any{"num": rhsNum}},
		},
		Body: []Stmt{&BreakStmt{}},
	}, nil
}

// parseBhvMultiReturn parses a multi-return binding list.
// The first binding (firstTok) and the comma after it have been consumed.
func (p *parser) parseBhvMultiReturn(firstTok token, firstMutable, firstDiscard bool, syms *symbolTable) ([]Stmt, error) {
	comment := p.docComment

	var bindings []MultiBinding
	if firstDiscard {
		bindings = append(bindings, MultiBinding{Discard: true})
	} else {
		bindings = append(bindings, MultiBinding{
			Name:    firstTok.val,
			Mutable: firstMutable,
		})
	}

	activeModifier := -1
	if !firstDiscard {
		if firstMutable {
			activeModifier = 1
		} else {
			activeModifier = 0
		}
	}

	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokEquals {
			break
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected identifier, '_', 'let', 'var', or '=' in binding list, got %s", tok.describe())
		}

		switch tok.val {
		case "_":
			bindings = append(bindings, MultiBinding{Discard: true})
		case "let":
			activeModifier = 0
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: false})
		case "var":
			activeModifier = 1
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: true})
		default:
			if activeModifier >= 0 {
				bindings = append(bindings, MultiBinding{
					Name:    tok.val,
					Mutable: activeModifier == 1,
				})
			} else {
				bindings = append(bindings, MultiBinding{Name: tok.val})
			}
		}

		sep, err := p.next()
		if err != nil {
			return nil, err
		}
		if sep.kind == tokEquals {
			break
		}
		if sep.kind != tokComma {
			return nil, p.errorf(sep.pos, "expected ',' or '=' in binding list, got %s", sep.describe())
		}
	}

	// Parse the RHS: function call or instruction
	calleeTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}

	// Validate new variable names
	for _, bind := range bindings {
		if bind.Discard {
			continue
		}
		if err := p.checkVarName(bind.Name, syms, calleeTok.pos); err != nil {
			return nil, err
		}
	}

	if calleeTok.val == "instruction" {
		rawFrame, err := p.parseInstruction()
		if err != nil {
			return nil, err
		}
		if err := p.checkInstructionDirections(rawFrame, syms, calleeTok.pos); err != nil {
			return nil, err
		}
		retCount := frameReturnCount(rawFrame)
		if retCount == 0 {
			return nil, p.errorf(calleeTok.pos, "instruction has no return slots (@N); cannot assign its result")
		}
		if len(bindings) > retCount {
			return nil, p.errorf(calleeTok.pos, "too many bindings (%d) for instruction which returns %d values", len(bindings), retCount)
		}

		// Register new variables
		for _, bind := range bindings {
			if !bind.Discard {
				syms.vars[bind.Name] = varInfo{mutable: bind.Mutable}
				syms.usedVars[bind.Name] = true
			}
		}

		return []Stmt{&MultiReturnStmt{
			Bindings: bindings,
			Value:    &InstructionExpr{Frame: rawFrame},
			Comment:  comment,
		}}, nil
	}

	fn := p.fns[calleeTok.val]
	if fn == nil {
		return nil, p.errorf(calleeTok.pos, "unknown function %q", calleeTok.val)
	}
	if !fn.hasReturn() {
		return nil, p.errorf(calleeTok.pos, "function %q has no return value", calleeTok.val)
	}
	if len(bindings) > fn.returnCount() {
		return nil, p.errorf(calleeTok.pos, "too many bindings (%d) for function %q which returns %d values", len(bindings), calleeTok.val, fn.returnCount())
	}

	// Register new variables before parsing args (they may be referenced)
	for _, bind := range bindings {
		if !bind.Discard {
			syms.vars[bind.Name] = varInfo{mutable: bind.Mutable}
			syms.usedVars[bind.Name] = true
		}
	}

	args, kwArgs, err := p.parseBhvCallArgs(fn, calleeTok, syms)
	if err != nil {
		return nil, err
	}

	return []Stmt{&MultiReturnStmt{
		Bindings: bindings,
		Value:    &CallExpr{Name: calleeTok.val, Args: args, KwArgs: kwArgs},
		Comment:  comment,
	}}, nil
}

// parseBhvStmtBlock parses a brace-delimited block of statements.
// The opening '{' has been consumed. Reads until '}'.
func (p *parser) parseBhvStmtBlock(syms *symbolTable) ([]Stmt, error) {
	var stmts []Stmt
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind == tokEOF {
			return nil, p.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected statement, got %s", tok.describe())
		}

		switch tok.val {
		case "lock":
			stmts = append(stmts, &LockStmt{Unlock: false, Comment: p.docComment})
		case "unlock":
			stmts = append(stmts, &LockStmt{Unlock: true, Comment: p.docComment})
		default:
			parsed, err := p.parseBhvDefaultStmt(tok, syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, parsed...)
		}
	}
	return stmts, nil
}

// -----------------------------------------------------------------------
// Behavior-level emitter: walks []Stmt and emits frames
// -----------------------------------------------------------------------

// emitBhvExprGetValue resolves an Expr to a value for use in instruction
// slots. Simple values pass through; complex expressions emit frames and
// return a temp variable name.
func (p *parser) emitBhvExprGetValue(expr Expr, syms *symbolTable, b *frameBuilder, comment string) (any, error) {
	switch e := expr.(type) {
	case *LiteralExpr:
		return e.Value, nil
	case *IdentExpr:
		return e.Name, nil
	case *ArithExpr:
		tmp := allocUniqueVar("@arith1", syms.usedVars)
		if err := p.emitBhvArithTo(e, tmp, syms, b, comment); err != nil {
			return nil, err
		}
		return tmp, nil
	case *ConstructorExpr:
		if val, ok := tryResolveConstructorLiteral(e); ok {
			return val, nil
		}
		tmp := allocUniqueVar("@ctor", syms.usedVars)
		if err := p.emitBhvConstructorTo(e, tmp, syms, b, comment); err != nil {
			return nil, err
		}
		return tmp, nil
	case *AmpersandExpr:
		if val, ok := tryResolveAmpersandLiteral(e); ok {
			return val, nil
		}
		tmp := allocUniqueVar("@amp", syms.usedVars)
		if err := p.emitBhvAmpersandTo(e, tmp, syms, b, comment); err != nil {
			return nil, err
		}
		return tmp, nil
	case *CallExpr:
		tmp := allocUniqueVar("@call", syms.usedVars)
		resolvedArgs, resolvedKwArgs, err := p.emitBhvCallExprArgs(e.Args, e.KwArgs, syms, b)
		if err != nil {
			return nil, err
		}
		if err := p.checkCallDirections(p.fns[e.Name], e.Name, resolvedArgs, resolvedKwArgs, syms, 0); err != nil {
			return nil, err
		}
		if err := p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, []any{tmp}, b, 0, comment, syms.usedVars); err != nil {
			return nil, err
		}
		return tmp, nil
	default:
		return nil, fmt.Errorf("unsupported expression type %T in emitBhvExprGetValue", expr)
	}
}

// emitBhvCallExprArgs resolves AST Expr args to values for expandCall.
func (p *parser) emitBhvCallExprArgs(args []Expr, kwArgs map[string]Expr, syms *symbolTable, b *frameBuilder) ([]any, map[string]any, error) {
	resolvedArgs := make([]any, len(args))
	for i, arg := range args {
		val, err := p.emitBhvExprGetValue(arg, syms, b, "")
		if err != nil {
			return nil, nil, err
		}
		resolvedArgs[i] = val
	}
	resolvedKwArgs := map[string]any{}
	for kw, arg := range kwArgs {
		val, err := p.emitBhvExprGetValue(arg, syms, b, "")
		if err != nil {
			return nil, nil, err
		}
		resolvedKwArgs[kw] = val
	}
	return resolvedArgs, resolvedKwArgs, nil
}

// emitBhvExprTo emits an expression writing the result to target.
func (p *parser) emitBhvExprTo(expr Expr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	switch e := expr.(type) {
	case *LiteralExpr:
		// Distinguish set_number from set_reg
		if m, ok := e.Value.(map[string]any); ok {
			if _, hasNum := m["num"]; hasNum && len(m) == 1 {
				f := map[string]any{"op": "set_number", "2": m, "3": target}
				setComment(f, comment)
				b.emit(f)
				return nil
			}
		}
		f := map[string]any{"op": "set_reg", "1": e.Value, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	case *IdentExpr:
		f := map[string]any{"op": "set_reg", "1": e.Name, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	case *ArithExpr:
		return p.emitBhvArithTo(e, target, syms, b, comment)
	case *CallExpr:
		resolvedArgs, resolvedKwArgs, err := p.emitBhvCallExprArgs(e.Args, e.KwArgs, syms, b)
		if err != nil {
			return err
		}
		if err := p.checkCallDirections(p.fns[e.Name], e.Name, resolvedArgs, resolvedKwArgs, syms, 0); err != nil {
			return err
		}
		return p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, []any{target}, b, 0, comment, syms.usedVars)
	case *InstructionExpr:
		resolved := resolveInstructionFrame(e.Frame, []any{target}, nil, nil, comment)
		b.emit(resolved)
		return nil
	case *ConstructorExpr:
		return p.emitBhvConstructorTo(e, target, syms, b, comment)
	case *AmpersandExpr:
		return p.emitBhvAmpersandTo(e, target, syms, b, comment)
	case *CompareExpr:
		return p.emitBhvBoolExprTo(expr, target, syms, b, comment)
	case *TypeCheckExpr:
		return p.emitBhvBoolExprTo(expr, target, syms, b, comment)
	case *TruthyExpr:
		return p.emitBhvBoolExprTo(expr, target, syms, b, comment)
	case *BoolChainExpr:
		return p.emitBhvBoolExprTo(expr, target, syms, b, comment)
	}
	return fmt.Errorf("unsupported expression type %T in emitBhvExprTo", expr)
}

// emitBhvArithTo emits an arithmetic expression chain writing to target.
// The last operation writes directly to target (matching rewriteLastArithTarget).
// Uses a per-tree counter for intermediate temp naming: @arith1, @arith2, etc.
func (p *parser) emitBhvArithTo(expr *ArithExpr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	ac := &arithCounter{}
	_, err := p.emitBhvArithNode(expr, target, syms, b, comment, ac)
	return err
}

// emitBhvArithNode recursively emits an ArithExpr node. Non-ArithExpr children
// are resolved via emitBhvExprGetValue. ArithExpr children are emitted
// recursively with a shared arithCounter. The last (outermost) operation
// writes directly to the caller's target.
func (p *parser) emitBhvArithNode(expr *ArithExpr, target any, syms *symbolTable, b *frameBuilder, comment string, ac *arithCounter) (any, error) {
	// Resolve LHS
	var lhs any
	if sub, ok := expr.LHS.(*ArithExpr); ok {
		tmp := ac.next(syms.usedVars)
		val, err := p.emitBhvArithNode(sub, tmp, syms, b, "", ac)
		if err != nil {
			return nil, err
		}
		lhs = val
	} else {
		val, err := p.emitBhvExprGetValue(expr.LHS, syms, b, "")
		if err != nil {
			return nil, err
		}
		lhs = val
	}

	// Resolve RHS
	var rhs any
	if sub, ok := expr.RHS.(*ArithExpr); ok {
		tmp := ac.next(syms.usedVars)
		val, err := p.emitBhvArithNode(sub, tmp, syms, b, "", ac)
		if err != nil {
			return nil, err
		}
		rhs = val
	} else {
		val, err := p.emitBhvExprGetValue(expr.RHS, syms, b, "")
		if err != nil {
			return nil, err
		}
		rhs = val
	}

	f := map[string]any{
		"op": arithmeticOpName(expr.Op),
		"1":  lhs,
		"2":  rhs,
		"3":  target,
	}
	setComment(f, comment)
	b.emit(f)
	return target, nil
}

// emitBhvConstructorTo emits a constructor expression to target.
func (p *parser) emitBhvConstructorTo(ctor *ConstructorExpr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	if val, ok := tryResolveConstructorLiteral(ctor); ok {
		f := map[string]any{"op": "set_reg", "1": val, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}
	// Runtime: only Coordinate can be runtime
	if ctor.TypeName == "Coordinate" {
		xVal, err := p.emitBhvExprGetValue(ctor.Args[0], syms, b, "")
		if err != nil {
			return err
		}
		yVal, err := p.emitBhvExprGetValue(ctor.Args[1], syms, b, "")
		if err != nil {
			return err
		}
		return p.expandCall("combine_coordinate", []any{xVal, yVal}, nil, []any{target}, b, 0, comment, syms.usedVars)
	}
	return fmt.Errorf("unknown constructor %q", ctor.TypeName)
}

// emitBhvAmpersandTo emits an & expression to target.
func (p *parser) emitBhvAmpersandTo(amp *AmpersandExpr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	if val, ok := tryResolveAmpersandLiteral(amp); ok {
		f := map[string]any{"op": "set_reg", "1": val, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}
	baseVal, err := p.emitBhvExprGetValue(amp.Value, syms, b, "")
	if err != nil {
		return err
	}
	numVal, err := p.emitBhvExprGetValue(amp.Num, syms, b, "")
	if err != nil {
		return err
	}
	return p.expandCall("set_number", []any{baseVal, numVal}, nil, []any{target}, b, 0, comment, syms.usedVars)
}

// emitBhvBoolExprTo emits a boolean expression (comparison/typecheck/truthy/chain)
// writing the result (1 or false) to target.
func (p *parser) emitBhvBoolExprTo(expr Expr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	// Pre-resolve the expression: emit arithmetic frames, resolve operands.
	resolved, err := p.resolveBhvBoolTree(expr, syms, b)
	if err != nil {
		return err
	}

	// For single-leaf expressions, delegate to the specialized emitters
	// that match the old codegen behavior (omitting "next" for > < !=).
	if resolved.isLeaf() {
		t := resolved.term
		switch {
		case isTypeCheckOp(t.op):
			p.emitTypeCheck(t.lhs, target, t.rhs.(string), b, comment)
		case t.op == tokTruthy:
			p.emitTruthyCheck(t.lhs, target, b, comment)
		default:
			p.emitComparison(t.op, t.lhs, t.rhs, target, b, comment)
		}
		return nil
	}

	// Chain or group: use recursive emitter with explicit targets.
	totalChecks := resolved.frameCount()
	base := b.pos()
	falsePos := base + totalChecks
	truePos := base + totalChecks + 1
	afterPos := base + totalChecks + 2

	p.emitResolvedBoolFrames(resolved, frameRef(truePos), frameRef(falsePos), b, comment)

	// False frame
	b.emit(map[string]any{
		"op":   "set_reg",
		"1":    false,
		"2":    target,
		"next": frameRef(afterPos),
	})
	// True frame
	b.emit(map[string]any{
		"op": "set_reg",
		"1":  map[string]any{"num": 1},
		"2":  target,
	})
	return nil
}

// resolvedBoolExpr is an internal tree used after pre-resolving operands.
// All operands are resolved to values (any), not AST Exprs.
type resolvedBoolExpr struct {
	// Leaf: term is set
	term *comparisonTerm
	// Group: chainOp and children are set
	chainOp  tokenKind
	children []*resolvedBoolExpr
}

func (e *resolvedBoolExpr) isLeaf() bool { return e.term != nil }

func (e *resolvedBoolExpr) frameCount() int {
	if e.isLeaf() {
		return 1
	}
	n := 0
	for _, child := range e.children {
		n += child.frameCount()
	}
	return n
}

// resolveBhvBoolTree walks an Expr tree, emitting arithmetic frames and
// resolving all operands to values, producing a resolvedBoolExpr tree.
func (p *parser) resolveBhvBoolTree(expr Expr, syms *symbolTable, b *frameBuilder) (*resolvedBoolExpr, error) {
	switch e := expr.(type) {
	case *CompareExpr:
		lhs, err := p.emitBhvExprGetValue(e.LHS, syms, b, "")
		if err != nil {
			return nil, err
		}
		rhs, err := p.emitBhvExprGetValue(e.RHS, syms, b, "")
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: e.Op, lhs: lhs, rhs: rhs}}, nil
	case *TypeCheckExpr:
		lhs, err := p.emitBhvExprGetValue(e.Value, syms, b, "")
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: tokIs, lhs: lhs, rhs: e.TypeSlot}}, nil
	case *TruthyExpr:
		lhs, err := p.emitBhvExprGetValue(e.Value, syms, b, "")
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: tokTruthy, lhs: lhs}}, nil
	case *BoolChainExpr:
		children := make([]*resolvedBoolExpr, len(e.Children))
		for i, child := range e.Children {
			resolved, err := p.resolveBhvBoolTree(child, syms, b)
			if err != nil {
				return nil, err
			}
			children[i] = resolved
		}
		return &resolvedBoolExpr{chainOp: e.Op, children: children}, nil
	default:
		return nil, fmt.Errorf("unsupported boolean expression type %T", expr)
	}
}

// emitResolvedBoolFrames recursively emits check frames for a resolved tree.
func (p *parser) emitResolvedBoolFrames(expr *resolvedBoolExpr, trueTarget, falseTarget frameRef, b *frameBuilder, comment string) {
	if expr.isLeaf() {
		p.emitBoolCheckFrame(expr.term, trueTarget, falseTarget, b, comment)
		return
	}

	for i, child := range expr.children {
		isLast := i == len(expr.children)-1
		childComment := ""
		if i == 0 {
			childComment = comment
		}

		if expr.chainOp == tokDoubleAmpersand {
			if isLast {
				p.emitResolvedBoolFrames(child, trueTarget, falseTarget, b, childComment)
			} else {
				nextChildPos := frameRef(b.pos() + child.frameCount())
				p.emitResolvedBoolFrames(child, nextChildPos, falseTarget, b, childComment)
			}
		} else {
			if isLast {
				p.emitResolvedBoolFrames(child, trueTarget, falseTarget, b, childComment)
			} else {
				nextChildPos := frameRef(b.pos() + child.frameCount())
				p.emitResolvedBoolFrames(child, trueTarget, nextChildPos, b, childComment)
			}
		}
	}
}

// emitBehaviorStmts walks a []Stmt list and emits frames, handling deferred
// bodies from if statements, break target patching from loops, and lock/unlock
// mode tracking. This is the main behavior-level statement emitter.
func (p *parser) emitBehaviorStmts(stmts []Stmt, b *frameBuilder, syms *symbolTable, mode *execMode) error {
	var deferred []deferredBody
	breakTargetFrame := -1
	resumeFrame := -1

	for i, stmt := range stmts {
		switch s := stmt.(type) {
		case *LockStmt:
			op := "lock"
			targetMode := modeLocked
			if s.Unlock {
				op = "unlock"
				targetMode = modeUnlocked
			}
			if *mode != targetMode {
				f := map[string]any{"op": op}
				setComment(f, s.Comment)
				b.emit(f)
				*mode = targetMode
			}

		case *IfStmt:
			if err := p.emitBhvIfStmt(s, b, syms, mode, &deferred); err != nil {
				return err
			}

		case *WhileStmt:
			if err := p.emitBhvWhileStmt(s, b, syms); err != nil {
				return err
			}
			*mode = modeUnknown

		case *LoopStmt:
			checkFrame, err := p.emitBhvLoopStmt(s, b, syms)
			if err != nil {
				return err
			}
			if checkFrame >= 0 {
				breakTargetFrame = checkFrame + 1
				resumeFrame = b.pos()
				b.seek(breakTargetFrame)
			}
			*mode = modeUnknown

		default:
			framesBefore := b.pos()
			if err := p.emitBhvStmtSimple(stmt, b, syms); err != nil {
				return err
			}
			// Scan newly emitted frames for lock/unlock mode tracking.
			// Since all function calls are inlined, any lock/unlock inside
			// a called function appears as a frame in the builder.
			for j := framesBefore; j < b.pos(); j++ {
				if op, ok := b.get(j)["op"].(string); ok {
					switch op {
					case "lock":
						*mode = modeLocked
					case "unlock":
						*mode = modeUnlocked
					}
				}
			}
		}

		// Break target patching: after a loop emits, the cursor is at the
		// break target position. The next statement overwrites it. Once that
		// statement has emitted, patch the break target's "next" to either
		// false (last statement) or frameRef(resumeFrame) (skip loop body).
		if breakTargetFrame >= 0 && b.pos()-1 == breakTargetFrame {
			instr := b.get(breakTargetFrame)
			if i == len(stmts)-1 {
				instr["next"] = false
			} else {
				instr["next"] = frameRef(resumeFrame)
			}
			b.seek(resumeFrame)
			breakTargetFrame = -1
			resumeFrame = -1
		}
	}

	// Emit deferred bodies after all main-line frames.
	mainFrameCount := b.pos()
	if len(deferred) > 0 {
		// Prevent the last main-line frame from falling into deferred frames.
		if mainFrameCount > 0 {
			lastInstr := b.get(mainFrameCount - 1)
			if _, hasNext := lastInstr["next"]; !hasNext {
				lastInstr["next"] = false
			}
		}

		// Sort: reverse chronological by check frame, slot "1" before "2".
		sort.SliceStable(deferred, func(i, j int) bool {
			if deferred[i].checkFrame != deferred[j].checkFrame {
				return deferred[i].checkFrame > deferred[j].checkFrame
			}
			return deferred[i].slot < deferred[j].slot
		})

		for i := range deferred {
			d := &deferred[i]
			bodyFrame := b.pos()
			rebased := rebaseFrameRefs(d.frames, bodyFrame)
			for _, f := range rebased {
				b.emit(f)
			}
			// Set "next" on the body's last frame.
			lastBody := b.get(b.pos() - 1)
			if d.continuation < mainFrameCount {
				lastBody["next"] = frameRef(d.continuation)
			} else {
				lastBody["next"] = false
			}
			// Patch the check_number's branch slot.
			checkInstr := b.get(d.checkFrame)
			checkInstr[d.slot] = frameRef(bodyFrame)
		}
	}

	return nil
}

// emitBhvStmtSimple emits frames for a non-control-flow statement.
func (p *parser) emitBhvStmtSimple(stmt Stmt, b *frameBuilder, syms *symbolTable) error {
	switch s := stmt.(type) {
	case *InstructionStmt:
		resolved := resolveInstructionFrame(s.Frame, nil, nil, nil, s.Comment)
		b.emit(resolved)
		return nil

	case *CallStmt:
		resolvedArgs, resolvedKwArgs, err := p.emitBhvCallExprArgs(s.Args, s.KwArgs, syms, b)
		if err != nil {
			return err
		}
		fn := p.fns[s.Name]
		if err := p.checkCallDirections(fn, s.Name, resolvedArgs, resolvedKwArgs, syms, 0); err != nil {
			return err
		}
		return p.expandCall(s.Name, resolvedArgs, resolvedKwArgs, nil, b, 0, s.Comment, syms.usedVars)

	case *LetStmt:
		return p.emitBhvExprTo(s.Value, s.Name, syms, b, s.Comment)

	case *AssignStmt:
		var target any
		if s.Internal {
			// Compiler-generated assign (e.g., continuation after fn call);
			// skip mutability check — target was just declared.
			target = s.Target
		} else {
			var err error
			target, err = p.resolveAssignTarget(s.Target, syms, 0, false)
			if err != nil {
				return err
			}
		}
		return p.emitBhvExprTo(s.Value, target, syms, b, s.Comment)

	case *CompoundAssignStmt:
		target, err := p.resolveAssignTarget(s.Target, syms, 0, true)
		if err != nil {
			return err
		}
		rhs, err := p.emitBhvExprGetValue(s.Value, syms, b, "")
		if err != nil {
			return err
		}
		f := map[string]any{
			"op": compoundAssignOpName(s.Op),
			"1":  target,
			"2":  rhs,
			"3":  target,
		}
		setComment(f, s.Comment)
		b.emit(f)
		return nil

	case *IncrDecrStmt:
		target, err := p.resolveAssignTarget(s.Target, syms, 0, true)
		if err != nil {
			return err
		}
		op := "add"
		if s.Op == tokMinusMinus {
			op = "sub"
		}
		f := map[string]any{
			"op": op,
			"1":  target,
			"2":  map[string]any{"num": 1},
			"3":  target,
		}
		setComment(f, s.Comment)
		b.emit(f)
		return nil

	case *MultiReturnStmt:
		retVals := make([]any, len(s.Bindings))
		for i, bind := range s.Bindings {
			if bind.Discard {
				retVals[i] = false
			} else {
				retVals[i] = bind.Name
			}
		}
		switch v := s.Value.(type) {
		case *CallExpr:
			resolvedArgs, resolvedKwArgs, err := p.emitBhvCallExprArgs(v.Args, v.KwArgs, syms, b)
			if err != nil {
				return err
			}
			fn := p.fns[v.Name]
			if err := p.checkCallDirections(fn, v.Name, resolvedArgs, resolvedKwArgs, syms, 0); err != nil {
				return err
			}
			return p.expandCall(v.Name, resolvedArgs, resolvedKwArgs, retVals, b, 0, s.Comment, syms.usedVars)
		case *InstructionExpr:
			resolved := resolveInstructionFrame(v.Frame, retVals, nil, nil, s.Comment)
			b.emit(resolved)
			return nil
		}
		return fmt.Errorf("unsupported multi-return value type %T", s.Value)
	}

	return fmt.Errorf("unsupported statement type %T", stmt)
}

// emitBhvIfStmt emits an if/else-if/else statement, appending deferred bodies
// to the caller's deferred list for emission after all main-line frames.
func (p *parser) emitBhvIfStmt(s *IfStmt, b *frameBuilder, syms *symbolTable, mode *execMode, deferred *[]deferredBody) error {
	cmp, ok := s.Cond.(*CompareExpr)
	if !ok {
		return fmt.Errorf("if condition must be a CompareExpr, got %T", s.Cond)
	}

	lhs, err := p.emitBhvExprGetValue(cmp.LHS, syms, b, "")
	if err != nil {
		return err
	}
	rhs, err := p.emitBhvExprGetValue(cmp.RHS, syms, b, "")
	if err != nil {
		return err
	}

	check := map[string]any{
		"op":        "check_number",
		checkValue:  lhs,
		checkTarget: rhs,
	}
	setComment(check, s.Comment)
	checkFrame := b.emit(check)

	// Compile body into separate builder
	bodyBuilder := &frameBuilder{}
	bodyMode := modeUnknown
	if err := p.emitBehaviorStmts(s.Body, bodyBuilder, syms, &bodyMode); err != nil {
		return err
	}
	bodyFrames := bodyBuilder.frames

	switch cmp.Op {
	case tokLess:
		// a < N: body when smaller. Deferred.
		*deferred = append(*deferred, deferredBody{
			frames:     bodyFrames,
			checkFrame: checkFrame,
			slot:       checkSmaller,
		})

	case tokGreaterEquals:
		// a >= N: body when larger or equal. Inline (both fall through).
		rebased := rebaseFrameRefs(bodyFrames, b.pos())
		for _, f := range rebased {
			b.emit(f)
		}
		if s.Else != nil {
			elseBuilder := &frameBuilder{}
			elseMode := modeUnknown
			if err := p.emitBehaviorStmts(s.Else, elseBuilder, syms, &elseMode); err != nil {
				return err
			}
			*deferred = append(*deferred, deferredBody{
				frames:     elseBuilder.frames,
				checkFrame: checkFrame,
				slot:       checkSmaller,
			})
		}

	case tokDoubleEquals:
		// a == N: body when equal. Inline (falls through).
		rebased := rebaseFrameRefs(bodyFrames, b.pos())
		for _, f := range rebased {
			b.emit(f)
		}
		if len(s.ElseIfs) > 0 {
			ei := s.ElseIfs[0]
			eiCmp, ok := ei.Cond.(*CompareExpr)
			if !ok {
				return fmt.Errorf("else-if condition must be CompareExpr")
			}
			var slot string
			switch eiCmp.Op {
			case tokGreater:
				slot = checkLarger
			case tokLess:
				slot = checkSmaller
			default:
				return fmt.Errorf("unsupported else-if operator")
			}
			eiBuilder := &frameBuilder{}
			eiMode := modeUnknown
			if err := p.emitBehaviorStmts(ei.Body, eiBuilder, syms, &eiMode); err != nil {
				return err
			}
			*deferred = append(*deferred, deferredBody{
				frames:     eiBuilder.frames,
				checkFrame: checkFrame,
				slot:       slot,
			})
			if s.Else != nil {
				var elseSlot string
				if slot == checkLarger {
					elseSlot = checkSmaller
				} else {
					elseSlot = checkLarger
				}
				elseBuilder := &frameBuilder{}
				elseMode := modeUnknown
				if err := p.emitBehaviorStmts(s.Else, elseBuilder, syms, &elseMode); err != nil {
					return err
				}
				*deferred = append(*deferred, deferredBody{
					frames:     elseBuilder.frames,
					checkFrame: checkFrame,
					slot:       elseSlot,
				})
			}
		} else if s.Else != nil {
			elseBuilder := &frameBuilder{}
			elseMode := modeUnknown
			if err := p.emitBehaviorStmts(s.Else, elseBuilder, syms, &elseMode); err != nil {
				return err
			}
			// For == with plain else: both slots go to the else body
			*deferred = append(*deferred, deferredBody{
				frames:     elseBuilder.frames,
				checkFrame: checkFrame,
				slot:       checkLarger,
			})
			*deferred = append(*deferred, deferredBody{
				frames:     elseBuilder.frames,
				checkFrame: checkFrame,
				slot:       checkSmaller,
			})
		}

	case tokGreater:
		// a > N: body when larger. Deferred.
		*deferred = append(*deferred, deferredBody{
			frames:     bodyFrames,
			checkFrame: checkFrame,
			slot:       checkLarger,
		})
	}

	// Set continuation on all deferred bodies from this check frame.
	continuation := b.pos()
	for i := range *deferred {
		if (*deferred)[i].continuation == 0 && (*deferred)[i].checkFrame == checkFrame {
			(*deferred)[i].continuation = continuation
		}
	}

	*mode = modeUnknown
	return nil
}

// emitBhvWhileStmt emits a while loop.
func (p *parser) emitBhvWhileStmt(s *WhileStmt, b *frameBuilder, syms *symbolTable) error {
	cmp, ok := s.Cond.(*CompareExpr)
	if !ok {
		return fmt.Errorf("while condition must be a CompareExpr, got %T", s.Cond)
	}

	lhs, err := p.emitBhvExprGetValue(cmp.LHS, syms, b, "")
	if err != nil {
		return err
	}
	rhs, err := p.emitBhvExprGetValue(cmp.RHS, syms, b, "")
	if err != nil {
		return err
	}

	check := map[string]any{
		"op":        "check_number",
		checkValue:  lhs,
		checkTarget: rhs,
	}
	setComment(check, s.Comment)
	checkFrame := b.emit(check)

	// Compile body
	bodyBuilder := &frameBuilder{}
	bodyMode := modeUnknown
	if err := p.emitBehaviorStmts(s.Body, bodyBuilder, syms, &bodyMode); err != nil {
		return err
	}

	rebased := rebaseFrameRefs(bodyBuilder.frames, b.pos())
	for _, f := range rebased {
		b.emit(f)
	}

	// Loop back
	lastBody := b.get(b.pos() - 1)
	lastBody["next"] = frameRef(checkFrame)

	// Exit when larger
	check[checkLarger] = frameRef(b.pos())

	return nil
}

// emitBhvLoopStmt emits an unconditional loop with if/break.
// Returns the check frame index (for break target patching) or -1.
func (p *parser) emitBhvLoopStmt(s *LoopStmt, b *frameBuilder, syms *symbolTable) (int, error) {
	loopStart := b.pos()
	checkFrame := -1
	loopMode := modeUnknown

	for _, stmt := range s.Body {
		if ifStmt, ok := stmt.(*IfStmt); ok {
			if len(ifStmt.Body) == 1 {
				if _, isBreak := ifStmt.Body[0].(*BreakStmt); isBreak {
					// This is the if/break pattern
					cmp, ok := ifStmt.Cond.(*CompareExpr)
					if !ok {
						return -1, fmt.Errorf("if/break condition must be CompareExpr")
					}
					lhs, err := p.emitBhvExprGetValue(cmp.LHS, syms, b, "")
					if err != nil {
						return -1, err
					}
					rhs, err := p.emitBhvExprGetValue(cmp.RHS, syms, b, "")
					if err != nil {
						return -1, err
					}
					f := map[string]any{
						"op":        "check_number",
						checkValue:  lhs,
						checkTarget: rhs,
					}
					setComment(f, ifStmt.Comment)
					checkFrame = b.emit(f)
					// Reserve break target frame
					b.emit(nil)
					// Set if_smaller to skip past break target
					f[checkSmaller] = frameRef(b.pos())
					continue
				}
			}
		}
		// Regular statement — handle lock/unlock inline, delegate rest
		if ls, ok := stmt.(*LockStmt); ok {
			op := "lock"
			targetMode := modeLocked
			if ls.Unlock {
				op = "unlock"
				targetMode = modeUnlocked
			}
			if loopMode != targetMode {
				f := map[string]any{"op": op}
				setComment(f, ls.Comment)
				b.emit(f)
				loopMode = targetMode
			}
		} else {
			if err := p.emitBhvStmtSimple(stmt, b, syms); err != nil {
				return -1, err
			}
		}
	}

	// Loop back
	if checkFrame >= 0 {
		lastInstr := b.get(b.pos() - 1)
		lastInstr["next"] = frameRef(loopStart)
	}

	return checkFrame, nil
}
