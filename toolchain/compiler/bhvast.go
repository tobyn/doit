package compiler

// bhvast.go — Behavior-level AST parsing and emission (Phase 2).
//
// Expression parsers return Expr nodes (no frame emission).
// Statement parsers return Stmt/[]Stmt nodes.
// The emitter (emitBehaviorStmts) walks []Stmt and emits frames.

import (
	"fmt"
	"strconv"
	"strings"
)

// -----------------------------------------------------------------------
// Expression parsers → Expr nodes
//
// These parsers are parameterized by an operandResolver callback, allowing
// them to be shared between behavior-level and fn body contexts.
// -----------------------------------------------------------------------

// operandResolver resolves a bare identifier token to an Expr.
// Used to abstract $register/parameter resolution for shared expression parsers.
type operandResolver func(tok token) (Expr, error)

// bhvResolver returns an operandResolver for behavior-level contexts.
func (p *parser) bhvResolver(syms *symbolTable) operandResolver {
	return func(tok token) (Expr, error) {
		return p.resolveBhvOperand(tok, syms)
	}
}

// parseArithPrimary parses an arithmetic atom: number literal, null,
// variable, $register, constructor, unary minus, or a parenthesized
// sub-expression.
func (p *parser) parseArithPrimary(resolve operandResolver) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	switch tok.kind {
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		return &LiteralExpr{Value: map[string]any{"num": num}}, nil
	case tokMinus:
		inner, err := p.parseArithPrimary(resolve)
		if err != nil {
			return nil, err
		}
		// Compile-time fold for number literals
		if lit, ok := inner.(*LiteralExpr); ok {
			if m, ok := lit.Value.(map[string]any); ok {
				if n, ok := m["num"]; ok {
					return &LiteralExpr{Value: map[string]any{"num": -(n.(int))}}, nil
				}
			}
		}
		// Desugar -expr to 0 - expr
		return &ArithExpr{
			Op:  tokMinus,
			LHS: &LiteralExpr{Value: map[string]any{"num": 0}},
			RHS: inner,
		}, nil
	case tokLParen:
		val, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return val, nil
	case tokIdent:
		if tok.val == "null" || tok.val == "false" {
			return &LiteralExpr{Value: false}, nil
		}
		if tok.val == "true" {
			return &LiteralExpr{Value: map[string]any{"num": 1}}, nil
		}
		if isConstructor(tok.val) {
			return p.parseArithConstructor(tok, resolve)
		}
		if tok.val == "Unit" {
			return nil, p.errorf(tok.pos, "Unit has no constructor; unit values are produced by instructions at runtime")
		}
		// Check constants (direct lookup) before function resolution
		if c, ok := p.consts[tok.val]; ok {
			return &LiteralExpr{Value: c.value}, nil
		}
		if p.callExprParser != nil {
			name, callee, err := p.resolveFnName(tok)
			if err != nil {
				return nil, err
			}
			// Check if resolved name is a namespace constant (from ns.name dot access)
			if c, ok := p.consts[name]; ok {
				return &LiteralExpr{Value: c.value}, nil
			}
			if callee != nil && callee.hasReturn() {
				return p.callExprParser(callee, token{kind: tokIdent, val: name, pos: tok.pos})
			}
		}
		return resolve(tok)
	default:
		return nil, p.errorf(tok.pos, "expected number, variable, or '(' in arithmetic expression, got %s", tok.describe())
	}
}

// parseArithConstructor parses a type constructor in arithmetic/comparison
// context. Produces a LiteralExpr (all-literal args) or ConstructorExpr.
func (p *parser) parseArithConstructor(nameTok token, resolve operandResolver) (Expr, error) {
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
		ctor := &ConstructorExpr{
			TypeName: nameTok.val,
			Args:     []Expr{&LiteralExpr{Value: argTok.val}},
		}
		if val, ok := tryResolveConstructorLiteral(ctor); ok {
			return &LiteralExpr{Value: val}, nil
		}
		return ctor, nil
	case "Coordinate":
		x, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokComma); err != nil {
			return nil, err
		}
		y, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		ctor := &ConstructorExpr{
			TypeName: "Coordinate",
			Args:     []Expr{x, y},
		}
		if val, ok := tryResolveConstructorLiteral(ctor); ok {
			return &LiteralExpr{Value: val}, nil
		}
		return ctor, nil
	case "Range":
		arg1, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			ctor := &ConstructorExpr{
				TypeName: "Range",
				Args: []Expr{
					&LiteralExpr{Value: map[string]any{"num": 0}},
					arg1,
					&LiteralExpr{Value: map[string]any{"num": 1}},
				},
			}
			if val, ok := tryResolveConstructorLiteral(ctor); ok {
				return &LiteralExpr{Value: val}, nil
			}
			return ctor, nil
		}
		if peek.kind != tokComma {
			return nil, p.errorf(peek.pos, "expected ',' or ')' after Range argument, got %s", peek.describe())
		}
		arg2, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		peek, err = p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			ctor := &ConstructorExpr{
				TypeName: "Range",
				Args:     []Expr{arg1, arg2, &LiteralExpr{Value: map[string]any{"num": 1}}},
			}
			if val, ok := tryResolveConstructorLiteral(ctor); ok {
				return &LiteralExpr{Value: val}, nil
			}
			return ctor, nil
		}
		if peek.kind != tokComma {
			return nil, p.errorf(peek.pos, "expected ',' or ')' after Range argument, got %s", peek.describe())
		}
		arg3, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		ctor := &ConstructorExpr{
			TypeName: "Range",
			Args:     []Expr{arg1, arg2, arg3},
		}
		if val, ok := tryResolveConstructorLiteral(ctor); ok {
			return &LiteralExpr{Value: val}, nil
		}
		return ctor, nil
	default:
		return nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
	}
}

// parseArithTerm parses `primary (* | / primary)*`.
func (p *parser) parseArithTerm(resolve operandResolver) (Expr, error) {
	lhs, err := p.parseArithPrimary(resolve)
	if err != nil {
		return nil, err
	}
	return p.parseArithTermFrom(lhs, resolve)
}

// parseArithTermFrom parses `(* | / primary)*` from an already-parsed first.
func (p *parser) parseArithTermFrom(first Expr, resolve operandResolver) (Expr, error) {
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
		rhs, err := p.parseArithPrimary(resolve)
		if err != nil {
			return nil, err
		}
		expr := &ArithExpr{Op: peek.kind, LHS: result, RHS: rhs}
		if folded, ok := tryFoldArith(expr); ok {
			result = folded
		} else {
			result = expr
		}
	}
}

// parseArithExpr parses `term (+ | - term)*`.
func (p *parser) parseArithExpr(resolve operandResolver) (Expr, error) {
	lhs, err := p.parseArithTerm(resolve)
	if err != nil {
		return nil, err
	}
	return p.parseArithExprFrom(lhs, resolve)
}

// parseArithExprFrom parses `(+ | - term)*` from an already-parsed first.
func (p *parser) parseArithExprFrom(first Expr, resolve operandResolver) (Expr, error) {
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
		rhs, err := p.parseArithTerm(resolve)
		if err != nil {
			return nil, err
		}
		expr := &ArithExpr{Op: peek.kind, LHS: result, RHS: rhs}
		if folded, ok := tryFoldArith(expr); ok {
			result = folded
		} else {
			result = expr
		}
	}
}

// tryFoldArith attempts to fold an ArithExpr with two literal numeric operands
// into a single LiteralExpr. Returns (nil, false) if either operand is not a
// numeric literal or if division by zero would occur.
func tryFoldArith(expr *ArithExpr) (*LiteralExpr, bool) {
	lLit, lOk := expr.LHS.(*LiteralExpr)
	rLit, rOk := expr.RHS.(*LiteralExpr)
	if !lOk || !rOk {
		return nil, false
	}
	lNum, lHas := extractNum(lLit.Value)
	rNum, rHas := extractNum(rLit.Value)
	if !lHas || !rHas {
		return nil, false
	}
	var result int
	switch expr.Op {
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
	return &LiteralExpr{Value: map[string]any{"num": result}}, true
}

// isCompileTimeConstant reports whether a LiteralExpr value represents an
// actual compile-time constant (number, typed value, coordinate, null/false)
// as opposed to a runtime reference (parameter index as int, variable name
// as string).
func isCompileTimeConstant(v any) bool {
	switch v.(type) {
	case map[string]any, bool:
		return true
	default:
		return v == nil
	}
}

// tryFoldCompare attempts to fold a CompareExpr with two literal operands
// into a boolean LiteralExpr. Returns (nil, false) if either operand is not
// a compile-time constant literal.
func tryFoldCompare(expr *CompareExpr) (*LiteralExpr, bool) {
	lLit, lOk := expr.LHS.(*LiteralExpr)
	rLit, rOk := expr.RHS.(*LiteralExpr)
	if !lOk || !rOk {
		return nil, false
	}
	// Only fold actual compile-time constants, not runtime references
	// (parameter indices are int, variable names are string).
	if !isCompileTimeConstant(lLit.Value) || !isCompileTimeConstant(rLit.Value) {
		return nil, false
	}
	if evalCompare(expr.Op, lLit.Value, rLit.Value) {
		return &LiteralExpr{Value: map[string]any{"num": 1}}, true
	}
	return &LiteralExpr{Value: false}, true
}

// tryFoldBoolChain attempts to fold a BoolChainExpr when all children are
// literal values. Returns (nil, false) if any child is not a LiteralExpr.
func tryFoldBoolChain(expr *BoolChainExpr) (*LiteralExpr, bool) {
	for _, child := range expr.Children {
		if _, ok := child.(*LiteralExpr); !ok {
			return nil, false
		}
	}
	switch expr.Op {
	case tokDoubleAmpersand:
		// All must be truthy
		for _, child := range expr.Children {
			if !isTruthy(child.(*LiteralExpr).Value) {
				return &LiteralExpr{Value: false}, true
			}
		}
		return &LiteralExpr{Value: map[string]any{"num": 1}}, true
	case tokDoublePipe:
		// Any truthy is enough
		for _, child := range expr.Children {
			if isTruthy(child.(*LiteralExpr).Value) {
				return &LiteralExpr{Value: map[string]any{"num": 1}}, true
			}
		}
		return &LiteralExpr{Value: false}, true
	}
	return nil, false
}

// tryFoldNot attempts to fold a NotExpr when the inner value is a literal.
// Returns (nil, false) if the inner value is not a LiteralExpr.
func tryFoldNot(expr *NotExpr) (*LiteralExpr, bool) {
	lit, ok := expr.Value.(*LiteralExpr)
	if !ok {
		return nil, false
	}
	if isTruthy(lit.Value) {
		return &LiteralExpr{Value: false}, true
	}
	return &LiteralExpr{Value: map[string]any{"num": 1}}, true
}

// parseArithExprFromFull parses a full PEMDAS expression from an
// already-parsed first value.
func (p *parser) parseArithExprFromFull(first Expr, resolve operandResolver) (Expr, error) {
	termResult, err := p.parseArithTermFrom(first, resolve)
	if err != nil {
		return nil, err
	}
	return p.parseArithExprFrom(termResult, resolve)
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
	if _, ok := syms.lookupVar(tok.val); !ok {
		// Check constants before erroring
		if c, ok := p.consts[tok.val]; ok {
			return &LiteralExpr{Value: c.value}, nil
		}
		if tok.val == "Unit" {
			return nil, p.errorf(tok.pos, "Unit has no constructor; unit values are produced by instructions at runtime")
		}
		return nil, p.errorf(tok.pos, "unknown function or variable %q", tok.val)
	}
	syms.markUsed(tok.val)
	return &IdentExpr{Name: tok.val}, nil
}

// parseBoolPrimary parses a single boolean term: parenthesized
// sub-expression, or value (with optional arithmetic) followed by
// comparison operator, 'is', or nothing (truthy check).
func (p *parser) parseBoolPrimary(resolve operandResolver) (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}

	if tok.kind == tokBang {
		inner, err := p.parseBoolPrimary(resolve)
		if err != nil {
			return nil, err
		}
		notExpr := &NotExpr{Value: inner}
		if folded, ok := tryFoldNot(notExpr); ok {
			return folded, nil
		}
		return notExpr, nil
	}

	if tok.kind == tokLParen {
		inner, err := p.parseBoolExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return inner, nil
	}

	var lhs Expr
	if tok.kind == tokNumber || tok.kind == tokMinus || tok.kind == tokIdent {
		p.unget(tok)
		lhs, err = p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
	} else if tok.kind == tokString {
		return nil, p.errorf(tok.pos, "strings have no runtime representation and cannot be stored in variables; use them directly as function arguments (e.g., notify \"hello\")")
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
		rhs, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		cmp := &CompareExpr{Op: cmpTok.kind, LHS: lhs, RHS: rhs}
		if folded, ok := tryFoldCompare(cmp); ok {
			return folded, nil
		}
		return cmp, nil
	}
	if cmpTok.kind == tokEquals {
		return nil, p.errorf(cmpTok.pos, "unexpected '=' — use '==' for comparison")
	}

	p.unget(cmpTok)
	return &TruthyExpr{Value: lhs}, nil
}

// parseBoolExpr parses a complete boolean expression.
func (p *parser) parseBoolExpr(resolve operandResolver) (Expr, error) {
	first, err := p.parseBoolPrimary(resolve)
	if err != nil {
		return nil, err
	}
	return p.parseBoolChain(first, resolve)
}

// parseBoolChain peeks for &&/||. If absent, returns first unchanged.
// Implements standard precedence: && binds tighter than ||.
func (p *parser) parseBoolChain(first Expr, resolve operandResolver) (Expr, error) {
	// Collect an &&-chain starting from first.
	andGroup, err := p.collectAndChain(first, resolve)
	if err != nil {
		return nil, err
	}

	// Check for ||. If absent, return the &&-chain (or single term).
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek.kind != tokDoublePipe {
		p.unget(peek)
		return andGroup, nil
	}

	// We have ||. Build an ||-chain of &&-groups.
	orChildren := []Expr{andGroup}
	for {
		next, err := p.parseBoolPrimary(resolve)
		if err != nil {
			return nil, err
		}
		group, err := p.collectAndChain(next, resolve)
		if err != nil {
			return nil, err
		}
		orChildren = append(orChildren, group)

		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind != tokDoublePipe {
			p.unget(tok)
			break
		}
	}

	chain := &BoolChainExpr{Op: tokDoublePipe, Children: orChildren}
	if folded, ok := tryFoldBoolChain(chain); ok {
		return folded, nil
	}
	return chain, nil
}

// collectAndChain collects an &&-chain starting from first.
// Returns first unchanged if no && follows.
func (p *parser) collectAndChain(first Expr, resolve operandResolver) (Expr, error) {
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek.kind != tokDoubleAmpersand {
		p.unget(peek)
		return first, nil
	}

	children := []Expr{first}
	for {
		next, err := p.parseBoolPrimary(resolve)
		if err != nil {
			return nil, err
		}
		children = append(children, next)

		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind != tokDoubleAmpersand {
			p.unget(tok)
			break
		}
	}

	chain := &BoolChainExpr{Op: tokDoubleAmpersand, Children: children}
	if folded, ok := tryFoldBoolChain(chain); ok {
		return folded, nil
	}
	return chain, nil
}

// parseBhvArgExpr parses a single argument value into an Expr.
func (p *parser) parseBhvArgExpr(syms *symbolTable) (Expr, error) {
	resolve := p.bhvResolver(syms)
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	var base Expr
	switch tok.kind {
	case tokString:
		base = &LiteralExpr{Value: tok.val}
	case tokMinus, tokNumber:
		p.unget(tok)
		result, err := p.parseArithExpr(resolve)
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
		} else if isConstructor(tok.val) {
			ctor, err := p.parseBhvConstructorExpr(tok, syms)
			if err != nil {
				return nil, err
			}
			base = ctor
		} else if tok.val == "locked" || tok.val == "unlocked" {
			mbe, err := p.parseBhvModeBlockExpr(tok.val == "unlocked", syms, "")
			if err != nil {
				return nil, err
			}
			result, err := p.parseArithExprFromFull(Expr(mbe), resolve)
			if err != nil {
				return nil, err
			}
			base = result
		} else if tok.val == "if" {
			ifExpr, err := p.parseBhvIfExpr(syms, "")
			if err != nil {
				return nil, err
			}
			result, err := p.parseArithExprFromFull(Expr(ifExpr), resolve)
			if err != nil {
				return nil, err
			}
			base = result
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
			result, err := p.parseArithExprFromFull(resolved, resolve)
			if err != nil {
				return nil, err
			}
			base = result
		} else {
			p.unget(tok)
			result, err := p.parseArithExpr(resolve)
			if err != nil {
				return nil, err
			}
			base = result
		}
	case tokLParen:
		// Parenthesized expression: (a > 5), (a + 1), etc.
		inner, err := p.parseBoolExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		if truthy, ok := inner.(*TruthyExpr); ok {
			// Simple value — allow arithmetic continuation
			result, err := p.parseArithExprFromFull(truthy.Value, resolve)
			if err != nil {
				return nil, err
			}
			base = result
		} else {
			base = inner
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
		if ctor, ok := base.(*ConstructorExpr); ok && ctor.TypeName == "Range" {
			return nil, p.errorf(peek.pos, "'&' cannot be used with Range (it would overwrite the step field)")
		}
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
// Delegates to the shared parseConstructorExpr, then checks for trailing &.
func (p *parser) parseBhvConstructorExpr(nameTok token, syms *symbolTable) (Expr, error) {
	parseArg := func() (Expr, error) { return p.parseBhvArgExpr(syms) }
	base, err := p.parseConstructorExpr(nameTok, parseArg)
	if err != nil {
		return nil, err
	}
	// Range doesn't support &; Item/Component/Technology/Value/Coordinate do.
	if ctorExpr, ok := base.(*ConstructorExpr); ok && ctorExpr.TypeName == "Range" {
		return base, nil
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

// parseBhvCallArgs parses a function call's argument list into AST Exprs.
// Supports both unparenthesized (notify "Hello") and parenthesized
// (notify("Hello")) forms.
func (p *parser) parseBhvCallArgs(fn *fnDef, nameTok token, syms *symbolTable) ([]Expr, map[string]Expr, error) {
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

	posCount := fn.positionalCount()
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
		// In parenthesized call mode, try boolean continuation
		// to support: notify(val > 5), add(a, b == c)
		if paren {
			resolve := p.bhvResolver(syms)
			cont, handled, err := p.maybeExprContinuation(val, resolve)
			if err != nil {
				return nil, nil, err
			}
			if handled {
				val = cont
			}
		}
		args[i] = val
	}

	// Parse optional keyword args
	var kwArgs map[string]Expr
	peek, err = p.next()
	if err != nil {
		return nil, nil, err
	}
	if (peek.kind == tokString || peek.kind == tokNumber) && fn.positionalCount() < len(fn.params) {
		return nil, nil, p.errorf(peek.pos,
			"too many positional arguments for %s (remaining parameters are keyword-only)", nameTok.val)
	}
	if peek.kind == tokComma && fn.positionalCount() < len(fn.params) {
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

	if paren {
		if _, err := p.expect(tokRParen); err != nil {
			return nil, nil, err
		}
	}

	return args, kwArgs, nil
}

// maybeExprContinuation peeks for comparison/is/&&/|| after a value.
// Returns (expr, true) if continuation found, (original, false) otherwise.
func (p *parser) maybeExprContinuation(valueExpr Expr, resolve operandResolver) (Expr, bool, error) {
	peek, err := p.next()
	if err != nil {
		return nil, false, err
	}
	if isComparisonOp(peek.kind) {
		rhs, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, false, err
		}
		cmp := Expr(&CompareExpr{Op: peek.kind, LHS: valueExpr, RHS: rhs})
		chained, err := p.parseBoolChain(cmp, resolve)
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
		chained, err := p.parseBoolChain(tc, resolve)
		if err != nil {
			return nil, false, err
		}
		return chained, true, nil
	}
	if peek.kind == tokDoubleAmpersand || peek.kind == tokDoublePipe {
		p.unget(peek)
		truthy := Expr(&TruthyExpr{Value: valueExpr})
		chained, err := p.parseBoolChain(truthy, resolve)
		if err != nil {
			return nil, false, err
		}
		return chained, true, nil
	}
	p.unget(peek)
	return valueExpr, false, nil
}

// maybeBhvExprContinuation peeks for comparison/is/&&/|| after a value.
// Returns (expr, true) if continuation found, (original, false) otherwise.
func (p *parser) maybeBhvExprContinuation(valueExpr Expr, syms *symbolTable) (Expr, bool, error) {
	return p.maybeExprContinuation(valueExpr, p.bhvResolver(syms))
}

// -----------------------------------------------------------------------
// Statement parsers → Stmt nodes
// -----------------------------------------------------------------------

// parseBhvVarInit parses the RHS of a var/let declaration after '='.
// May return multiple statements (e.g., fn call + boolean continuation).
func (p *parser) parseBhvVarInit(nameTok token, mutable bool, syms *symbolTable) ([]Stmt, error) {
	resolve := p.bhvResolver(syms)
	comment := p.docComment
	rhsTok, err := p.next()
	if err != nil {
		return nil, err
	}

	// Mode block expression RHS: let x = unlocked { ... }
	// Supports continuation: let x = unlocked { get_number v } + 1
	if rhsTok.kind == tokIdent && (rhsTok.val == "locked" || rhsTok.val == "unlocked") {
		mbe, err := p.parseBhvModeBlockExpr(rhsTok.val == "unlocked", syms, comment)
		if err != nil {
			return nil, err
		}
		result, err := p.parseArithExprFromFull(Expr(mbe), resolve)
		if err != nil {
			return nil, err
		}
		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)
		final, handled, err := p.maybeBhvExprContinuation(result, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
		}
		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
	}

	// If-expression RHS: let x = if cond { ... } else { ... }
	// Supports continuation: let x = if cond { a } else { b } + 1
	if rhsTok.kind == tokIdent && rhsTok.val == "if" {
		ifExpr, err := p.parseBhvIfExpr(syms, comment)
		if err != nil {
			return nil, err
		}
		result, err := p.parseArithExprFromFull(Expr(ifExpr), resolve)
		if err != nil {
			return nil, err
		}
		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)
		final, handled, err := p.maybeBhvExprContinuation(result, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
		}
		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
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
		result, err := p.parseArithExprFromFull(numExpr, resolve)
		if err != nil {
			return nil, err
		}

		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)

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
		// Range constructor doesn't handle & internally; check and error
		if ctorExpr, ok := ctor.(*ConstructorExpr); ok && ctorExpr.TypeName == "Range" {
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokAmpersand {
				return nil, p.errorf(peek.pos, "'&' cannot be used with Range (it would overwrite the step field)")
			}
			p.unget(peek)
		}
		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)
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
		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)
		return []Stmt{&LetStmt{
			Name:    nameTok.val,
			Mutable: mutable,
			Value:   &InstructionExpr{Frame: rawFrame},
			Comment: comment,
		}}, nil
	}

	if rhsTok.kind == tokIdent {
		// Boolean/null keyword literals: true, false, null.
		// Handle before function lookup so these are treated as literals,
		// not as unknown functions or variable names.
		if rhsTok.val == "null" || rhsTok.val == "false" || rhsTok.val == "true" {
			var litVal any
			if rhsTok.val == "true" {
				litVal = map[string]any{"num": 1}
			} else {
				litVal = false
			}
			litExpr := Expr(&LiteralExpr{Value: litVal})
			result, err := p.parseArithExprFromFull(litExpr, resolve)
			if err != nil {
				return nil, err
			}
			syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)
			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
			}
			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
		}

		rhsName, fn, fnErr := p.resolveFnName(rhsTok)
		if fnErr != nil {
			return nil, fnErr
		}
		// Check if resolveFnName resolved to a constant (e.g., ns.CONST)
		if c, ok := p.consts[rhsName]; ok {
			litExpr := Expr(&LiteralExpr{Value: c.value})
			result, err := p.parseArithExprFromFull(litExpr, resolve)
			if err != nil {
				return nil, err
			}
			syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)
			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
			}
			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
		}
		if fn == nil {
			// Not a function — parse as value with arithmetic/comparison/boolean
			resolved, err := resolve(rhsTok)
			if err != nil {
				return nil, err
			}

			result, err := p.parseArithExprFromFull(resolved, resolve)
			if err != nil {
				return nil, err
			}

			// Check for & after variable/expression (not supported in declarations)
			ampPeek, err := p.next()
			if err != nil {
				return nil, err
			}
			if ampPeek.kind == tokAmpersand {
				return nil, p.errorf(ampPeek.pos, "'&' requires a type constructor on the left side; use set_number to attach a number to an existing value")
			}
			p.unget(ampPeek)

			syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)

			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
			}

			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
		}
		if !fn.hasReturn() {
			return nil, p.errorf(rhsTok.pos, "function %q has no return value", rhsName)
		}
		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)
		args, kwArgs, err := p.parseBhvCallArgs(fn, token{kind: tokIdent, val: rhsName, pos: rhsTok.pos}, syms)
		if err != nil {
			return nil, err
		}

		callExpr := &CallExpr{Name: rhsName, Args: args, KwArgs: kwArgs}

		// Check for comparison/boolean continuation after fn call.
		// Use a temp variable for the fn result so the intermediate value
		// is not observable in the target variable.
		tmp := allocUniqueVar("@call", syms.usedVars)
		contExpr, handled, err := p.maybeBhvExprContinuation(&IdentExpr{Name: tmp}, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			return []Stmt{
				&LetStmt{Name: tmp, Mutable: false, Value: callExpr, Comment: comment},
				&LetStmt{Name: nameTok.val, Mutable: mutable, Value: contExpr, Comment: ""},
			}, nil
		}
		delete(syms.usedVars, tmp) // undo temp allocation when unused

		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: callExpr, Comment: comment}}, nil
	}

	if rhsTok.kind == tokLParen {
		p.unget(rhsTok)
		expr, err := p.parseBoolExpr(resolve)
		if err != nil {
			return nil, err
		}
		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)

		// Single truthy = parenthesized value — check for arithmetic continuation
		if truthy, ok := expr.(*TruthyExpr); ok {
			innerVal := truthy.Value
			result, err := p.parseArithExprFromFull(innerVal, resolve)
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

	if rhsTok.kind == tokBang || rhsTok.kind == tokMinus {
		p.unget(rhsTok)
		expr, err := p.parseBoolExpr(resolve)
		if err != nil {
			return nil, err
		}
		syms.declareVarWarn(nameTok.val, mutable, p, nameTok.pos)

		// Unary minus produces TruthyExpr wrapping arithmetic — unwrap it
		if truthy, ok := expr.(*TruthyExpr); ok {
			result := truthy.Value
			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: final, Comment: comment}}, nil
			}
			return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: result, Comment: comment}}, nil
		}

		return []Stmt{&LetStmt{Name: nameTok.val, Mutable: mutable, Value: expr, Comment: comment}}, nil
	}

	if rhsTok.kind == tokString {
		return nil, p.errorf(rhsTok.pos, "strings have no runtime representation and cannot be stored in variables; use them directly as function arguments (e.g., notify \"hello\")")
	}
	return nil, p.errorf(rhsTok.pos, "expected number, function call, or constructor after '=', got %s", rhsTok.describe())
}

// parseBhvDefaultStmt parses a function call, assignment, compound assignment,
// or increment/decrement. Returns one or more statements.
func (p *parser) parseBhvDefaultStmt(tok token, syms *symbolTable) ([]Stmt, error) {
	resolve := p.bhvResolver(syms)
	comment := p.docComment
	tok2, err := p.next()
	if err != nil {
		return nil, err
	}

	if tok2.kind == tokPlusPlus || tok2.kind == tokMinusMinus || tok2.kind == tokEquals || isCompoundAssignOp(tok2.kind) {
		if err := p.checkVarName(tok.val, syms, tok.pos); err != nil {
			return nil, err
		}
	}

	if tok2.kind == tokPlusPlus {
		return []Stmt{&IncrDecrStmt{Target: tok.val, Op: tokPlusPlus, Comment: comment, Pos: tok.pos}}, nil
	}
	if tok2.kind == tokMinusMinus {
		return []Stmt{&IncrDecrStmt{Target: tok.val, Op: tokMinusMinus, Comment: comment, Pos: tok.pos}}, nil
	}

	if tok2.kind == tokEquals {
		rhsTok, err := p.next()
		if err != nil {
			return nil, err
		}

		// Mode block expression RHS: x = unlocked { ... }
		// Supports continuation: x = unlocked { get_number v } + 1
		if rhsTok.kind == tokIdent && (rhsTok.val == "locked" || rhsTok.val == "unlocked") {
			mbe, err := p.parseBhvModeBlockExpr(rhsTok.val == "unlocked", syms, comment)
			if err != nil {
				return nil, err
			}
			result, err := p.parseArithExprFromFull(Expr(mbe), resolve)
			if err != nil {
				return nil, err
			}
			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment, Pos: tok.pos}}, nil
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment, Pos: tok.pos}}, nil
		}

		// If-expression RHS: x = if cond { ... } else { ... }
		// Supports continuation: x = if cond { a } else { b } + 1
		if rhsTok.kind == tokIdent && rhsTok.val == "if" {
			ifExpr, err := p.parseBhvIfExpr(syms, comment)
			if err != nil {
				return nil, err
			}
			result, err := p.parseArithExprFromFull(Expr(ifExpr), resolve)
			if err != nil {
				return nil, err
			}
			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment, Pos: tok.pos}}, nil
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment, Pos: tok.pos}}, nil
		}

		if rhsTok.kind == tokNumber {
			num, _ := strconv.Atoi(rhsTok.val)
			numExpr := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
			result, err := p.parseArithExprFromFull(numExpr, resolve)
			if err != nil {
				return nil, err
			}

			final, handled, err := p.maybeBhvExprContinuation(result, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment, Pos: tok.pos}}, nil
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment, Pos: tok.pos}}, nil
		}

		if rhsTok.kind == tokIdent && isConstructor(rhsTok.val) {
			p.unget(rhsTok)
			ctor, err := p.parseBhvArgExpr(syms)
			if err != nil {
				return nil, err
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: ctor, Comment: comment, Pos: tok.pos}}, nil
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
				Pos:     tok.pos,
			}}, nil
		}

		if rhsTok.kind == tokIdent {
			// Boolean/null keyword literals: true, false, null.
			// Handle before function lookup so these are treated as literals,
			// not as unknown functions or variable names.
			if rhsTok.val == "null" || rhsTok.val == "false" || rhsTok.val == "true" {
				var litVal any
				if rhsTok.val == "true" {
					litVal = map[string]any{"num": 1}
				} else {
					litVal = false
				}
				litExpr := Expr(&LiteralExpr{Value: litVal})
				result, err := p.parseArithExprFromFull(litExpr, resolve)
				if err != nil {
					return nil, err
				}
				final, handled, err := p.maybeBhvExprContinuation(result, syms)
				if err != nil {
					return nil, err
				}
				if handled {
					return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment, Pos: tok.pos}}, nil
				}
				return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment, Pos: tok.pos}}, nil
			}

			rhsName, fn, fnErr := p.resolveFnName(rhsTok)
			if fnErr != nil {
				return nil, fnErr
			}
			if fn == nil {
				// Not a function — value + arithmetic/comparison/boolean
				resolved, err := resolve(rhsTok)
				if err != nil {
					return nil, err
				}
				result, err := p.parseArithExprFromFull(resolved, resolve)
				if err != nil {
					return nil, err
				}

				// Check for & after variable/expression (not supported in assignments)
				ampPeek, err := p.next()
				if err != nil {
					return nil, err
				}
				if ampPeek.kind == tokAmpersand {
					return nil, p.errorf(ampPeek.pos, "'&' requires a type constructor on the left side; use set_number to attach a number to an existing value")
				}
				p.unget(ampPeek)

				final, handled, err := p.maybeBhvExprContinuation(result, syms)
				if err != nil {
					return nil, err
				}
				if handled {
					return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment, Pos: tok.pos}}, nil
				}

				return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment, Pos: tok.pos}}, nil
			}
			if !fn.hasReturn() {
				return nil, p.errorf(rhsTok.pos, "function %q has no return value", rhsName)
			}
			args, kwArgs, err := p.parseBhvCallArgs(fn, token{kind: tokIdent, val: rhsName, pos: rhsTok.pos}, syms)
			if err != nil {
				return nil, err
			}

			callExpr := &CallExpr{Name: rhsName, Args: args, KwArgs: kwArgs}

			// Check for continuation after fn call.
			// Use a temp variable for the fn result so the intermediate value
			// is not observable in the target variable.
			tmp := allocUniqueVar("@call", syms.usedVars)
			contExpr, handled, err := p.maybeBhvExprContinuation(&IdentExpr{Name: tmp}, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{
					&LetStmt{Name: tmp, Mutable: false, Value: callExpr, Comment: comment},
					&AssignStmt{Target: tok.val, Value: contExpr, Comment: "", Pos: tok.pos},
				}, nil
			}
			delete(syms.usedVars, tmp) // undo temp allocation when unused
			return []Stmt{&AssignStmt{Target: tok.val, Value: callExpr, Comment: comment, Pos: tok.pos}}, nil
		}

		if rhsTok.kind == tokLParen {
			p.unget(rhsTok)
			expr, err := p.parseBoolExpr(resolve)
			if err != nil {
				return nil, err
			}

			if truthy, ok := expr.(*TruthyExpr); ok {
				innerVal := truthy.Value
				result, err := p.parseArithExprFromFull(innerVal, resolve)
				if err != nil {
					return nil, err
				}
				final, handled, err := p.maybeBhvExprContinuation(result, syms)
				if err != nil {
					return nil, err
				}
				if handled {
					return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment, Pos: tok.pos}}, nil
				}
				return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment, Pos: tok.pos}}, nil
			}

			// Check for continuation after parenthesized expression
			contExpr, handled, err := p.maybeBhvExprContinuation(&IdentExpr{Name: tok.val}, syms)
			if err != nil {
				return nil, err
			}
			if handled {
				return []Stmt{
					&AssignStmt{Target: tok.val, Value: expr, Comment: comment, Pos: tok.pos},
					&AssignStmt{Target: tok.val, Value: contExpr, Comment: "", Pos: tok.pos},
				}, nil
			}
			return []Stmt{&AssignStmt{Target: tok.val, Value: expr, Comment: comment, Pos: tok.pos}}, nil
		}

		if rhsTok.kind == tokBang || rhsTok.kind == tokMinus {
			p.unget(rhsTok)
			expr, err := p.parseBoolExpr(resolve)
			if err != nil {
				return nil, err
			}

			// Unary minus produces TruthyExpr wrapping arithmetic — unwrap it
			if truthy, ok := expr.(*TruthyExpr); ok {
				result := truthy.Value
				final, handled, err := p.maybeBhvExprContinuation(result, syms)
				if err != nil {
					return nil, err
				}
				if handled {
					return []Stmt{&AssignStmt{Target: tok.val, Value: final, Comment: comment, Pos: tok.pos}}, nil
				}
				return []Stmt{&AssignStmt{Target: tok.val, Value: result, Comment: comment, Pos: tok.pos}}, nil
			}

			return []Stmt{&AssignStmt{Target: tok.val, Value: expr, Comment: comment, Pos: tok.pos}}, nil
		}

		if rhsTok.kind == tokString {
			return nil, p.errorf(rhsTok.pos, "strings have no runtime representation and cannot be stored in variables; use them directly as function arguments (e.g., notify \"hello\")")
		}
		return nil, p.errorf(rhsTok.pos, "expected number, function call, constructor, or instruction after '=', got %s", rhsTok.describe())
	}

	if isCompoundAssignOp(tok2.kind) {
		rhs, err := p.parseBoolExpr(resolve)
		if err != nil {
			return nil, err
		}
		// Unwrap TruthyExpr for plain arithmetic/value results
		if truthy, ok := rhs.(*TruthyExpr); ok {
			rhs = truthy.Value
		}
		return []Stmt{&CompoundAssignStmt{Target: tok.val, Op: tok2.kind, Value: rhs, Comment: comment, Pos: tok.pos}}, nil
	}

	// Function call
	p.unget(tok2)
	name, fn, fnErr := p.resolveFnName(tok)
	if fnErr != nil {
		return nil, fnErr
	}
	if fn == nil {
		return nil, p.errorf(tok.pos, "unknown function %q", tok.val)
	}
	args, kwArgs, err := p.parseBhvCallArgs(fn, token{kind: tokIdent, val: name, pos: tok.pos}, syms)
	if err != nil {
		return nil, err
	}
	return []Stmt{&CallStmt{Name: name, Args: args, KwArgs: kwArgs, Comment: comment}}, nil
}

// parseBhvIfStmt parses an if/else-if/else statement with full boolean
// expression support (comparisons, &&/||, is, truthy, function calls).
func (p *parser) parseBhvIfStmt(syms *symbolTable) (*IfStmt, error) {
	comment := p.docComment
	resolve := p.bhvResolver(syms)
	cond, err := p.parseBoolPrimary(resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, resolve)
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	body, err := p.parseBhvStmtBlockInner(syms)
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
			elseBody, err := p.parseBhvStmtBlockInner(syms)
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
	resolve := p.bhvResolver(syms)
	cond, err := p.parseBoolPrimary(resolve)
	if err != nil {
		return err
	}
	cond, err = p.parseBoolChain(cond, resolve)
	if err != nil {
		return err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	body, err := p.parseBhvStmtBlockInner(syms)
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
		elseBody, err := p.parseBhvStmtBlockInner(syms)
		if err != nil {
			return err
		}
		stmt.Else = elseBody
	} else {
		p.unget(tok)
	}

	return nil
}

// parseBhvWhileStmt parses a while loop with full boolean expression support.
func (p *parser) parseBhvWhileStmt(syms *symbolTable, label ...string) (*WhileStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}
	comment := p.docComment
	resolve := p.bhvResolver(syms)
	cond, err := p.parseBoolPrimary(resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, resolve)
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	p.enterLoop(lbl)
	body, err := p.parseBhvStmtBlockInner(syms)
	p.exitLoop(lbl)
	if err != nil {
		return nil, err
	}

	return &WhileStmt{Label: lbl, Cond: cond, Body: body, Comment: comment}, nil
}

// parseBhvLoopStmt parses a loop { ... } or loop N { ... } block.
func (p *parser) parseBhvLoopStmt(syms *symbolTable, label ...string) (*LoopStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}
	comment := p.docComment

	// Peek for count expression
	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	var count Expr
	if peek.kind == tokLBrace {
		// Infinite loop: loop { ... }
		p.enterLoop(lbl)
		body, err := p.parseBhvStmtBlockInner(syms)
		p.exitLoop(lbl)
		if err != nil {
			return nil, err
		}
		return &LoopStmt{Label: lbl, Body: body, Comment: comment}, nil
	}

	// Counted loop: parse count expression
	resolve := p.bhvResolver(syms)
	switch peek.kind {
	case tokNumber:
		num, _ := strconv.Atoi(peek.val)
		count = &LiteralExpr{Value: map[string]any{"num": num}}
		// Check for arithmetic continuation
		count, err = p.parseArithExprFromFull(count, resolve)
		if err != nil {
			return nil, err
		}
	case tokLParen:
		count, err = p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
	case tokIdent:
		resolved, err := resolve(peek)
		if err != nil {
			return nil, err
		}
		count, err = p.parseArithExprFromFull(resolved, resolve)
		if err != nil {
			return nil, err
		}
	default:
		return nil, p.errorf(peek.pos, "expected '{' or count expression after 'loop', got %s", peek.describe())
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	p.enterLoop(lbl)
	body, err := p.parseBhvStmtBlockInner(syms)
	p.exitLoop(lbl)
	if err != nil {
		return nil, err
	}
	return &LoopStmt{Label: lbl, Count: count, Body: body, Comment: comment}, nil
}

// parseBhvWaitStmt parses a wait statement: `wait <ticks>` or `wait <ticks> { body; cond }`.
func (p *parser) parseBhvWaitStmt(syms *symbolTable) (*WaitStmt, error) {
	comment := p.docComment
	resolve := p.bhvResolver(syms)

	// Parse ticks expression (same pattern as parseBhvLoopStmt count)
	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	var ticks Expr
	switch peek.kind {
	case tokNumber:
		num, _ := strconv.Atoi(peek.val)
		ticks = &LiteralExpr{Value: map[string]any{"num": num}}
		ticks, err = p.parseArithExprFromFull(ticks, resolve)
		if err != nil {
			return nil, err
		}
	case tokLParen:
		ticks, err = p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
	case tokIdent:
		resolved, err := resolve(peek)
		if err != nil {
			return nil, err
		}
		ticks, err = p.parseArithExprFromFull(resolved, resolve)
		if err != nil {
			return nil, err
		}
	default:
		return nil, p.errorf(peek.pos, "expected ticks expression after 'wait', got %s", peek.describe())
	}

	// Check for optional condition block
	peek2, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek2.kind != tokLBrace {
		// Simple wait: wait <ticks>
		p.unget(peek2)
		return &WaitStmt{Ticks: ticks, Comment: comment}, nil
	}

	// Block wait: wait <ticks> { body; cond }
	stmts, err := p.parseBhvStmtBlockInner(syms, true)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(peek2.pos, "empty wait block")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, p.errorf(peek2.pos, "last item in wait block must be a value-producing expression")
	}
	return &WaitStmt{
		Ticks:   ticks,
		Body:    stmts[:len(stmts)-1],
		Tail:    tail.Expr,
		Comment: comment,
	}, nil
}

// parseBhvForStmt parses a for i in <range> { ... } loop.
func (p *parser) parseBhvForStmt(syms *symbolTable, label ...string) (*ForStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}
	comment := p.docComment

	// Parse iteration variable
	iterTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if err := p.checkVarName(iterTok.val, syms, iterTok.pos); err != nil {
		return nil, err
	}

	// Expect 'in'
	inTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if inTok.val != "in" {
		return nil, p.errorf(inTok.pos, "expected 'in' after for variable, got %q", inTok.val)
	}

	// Parse range expression: Range constructor, variable, or $register
	rangeTok, err := p.next()
	if err != nil {
		return nil, err
	}
	var rangeExpr Expr
	if rangeTok.kind == tokIdent && rangeTok.val == "Range" {
		rangeExpr, err = p.parseBhvConstructorExpr(rangeTok, syms)
		if err != nil {
			return nil, err
		}
	} else if rangeTok.kind == tokIdent {
		resolved, err := p.resolveBhvOperand(rangeTok, syms)
		if err != nil {
			return nil, err
		}
		rangeExpr = resolved
	} else {
		return nil, p.errorf(rangeTok.pos, "expected Range constructor or variable after 'in', got %s", rangeTok.describe())
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	// Push scope for iter var + body
	saved := syms.pushScope()
	syms.declareVarWarn(iterTok.val, false, p, iterTok.pos)

	p.enterLoop(lbl)
	body, err := p.parseBhvStmtBlockInner(syms)
	p.exitLoop(lbl)

	syms.popScope(saved)

	if err != nil {
		return nil, err
	}

	return &ForStmt{Label: lbl, IterVar: iterTok.val, Range: rangeExpr, Body: body, Comment: comment}, nil
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
			Pos:     firstTok.pos,
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
			bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: false, Pos: nameTok.pos})
		case "var":
			activeModifier = 1
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, MultiBinding{Name: nameTok.val, Mutable: true, Pos: nameTok.pos})
		default:
			if activeModifier >= 0 {
				bindings = append(bindings, MultiBinding{
					Name:    tok.val,
					Mutable: activeModifier == 1,
					Pos:     tok.pos,
				})
			} else {
				bindings = append(bindings, MultiBinding{Name: tok.val, Pos: tok.pos})
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

	// Parse the RHS: expression list
	firstTok, err := p.next()
	if err != nil {
		return nil, err
	}

	// Validate new variable names
	for _, bind := range bindings {
		if bind.Discard {
			continue
		}
		if err := p.checkVarName(bind.Name, syms, firstTok.pos); err != nil {
			return nil, err
		}
	}

	// Instruction is only valid as the sole RHS item
	if firstTok.kind == tokIdent && firstTok.val == "instruction" {
		rawFrame, err := p.parseInstruction()
		if err != nil {
			return nil, err
		}
		if err := p.checkInstructionDirections(rawFrame, syms, firstTok.pos); err != nil {
			return nil, err
		}
		retCount := frameReturnCount(rawFrame)
		if retCount == 0 {
			return nil, p.errorf(firstTok.pos, "instruction has no return slots (@N); cannot assign its result")
		}
		if len(bindings) > retCount {
			return nil, p.errorf(firstTok.pos, "too many bindings (%d) for instruction which returns %d values", len(bindings), retCount)
		}

		// Register new variables
		for _, bind := range bindings {
			if !bind.Discard {
				syms.declareVarWarn(bind.Name, bind.Mutable, p, bind.Pos)
			}
		}

		return []Stmt{&MultiReturnStmt{
			Bindings: bindings,
			Value:    &InstructionExpr{Frame: rawFrame},
			Comment:  comment,
		}}, nil
	}

	// Parse expression list items
	p.unget(firstTok)
	var items []Expr
	bindingsConsumed := 0
	varsRegistered := false

	for bindingsConsumed < len(bindings) {
		if len(items) > 0 {
			comma, err := p.next()
			if err != nil {
				return nil, err
			}
			if comma.kind != tokComma {
				p.unget(comma)
				// If we have a single function call that doesn't fill all bindings,
				// give the specific "too many bindings" error.
				if len(items) == 1 {
					if ce, ok := items[0].(*CallExpr); ok {
						fn := p.fns[ce.Name]
						return nil, p.errorf(firstTok.pos, "too many bindings (%d) for function %q which returns %d values", len(bindings), ce.Name, fn.returnCount())
					}
				}
				return nil, p.errorf(comma.pos, "expected ',' between expression list items, got %s", comma.describe())
			}
		}

		tok, err := p.next()
		if err != nil {
			return nil, err
		}

		if tok.kind == tokIdent && (tok.val == "locked" || tok.val == "unlocked") {
			mbe, err := p.parseBhvModeBlockExpr(tok.val == "unlocked", syms, comment)
			if err != nil {
				return nil, err
			}
			items = append(items, mbe)
			bindingsConsumed += p.exprArity(mbe.Tail)
			continue
		}

		if tok.kind == tokIdent && tok.val == "if" {
			ifExpr, err := p.parseBhvIfExpr(syms, comment)
			if err != nil {
				return nil, err
			}
			items = append(items, ifExpr)
			bindingsConsumed += p.ifExprArity(ifExpr)
			continue
		}

		if tok.kind == tokIdent {
			name, fn, fnErr := p.resolveFnName(tok)
			if fnErr != nil {
				return nil, fnErr
			}
			if fn != nil {
				if !fn.hasReturn() {
					return nil, p.errorf(tok.pos, "function %q has no return value", name)
				}
				// Register variables before parsing args (they may be referenced)
				if !varsRegistered {
					for _, bind := range bindings {
						if !bind.Discard {
							syms.declareVarWarn(bind.Name, bind.Mutable, p, bind.Pos)
						}
					}
					varsRegistered = true
				}
				args, kwArgs, err := p.parseBhvCallArgs(fn, token{kind: tokIdent, val: name, pos: tok.pos}, syms)
				if err != nil {
					return nil, err
				}
				items = append(items, &CallExpr{Name: name, Args: args, KwArgs: kwArgs})
				bindingsConsumed += fn.returnCount()
				continue
			}
		}

		// Simple expression
		p.unget(tok)
		expr, err := p.parseBhvArgExpr(syms)
		if err != nil {
			return nil, err
		}
		items = append(items, expr)
		bindingsConsumed++
	}

	// Handle prefix matching on last item
	if bindingsConsumed > len(bindings) {
		if _, ok := items[len(items)-1].(*CallExpr); !ok {
			return nil, p.errorf(firstTok.pos, "too many values for %d bindings", len(bindings))
		}
		bindingsConsumed = len(bindings)
	}

	// Check no trailing expressions
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek.kind == tokComma {
		return nil, p.errorf(peek.pos, "too many expressions for %d bindings", len(bindings))
	}
	p.unget(peek)

	// Register variables (if not already done for function call args)
	if !varsRegistered {
		for _, bind := range bindings {
			if !bind.Discard {
				syms.declareVarWarn(bind.Name, bind.Mutable, p, bind.Pos)
			}
		}
	}

	// If single item, use existing representation directly
	if len(items) == 1 {
		return []Stmt{&MultiReturnStmt{
			Bindings: bindings,
			Value:    items[0],
			Comment:  comment,
		}}, nil
	}

	// Multiple items: wrap in ExprListExpr
	return []Stmt{&MultiReturnStmt{
		Bindings: bindings,
		Value:    &ExprListExpr{Exprs: items},
		Comment:  comment,
	}}, nil
}

// exprArity returns the arity (number of values produced) of an expression.
func (p *parser) exprArity(expr Expr) int {
	switch e := expr.(type) {
	case *CallExpr:
		if fn := p.fns[e.Name]; fn != nil {
			return fn.returnCount()
		}
	case *IfExpr:
		return p.ifExprArity(e)
	case *ModeBlockExpr:
		return p.exprArity(e.Tail)
	}
	return 1
}

// ifExprArity returns the maximum arity across all branches of an if-expression.
func (p *parser) ifExprArity(e *IfExpr) int {
	max := p.exprArity(e.Tail)
	for _, elif := range e.ElseIfs {
		if a := p.exprArity(elif.Tail); a > max {
			max = a
		}
	}
	if e.ElsTail != nil {
		if a := p.exprArity(e.ElsTail); a > max {
			max = a
		}
	}
	return max
}

// parseBhvModeBlockExpr parses a locked/unlocked block used as an expression.
// The keyword has been consumed. Expects '{', body statements, a tail
// expression, and '}'. Returns a ModeBlockExpr.
func (p *parser) parseBhvModeBlockExpr(unlock bool, syms *symbolTable, comment string) (*ModeBlockExpr, error) {
	lbrace, err := p.expect(tokLBrace)
	if err != nil {
		return nil, err
	}
	stmts, err := p.parseBhvStmtBlockInner(syms, true)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(lbrace.pos, "empty mode block expression")
	}
	// The last statement must be an exprTailStmt
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, p.errorf(lbrace.pos, "last item in mode block expression must be a value-producing expression")
	}
	return &ModeBlockExpr{
		Unlock:  unlock,
		Body:    stmts[:len(stmts)-1],
		Tail:    tail.Expr,
		Comment: comment,
	}, nil
}

// parseBhvIfExprBranch parses a brace-delimited expression block (statements +
// tail expression) for an if-expression branch.
func (p *parser) parseBhvIfExprBranch(syms *symbolTable) ([]Stmt, Expr, error) {
	lbrace, err := p.expect(tokLBrace)
	if err != nil {
		return nil, nil, err
	}
	stmts, err := p.parseBhvStmtBlockInner(syms, true)
	if err != nil {
		return nil, nil, err
	}
	if len(stmts) == 0 {
		return nil, nil, p.errorf(lbrace.pos, "empty if-expression branch")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, nil, p.errorf(lbrace.pos, "last item in if-expression branch must be a value-producing expression")
	}
	return stmts[:len(stmts)-1], tail.Expr, nil
}

// parseBhvIfExpr parses an if-expression after the 'if' keyword has been
// consumed. Uses the full boolean expression parser for conditions.
func (p *parser) parseBhvIfExpr(syms *symbolTable, comment string) (*IfExpr, error) {
	resolve := p.bhvResolver(syms)

	// Parse condition
	cond, err := p.parseBoolPrimary(resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, resolve)
	if err != nil {
		return nil, err
	}

	// Parse if body
	body, tail, err := p.parseBhvIfExprBranch(syms)
	if err != nil {
		return nil, err
	}

	expr := &IfExpr{
		Cond:    cond,
		Body:    body,
		Tail:    tail,
		Comment: comment,
	}

	// Parse else-if / else chain (else is optional)
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind != tokIdent || tok.val != "else" {
			// No else clause — uncovered branches produce null
			p.unget(tok)
			return expr, nil
		}
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent && peek.val == "if" {
			// else if
			eiCond, err := p.parseBoolPrimary(resolve)
			if err != nil {
				return nil, err
			}
			eiCond, err = p.parseBoolChain(eiCond, resolve)
			if err != nil {
				return nil, err
			}
			eiBody, eiTail, err := p.parseBhvIfExprBranch(syms)
			if err != nil {
				return nil, err
			}
			expr.ElseIfs = append(expr.ElseIfs, ElseIfExprClause{
				Cond: eiCond,
				Body: eiBody,
				Tail: eiTail,
			})
		} else {
			// plain else
			p.unget(peek)
			elsBody, elsTail, err := p.parseBhvIfExprBranch(syms)
			if err != nil {
				return nil, err
			}
			expr.ElsBody = elsBody
			expr.ElsTail = elsTail
			return expr, nil
		}
	}
}

// parseBhvStmtBlock parses a brace-delimited block of statements.
// The opening '{' has been consumed. Reads until '}'.
func (p *parser) parseBhvStmtBlock(syms *symbolTable) ([]Stmt, error) {
	return p.parseBhvStmtBlockInner(syms, false)
}

// parseBhvStmtBlockInner parses a brace-delimited block of statements.
// 'break' is allowed when p.loopDepth > 0. If exprTail is true, the last
// item may be a bare expression (wrapped in exprTailStmt).
func (p *parser) parseBhvStmtBlockInner(syms *symbolTable, exprTail ...bool) ([]Stmt, error) {
	allowExprTail := len(exprTail) > 0 && exprTail[0]
	saved := syms.pushScope()
	defer syms.popScope(saved)
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
			if allowExprTail && tok.kind == tokNumber {
				// Number as expression tail in a mode block expression
				resolve := p.bhvResolver(syms)
				num, _ := strconv.Atoi(tok.val)
				numExpr := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
				result, err := p.parseArithExprFromFull(numExpr, resolve)
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(tokRBrace); err != nil {
					return nil, err
				}
				stmts = append(stmts, &exprTailStmt{Expr: result})
				return stmts, nil
			}
			if allowExprTail && tok.kind == tokLParen {
				// Parenthesized expression tail
				resolve := p.bhvResolver(syms)
				p.unget(tok)
				expr, err := p.parseBoolExpr(resolve)
				if err != nil {
					return nil, err
				}
				if truthy, ok := expr.(*TruthyExpr); ok {
					expr = truthy.Value
				}
				if _, err := p.expect(tokRBrace); err != nil {
					return nil, err
				}
				stmts = append(stmts, &exprTailStmt{Expr: expr})
				return stmts, nil
			}
			return nil, p.errorf(tok.pos, "expected statement, got %s", tok.describe())
		}
		comment := p.docComment

		switch tok.val {
		case "locked":
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			body, err := p.parseBhvStmtBlockInner(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, &ModeBlockStmt{Unlock: false, Body: body, Comment: comment})
		case "unlocked":
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			body, err := p.parseBhvStmtBlockInner(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, &ModeBlockStmt{Unlock: true, Body: body, Comment: comment})
		case "instruction":
			rawFrame, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			if err := p.checkInstructionDirections(rawFrame, syms, tok.pos); err != nil {
				return nil, err
			}
			stmts = append(stmts, &InstructionStmt{Frame: rawFrame, Comment: comment})
		case "var":
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			if nameTok.val == "_" {
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind != tokComma {
					return nil, p.errorf(nameTok.pos, "'_' cannot be used as a variable name")
				}
				parsed, err := p.parseBhvMultiReturn(nameTok, true, true, syms)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, parsed...)
			} else {
				if err := p.checkVarName(nameTok.val, syms, nameTok.pos); err != nil {
					return nil, err
				}
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind == tokComma {
					parsed, err := p.parseBhvMultiReturn(nameTok, true, false, syms)
					if err != nil {
						return nil, err
					}
					stmts = append(stmts, parsed...)
				} else if sep.kind == tokEquals {
					parsed, err := p.parseBhvVarInit(nameTok, true, syms)
					if err != nil {
						return nil, err
					}
					stmts = append(stmts, parsed...)
				} else {
					return nil, p.errorf(sep.pos, "expected ',' or '=' after var identifier, got %s", sep.describe())
				}
			}
		case "let":
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			if nameTok.val == "_" {
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind != tokComma {
					return nil, p.errorf(nameTok.pos, "'_' cannot be used as a variable name")
				}
				parsed, err := p.parseBhvMultiReturn(nameTok, false, true, syms)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, parsed...)
			} else {
				if err := p.checkVarName(nameTok.val, syms, nameTok.pos); err != nil {
					return nil, err
				}
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind == tokComma {
					parsed, err := p.parseBhvMultiReturn(nameTok, false, false, syms)
					if err != nil {
						return nil, err
					}
					stmts = append(stmts, parsed...)
				} else if sep.kind == tokEquals {
					parsed, err := p.parseBhvVarInit(nameTok, false, syms)
					if err != nil {
						return nil, err
					}
					stmts = append(stmts, parsed...)
				} else {
					return nil, p.errorf(sep.pos, "expected ',' or '=' after let identifier, got %s", sep.describe())
				}
			}
		case "_":
			sep, err := p.next()
			if err != nil {
				return nil, err
			}
			if sep.kind == tokComma {
				parsed, err := p.parseBhvMultiReturn(tok, false, true, syms)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, parsed...)
			} else if sep.kind == tokEquals {
				calleeTok, err := p.expect(tokIdent)
				if err != nil {
					return nil, err
				}
				name, fn, fnErr := p.resolveFnName(calleeTok)
				if fnErr != nil {
					return nil, fnErr
				}
				if fn == nil {
					return nil, p.errorf(calleeTok.pos, "unknown function %q", calleeTok.val)
				}
				args, kwArgs, err := p.parseBhvCallArgs(fn, token{kind: tokIdent, val: name, pos: calleeTok.pos}, syms)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, &CallStmt{
					Name:    name,
					Args:    args,
					KwArgs:  kwArgs,
					Comment: comment,
				})
			} else {
				return nil, p.errorf(sep.pos, "expected ',' or '=' after '_', got %s", sep.describe())
			}
		case "if":
			if allowExprTail {
				// Try as if-expression tail
				ifExpr, err := p.parseBhvIfExpr(syms, comment)
				if err != nil {
					return nil, err
				}
				// Check if this was the last item in the block
				peek, err := p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokRBrace {
					stmts = append(stmts, &exprTailStmt{Expr: ifExpr})
					return stmts, nil
				}
				// Not the tail — cannot use if-expression as a statement
				return nil, p.errorf(peek.pos, "if-expression can only appear as the last item in an expression block")
			}
			ifStmt, err := p.parseBhvIfStmt(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, ifStmt)
		case "while":
			whileStmt, err := p.parseBhvWhileStmt(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, whileStmt)
		case "loop":
			loopStmt, err := p.parseBhvLoopStmt(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, loopStmt)
		case "for":
			forStmt, err := p.parseBhvForStmt(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, forStmt)
		case "wait":
			waitStmt, err := p.parseBhvWaitStmt(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, waitStmt)
		case "break":
			if p.loopDepth == 0 {
				return nil, p.errorf(tok.pos, "'break' outside of loop")
			}
			label := ""
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokIdent {
				if !p.loopLabels[peek.val] {
					return nil, p.errorf(peek.pos, "unknown loop label %q", peek.val)
				}
				label = peek.val
			} else {
				p.unget(peek)
			}
			stmts = append(stmts, &BreakStmt{Label: label, Comment: comment})
		case "return":
			return nil, p.errorf(tok.pos, "'return' can only be used inside function bodies")
		case "fn", "private":
			return nil, p.errorf(tok.pos, "function definitions cannot be nested inside behavior bodies")
		case "behavior":
			return nil, p.errorf(tok.pos, "behavior definitions cannot be nested")
		case "else":
			return nil, p.errorf(tok.pos, "'else' without matching 'if'")
		case "continue":
			return nil, p.errorf(tok.pos, "'continue' is not supported; use labeled 'break' to exit a specific loop")
		default:
			// Check for labeled loop/while/for: `ident: loop { ... }` or `ident: while ...` or `ident: for ...`
			// Save doc comment before label lookahead — p.next() resets it.
			savedComment := p.docComment
			if !isConstructor(tok.val) && tok.val != "null" && tok.val != "true" && tok.val != "false" {
				peek, err := p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokColon {
					peek2, err := p.next()
					if err != nil {
						return nil, err
					}
					if peek2.kind == tokIdent && (peek2.val == "loop" || peek2.val == "while" || peek2.val == "for") {
						label := tok.val
						if p.loopLabels[label] {
							return nil, p.errorf(tok.pos, "duplicate loop label %q", label)
						}
						switch peek2.val {
						case "loop":
							loopStmt, err := p.parseBhvLoopStmt(syms, label)
							if err != nil {
								return nil, err
							}
							stmts = append(stmts, loopStmt)
						case "while":
							whileStmt, err := p.parseBhvWhileStmt(syms, label)
							if err != nil {
								return nil, err
							}
							stmts = append(stmts, whileStmt)
						case "for":
							forStmt, err := p.parseBhvForStmt(syms, label)
							if err != nil {
								return nil, err
							}
							stmts = append(stmts, forStmt)
						}
						continue
					}
					p.unget(peek2)
				}
				p.unget(peek)
			}
			p.docComment = savedComment

			if allowExprTail {
				name, fn, fnErr := p.resolveFnName(tok)
				if fnErr != nil {
					return nil, fnErr
				}
				peek, err := p.next()
				if err != nil {
					return nil, err
				}
				p.unget(peek)

				// Constructor as tail expression
				if isConstructor(tok.val) {
					ctor, err := p.parseBhvConstructorExpr(tok, syms)
					if err != nil {
						return nil, err
					}
					// Range constructor doesn't handle & internally; check and error
					if ctorExpr, ok := ctor.(*ConstructorExpr); ok && ctorExpr.TypeName == "Range" {
						peek, err := p.next()
						if err != nil {
							return nil, err
						}
						if peek.kind == tokAmpersand {
							return nil, p.errorf(peek.pos, "'&' cannot be used with Range (it would overwrite the step field)")
						}
						p.unget(peek)
					}
					if _, err := p.expect(tokRBrace); err != nil {
						return nil, err
					}
					stmts = append(stmts, &exprTailStmt{Expr: ctor})
					return stmts, nil
				}

				// null/true/false as tail expression
				if tok.val == "null" || tok.val == "false" {
					if _, err := p.expect(tokRBrace); err != nil {
						return nil, err
					}
					stmts = append(stmts, &exprTailStmt{Expr: &LiteralExpr{Value: false}})
					return stmts, nil
				}
				if tok.val == "true" {
					if _, err := p.expect(tokRBrace); err != nil {
						return nil, err
					}
					stmts = append(stmts, &exprTailStmt{Expr: &LiteralExpr{Value: map[string]any{"num": 1}}})
					return stmts, nil
				}

				isExprTail := false
				if fn != nil && fn.hasReturn() && peek.kind != tokEquals && !isCompoundAssignOp(peek.kind) && peek.kind != tokPlusPlus && peek.kind != tokMinusMinus {
					isExprTail = true
				} else if fn == nil && !isConstructor(tok.val) && peek.kind != tokEquals && !isCompoundAssignOp(peek.kind) && peek.kind != tokPlusPlus && peek.kind != tokMinusMinus {
					isExprTail = true
				}

				if isExprTail {
					resolve := p.bhvResolver(syms)
					var result Expr
					if fn != nil && fn.hasReturn() {
						// Function call as initial expression
						args, kwArgs, err := p.parseBhvCallArgs(fn, token{kind: tokIdent, val: name, pos: tok.pos}, syms)
						if err != nil {
							return nil, err
						}
						result = &CallExpr{Name: name, Args: args, KwArgs: kwArgs}
					} else {
						// Variable or value as initial expression
						resolved, err := resolve(tok)
						if err != nil {
							return nil, err
						}
						result = resolved
					}
					result, err = p.parseArithExprFromFull(result, resolve)
					if err != nil {
						return nil, err
					}
					final, handled, err := p.maybeBhvExprContinuation(result, syms)
					if err != nil {
						return nil, err
					}
					if handled {
						result = final
					}
					if _, err := p.expect(tokRBrace); err != nil {
						return nil, err
					}
					stmts = append(stmts, &exprTailStmt{Expr: result})
					return stmts, nil
				}
			}

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
	case *ModeBlockExpr:
		tmp := allocUniqueVar("@mode", syms.usedVars)
		if err := p.emitBhvModeBlockExpr(e, tmp, syms, b, comment); err != nil {
			return nil, err
		}
		return tmp, nil
	case *IfExpr:
		tmp := allocUniqueVar("@if", syms.usedVars)
		if err := p.emitBhvIfExpr(e, tmp, syms, b, comment); err != nil {
			return nil, err
		}
		return tmp, nil
	case *CompareExpr, *TypeCheckExpr, *TruthyExpr, *BoolChainExpr, *NotExpr:
		tmp := allocUniqueVar("@bool", syms.usedVars)
		if err := p.emitBhvBoolExprTo(expr, tmp, syms, b, comment); err != nil {
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
	case *NotExpr:
		return p.emitBhvBoolExprTo(expr, target, syms, b, comment)
	case *ModeBlockExpr:
		return p.emitBhvModeBlockExpr(e, target, syms, b, comment)
	case *IfExpr:
		return p.emitBhvIfExpr(e, target, syms, b, comment)
	}
	return fmt.Errorf("unsupported expression type %T in emitBhvExprTo", expr)
}

// emitBhvArithTo emits an arithmetic expression chain writing to target.
func (p *parser) emitBhvArithTo(expr *ArithExpr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	ac := &arithCounter{}
	_, err := p.emitArithNode(expr, target, b, syms.usedVars, comment, ac, func(e Expr) (any, error) {
		return p.emitBhvExprGetValue(e, syms, b, "")
	})
	return err
}

// emitBhvConstructorTo emits a constructor expression to target.
func (p *parser) emitBhvConstructorTo(ctor *ConstructorExpr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	if val, ok := tryResolveConstructorLiteral(ctor); ok {
		f := map[string]any{"op": "set_reg", "1": val, "2": target}
		setComment(f, comment)
		b.emit(f)
		return nil
	}
	// Runtime: Coordinate and Range can be runtime
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
	if ctor.TypeName == "Range" {
		// combine_register step, false, x: start, y: stop
		stepVal, err := p.emitBhvExprGetValue(ctor.Args[2], syms, b, "")
		if err != nil {
			return err
		}
		startVal, err := p.emitBhvExprGetValue(ctor.Args[0], syms, b, "")
		if err != nil {
			return err
		}
		stopVal, err := p.emitBhvExprGetValue(ctor.Args[1], syms, b, "")
		if err != nil {
			return err
		}
		kwArgs := map[string]any{"x": startVal, "y": stopVal}
		return p.expandCall("combine_register", []any{stepVal, false}, kwArgs, []any{target}, b, 0, comment, syms.usedVars)
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
	resolved, err := p.resolveBhvBoolTree(expr, syms, b)
	if err != nil {
		return err
	}
	p.emitResolvedBoolExprTo(resolved, target, b, comment)
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

// resolveBhvBoolTree delegates to the unified resolveBoolTree with
// behavior-level operand resolution.
func (p *parser) resolveBhvBoolTree(expr Expr, syms *symbolTable, b *frameBuilder) (*resolvedBoolExpr, error) {
	return p.resolveBoolTree(expr, func(e Expr) (any, error) {
		return p.emitBhvExprGetValue(e, syms, b, "")
	})
}

// negateResolved pushes negation down to leaves of a resolved boolean tree.
// Leaf: toggle negated. Group: De Morgan's law — swap chainOp, recurse.
func negateResolved(expr *resolvedBoolExpr) {
	if expr.isLeaf() {
		expr.term.negated = !expr.term.negated
		return
	}
	// De Morgan's: !(a && b) → !a || !b, !(a || b) → !a && !b
	if expr.chainOp == tokDoubleAmpersand {
		expr.chainOp = tokDoublePipe
	} else {
		expr.chainOp = tokDoubleAmpersand
	}
	for _, child := range expr.children {
		negateResolved(child)
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

// stripFallThrough removes frameRef branch slots from check frames at
// positions [start, start+count) that point to the natural fall-through
// (the immediately following frame). This produces minimal check frame
// output matching the VM's default fall-through behavior.
func stripFallThrough(b *frameBuilder, start, count int) {
	for i := start; i < start+count; i++ {
		f := b.get(i)
		nextPos := frameRef(i + 1)
		for k, v := range f {
			if ref, ok := v.(frameRef); ok && ref == nextPos {
				delete(f, k)
			}
		}
	}
}

// emitBehaviorStmts walks a []Stmt list and emits frames. Mode
// transitions are emitted on-the-fly via frameBuilder.mode tracking —
// ModeBlockStmt blocks emit transitions only when needed and restore mode
// on exit. Returns the total frame count.
func (p *parser) emitBehaviorStmts(stmts []Stmt, b *frameBuilder, syms *symbolTable) (int, error) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ModeBlockStmt:
			if err := p.emitBhvModeBlock(s, b, syms); err != nil {
				return 0, err
			}

		case *IfStmt:
			// Check for if/break pattern inside a loop body
			if p.isIfBreak(s) {
				if err := p.emitBhvIfBreak(s, b, syms); err != nil {
					return 0, err
				}
			} else {
				if err := p.emitBhvIfStmt(s, b, syms); err != nil {
					return 0, err
				}
			}

		case *WhileStmt:
			if err := p.emitBhvWhileStmt(s, b, syms); err != nil {
				return 0, err
			}

		case *LoopStmt:
			if err := p.emitBhvLoopStmt(s, b, syms); err != nil {
				return 0, err
			}

		case *ForStmt:
			if err := p.emitBhvForStmt(s, b, syms); err != nil {
				return 0, err
			}

		case *WaitStmt:
			if err := p.emitBhvWaitStmt(s, b, syms); err != nil {
				return 0, err
			}

		case *BreakStmt:
			f := map[string]any{"op": "@break"}
			if s.Label != "" {
				f["label"] = s.Label
			}
			b.emit(f)

		default:
			if err := p.emitBhvStmtSimple(stmt, b, syms); err != nil {
				return 0, err
			}
		}
	}

	return b.pos(), nil
}

// isIfBreak detects the if/break pattern: IfStmt with a single BreakStmt
// body and no else/else-if.
func (p *parser) isIfBreak(s *IfStmt) bool {
	if len(s.ElseIfs) > 0 || s.Else != nil {
		return false
	}
	if len(s.Body) != 1 {
		return false
	}
	_, isBreak := s.Body[0].(*BreakStmt)
	return isBreak
}

// emitBhvIfBreak emits check frames + @break placeholder for the
// if/break pattern inside a loop. Uses resolveBhvBoolTree for full
// boolean expression support.
func (p *parser) emitBhvIfBreak(s *IfStmt, b *frameBuilder, syms *symbolTable) error {
	resolved, err := p.resolveBhvBoolTree(s.Cond, syms, b)
	if err != nil {
		return err
	}

	checkStart := b.pos()
	checkCount := resolved.frameCount()

	// true → @break placeholder (right after check frames)
	trueBranch := frameRef(checkStart + checkCount)
	// false → continuation (after @break placeholder)
	falsePlaceholder := frameRef(checkStart + checkCount + 1)

	if resolved.isLeaf() {
		p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, s.Comment)
	} else {
		p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, s.Comment)
	}
	stripFallThrough(b, checkStart, checkCount)

	// Emit @break placeholder
	breakFrame := map[string]any{"op": "@break"}
	breakLabel := s.Body[0].(*BreakStmt).Label
	if breakLabel != "" {
		breakFrame["label"] = breakLabel
	}
	b.emit(breakFrame)

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
		syms.declareVar(s.Name, s.Mutable)
		return p.emitBhvExprTo(s.Value, s.Name, syms, b, s.Comment)

	case *AssignStmt:
		var target any
		if s.Internal {
			// Compiler-generated assign (e.g., continuation after fn call);
			// skip mutability check — target was just declared.
			target = s.Target
		} else {
			var err error
			target, err = p.resolveAssignTarget(s.Target, syms, s.Pos, false)
			if err != nil {
				return err
			}
		}
		return p.emitBhvExprTo(s.Value, target, syms, b, s.Comment)

	case *CompoundAssignStmt:
		target, err := p.resolveAssignTarget(s.Target, syms, s.Pos, true)
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
		target, err := p.resolveAssignTarget(s.Target, syms, s.Pos, true)
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
				syms.declareVar(bind.Name, bind.Mutable)
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
		case *ModeBlockExpr:
			return p.emitBhvModeBlockExprMulti(v, retVals, syms, b, s.Comment)
		case *IfExpr:
			return p.emitBhvIfExprMulti(v, retVals, syms, b, s.Comment)
		case *InstructionExpr:
			resolved := resolveInstructionFrame(v.Frame, retVals, nil, nil, s.Comment)
			b.emit(resolved)
			return nil
		case *ExprListExpr:
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
					resolvedArgs, resolvedKwArgs, err := p.emitBhvCallExprArgs(e.Args, e.KwArgs, syms, b)
					if err != nil {
						return err
					}
					if err := p.checkCallDirections(fn, e.Name, resolvedArgs, resolvedKwArgs, syms, 0); err != nil {
						return err
					}
					if err := p.expandCall(e.Name, resolvedArgs, resolvedKwArgs, callRetVals, b, 0, s.Comment, syms.usedVars); err != nil {
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
							if err := p.emitBhvModeBlockExpr(e, retVals[bindIdx], syms, b, s.Comment); err != nil {
								return err
							}
						}
					} else {
						mbeRetVals := retVals[bindIdx : bindIdx+mbeArity]
						if err := p.emitBhvModeBlockExprMulti(e, mbeRetVals, syms, b, s.Comment); err != nil {
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
							if err := p.emitBhvIfExpr(e, retVals[bindIdx], syms, b, s.Comment); err != nil {
								return err
							}
						}
					} else {
						ifRetVals := retVals[bindIdx : bindIdx+ifArity]
						if err := p.emitBhvIfExprMulti(e, ifRetVals, syms, b, s.Comment); err != nil {
							return err
						}
					}
					bindIdx += ifArity
				default:
					if !s.Bindings[bindIdx].Discard {
						if err := p.emitBhvExprTo(expr, retVals[bindIdx], syms, b, s.Comment); err != nil {
							return err
						}
					}
					bindIdx++
				}
			}
			return nil
		}
		return fmt.Errorf("unsupported multi-return value type %T", s.Value)
	}

	return fmt.Errorf("unsupported statement type %T", stmt)
}

// emitBhvModeBlock emits a locked { ... } or unlocked { ... } block.
// It emits a mode transition frame on entry (if needed), recurses into the
// body, then restores the mode on exit (if needed).
func (p *parser) emitBhvModeBlock(s *ModeBlockStmt, b *frameBuilder, syms *symbolTable) error {
	savedMode := emitModeEntry(b, s.Unlock, s.Comment)
	savedScope := syms.pushScope()
	if _, err := p.emitBehaviorStmts(s.Body, b, syms); err != nil {
		return err
	}
	syms.popScope(savedScope)
	emitModeExit(b, savedMode)
	return nil
}

// emitBhvModeBlockExpr emits a locked/unlocked block expression, writing
// the tail expression's result to target. Handles multi-return tails via
// retVals slice when target is a slice.
func (p *parser) emitBhvModeBlockExpr(e *ModeBlockExpr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	mbeComment := e.Comment
	if mbeComment == "" {
		mbeComment = comment
	}
	savedMode := emitModeEntry(b, e.Unlock, mbeComment)
	savedScope := syms.pushScope()
	if _, err := p.emitBehaviorStmts(e.Body, b, syms); err != nil {
		return err
	}
	if err := p.emitBhvExprTo(e.Tail, target, syms, b, mbeComment); err != nil {
		return err
	}
	syms.popScope(savedScope)
	emitModeExit(b, savedMode)
	return nil
}

// emitBhvModeBlockExprMulti emits a mode block expression with multi-return
// tail, directing return values to the given retVals slice.
func (p *parser) emitBhvModeBlockExprMulti(e *ModeBlockExpr, retVals []any, syms *symbolTable, b *frameBuilder, comment string) error {
	mbeComment := e.Comment
	if mbeComment == "" {
		mbeComment = comment
	}
	savedMode := emitModeEntry(b, e.Unlock, mbeComment)
	savedScope := syms.pushScope()
	if _, err := p.emitBehaviorStmts(e.Body, b, syms); err != nil {
		return err
	}
	// Tail must be a CallExpr for multi-return
	ce, ok := e.Tail.(*CallExpr)
	if !ok {
		return fmt.Errorf("multi-return mode block expression tail must be a call, got %T", e.Tail)
	}
	resolvedArgs, resolvedKwArgs, err := p.emitBhvCallExprArgs(ce.Args, ce.KwArgs, syms, b)
	if err != nil {
		return err
	}
	fn := p.fns[ce.Name]
	if err := p.checkCallDirections(fn, ce.Name, resolvedArgs, resolvedKwArgs, syms, 0); err != nil {
		return err
	}
	if err := p.expandCall(ce.Name, resolvedArgs, resolvedKwArgs, retVals, b, 0, mbeComment, syms.usedVars); err != nil {
		return err
	}
	syms.popScope(savedScope)
	emitModeExit(b, savedMode)
	return nil
}

// emitBhvIfExpr emits an if-expression writing each branch's tail to target.
// Uses forward-jump patching (same pattern as emitFnIfStmt).
func (p *parser) emitBhvIfExpr(e *IfExpr, target any, syms *symbolTable, b *frameBuilder, comment string) error {
	// Collect all conditional branches
	type branch struct {
		cond Expr
		body []Stmt
		tail Expr
	}
	branches := []branch{{cond: e.Cond, body: e.Body, tail: e.Tail}}
	for _, elif := range e.ElseIfs {
		branches = append(branches, branch{cond: elif.Cond, body: elif.Body, tail: elif.Tail})
	}

	var jumpsToPatch []int

	for i, br := range branches {
		brComment := ""
		if i == 0 {
			brComment = comment
		}

		// Resolve condition and emit check frames with placeholder false branch
		resolved, err := p.resolveBhvBoolTree(br.cond, syms, b)
		if err != nil {
			return err
		}

		checkStart := b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount)
		falsePlaceholder := frameRef(0)

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, brComment)
		}

		// Emit body
		savedScope := syms.pushScope()
		if _, err := p.emitBehaviorStmts(br.body, b, syms); err != nil {
			return err
		}

		// Emit tail expression to target
		if err := p.emitBhvExprTo(br.tail, target, syms, b, ""); err != nil {
			return err
		}
		syms.popScope(savedScope)

		// Emit jump-to-continuation
		jumpIdx := b.pos()
		b.emit(map[string]any{
			"op":   "set_reg",
			"1":    false,
			"2":    false,
			"next": frameRef(0), // patched later
		})
		jumpsToPatch = append(jumpsToPatch, jumpIdx)

		patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, frameRef(b.pos()))
	}

	// Emit else body + tail (or null for missing else)
	if e.ElsTail != nil {
		savedScope := syms.pushScope()
		if _, err := p.emitBehaviorStmts(e.ElsBody, b, syms); err != nil {
			return err
		}

		if err := p.emitBhvExprTo(e.ElsTail, target, syms, b, ""); err != nil {
			return err
		}
		syms.popScope(savedScope)
	} else {
		// No else clause — assign null to target
		b.emit(map[string]any{
			"op": "set_reg",
			"1":  false,
			"2":  target,
		})
	}

	// Patch all jumps-to-continuation
	afterAll := frameRef(b.pos())
	for _, idx := range jumpsToPatch {
		b.get(idx)["next"] = afterAll
	}

	return nil
}

// emitBhvIfExprMulti emits an if-expression with multi-return tails,
// directing return values to the given retVals slice.
func (p *parser) emitBhvIfExprMulti(e *IfExpr, retVals []any, syms *symbolTable, b *frameBuilder, comment string) error {
	type branch struct {
		cond Expr
		body []Stmt
		tail Expr
	}
	branches := []branch{{cond: e.Cond, body: e.Body, tail: e.Tail}}
	for _, elif := range e.ElseIfs {
		branches = append(branches, branch{cond: elif.Cond, body: elif.Body, tail: elif.Tail})
	}

	var jumpsToPatch []int

	for i, br := range branches {
		brComment := ""
		if i == 0 {
			brComment = comment
		}

		resolved, err := p.resolveBhvBoolTree(br.cond, syms, b)
		if err != nil {
			return err
		}

		checkStart := b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount)
		falsePlaceholder := frameRef(0)

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, brComment)
		}

		savedScope := syms.pushScope()
		if _, err := p.emitBehaviorStmts(br.body, b, syms); err != nil {
			return err
		}

		// Emit tail to retVals
		if err := p.emitBhvIfExprTailMulti(br.tail, retVals, syms, b); err != nil {
			return err
		}
		syms.popScope(savedScope)

		jumpIdx := b.pos()
		b.emit(map[string]any{
			"op":   "set_reg",
			"1":    false,
			"2":    false,
			"next": frameRef(0),
		})
		jumpsToPatch = append(jumpsToPatch, jumpIdx)

		patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, frameRef(b.pos()))
	}

	// Else body + tail (or null for missing else)
	if e.ElsTail != nil {
		savedScope := syms.pushScope()
		if _, err := p.emitBehaviorStmts(e.ElsBody, b, syms); err != nil {
			return err
		}

		if err := p.emitBhvIfExprTailMulti(e.ElsTail, retVals, syms, b); err != nil {
			return err
		}
		syms.popScope(savedScope)
	} else {
		// No else clause — zero all retVal slots
		for _, rv := range retVals {
			b.emit(map[string]any{
				"op": "set_reg",
				"1":  false,
				"2":  rv,
			})
		}
	}

	afterAll := frameRef(b.pos())
	for _, idx := range jumpsToPatch {
		b.get(idx)["next"] = afterAll
	}

	return nil
}

// emitBhvIfExprTailMulti emits a tail expression directing values to retVals.
// If the tail is a CallExpr, uses expandCall. Otherwise, emits to retVals[0]
// and zeros remaining slots.
func (p *parser) emitBhvIfExprTailMulti(tail Expr, retVals []any, syms *symbolTable, b *frameBuilder) error {
	if ce, ok := tail.(*CallExpr); ok {
		resolvedArgs, resolvedKwArgs, err := p.emitBhvCallExprArgs(ce.Args, ce.KwArgs, syms, b)
		if err != nil {
			return err
		}
		fn := p.fns[ce.Name]
		if err := p.checkCallDirections(fn, ce.Name, resolvedArgs, resolvedKwArgs, syms, 0); err != nil {
			return err
		}
		return p.expandCall(ce.Name, resolvedArgs, resolvedKwArgs, retVals, b, 0, "", syms.usedVars)
	}
	// Single-return tail: emit to first retVal, zero rest
	if err := p.emitBhvExprTo(tail, retVals[0], syms, b, ""); err != nil {
		return err
	}
	for i := 1; i < len(retVals); i++ {
		b.emit(map[string]any{"op": "set_reg", "1": false, "2": retVals[i]})
	}
	return nil
}

// emitBhvIfStmt emits an if/else-if/else statement using forward-jump
// patching (same pattern as emitBhvIfExpr and emitFnIfStmt).
func (p *parser) emitBhvIfStmt(s *IfStmt, b *frameBuilder, syms *symbolTable) error {
	// Collect all conditional branches
	type branch struct {
		cond Expr
		body []Stmt
	}
	branches := []branch{{cond: s.Cond, body: s.Body}}
	for _, elif := range s.ElseIfs {
		branches = append(branches, branch{cond: elif.Cond, body: elif.Body})
	}

	var jumpsToPatch []int

	for i, br := range branches {
		brComment := ""
		if i == 0 {
			brComment = s.Comment
		}

		// Resolve condition and emit check frames with placeholder false branch
		resolved, err := p.resolveBhvBoolTree(br.cond, syms, b)
		if err != nil {
			return err
		}

		checkStart := b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount)
		falsePlaceholder := frameRef(0)

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, brComment)
		}
		stripFallThrough(b, checkStart, checkCount)

		// Emit body directly into b
		savedScope := syms.pushScope()
		if _, err := p.emitBehaviorStmts(br.body, b, syms); err != nil {
			return err
		}
		syms.popScope(savedScope)

		// If there's more branches or an else, emit jump-to-continuation
		hasMore := i < len(branches)-1 || len(s.Else) > 0
		if hasMore {
			jumpIdx := b.pos()
			b.emit(map[string]any{
				"op":   "set_reg",
				"1":    false,
				"2":    false,
				"next": frameRef(0), // patched to after all branches
			})
			jumpsToPatch = append(jumpsToPatch, jumpIdx)
		}

		patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, frameRef(b.pos()))
	}

	// Emit else body if present
	if len(s.Else) > 0 {
		savedScope := syms.pushScope()
		if _, err := p.emitBehaviorStmts(s.Else, b, syms); err != nil {
			return err
		}
		syms.popScope(savedScope)
	}

	// Patch all jumps-to-continuation to point to after everything
	afterAll := frameRef(b.pos())
	for _, idx := range jumpsToPatch {
		b.get(idx)["next"] = afterAll
	}

	return nil
}

// emitBhvWaitStmt emits a wait statement.
// Simple: emit wait frame. Block: wait → body → condition check → back-edge.
func (p *parser) emitBhvWaitStmt(s *WaitStmt, b *frameBuilder, syms *symbolTable) error {
	// Resolve ticks expression
	ticksVal, err := p.emitBhvExprGetValue(s.Ticks, syms, b, "")
	if err != nil {
		return err
	}

	if s.Tail == nil {
		// Simple wait: just emit the wait frame
		f := map[string]any{"op": "wait", "1": ticksVal}
		setComment(f, s.Comment)
		b.emit(f)
		return nil
	}

	// Block wait: snapshot ticks if needed, then wait → body → cond → loop back

	// Snapshot: copy ticks to temp var so they're only evaluated once.
	// Skip for pure number literals (they can't change between iterations).
	ticksVar := ticksVal
	needsSnapshot := true
	if lit, ok := s.Ticks.(*LiteralExpr); ok {
		if _, isMap := lit.Value.(map[string]any); isMap {
			needsSnapshot = false // pure number literal like {"num": 5}
		}
	}
	if needsSnapshot {
		tmp := allocUniqueVar("@wait", syms.usedVars)
		b.emit(map[string]any{
			"op": "set_reg",
			"1":  ticksVal,
			"2":  tmp,
		})
		ticksVar = tmp
	}

	// Emit wait frame
	waitFrame := map[string]any{"op": "wait", "1": ticksVar}
	setComment(waitFrame, s.Comment)
	waitPos := b.emit(waitFrame)

	// Emit body
	savedScope := syms.pushScope()
	if _, err := p.emitBehaviorStmts(s.Body, b, syms); err != nil {
		return err
	}
	syms.popScope(savedScope)

	// Emit tail condition to temp var
	condVar := allocUniqueVar("@wcond", syms.usedVars)
	if err := p.emitBhvExprTo(s.Tail, condVar, syms, b, ""); err != nil {
		return err
	}

	// Emit truthy check: compare_register condVar, false
	// Different (truthy) → afterWait, Equal (falsy / next) → waitPos
	afterWait := frameRef(b.pos() + 1)
	b.emit(map[string]any{
		"op":                 "compare_register",
		compareRegDifferent:  afterWait,
		compareRegValue1:     condVar,
		compareRegValue2:     false,
		"next":               frameRef(waitPos),
	})

	return nil
}

// emitBhvWhileStmt emits a while loop using forward-jump patching for
// the condition (same pattern as emitFnWhileStmt).
func (p *parser) emitBhvWhileStmt(s *WhileStmt, b *frameBuilder, syms *symbolTable) error {
	loopStart := b.pos()

	// Resolve condition and emit check frames
	resolved, err := p.resolveBhvBoolTree(s.Cond, syms, b)
	if err != nil {
		return err
	}

	checkStart := b.pos()
	checkCount := resolved.frameCount()
	trueBranch := frameRef(checkStart + checkCount)
	falsePlaceholder := frameRef(0)

	if resolved.isLeaf() {
		p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, b, s.Comment)
	} else {
		p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, b, s.Comment)
	}
	stripFallThrough(b, checkStart, checkCount)

	origLen := len(b.frames)

	// Emit body directly into b
	savedScope := syms.pushScope()
	if _, err := p.emitBehaviorStmts(s.Body, b, syms); err != nil {
		return err
	}
	syms.popScope(savedScope)

	// Jump back to loop start.
	emitLoopBackEdge(b, loopStart, frameRef(loopStart))

	afterLoop := frameRef(b.pos())
	patchFalseBranches(b, checkStart, checkCount, falsePlaceholder, afterLoop)

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

// emitBhvLoopStmt emits a loop with optional break support.
func (p *parser) emitBhvLoopStmt(s *LoopStmt, b *frameBuilder, syms *symbolTable) error {
	if s.Count != nil {
		return p.emitBhvCountedLoop(s, b, syms)
	}

	loopStart := b.pos()

	// Compile body
	savedScope := syms.pushScope()
	origLen := len(b.frames)
	if _, err := p.emitBehaviorStmts(s.Body, b, syms); err != nil {
		return err
	}
	syms.popScope(savedScope)

	// Loop back: set last frame's "next" to loop start.
	emitLoopBackEdge(b, loopStart, frameRef(loopStart))

	afterLoop := frameRef(b.pos())
	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

// emitBhvCountedLoop emits a counted loop: loop N { ... }
// Frame layout: INIT → CHECK → BODY → INCR → (back to CHECK)
func (p *parser) emitBhvCountedLoop(s *LoopStmt, b *frameBuilder, syms *symbolTable) error {
	counterVar := allocUniqueVar("@loop", syms.usedVars)

	// Resolve count expression
	limit, err := p.emitBhvExprGetValue(s.Count, syms, b, "")
	if err != nil {
		return err
	}

	// INIT: set_number 0 → counter
	b.emit(map[string]any{
		"op": "set_number",
		"2":  map[string]any{"num": 0},
		"3":  counterVar,
	})

	// CHECK: check_number counter vs limit
	checkFrame := b.emit(map[string]any{
		"op":        "check_number",
		checkValue:  counterVar,
		checkTarget: limit,
	})
	setComment(b.get(checkFrame), s.Comment)

	// Compile body
	savedScope := syms.pushScope()
	origLen := len(b.frames)
	if _, err := p.emitBehaviorStmts(s.Body, b, syms); err != nil {
		return err
	}
	syms.popScope(savedScope)

	// INCR: add counter + 1 → counter, next → CHECK
	incrFrame := b.emit(map[string]any{
		"op":   "add",
		"1":    counterVar,
		"2":    map[string]any{"num": 1},
		"3":    counterVar,
		"next": frameRef(checkFrame),
	})

	// Set last body frame's "next" to incr (if not already set by inner control flow)
	patchLastBodyNext(b, origLen, incrFrame)

	// Patch CHECK exits: larger and equal → afterLoop
	afterLoop := frameRef(b.pos())
	check := b.get(checkFrame)
	check[checkLarger] = afterLoop
	check["next"] = afterLoop

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

// emitBhvForStmt emits a for-in loop at behavior level.
func (p *parser) emitBhvForStmt(s *ForStmt, b *frameBuilder, syms *symbolTable) error {
	savedScope := syms.pushScope()
	iterVar := s.IterVar
	syms.declareVar(iterVar, false)

	var err error
	ctor, isCtor := s.Range.(*ConstructorExpr)
	if isCtor && ctor.TypeName == "Range" {
		err = p.emitBhvForStmtRange(s, ctor, b, syms)
	} else {
		err = p.emitBhvForStmtRuntime(s, b, syms)
	}
	syms.popScope(savedScope)
	return err
}

// emitBhvForStmtRange emits a for loop when the Range constructor is directly visible.
func (p *parser) emitBhvForStmtRange(s *ForStmt, ctor *ConstructorExpr, b *frameBuilder, syms *symbolTable) error {
	iterVar := s.IterVar

	stepLit, stepIsLit := ctor.Args[2].(*LiteralExpr)
	var stepSign int
	if stepIsLit {
		if m, ok := stepLit.Value.(map[string]any); ok {
			if n, ok := m["num"].(int); ok {
				if n > 0 {
					stepSign = 1
				} else {
					stepSign = -1
				}
			}
		}
	}

	if !stepIsLit || stepSign == 0 {
		return p.emitBhvForStmtRuntime(s, b, syms)
	}

	startVal, err := p.emitBhvExprGetValue(ctor.Args[0], syms, b, "")
	if err != nil {
		return err
	}
	stopVal, err := p.emitBhvExprGetValue(ctor.Args[1], syms, b, "")
	if err != nil {
		return err
	}
	stepVal, err := p.emitBhvExprGetValue(ctor.Args[2], syms, b, "")
	if err != nil {
		return err
	}

	// INIT: set_reg start → iterVar
	b.emit(map[string]any{
		"op": "set_reg",
		"1":  startVal,
		"2":  iterVar,
	})

	// CHECK: check_number iterVar vs stop
	check := map[string]any{
		"op":        "check_number",
		checkValue:  iterVar,
		checkTarget: stopVal,
	}
	setComment(check, s.Comment)
	checkFrame := b.emit(check)

	// Compile body
	origLen := len(b.frames)
	if _, err := p.emitBehaviorStmts(s.Body, b, syms); err != nil {
		return err
	}

	// INCR: add iterVar + step → iterVar, next → CHECK
	incrFrame := b.emit(map[string]any{
		"op":   "add",
		"1":    iterVar,
		"2":    stepVal,
		"3":    iterVar,
		"next": frameRef(checkFrame),
	})

	// Set last body frame's "next" to incr (if not already set by inner control flow)
	patchLastBodyNext(b, origLen, incrFrame)

	afterLoop := frameRef(b.pos())
	if stepSign > 0 {
		check[checkLarger] = afterLoop
		check["next"] = afterLoop
	} else {
		check[checkSmaller] = afterLoop
		check["next"] = afterLoop
	}

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}

// emitBhvForStmtRuntime emits a for loop where the range is a runtime value (Path C).
func (p *parser) emitBhvForStmtRuntime(s *ForStmt, b *frameBuilder, syms *symbolTable) error {
	iterVar := s.IterVar

	rangeVal, err := p.emitBhvExprGetValue(s.Range, syms, b, "")
	if err != nil {
		return err
	}

	stepVar := allocUniqueVar("@step", syms.usedVars)
	startVar := allocUniqueVar("@start", syms.usedVars)
	stopVar := allocUniqueVar("@stop", syms.usedVars)
	retVals := []any{stepVar, false, false, startVar, stopVar}
	if err := p.expandCall("separate_register", []any{rangeVal}, nil, retVals, b, 0, "", syms.usedVars); err != nil {
		return err
	}

	// INIT: set_reg @start → iterVar
	b.emit(map[string]any{
		"op": "set_reg",
		"1":  startVar,
		"2":  iterVar,
	})

	// STEP_CHK: check_number @step vs 0
	stepCheck := map[string]any{
		"op":        "check_number",
		checkValue:  stepVar,
		checkTarget: map[string]any{"num": 0},
	}
	setComment(stepCheck, s.Comment)
	stepCheckFrame := b.emit(stepCheck)

	// CHECK_POS
	checkPos := map[string]any{
		"op":        "check_number",
		checkValue:  iterVar,
		checkTarget: stopVar,
	}
	checkPosFrame := b.emit(checkPos)

	// CHECK_NEG
	checkNeg := map[string]any{
		"op":        "check_number",
		checkValue:  iterVar,
		checkTarget: stopVar,
	}
	checkNegFrame := b.emit(checkNeg)

	// Compile body
	origLen := len(b.frames)
	if _, err := p.emitBehaviorStmts(s.Body, b, syms); err != nil {
		return err
	}

	// INCR
	incrFrame := b.emit(map[string]any{
		"op":   "add",
		"1":    iterVar,
		"2":    stepVar,
		"3":    iterVar,
		"next": frameRef(stepCheckFrame),
	})

	// Set last body frame's "next" to incr (if not already set by inner control flow)
	patchLastBodyNext(b, origLen, incrFrame)

	afterLoop := frameRef(b.pos())

	stepCheck[checkLarger] = frameRef(checkPosFrame)
	stepCheck[checkSmaller] = frameRef(checkNegFrame)
	stepCheck["next"] = afterLoop

	bodyStart := frameRef(origLen)
	checkPos[checkSmaller] = bodyStart
	checkPos[checkLarger] = afterLoop
	checkPos["next"] = afterLoop

	checkNeg[checkLarger] = bodyStart
	checkNeg[checkSmaller] = afterLoop
	checkNeg["next"] = afterLoop

	patchBreakPlaceholders(b, origLen, s.Label, afterLoop)

	return nil
}
