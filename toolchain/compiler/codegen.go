package compiler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
	"golang.org/x/text/language"
)

// frameHasReturnSlot reports whether a raw instruction frame contains any @N
// return slot markers.
func frameHasReturnSlot(frame map[string]any) bool {
	for _, v := range frame {
		if _, ok := v.(returnSlot); ok {
			return true
		}
	}
	return false
}

// frameReturnCount returns the number of return slots (@N markers) in a raw
// instruction frame.
func frameReturnCount(frame map[string]any) int {
	count := 0
	for _, v := range frame {
		if _, ok := v.(returnSlot); ok {
			count++
		}
	}
	return count
}

// resolveInstructionFrame converts a raw instruction frame (0-based reference
// keys) into a native-format frame (1-based keys) with parameter and return
// slot substitutions applied. retVals provides return targets (indexed by
// returnSlot(N)-1), paramMap substitutes string values, kwVars identifies
// keyword param variables to omit when absent, and comment sets the "cmt"
// field. Any of retVals, paramMap, kwVars may be nil.
func resolveInstructionFrame(frame map[string]any, retVals []any, paramMap map[string]any, kwVars map[string]bool, comment string) map[string]any {
	instr := make(map[string]any, len(frame))
	for k, v := range frame {
		// Convert 0-based reference keys to 1-based native keys.
		nativeKey := k
		if n, err := strconv.Atoi(k); err == nil {
			nativeKey = strconv.Itoa(n + 1)
		}
		if rs, ok := v.(returnSlot); ok {
			idx := int(rs) - 1
			if retVals != nil && idx < len(retVals) {
				instr[nativeKey] = retVals[idx]
			} else {
				instr[nativeKey] = false
			}
			continue
		}
		if s, ok := v.(string); ok {
			if paramMap != nil {
				if arg, ok := paramMap[s]; ok {
					instr[nativeKey] = arg
					continue
				}
			}
			if kwVars != nil && kwVars[s] {
				continue // omit absent keyword param
			}
		}
		instr[nativeKey] = v
	}
	setComment(instr, comment)
	return instr
}

// argDirection determines the effective direction of a resolved argument value
// at behavior level.
func argDirection(val any, syms *symbolTable) string {
	switch v := val.(type) {
	case int:
		if v < 0 {
			return "inout" // unit register
		}
		if v >= 1 && v <= len(syms.params) {
			dir := syms.params[v-1].direction
			if dir == "" {
				return "in"
			}
			return dir
		}
		return "inout"
	case string:
		if vi, ok := syms.lookupVar(v); ok {
			if !vi.mutable {
				return "in"
			}
			return "inout"
		}
		return "inout"
	default:
		return "in" // literals, maps, false
	}
}

// checkReadable verifies that a $param name can be read (i.e., is not out-only).
func (p *parser) checkReadable(name string, syms *symbolTable, pos int) error {
	if !strings.HasPrefix(name, "$") {
		return nil
	}
	if _, ok := unitRegisters[name]; ok {
		return nil // unit registers always readable
	}
	if idx, ok := syms.paramMap[name]; ok {
		if syms.params[idx-1].direction == "out" {
			return p.errorf(pos, "cannot read from output parameter %q", name)
		}
	}
	return nil
}

// checkCallDirections checks direction compatibility for each argument at a
// function call site.
func (p *parser) checkCallDirections(fn *fnDef, fnName string, args []any, kwArgs map[string]any, syms *symbolTable, pos int) error {
	posIdx := 0
	for _, pd := range fn.params {
		paramDir := pd.effectiveDirection()
		var argVal any
		if pd.keyword == "" {
			if posIdx < len(args) {
				argVal = args[posIdx]
			}
			posIdx++
		} else if kwArgs != nil {
			argVal = kwArgs[pd.keyword]
		}
		if argVal == nil {
			continue
		}
		aDir := argDirection(argVal, syms)
		if !canPass(paramDir, aDir) {
			return p.errorf(pos, "cannot pass %s parameter to %s parameter %q of %s",
				aDir, paramDir, pd.name, fnName)
		}
	}
	return nil
}

// checkInstructionDirections verifies that non-@N slots in an instruction
// frame don't read from out-only parameters.
func (p *parser) checkInstructionDirections(frame map[string]any, syms *symbolTable, pos int) error {
	for _, v := range frame {
		if _, ok := v.(returnSlot); ok {
			continue
		}
		if name, ok := v.(string); ok {
			if err := p.checkReadable(name, syms, pos); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkCallAnnotation validates a call-site direction annotation against a
// parameter definition. annotation is "" (no annotation), "in", "out", or
// "inout". If the parameter is out or inout, the annotation must be present
// and match. If the parameter is in, an explicit "in" is accepted but no
// annotation is required.
func (p *parser) checkCallAnnotation(annotation string, pd *paramDef, fnName string, pos int) error {
	expected := pd.effectiveDirection()
	if annotation == "" {
		if expected == "out" || expected == "inout" {
			return p.errorf(pos, "missing '%s' annotation for argument to %s parameter %q of %s",
				expected, expected, pd.name, fnName)
		}
		return nil
	}
	if annotation != expected {
		return p.errorf(pos, "argument has '%s' annotation but parameter %q of %s is '%s'",
			annotation, pd.name, fnName, expected)
	}
	return nil
}

func (p *parser) parseBehaviorBody(behaviorID string) (*codec.Object, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	value := map[string]any{}
	b := &frameBuilder{}
	syms := newSymbolTable()
	hasInstruction := false // true after any instruction-emitting statement

	// Enable function calls in boolean primary position (e.g., d || my_fn x)
	p.callExprParser = func(callee *fnDef, calleeTok token) (Expr, error) {
		args, kwArgs, err := p.parseBhvCallArgs(callee, calleeTok, syms)
		if err != nil {
			return nil, err
		}
		return &CallExpr{Name: calleeTok.val, Args: args, KwArgs: kwArgs}, nil
	}
	defer func() { p.callExprParser = nil }()

	// Phase 1: Parse attributes and statements into AST nodes.
	var stmts []Stmt
	var terminal Stmt // non-nil when the last statement was terminal
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
		if terminal != nil {
			p.warnf(tok.pos, "unreachable code after '%s'", terminalKeyword(terminal))
			p.unget(tok)
			if err := p.skipToCloseBrace(); err != nil {
				return nil, err
			}
			break
		}
		if tok.kind == tokAt {
			attr, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			switch attr.val {
			case "name":
				if _, exists := value["name"]; exists {
					return nil, p.errorf(tok.pos, "duplicate @name")
				}
				name, err := p.parseName()
				if err != nil {
					return nil, err
				}
				value["name"] = name
			case "param":
				if hasInstruction {
					return nil, p.errorf(tok.pos, "@param must be declared before any instructions")
				}
				if err := p.parseParamAttr(syms, tok.pos); err != nil {
					return nil, err
				}
			default:
				return nil, p.errorf(attr.pos, "unknown attribute @%s", attr.val)
			}
			continue
		}

		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected statement, got %s", tok.describe())
		}

		hasInstruction = true

		// Try shared keyword cases
		parsed, handled, err := p.parseBhvOneStmt(tok, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			stmts = append(stmts, parsed...)
			if last := stmts[len(stmts)-1]; isTerminalStmt(last) {
				terminal = last
			}
			continue
		}

		// Default case: labeled loops or regular statement
		labeled, err := p.tryParseLabeledLoop(tok, syms)
		if err != nil {
			return nil, err
		}
		if labeled != nil {
			stmts = append(stmts, labeled)
			continue
		}
		parsed, err = p.parseBhvDefaultStmt(tok, syms)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, parsed...)
	}

	// Phase 2: Emit frames from AST.
	b.mode = modeLocked
	if _, err := p.emitBehaviorStmts(stmts, b, syms); err != nil {
		return nil, err
	}

	// Emit parameter declarations.
	if len(syms.params) > 0 {
		params := make([]any, len(syms.params))
		pnames := make([]any, len(syms.params))
		for i, pi := range syms.params {
			params[i] = false // default value (empty)
			pnames[i] = pi.name
		}
		value["parameters"] = params
		value["pnames"] = pnames
	}

	if _, exists := value["name"]; !exists {
		value["name"] = behaviorID
	}

	b.finalize(value)
	return &codec.Object{Type: codec.Behavior, Value: value}, nil
}

// resolveAssignTarget resolves an assignment target identifier through the
// symbol table: $register → unit register int, param → index, else → variable name.
// Returns an error if the target is an immutable let variable or a parameter
// with incompatible direction. compound indicates a read+write operation (++, +=).
func (p *parser) resolveAssignTarget(name string, syms *symbolTable, pos int, compound bool) (any, error) {
	if vi, ok := syms.lookupVar(name); ok {
		if !vi.mutable {
			return nil, p.errorf(pos, "cannot assign to immutable variable %q", name)
		}
		if compound {
			syms.markUsed(name) // compound assignment reads the variable
		}
		return name, nil
	}
	if strings.HasPrefix(name, "$") {
		if reg, ok := unitRegisters[name]; ok {
			return reg, nil
		}
		if idx, ok := syms.paramMap[name]; ok {
			dir := syms.params[idx-1].direction
			if dir == "in" {
				return nil, p.errorf(pos, "cannot assign to input parameter %q", name)
			}
			if compound && dir == "out" {
				return nil, p.errorf(pos, "cannot read from output parameter %q", name)
			}
			return idx, nil
		}
		return nil, p.errorf(pos, "unknown register %q", name)
	}
	return nil, p.errorf(pos, "undeclared variable %q", name)
}

// emitComparison emits a 3-frame comparison pattern that produces a boolean
// value (1 for true, false/empty for false) in target.
//
// For numeric comparisons (>, <, >=, <=): uses check_number (3-way branch).
// For equality comparisons (==, !=): uses compare_register (2-way branch).
//
//	Frame N:   check_number or compare_register (branch to true or false)
//	Frame N+1: set_reg false → target, next → N+3 (false case)
//	Frame N+2: set_reg 1 → target (true case, falls through)
func (p *parser) emitComparison(op tokenKind, lhs, rhs, target any, b *frameBuilder, comment string) {
	falsePos := b.pos() + 1
	truePos := b.pos() + 2
	afterPos := b.pos() + 3

	var check map[string]any

	switch op {
	case tokDoubleEquals:
		check = map[string]any{
			"op":                "compare_register",
			compareRegValue1:    lhs,
			compareRegValue2:    rhs,
			compareRegDifferent: frameRef(falsePos),
			"next":              frameRef(truePos),
		}
	case tokNotEquals:
		check = map[string]any{
			"op":                "compare_register",
			compareRegValue1:    lhs,
			compareRegValue2:    rhs,
			compareRegDifferent: frameRef(truePos),
			// Equal falls through naturally to false (N+1)
		}
	default:
		check = map[string]any{
			"op":        "check_number",
			checkValue:  lhs,
			checkTarget: rhs,
		}
		switch op {
		case tokGreater:
			check[checkLarger] = frameRef(truePos)
			check[checkSmaller] = frameRef(falsePos)
		case tokLess:
			check[checkLarger] = frameRef(falsePos)
			check[checkSmaller] = frameRef(truePos)
		case tokGreaterEquals:
			check[checkLarger] = frameRef(truePos)
			check[checkSmaller] = frameRef(falsePos)
			check["next"] = frameRef(truePos)
		case tokLessEquals:
			check[checkLarger] = frameRef(falsePos)
			check[checkSmaller] = frameRef(truePos)
			check["next"] = frameRef(truePos)
		}
	}
	setComment(check, comment)

	b.emit(check)

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
}

// arithOpNames maps both arithmetic (tokPlus, etc.) and compound assignment
// (tokPlusEquals, etc.) token kinds to their stdlib opcode names.
var arithOpNames = map[tokenKind]string{
	tokPlus: "add", tokPlusEquals: "add",
	tokMinus: "sub", tokMinusEquals: "sub",
	tokStar: "mul", tokStarEquals: "mul",
	tokSlash: "div", tokSlashEquals: "div",
	tokPercent: "modulo", tokPercentEquals: "modulo",
}

// arithOpName maps an arithmetic or compound assignment token kind to the
// stdlib function opcode name.
func arithOpName(kind tokenKind) string { return arithOpNames[kind] }

// isHighPriorityArithOp reports whether the token kind is * or / (higher PEMDAS
// precedence than + and -).
func isHighPriorityArithOp(kind tokenKind) bool {
	return kind == tokStar || kind == tokSlash || kind == tokPercent
}

// isLowPriorityArithOp reports whether the token kind is + or - (lower PEMDAS
// precedence than * and /).
func isLowPriorityArithOp(kind tokenKind) bool {
	return kind == tokPlus || kind == tokMinus
}

// isCompoundAssignOp reports whether the token kind is a compound assignment
// operator (+=, -=, *=, /=).
func isCompoundAssignOp(kind tokenKind) bool {
	return kind == tokPlusEquals || kind == tokMinusEquals || kind == tokStarEquals || kind == tokSlashEquals || kind == tokPercentEquals
}

// isComparisonOp reports whether the token kind is a comparison operator
// (>, <, >=, <=, ==, !=).
func isComparisonOp(kind tokenKind) bool {
	return kind == tokGreater || kind == tokLess || kind == tokGreaterEquals || kind == tokLessEquals ||
		kind == tokDoubleEquals || kind == tokNotEquals
}

// isEqualityOp reports whether the token kind is an equality operator (==, !=).
// Used to distinguish compare_register ops from check_number ops in chains.
func isEqualityOp(kind tokenKind) bool {
	return kind == tokDoubleEquals || kind == tokNotEquals
}

// isTypeCheckOp reports whether the token kind is the 'is' type check operator.
func isTypeCheckOp(kind tokenKind) bool {
	return kind == tokIs
}

// parseIsRHS consumes the next token and validates it as a type name for the
// 'is' operator. Returns the wire-format slot string for value_type.
func (p *parser) parseIsRHS() (string, error) {
	tok, err := p.next()
	if err != nil {
		return "", err
	}
	if tok.kind != tokIdent {
		return "", p.errorf(tok.pos, "expected type name after 'is', got %s", tok.describe())
	}
	slot, ok := typeCheckSlot(tok.val)
	if !ok {
		switch tok.val {
		case "Number":
			return "", p.errorf(tok.pos, "'is Number' is not supported; value_type cannot distinguish numbers from empty registers")
		case "Range":
			return "", p.errorf(tok.pos, "'is Range' is not supported; Range uses Coordinate at the VM level")
		default:
			return "", p.errorf(tok.pos, "unknown type %q in 'is' expression; expected Item, Unit, Component, Technology, Value, or Coordinate", tok.val)
		}
	}
	return slot, nil
}

// emitTypeCheck emits a 3-frame value_type + set_reg + set_reg pattern that
// produces a boolean value (1 for true, false/empty for false) in target.
//
//	Frame N:   value_type (matching type branch → true, all others → false)
//	Frame N+1: set_reg false → target, next → N+3 (false case)
//	Frame N+2: set_reg 1 → target (true case, falls through)
func (p *parser) emitTypeCheck(lhs, target any, typeSlot string, b *frameBuilder, comment string) {
	falsePos := b.pos() + 1
	truePos := b.pos() + 2
	afterPos := b.pos() + 3

	check := map[string]any{
		"op":           "value_type",
		valueTypeInput: lhs,
	}
	// Route matching type to true, all others + "next" to false.
	for _, slot := range allTypeSlots {
		if slot == typeSlot {
			check[slot] = frameRef(truePos)
		} else {
			check[slot] = frameRef(falsePos)
		}
	}
	check["next"] = frameRef(falsePos)
	setComment(check, comment)

	b.emit(check)

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
}

// emitTruthyCheck emits a 3-frame compare_register + set_reg + set_reg pattern
// that tests whether lhs is truthy (non-empty). Empty → false, non-empty → 1.
func (p *parser) emitTruthyCheck(lhs, target any, b *frameBuilder, comment string) {
	falsePos := b.pos() + 1
	truePos := b.pos() + 2
	afterPos := b.pos() + 3

	check := map[string]any{
		"op":                "compare_register",
		compareRegValue1:    lhs,
		compareRegValue2:    false,
		compareRegDifferent: frameRef(truePos),
		"next":              frameRef(falsePos),
	}
	setComment(check, comment)
	b.emit(check)

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
}

// emitResolvedBoolExprTo emits the post-resolve boolean expression frames.
// For a single non-negated leaf, delegates to the specialized emitter.
// For chains/groups, emits the recursive bool frames with false/true set_reg.
func (p *parser) emitResolvedBoolExprTo(resolved *resolvedBoolExpr, target any, b *frameBuilder, comment string) {
	if resolved.isLeaf() && !resolved.term.negated {
		t := resolved.term
		switch {
		case isTypeCheckOp(t.op):
			p.emitTypeCheck(t.lhs, target, t.rhs.(string), b, comment)
		case t.op == tokTruthy:
			p.emitTruthyCheck(t.lhs, target, b, comment)
		default:
			p.emitComparison(t.op, t.lhs, t.rhs, target, b, comment)
		}
		return
	}

	totalChecks := resolved.frameCount()
	base := b.pos()
	falsePos := base + totalChecks
	truePos := base + totalChecks + 1
	afterPos := base + totalChecks + 2

	p.emitResolvedBoolFrames(resolved, frameRef(truePos), frameRef(falsePos), b, comment)

	b.emit(map[string]any{
		"op":   "set_reg",
		"1":    false,
		"2":    target,
		"next": frameRef(afterPos),
	})
	b.emit(map[string]any{
		"op": "set_reg",
		"1":  map[string]any{"num": 1},
		"2":  target,
	})
}

// comparisonTerm holds the parsed components of a single comparison expression.
// For comparison ops (>, <, etc.), rhs is any (resolved operand).
// For the 'is' type check op, rhs is a string (the wire-format slot key).
// For tokTruthy, rhs is nil (only lhs is used).
type comparisonTerm struct {
	op      tokenKind // tokGreater, tokLess, tokGreaterEquals, tokLessEquals, tokDoubleEquals, tokNotEquals, tokIs, or tokTruthy
	lhs     any
	rhs     any
	negated bool // true when prefixed with !
}

// resolveBoolTree walks an Expr tree, emitting arithmetic frames via the
// emit callback and resolving all operands to values, producing a
// resolvedBoolExpr tree. Used by both behavior-level and fn body paths.
func (p *parser) resolveBoolTree(expr Expr, emit func(Expr) (any, error)) (*resolvedBoolExpr, error) {
	switch e := expr.(type) {
	case *CompareExpr:
		lhs, err := emit(e.LHS)
		if err != nil {
			return nil, err
		}
		rhs, err := emit(e.RHS)
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: e.Op, lhs: lhs, rhs: rhs}}, nil
	case *TypeCheckExpr:
		lhs, err := emit(e.Value)
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: tokIs, lhs: lhs, rhs: e.TypeSlot}}, nil
	case *TruthyExpr:
		lhs, err := emit(e.Value)
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: tokTruthy, lhs: lhs}}, nil
	case *LiteralExpr:
		// Folded constant (e.g., from comparison folding). Treat as a truthy
		// check on the literal value.
		lhs, err := emit(e)
		if err != nil {
			return nil, err
		}
		return &resolvedBoolExpr{term: &comparisonTerm{op: tokTruthy, lhs: lhs}}, nil
	case *NotExpr:
		resolved, err := p.resolveBoolTree(e.Value, emit)
		if err != nil {
			return nil, err
		}
		negateResolved(resolved)
		return resolved, nil
	case *BoolChainExpr:
		children := make([]*resolvedBoolExpr, len(e.Children))
		for i, child := range e.Children {
			resolved, err := p.resolveBoolTree(child, emit)
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

// emitArithNode recursively emits an ArithExpr node. Non-ArithExpr children
// are resolved via the emit callback. ArithExpr children are emitted
// recursively with a shared arithCounter. The last (outermost) operation
// writes directly to the caller's target.
func (p *parser) emitArithNode(expr *ArithExpr, target any, b *frameBuilder, usedVars map[string]bool, comment string, ac *arithCounter, emit func(Expr) (any, error)) (any, error) {
	var lhs any
	if sub, ok := expr.LHS.(*ArithExpr); ok {
		tmp := ac.next(usedVars)
		val, err := p.emitArithNode(sub, tmp, b, usedVars, "", ac, emit)
		if err != nil {
			return nil, err
		}
		lhs = val
	} else {
		val, err := emit(expr.LHS)
		if err != nil {
			return nil, err
		}
		lhs = val
	}

	var rhs any
	if sub, ok := expr.RHS.(*ArithExpr); ok {
		tmp := ac.next(usedVars)
		val, err := p.emitArithNode(sub, tmp, b, usedVars, "", ac, emit)
		if err != nil {
			return nil, err
		}
		rhs = val
	} else {
		val, err := emit(expr.RHS)
		if err != nil {
			return nil, err
		}
		rhs = val
	}

	f := map[string]any{
		"op": arithOpName(expr.Op),
		"1":  lhs,
		"2":  rhs,
		"3":  target,
	}
	setComment(f, comment)
	b.emit(f)
	return target, nil
}

func (p *parser) emitBoolCheckFrame(term *comparisonTerm, trueTarget, falseTarget frameRef, b *frameBuilder, comment string) {
	if term.negated {
		trueTarget, falseTarget = falseTarget, trueTarget
	}

	var check map[string]any

	if term.op == tokTruthy {
		check = map[string]any{
			"op":                "compare_register",
			compareRegValue1:    term.lhs,
			compareRegValue2:    false,
			compareRegDifferent: trueTarget,
			"next":              falseTarget,
		}
		setComment(check, comment)
		b.emit(check)
		return
	}

	if isTypeCheckOp(term.op) {
		typeSlot := term.rhs.(string)
		check = map[string]any{
			"op":           "value_type",
			valueTypeInput: term.lhs,
		}
		for _, slot := range allTypeSlots {
			if slot == typeSlot {
				check[slot] = trueTarget
			} else {
				check[slot] = falseTarget
			}
		}
		check["next"] = falseTarget
	} else if isEqualityOp(term.op) {
		check = map[string]any{
			"op":             "compare_register",
			compareRegValue1: term.lhs,
			compareRegValue2: term.rhs,
		}
		switch term.op {
		case tokDoubleEquals:
			check[compareRegDifferent] = falseTarget
			check["next"] = trueTarget
		case tokNotEquals:
			check[compareRegDifferent] = trueTarget
			check["next"] = falseTarget
		}
	} else {
		check = map[string]any{
			"op":        "check_number",
			checkValue:  term.lhs,
			checkTarget: term.rhs,
		}
		switch term.op {
		case tokGreater:
			check[checkLarger] = trueTarget
			check[checkSmaller] = falseTarget
			check["next"] = falseTarget
		case tokLess:
			check[checkLarger] = falseTarget
			check[checkSmaller] = trueTarget
			check["next"] = falseTarget
		case tokGreaterEquals:
			check[checkLarger] = trueTarget
			check[checkSmaller] = falseTarget
			check["next"] = trueTarget
		case tokLessEquals:
			check[checkLarger] = falseTarget
			check[checkSmaller] = trueTarget
			check["next"] = trueTarget
		}
	}

	setComment(check, comment)
	b.emit(check)
}

// parseParamAttr parses an @param attribute inside a behavior body.
// Syntax: @param <direction> <name> <"display name" | { localized block }>
// Direction is one of: in, out, inout.
func (p *parser) parseParamAttr(syms *symbolTable, pos int) error {
	// Parse direction
	dirTok, err := p.expect(tokIdent)
	if err != nil {
		return err
	}
	switch dirTok.val {
	case "in", "out", "inout":
		// valid
	default:
		return p.errorf(dirTok.pos, "expected parameter direction (in, out, inout), got %q", dirTok.val)
	}

	// Parse variable name
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return err
	}
	dollarName := "$" + nameTok.val

	// Check for naming conflicts
	if _, ok := unitRegisters[dollarName]; ok {
		return p.errorf(nameTok.pos, "parameter name %q conflicts with a built-in register", dollarName)
	}
	if _, ok := syms.paramMap[dollarName]; ok {
		return p.errorf(nameTok.pos, "duplicate parameter %q", dollarName)
	}

	// Parse display name: string literal or localized block
	displayName := nameTok.val
	peek, err := p.next()
	if err != nil {
		return err
	}
	if peek.kind == tokString {
		displayName = peek.val
	} else if peek.kind == tokIdent && peek.val == "localize" {
		resolved, err := p.parseLocalize()
		if err != nil {
			return err
		}
		displayName = resolved
	} else {
		p.unget(peek)
	}

	if len(syms.params) >= 10 {
		return p.errorf(pos, "too many parameters (maximum 10)")
	}

	idx := len(syms.params) + 1
	syms.params = append(syms.params, paramInfo{
		index:     idx,
		name:      displayName,
		direction: dirTok.val,
	})
	syms.paramMap[dollarName] = idx
	return nil
}

// parseConstructorExpr parses a type constructor call into a ConstructorExpr.
// The constructor name token has already been consumed. parseArg is called to
// parse each argument expression (parseBhvArgExpr at behavior level,
// parseFnBodyExpr in fn bodies). The caller handles any trailing & operator.
func (p *parser) parseConstructorExpr(nameTok token, parseArg func() (Expr, error)) (Expr, error) {
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
		return &ConstructorExpr{
			TypeName: nameTok.val,
			Args:     []Expr{&LiteralExpr{Value: argTok.val}},
		}, nil
	case "Coordinate":
		x, err := parseArg()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokComma); err != nil {
			return nil, err
		}
		y, err := parseArg()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return &ConstructorExpr{
			TypeName: "Coordinate",
			Args:     []Expr{x, y},
		}, nil
	case "Range":
		arg1, err := parseArg()
		if err != nil {
			return nil, err
		}
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			return &ConstructorExpr{
				TypeName: "Range",
				Args: []Expr{
					&LiteralExpr{Value: map[string]any{"num": 0}},
					arg1,
					&LiteralExpr{Value: map[string]any{"num": 1}},
				},
			}, nil
		}
		if peek.kind != tokComma {
			return nil, p.errorf(peek.pos, "expected ',' or ')' after Range argument, got %s", peek.describe())
		}
		arg2, err := parseArg()
		if err != nil {
			return nil, err
		}
		peek, err = p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			return &ConstructorExpr{
				TypeName: "Range",
				Args:     []Expr{arg1, arg2, &LiteralExpr{Value: map[string]any{"num": 1}}},
			}, nil
		}
		if peek.kind != tokComma {
			return nil, p.errorf(peek.pos, "expected ',' or ')' after Range argument, got %s", peek.describe())
		}
		arg3, err := parseArg()
		if err != nil {
			return nil, err
		}
		if lit, ok := arg3.(*LiteralExpr); ok {
			if m, ok := lit.Value.(map[string]any); ok {
				if n, ok := m["num"]; ok && n == 0 {
					return nil, p.errorf(nameTok.pos, "Range step cannot be zero")
				}
			}
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return &ConstructorExpr{
			TypeName: "Range",
			Args:     []Expr{arg1, arg2, arg3},
		}, nil
	}
	return nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
}

// checkVarName validates that a variable name doesn't conflict with existing
// declarations or reserved names.
func (p *parser) checkVarName(name string, syms *symbolTable, pos int) error {
	if isConstructor(name) {
		return p.errorf(pos, "%q is a type constructor and cannot be used as a variable name", name)
	}
	if Keywords[name] {
		return p.errorf(pos, "%q is a reserved keyword and cannot be used as a variable name", name)
	}
	return nil
}

// parseName parses the value of an @name attribute. It handles both the simple
// string form and the localize { ... } form.
func (p *parser) parseName() (string, error) {
	tok, err := p.next()
	if err != nil {
		return "", err
	}
	if tok.kind == tokString {
		return tok.val, nil
	}
	if tok.kind == tokIdent && tok.val == "localize" {
		return p.parseLocalize()
	}
	return "", p.errorf(tok.pos, "expected string or 'localize' after @name, got %s", tok.describe())
}

// parseLocalize expects '{' then parses locale/string pairs via resolveLocalize.
func (p *parser) parseLocalize() (string, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return "", err
	}
	return p.resolveLocalize()
}

// matchLocale returns the index of the best-matching locale from locales
// for the given desired locale. If desired is empty, returns 0.
func matchLocale(desired string, locales []string) int {
	if desired == "" {
		return 0
	}
	tags := make([]language.Tag, len(locales))
	for i, l := range locales {
		tags[i] = language.Make(strings.ReplaceAll(l, "_", "-"))
	}
	d := language.Make(strings.ReplaceAll(desired, "_", "-"))
	matcher := language.NewMatcher(tags)
	_, idx, _ := matcher.Match(d)
	return idx
}

// resolveLocalize parses locale/string pairs until '}' and returns the
// best match for p.locale. If p.locale is empty, the first entry is used.
func (p *parser) resolveLocalize() (string, error) {
	type entry struct {
		locale string
		name   string
	}
	var entries []entry

	for {
		tok, err := p.next()
		if err != nil {
			return "", err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind == tokEOF {
			return "", p.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
		if tok.kind != tokIdent {
			return "", p.errorf(tok.pos, "expected locale identifier or '}', got %s", tok.describe())
		}
		nameTok, err := p.expect(tokString)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry{locale: tok.val, name: nameTok.val})
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("empty localize block")
	}

	locales := make([]string, len(entries))
	for i, e := range entries {
		locales[i] = e.locale
	}
	idx := matchLocale(p.locale, locales)
	return entries[idx].name, nil
}

// patchFalseBranches replaces falsePlaceholder frameRef values in check
// frames at positions [start, start+count) with target. Used by if/while
// emitters in both behavior-level and fn body paths.
func patchFalseBranches(b *frameBuilder, start, count int, placeholder, target frameRef) {
	for j := start; j < start+count; j++ {
		f := b.get(j)
		for k, v := range f {
			if ref, ok := v.(frameRef); ok && ref == placeholder {
				f[k] = target
			}
		}
	}
}

// patchBreakPlaceholders replaces @break placeholder frames in
// b.frames[from:] whose label matches (or is empty) with a noop jump
// to target. Used by all loop emitters (while, loop, counted loop, for)
// in both behavior-level and fn body paths.
func patchBreakPlaceholders(b *frameBuilder, from int, label string, target frameRef) {
	for j := from; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@break" {
			fLabel, _ := f["label"].(string)
			if fLabel == "" || fLabel == label {
				b.frames[j] = map[string]any{
					"op":   "set_reg",
					"1":    false,
					"2":    false,
					"next": target,
				}
			}
		}
	}
}

// emitLoopBackEdge emits the back-edge jump for while and infinite loops.
// If the last body frame is @break, a noop jump is emitted (the @break frame
// will be patched separately). If the last frame has no "next", it gets one.
// Otherwise a noop jump is emitted to avoid clobbering inner control flow.
// The bodyStart parameter guards against empty bodies (no frames emitted).
func emitLoopBackEdge(b *frameBuilder, bodyStart int, target frameRef) {
	if b.pos() <= bodyStart {
		return
	}
	lastFrame := b.get(b.pos() - 1)
	if op, _ := lastFrame["op"].(string); op == "@break" {
		b.emit(map[string]any{"op": "set_reg", "1": false, "2": false, "next": target})
	} else if _, hasNext := lastFrame["next"]; !hasNext {
		lastFrame["next"] = target
	} else {
		b.emit(map[string]any{"op": "set_reg", "1": false, "2": false, "next": target})
	}
}

// patchLastBodyNext sets "next" on the last body frame to point to nextFrame
// (typically the INCR frame in counted/for loops). Skips if the body is
// empty, or if the last frame is @break (will be patched separately) or
// already has a "next". bodyStart is the frame index where the body began.
func patchLastBodyNext(b *frameBuilder, bodyStart int, nextFrame int) {
	if nextFrame-1 < bodyStart {
		return
	}
	lastBodyFrame := b.get(nextFrame - 1)
	if op, _ := lastBodyFrame["op"].(string); op != "@break" {
		if _, hasNext := lastBodyFrame["next"]; !hasNext {
			lastBodyFrame["next"] = frameRef(nextFrame)
		}
	}
}

// -----------------------------------------------------------------------
// Unified control flow emitters
//
// These methods on *parser take an *emitContext to abstract the
// differences between behavior-level and fn body emission. Each replaces
// a bhv/fn pair of near-identical functions.
// -----------------------------------------------------------------------

// emitTailMulti emits a tail expression directing values to retVals.
// If the tail is a CallExpr, uses expandCallExpr. Otherwise, emits to
// retVals[0] and zeros remaining slots.
func (p *parser) emitTailMulti(ctx *emitContext, tail Expr, retVals []any, comment string) error {
	if ce, ok := tail.(*CallExpr); ok {
		return ctx.expandCallExpr(ce, retVals, comment)
	}
	if err := ctx.exprTo(tail, retVals[0], comment); err != nil {
		return err
	}
	for i := 1; i < len(retVals); i++ {
		ctx.b.emit(map[string]any{"op": "set_reg", "1": false, "2": retVals[i]})
	}
	return nil
}

// emitLoopStmt emits a loop/break (infinite or counted).
func (p *parser) emitLoopStmt(s *LoopStmt, ctx *emitContext, comment string) error {
	if s.Count != nil {
		return p.emitCountedLoop(s, ctx, comment)
	}

	loopStart := ctx.b.pos()
	origLen := len(ctx.b.frames)

	ctx.pushScope()
	if err := ctx.emitBody(s.Body); err != nil {
		ctx.popScope()
		return err
	}
	ctx.popScope()

	emitLoopBackEdge(ctx.b, loopStart, frameRef(loopStart))

	afterLoop := frameRef(ctx.b.pos())
	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// emitCountedLoop emits a counted loop: loop N { ... }
func (p *parser) emitCountedLoop(s *LoopStmt, ctx *emitContext, comment string) error {
	counterVar := allocUniqueVar("@loop", ctx.usedVars)

	limit, err := ctx.exprGetValue(s.Count, "")
	if err != nil {
		return err
	}

	// INIT: set_number 0 → counter
	ctx.b.emit(map[string]any{
		"op": "set_number",
		"2":  map[string]any{"num": 0},
		"3":  counterVar,
	})

	// CHECK: check_number counter vs limit
	checkFrame := ctx.b.emit(map[string]any{
		"op":        "check_number",
		checkValue:  counterVar,
		checkTarget: limit,
	})
	setComment(ctx.b.get(checkFrame), comment)

	origLen := len(ctx.b.frames)

	ctx.pushScope()
	if err := ctx.emitBody(s.Body); err != nil {
		ctx.popScope()
		return err
	}
	ctx.popScope()

	// INCR: add counter + 1 → counter, next → CHECK
	incrFrame := ctx.b.emit(map[string]any{
		"op":   "add",
		"1":    counterVar,
		"2":    map[string]any{"num": 1},
		"3":    counterVar,
		"next": frameRef(checkFrame),
	})

	patchLastBodyNext(ctx.b, origLen, incrFrame)

	afterLoop := frameRef(ctx.b.pos())
	check := ctx.b.get(checkFrame)
	check[checkLarger] = afterLoop
	check["next"] = afterLoop

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// emitWhileStmt emits a while loop.
func (p *parser) emitWhileStmt(s *WhileStmt, ctx *emitContext, comment string) error {
	loopStart := ctx.b.pos()

	resolved, err := ctx.resolveBool(s.Cond)
	if err != nil {
		return err
	}

	checkStart := ctx.b.pos()
	checkCount := resolved.frameCount()
	trueBranch := frameRef(checkStart + checkCount)
	falsePlaceholder := frameRef(0)

	if resolved.isLeaf() {
		p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, ctx.b, comment)
	} else {
		p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, ctx.b, comment)
	}
	stripFallThrough(ctx.b, checkStart, checkCount)

	origLen := len(ctx.b.frames)

	ctx.pushScope()
	if err := ctx.emitBody(s.Body); err != nil {
		ctx.popScope()
		return err
	}
	ctx.popScope()

	emitLoopBackEdge(ctx.b, loopStart, frameRef(loopStart))

	afterLoop := frameRef(ctx.b.pos())
	patchFalseBranches(ctx.b, checkStart, checkCount, falsePlaceholder, afterLoop)

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// emitWaitStmt emits a wait statement.
func (p *parser) emitWaitStmt(s *WaitStmt, ctx *emitContext, comment string) error {
	ticksVal, err := ctx.exprGetValue(s.Ticks, "")
	if err != nil {
		return err
	}

	if s.Tail == nil {
		f := map[string]any{"op": "wait", "1": ticksVal}
		setComment(f, comment)
		ctx.b.emit(f)
		return nil
	}

	// Snapshot ticks if needed.
	ticksVar := ticksVal
	needsSnapshot := true
	if lit, ok := s.Ticks.(*LiteralExpr); ok {
		if _, isMap := lit.Value.(map[string]any); isMap {
			needsSnapshot = false
		}
	}
	if needsSnapshot {
		tmp := allocUniqueVar("@wait", ctx.usedVars)
		ctx.b.emit(map[string]any{
			"op": "set_reg",
			"1":  ticksVal,
			"2":  tmp,
		})
		ticksVar = tmp
	}

	waitFrame := map[string]any{"op": "wait", "1": ticksVar}
	setComment(waitFrame, comment)
	waitPos := ctx.b.emit(waitFrame)

	ctx.pushScope()
	if err := ctx.emitBody(s.Body); err != nil {
		ctx.popScope()
		return err
	}
	ctx.popScope()

	condVar := allocUniqueVar("@wcond", ctx.usedVars)
	if err := ctx.exprTo(s.Tail, condVar, ""); err != nil {
		return err
	}

	afterWait := frameRef(ctx.b.pos() + 1)
	ctx.b.emit(map[string]any{
		"op":                "compare_register",
		compareRegDifferent: afterWait,
		compareRegValue1:    condVar,
		compareRegValue2:    false,
		"next":              frameRef(waitPos),
	})

	return nil
}

// emitModeBlockExpr emits a mode block expression.
func (p *parser) emitModeBlockExpr(e *ModeBlockExpr, retVals []any, ctx *emitContext, comment string) error {
	mbeComment := e.Comment
	if mbeComment == "" {
		mbeComment = comment
	}
	savedMode := emitModeEntry(ctx.b, e.Unlock, mbeComment)
	ctx.pushScope()
	if err := ctx.emitBody(e.Body); err != nil {
		ctx.popScope()
		emitModeExit(ctx.b, savedMode)
		return err
	}
	if err := p.emitTailMulti(ctx, e.Tail, retVals, mbeComment); err != nil {
		ctx.popScope()
		emitModeExit(ctx.b, savedMode)
		return err
	}
	ctx.popScope()
	emitModeExit(ctx.b, savedMode)
	return nil
}

// emitForStmt emits a for-in loop.
func (p *parser) emitForStmt(s *ForStmt, ctx *emitContext, comment string) error {
	ctx.pushScope()
	iterVar := ctx.declareIterVar(s.IterVar)

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

// emitForStmtRange emits a for loop when the Range constructor is directly visible.
func (p *parser) emitForStmtRange(s *ForStmt, ctor *ConstructorExpr, iterVar string, ctx *emitContext, comment string) error {
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
		return p.emitForStmtRuntime(s, iterVar, ctx, comment)
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

	// INIT
	ctx.b.emit(map[string]any{
		"op": "set_reg",
		"1":  startVal,
		"2":  iterVar,
	})

	// CHECK
	check := map[string]any{
		"op":        "check_number",
		checkValue:  iterVar,
		checkTarget: stopVal,
	}
	setComment(check, comment)
	checkFrame := ctx.b.emit(check)

	origLen := len(ctx.b.frames)

	if err := ctx.emitBody(s.Body); err != nil {
		return err
	}

	// INCR
	incrFrame := ctx.b.emit(map[string]any{
		"op":   "add",
		"1":    iterVar,
		"2":    stepVal,
		"3":    iterVar,
		"next": frameRef(checkFrame),
	})

	patchLastBodyNext(ctx.b, origLen, incrFrame)

	afterLoop := frameRef(ctx.b.pos())
	if stepSign > 0 {
		check[checkLarger] = afterLoop
		check["next"] = afterLoop
	} else {
		check[checkSmaller] = afterLoop
		check["next"] = afterLoop
	}

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// emitForStmtRuntime emits a for loop where the range is a runtime value (Path C).
func (p *parser) emitForStmtRuntime(s *ForStmt, iterVar string, ctx *emitContext, comment string) error {
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

	// INIT
	ctx.b.emit(map[string]any{
		"op": "set_reg",
		"1":  startVar,
		"2":  iterVar,
	})

	// STEP_CHK
	stepCheck := map[string]any{
		"op":        "check_number",
		checkValue:  stepVar,
		checkTarget: map[string]any{"num": 0},
	}
	setComment(stepCheck, comment)
	stepCheckFrame := ctx.b.emit(stepCheck)

	// CHECK_POS
	checkPos := map[string]any{
		"op":        "check_number",
		checkValue:  iterVar,
		checkTarget: stopVar,
	}
	checkPosFrame := ctx.b.emit(checkPos)

	// CHECK_NEG
	checkNeg := map[string]any{
		"op":        "check_number",
		checkValue:  iterVar,
		checkTarget: stopVar,
	}
	checkNegFrame := ctx.b.emit(checkNeg)

	origLen := len(ctx.b.frames)

	if err := ctx.emitBody(s.Body); err != nil {
		return err
	}

	// INCR
	incrFrame := ctx.b.emit(map[string]any{
		"op":   "add",
		"1":    iterVar,
		"2":    stepVar,
		"3":    iterVar,
		"next": frameRef(stepCheckFrame),
	})

	patchLastBodyNext(ctx.b, origLen, incrFrame)

	afterLoop := frameRef(ctx.b.pos())

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

	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// emitIfStmt emits an if/else if/else statement using forward-jump patching.
func (p *parser) emitIfStmt(s *IfStmt, ctx *emitContext, comment string) error {
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
			brComment = comment
		}

		resolved, err := ctx.resolveBool(br.cond)
		if err != nil {
			return err
		}

		checkStart := ctx.b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount)
		falsePlaceholder := frameRef(0)

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, ctx.b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, ctx.b, brComment)
		}
		stripFallThrough(ctx.b, checkStart, checkCount)

		ctx.pushScope()
		if err := ctx.emitBody(br.body); err != nil {
			ctx.popScope()
			return err
		}
		ctx.popScope()

		hasMore := i < len(branches)-1 || len(s.Else) > 0
		if hasMore {
			jumpIdx := ctx.b.pos()
			ctx.b.emit(map[string]any{
				"op":   "set_reg",
				"1":    false,
				"2":    false,
				"next": frameRef(0),
			})
			jumpsToPatch = append(jumpsToPatch, jumpIdx)
		}

		patchFalseBranches(ctx.b, checkStart, checkCount, falsePlaceholder, frameRef(ctx.b.pos()))
	}

	if len(s.Else) > 0 {
		ctx.pushScope()
		if err := ctx.emitBody(s.Else); err != nil {
			ctx.popScope()
			return err
		}
		ctx.popScope()
	}

	afterAll := frameRef(ctx.b.pos())
	for _, idx := range jumpsToPatch {
		ctx.b.get(idx)["next"] = afterAll
	}

	return nil
}

// emitIfExpr emits an if-expression directing each branch's tail to retVals.
func (p *parser) emitIfExpr(e *IfExpr, retVals []any, ctx *emitContext, comment string) error {
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

		resolved, err := ctx.resolveBool(br.cond)
		if err != nil {
			return err
		}

		checkStart := ctx.b.pos()
		checkCount := resolved.frameCount()
		trueBranch := frameRef(checkStart + checkCount)
		falsePlaceholder := frameRef(0)

		if resolved.isLeaf() {
			p.emitBoolCheckFrame(resolved.term, trueBranch, falsePlaceholder, ctx.b, brComment)
		} else {
			p.emitResolvedBoolFrames(resolved, trueBranch, falsePlaceholder, ctx.b, brComment)
		}
		stripFallThrough(ctx.b, checkStart, checkCount)

		ctx.pushScope()
		if err := ctx.emitBody(br.body); err != nil {
			ctx.popScope()
			return err
		}

		if err := p.emitTailMulti(ctx, br.tail, retVals, ""); err != nil {
			ctx.popScope()
			return err
		}
		ctx.popScope()

		jumpIdx := ctx.b.pos()
		ctx.b.emit(map[string]any{
			"op":   "set_reg",
			"1":    false,
			"2":    false,
			"next": frameRef(0),
		})
		jumpsToPatch = append(jumpsToPatch, jumpIdx)

		patchFalseBranches(ctx.b, checkStart, checkCount, falsePlaceholder, frameRef(ctx.b.pos()))
	}

	// Else body + tail (or null for missing else)
	if e.ElsTail != nil {
		ctx.pushScope()
		if err := ctx.emitBody(e.ElsBody); err != nil {
			ctx.popScope()
			return err
		}

		if err := p.emitTailMulti(ctx, e.ElsTail, retVals, ""); err != nil {
			ctx.popScope()
			return err
		}
		ctx.popScope()
	} else {
		for _, rv := range retVals {
			ctx.b.emit(map[string]any{
				"op": "set_reg",
				"1":  false,
				"2":  rv,
			})
		}
	}

	afterAll := frameRef(ctx.b.pos())
	for _, idx := range jumpsToPatch {
		ctx.b.get(idx)["next"] = afterAll
	}

	return nil
}
