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
			return p.errorf(pos, "cannot read from output parameter %s", name)
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

		switch tok.val {
		case "instruction":
			rawFrame, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			if err := p.checkInstructionDirections(rawFrame, syms, tok.pos); err != nil {
				return nil, err
			}
			stmts = append(stmts, &InstructionStmt{Frame: rawFrame, Comment: p.docComment})

		case "locked":
			comment := p.docComment
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			body, err := p.parseBhvStmtBlockInner(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, &ModeBlockStmt{Unlock: false, Body: body, Comment: comment})

		case "unlocked":
			comment := p.docComment
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			body, err := p.parseBhvStmtBlockInner(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, &ModeBlockStmt{Unlock: true, Body: body, Comment: comment})

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
				// _ = fn args → bare call, discard returns
				calleeTok, err := p.expect(tokIdent)
				if err != nil {
					return nil, err
				}
				fn := p.fns[calleeTok.val]
				if fn == nil {
					return nil, p.errorf(calleeTok.pos, "unknown function %q", calleeTok.val)
				}
				args, kwArgs, err := p.parseBhvCallArgs(fn, calleeTok, syms)
				if err != nil {
					return nil, err
				}
				stmts = append(stmts, &CallStmt{
					Name:    calleeTok.val,
					Args:    args,
					KwArgs:  kwArgs,
					Comment: p.docComment,
				})
			} else {
				return nil, p.errorf(sep.pos, "expected ',' or '=' after '_', got %s", sep.describe())
			}

		case "loop":
			loopStmt, err := p.parseBhvLoopStmt(syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, loopStmt)

		case "if":
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
			parsed, err := p.parseBhvDefaultStmt(tok, syms)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, parsed...)
		}
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
				return nil, p.errorf(pos, "cannot assign to input parameter %s", name)
			}
			if compound && dir == "out" {
				return nil, p.errorf(pos, "cannot read from output parameter %s", name)
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

// isArithmeticOp reports whether the token kind is an arithmetic operator
// (+, -, *, /).
func isArithmeticOp(kind tokenKind) bool {
	return kind == tokPlus || kind == tokMinus || kind == tokStar || kind == tokSlash || kind == tokPercent
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

// arithmeticOpName maps an arithmetic token kind to the stdlib function
// opcode name.
func arithmeticOpName(kind tokenKind) string { return arithOpNames[kind] }

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

// compoundAssignOpName maps a compound assignment token kind to the stdlib
// function opcode name.
func compoundAssignOpName(kind tokenKind) string { return arithOpNames[kind] }

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
		"op": arithmeticOpName(expr.Op),
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
