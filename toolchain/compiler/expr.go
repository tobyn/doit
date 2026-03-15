package compiler

// expr.go — Shared expression parsers parameterized by operandResolver.
//
// These parsers are used by both behavior-level and fn body contexts.
// They cover arithmetic expression parsing (PEMDAS), type constructors,
// dot access (.number/.value), compile-time constant folding, boolean
// expression parsing (comparisons, type checks, &&/|| chains, !), and
// expression continuations.

import "strconv"

// operandResolver resolves a bare identifier token to an Expr.
// Used to abstract $register/parameter resolution for shared expression parsers.
type operandResolver func(tok token) (Expr, error)

// parseEnumAccess parses :: member access after an enum name has been identified.
// Returns a LiteralExpr with the member's integer value.
func (p *parser) parseEnumAccess(nameTok token, e *enumDef) (Expr, error) {
	if _, err := p.expect(tokDoubleColon); err != nil {
		return nil, p.errorf(nameTok.pos, "enum %q requires '::' member access (e.g., %s::Member)", nameTok.val, nameTok.val)
	}
	memberTok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	val, ok := e.values[memberTok.val]
	if !ok {
		return nil, p.errorf(memberTok.pos, "enum %q has no member %q", nameTok.val, memberTok.val)
	}
	return &LiteralExpr{Value: map[string]any{"num": val}}, nil
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
		if tok.val == "infinity" {
			return &LiteralExpr{Value: map[string]any{"num": -2147483648}}, nil
		}
		if tok.val == "not_equal" {
			return &LiteralExpr{Value: map[string]any{"num": -2147483647}}, nil
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
		// Check enums (direct lookup) — requires :: member access
		if e, ok := p.enums[tok.val]; ok {
			return p.parseEnumAccess(tok, e)
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
			// Check if resolved name is a namespace enum (from ns.name dot access)
			if e, ok := p.enums[name]; ok {
				return p.parseEnumAccess(token{kind: tokIdent, val: name, pos: tok.pos}, e)
			}
			if callee != nil && callee.hasReturn() {
				return p.callExprParser(callee, token{kind: tokIdent, val: name, pos: tok.pos})
			}
		}
		return resolve(tok)
	case tokPercent:
		next, err := p.next()
		if err != nil {
			return nil, err
		}
		if next.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected identifier after '%%'")
		}
		return resolve(token{tokIdent, "%" + next.val, tok.pos})
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
		return nil, p.errorf(nameTok.pos, "unknown constructor %q; valid constructors are Item, Component, Technology, Value, Coordinate, Range", nameTok.val)
	}
}

// parseArithTerm parses `primary (* | / primary)*`.
func (p *parser) parseArithTerm(resolve operandResolver) (Expr, error) {
	lhs, err := p.parseArithPrimary(resolve)
	if err != nil {
		return nil, err
	}
	lhs, err = p.maybeParseDotAccess(lhs)
	if err != nil {
		return nil, err
	}
	return p.parseArithTermFrom(lhs, resolve)
}

// maybeParseDotAccess checks for postfix .number or .value accessor and
// wraps the expression in a DotAccessExpr if found. Loops to support
// chaining (e.g., expr.value.number).
func (p *parser) maybeParseDotAccess(expr Expr) (Expr, error) {
	for {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind != tokDot {
			p.unget(peek)
			return expr, nil
		}
		// Must be followed by "number" or "value"
		memberTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if memberTok.kind != tokIdent || (memberTok.val != "number" && memberTok.val != "value") {
			return nil, p.errorf(memberTok.pos, "expected 'number' or 'value' after '.', got %s", memberTok.describe())
		}
		expr = &DotAccessExpr{Value: expr, Field: memberTok.val}
	}
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
		// Disambiguate % as modulo vs faction register: if % is followed
		// by an identifier and then an assignment/increment operator, it's
		// the start of a new %name statement, not a modulo expression.
		if peek.kind == tokPercent {
			saved := p.save()
			ahead, err := p.next()
			if err != nil {
				return nil, err
			}
			if ahead.kind == tokIdent {
				after, err := p.next()
				if err != nil {
					return nil, err
				}
				if after.kind == tokEquals || isCompoundAssignOp(after.kind) ||
					after.kind == tokPlusPlus || after.kind == tokMinusMinus {
					p.restore(saved)
					p.unget(peek)
					return result, nil
				}
			}
			p.restore(saved)
		}
		rhs, err := p.parseArithPrimary(resolve)
		if err != nil {
			return nil, err
		}
		rhs, err = p.maybeParseDotAccess(rhs)
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
	switch v := v.(type) {
	case map[string]any:
		if _, hasFr := v["fr"]; hasFr {
			return false
		}
		return true
	case bool:
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

// parseSimpleExpr parses a simple numeric expression from an already-consumed
// token. Handles number literals (with arithmetic continuation), parenthesized
// expressions, and identifiers. errContext is used in the error message for
// unexpected tokens (e.g., "after 'loop'", "after 'wait'").
func (p *parser) parseSimpleExpr(tok token, resolve operandResolver, errContext string) (Expr, error) {
	switch tok.kind {
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		expr := Expr(&LiteralExpr{Value: map[string]any{"num": num}})
		return p.parseArithExprFromFull(expr, resolve)
	case tokLParen:
		expr, err := p.parseArithExpr(resolve)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return expr, nil
	case tokIdent:
		resolved, err := resolve(tok)
		if err != nil {
			return nil, err
		}
		return p.parseArithExprFromFull(resolved, resolve)
	default:
		return nil, p.errorf(tok.pos, "expected %s, got %s", errContext, tok.describe())
	}
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
	if tok.kind == tokNumber || tok.kind == tokMinus || tok.kind == tokIdent || tok.kind == tokPercent {
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

	saved := p.save()
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
	if cmpTok.kind == tokBang {
		isTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if isTok.kind == tokIdent && isTok.val == "is" {
			slot, err := p.parseIsRHS()
			if err != nil {
				return nil, err
			}
			return &NotExpr{Value: &TypeCheckExpr{Value: lhs, TypeSlot: slot}}, nil
		}
		// Not `!is` — restore scanner to before `!` and fall through.
		p.restore(saved)
		return &TruthyExpr{Value: lhs}, nil
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

// maybeExprContinuation peeks for comparison/is/&&/|| after a value.
// Returns (expr, true) if continuation found, (original, false) otherwise.
func (p *parser) maybeExprContinuation(valueExpr Expr, resolve operandResolver) (Expr, bool, error) {
	saved := p.save()
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
	if peek.kind == tokBang {
		isTok, err := p.next()
		if err != nil {
			return nil, false, err
		}
		if isTok.kind == tokIdent && isTok.val == "is" {
			slot, err := p.parseIsRHS()
			if err != nil {
				return nil, false, err
			}
			tc := Expr(&NotExpr{Value: &TypeCheckExpr{Value: valueExpr, TypeSlot: slot}})
			chained, err := p.parseBoolChain(tc, resolve)
			if err != nil {
				return nil, false, err
			}
			return chained, true, nil
		}
		// Not `!is` — restore scanner to before `!`.
		p.restore(saved)
		return valueExpr, false, nil
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
