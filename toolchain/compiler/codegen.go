package compiler

import (
	"fmt"
	"maps"
	"path"
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

// resolveInstructionFrame converts a raw instruction frame with parameter and
// return slot substitutions applied. retVals provides return targets (indexed
// by returnSlot(N)-1), paramMap substitutes string values, kwVars identifies
// keyword param variables to omit when absent, and comment sets the "cmt"
// field. Any of retVals, paramMap, kwVars may be nil.
func resolveInstructionFrame(frame map[string]any, retVals []any, paramMap map[string]any, kwVars map[string]bool, comment string) map[string]any {
	instr := make(map[string]any, len(frame))
	for k, v := range frame {
		nativeKey := k
		if rs, ok := v.(returnSlot); ok {
			idx := int(rs) - 1
			if retVals != nil && idx < len(retVals) {
				instr[nativeKey] = retVals[idx]
			} else {
				instr[nativeKey] = false
			}
			continue
		}
		if br, ok := v.(behaviorRef); ok {
			instr[nativeKey] = int(br)
			continue
		}
		if s, ok := v.(string); ok && k != "op" {
			if paramMap != nil {
				if arg, ok := paramMap[s]; ok {
					if br, ok := arg.(behaviorRef); ok {
						instr[nativeKey] = int(br)
					} else {
						instr[nativeKey] = arg
					}
					continue
				}
			}
			if kwVars != nil && kwVars[s] {
				continue // omit absent keyword param
			}
		}
		instr[nativeKey] = v
	}
	// Unwrap {"num": N} maps to plain integers for metadata fields.
	// Numbered keys (register slots) correctly use {"num": N} maps, but
	// metadata fields like "c" expect plain integers. The "txt" field
	// passes strings which won't match. The "op" field is always a string.
	for k, v := range instr {
		if _, err := strconv.Atoi(k); err != nil {
			if m, ok := v.(map[string]any); ok {
				if num, has := m["num"]; has && len(m) == 1 {
					instr[k] = num
				}
			}
		}
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
		if m, ok := val.(map[string]any); ok {
			if _, hasFr := m["fr"]; hasFr {
				return "inout"
			}
		}
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
		// Check param modifier: argument must be a behavior parameter
		if pd.isParam {
			if intVal, ok := argVal.(int); ok {
				if intVal >= 1 && intVal <= len(syms.params) {
					continue // valid behavior parameter
				}
			}
			return p.errorf(pos, "argument to param parameter %q of %s must be a behavior parameter",
				pd.name, fnName)
		}
		// Check behavior modifier: argument must be a behavior name
		if pd.isBehavior {
			if name, ok := argVal.(string); ok {
				if _, isBhv := p.bhvs[name]; isBhv {
					continue // valid behavior reference
				}
			}
			return p.errorf(pos, "argument to behavior parameter %q of %s must be a behavior reference",
				pd.name, fnName)
		}
	}
	return nil
}

// resolveBehaviorArgs resolves behavior-flagged arguments from behavior names
// to behaviorRef values by compiling dependencies. Modifies args/kwArgs in place.
func (p *parser) resolveBehaviorArgs(fn *fnDef, args []any, kwArgs map[string]any, pos int) error {
	posIdx := 0
	for _, pd := range fn.params {
		if !pd.isBehavior {
			if pd.keyword == "" {
				posIdx++
			}
			continue
		}
		if pd.keyword == "" {
			if posIdx < len(args) {
				if name, ok := args[posIdx].(string); ok {
					sub, err := p.resolveCallSub(name, pos)
					if err != nil {
						return err
					}
					args[posIdx] = behaviorRef(sub)
				}
			}
			posIdx++
		} else if kwArgs != nil {
			if name, ok := kwArgs[pd.keyword].(string); ok {
				sub, err := p.resolveCallSub(name, pos)
				if err != nil {
					return err
				}
				kwArgs[pd.keyword] = behaviorRef(sub)
			}
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
		// Allow `on` event handlers after terminal statements —
		// events are disconnected from the main flow.
		if terminal != nil && tok.kind == tokIdent && tok.val == "on" {
			onStmt, err := p.parseBhvOnEvent(syms, p.docComment)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, onStmt)
			continue
		}
		// Allow `label` after terminal statements — labels are jump
		// targets and must be emitted even if not reachable by fallthrough.
		if terminal != nil && tok.kind == tokIdent && tok.val == "label" {
			terminal = nil
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
			case "keepvars":
				if _, exists := value["keepvars"]; exists {
					return nil, p.errorf(tok.pos, "duplicate @keepvars")
				}
				value["keepvars"] = true
			case "keeparrays":
				if _, exists := value["keeparrays"]; exists {
					return nil, p.errorf(tok.pos, "duplicate @keeparrays")
				}
				value["keeparrays"] = "store"
			default:
				return nil, p.errorf(attr.pos, "unknown attribute @%s", attr.val)
			}
			continue
		}

		if tok.kind == tokLabel {
			hasInstruction = true
			label := tok.val
			if p.loopLabels[label] {
				return nil, p.errorf(tok.pos, "duplicate loop label %q", label)
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, err
			}
			kw, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			pctx := p.bhvParseCtx(syms)
			var stmt Stmt
			switch kw.val {
			case "loop":
				stmt, err = p.parseLoopStmt(pctx, p.docComment, label)
			case "while":
				stmt, err = p.parseWhileStmt(pctx, p.docComment, label)
			case "for":
				stmt, err = p.parseForStmt(pctx, p.docComment, label)
			default:
				return nil, p.errorf(kw.pos, "expected 'loop', 'while', or 'for' after label, got %s", kw.describe())
			}
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, stmt)
			continue
		}

		if tok.kind == tokPercent {
			next, err := p.next()
			if err != nil {
				return nil, err
			}
			if next.kind != tokIdent {
				return nil, p.errorf(tok.pos, "expected identifier after '%%'")
			}
			tok = token{tokIdent, "%" + next.val, tok.pos}
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected statement, got %s", tok.describe())
		}

		hasInstruction = true

		// Parse `on` event handlers at behavior top level
		if tok.val == "on" {
			onStmt, err := p.parseBhvOnEvent(syms, p.docComment)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, onStmt)
			continue
		}

		// Try shared keyword cases
		parsed, handled, err := p.parseBhvOneStmt(tok, syms)
		if err != nil {
			return nil, err
		}
		if handled {
			stmts = append(stmts, parsed...)
			if len(parsed) > 0 {
				if last := stmts[len(stmts)-1]; isTerminalStmt(last) {
					terminal = last
				}
			}
			continue
		}

		// Default case: regular statement
		parsed, err = p.parseBhvDefaultStmt(tok, syms)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, parsed...)
	}

	// Phase 2: Emit frames from AST.
	b.mode = modeUnknown
	if _, err := p.emitBehaviorStmts(stmts, b, syms); err != nil {
		return nil, err
	}

	// Phase 3: Emit deferred events after main flow.
	if len(b.deferredEvents) > 0 {
		if err := p.emitDeferredEvents(b, syms); err != nil {
			return nil, err
		}
	}

	// Emit parameter declarations.
	if len(syms.params) > 0 {
		params := make([]any, len(syms.params))
		pnames := make([]any, len(syms.params))
		hasPinits := false
		for i, pi := range syms.params {
			params[i] = pi.direction == "out" || pi.direction == "inout"
			pnames[i] = pi.name
			if pi.initValue != nil {
				hasPinits = true
			}
		}
		value["parameters"] = params
		value["pnames"] = pnames
		if hasPinits {
			pinits := make([]any, len(syms.params))
			for i, pi := range syms.params {
				if pi.initValue != nil {
					pinits[i] = pi.initValue
				} else {
					pinits[i] = false
				}
			}
			// Trim trailing false entries
			for len(pinits) > 0 && pinits[len(pinits)-1] == false {
				pinits = pinits[:len(pinits)-1]
			}
			value["pinits"] = pinits
		}
	}

	if _, exists := value["name"]; !exists {
		value["name"] = behaviorID
	}

	if err := b.validateNamedLabels(); err != nil {
		return nil, err
	}

	b.eliminateNoopBridges()
	b.finalize(value)

	// Attach compiled dependencies for behavior subroutine calls
	if len(p.dependencies) > 0 {
		deps := make([]any, len(p.dependencies))
		for i, d := range p.dependencies {
			deps[i] = d
		}
		value["dependencies"] = deps
	}

	return &codec.Object{Type: codec.Behavior, Value: value}, nil
}

// emitDeferredEvents emits all deferred event handlers after the main flow.
// It patches the last main-flow frame to prevent fall-through into event area,
// then emits each event setup + handler chain. Each handler's final frame is
// connected to its continuation point: if the event was deferred before the
// end of the main flow, the handler resumes at the frame that was about to be
// emitted at deferral time; otherwise the handler restarts the behavior.
func (p *parser) emitDeferredEvents(b *frameBuilder, syms *symbolTable) error {
	// Ensure no fall-through from main flow into event area.
	// If the last main-flow frame would fall through (no explicit "next"),
	// patch it with "next": false (restart).
	mainEnd := frameRef(b.pos())
	if mainEnd > 0 {
		lastFrame := b.get(int(mainEnd) - 1)
		if _, hasNext := lastFrame["next"]; !hasNext {
			lastFrame["next"] = false
		}
	}

	for _, de := range b.deferredEvents {
		evt := de.stmt
		fromFnBody := de.paramMap != nil

		// Determine handler continuation: if the event was deferred before
		// the end of the main flow, resume there; otherwise restart.
		var continuation any // frameRef or false
		if de.frameAtDeferral < mainEnd {
			continuation = de.frameAtDeferral
		} else {
			continuation = false
		}

		switch evt.Kind {
		case "parameter":
			// Resolve pnum from param name
			var pnum int
			if fromFnBody {
				// Function body: resolve param name through paramMap
				mapped, ok := de.paramMap[evt.Param]
				if !ok {
					return fmt.Errorf("unknown parameter %q in event handler", evt.Param)
				}
				idx, ok := mapped.(int)
				if !ok {
					return fmt.Errorf("parameter %q in event handler did not resolve to a behavior parameter", evt.Param)
				}
				pnum = idx
			} else {
				// Behavior-level: resolve from syms.paramMap
				idx, ok := syms.paramMap[evt.Param]
				if !ok {
					return fmt.Errorf("unknown parameter %q in event handler", evt.Param)
				}
				pnum = idx
			}

			handlerStart := frameRef(b.pos() + 1)
			f := map[string]any{
				"op":   "event_parameter",
				"pnum": pnum,
				"next": handlerStart,
			}
			setComment(f, evt.Comment)
			b.emit(f)

			// Emit handler body
			if fromFnBody {
				if err := p.emitFnBody(evt.Body, b, de.paramMap, syms.usedVars, evt.Comment, 0); err != nil {
					return err
				}
			} else {
				if _, err := p.emitBehaviorStmts(evt.Body, b, syms); err != nil {
					return err
				}
			}

			p.patchHandlerEnd(b, continuation)

		case "radio":
			// Evaluate band expression at compile time
			var env map[string]any
			if fromFnBody {
				env = de.paramMap
			}
			bandVal, ok := p.tryEvalExpr(evt.Band, env)
			if !ok {
				return fmt.Errorf("radio band must be a compile-time constant")
			}

			handlerStart := frameRef(b.pos() + 1)
			f := map[string]any{
				"op":   "event_radio",
				"band": bandVal,
				"next": handlerStart,
			}
			// Signal output register
			if evt.Signal != "" {
				if fromFnBody {
					sigReg := allocUniqueVar(evt.Signal, syms.usedVars)
					de.paramMap[evt.Signal] = sigReg
					f["0"] = sigReg
				} else {
					syms.declareVar(evt.Signal, false)
					f["0"] = syms.resolveReg(evt.Signal)
				}
			}
			setComment(f, evt.Comment)
			b.emit(f)

			// Emit handler body
			if fromFnBody {
				if err := p.emitFnBody(evt.Body, b, de.paramMap, syms.usedVars, evt.Comment, 0); err != nil {
					return err
				}
			} else {
				if _, err := p.emitBehaviorStmts(evt.Body, b, syms); err != nil {
					return err
				}
			}

			p.patchHandlerEnd(b, continuation)
		}
	}
	return nil
}

// patchHandlerEnd sets "next" on the last emitted frame if it doesn't already
// have one. continuation is either a frameRef (resume at that frame) or false
// (restart the behavior).
func (p *parser) patchHandlerEnd(b *frameBuilder, continuation any) {
	endPos := b.pos()
	if endPos > 0 {
		lastHandler := b.get(endPos - 1)
		if _, hasNext := lastHandler["next"]; !hasNext {
			lastHandler["next"] = continuation
		}
	}
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
		return vi.regName, nil
	}
	if strings.HasPrefix(name, "%") {
		return map[string]any{"fr": name[1:]}, nil
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
		"0":    false,
		"1":    target,
		"next": frameRef(afterPos),
	})

	// True frame
	b.emit(map[string]any{
		"op": "set_reg",
		"0":  map[string]any{"num": 1},
		"1":  target,
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
		"0":    false,
		"1":    target,
		"next": frameRef(afterPos),
	})

	// True frame
	b.emit(map[string]any{
		"op": "set_reg",
		"0":  map[string]any{"num": 1},
		"1":  target,
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
		"0":    false,
		"1":    target,
		"next": frameRef(afterPos),
	})

	// True frame
	b.emit(map[string]any{
		"op": "set_reg",
		"0":  map[string]any{"num": 1},
		"1":  target,
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
		"0":    false,
		"1":    target,
		"next": frameRef(afterPos),
	})
	b.emit(map[string]any{
		"op": "set_reg",
		"0":  map[string]any{"num": 1},
		"1":  target,
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
		"0":  lhs,
		"1":  rhs,
		"2":  target,
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
// Syntax: @param <direction> <name> <"display name" | { localized block }> [= <default>]
// Direction is one of: in, out, inout.
// Default values: = <number>, = <entity_id>, or = <entity_id> <number>.
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

	// Parse optional default value: = <value>
	var initValue any
	peek, err = p.next()
	if err != nil {
		return err
	}
	if peek.kind == tokEquals {
		if dirTok.val == "out" {
			return p.errorf(peek.pos, "output parameter %q cannot have a default value", dollarName)
		}
		initValue, err = p.parseParamInitValue()
		if err != nil {
			return err
		}
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
		initValue: initValue,
	})
	syms.paramMap[dollarName] = idx
	return nil
}

// parseParamInitValue parses a parameter default value after '='.
// Returns a register value object: {"num": N}, {"id": "..."}, or {"id": "...", "num": N}.
func (p *parser) parseParamInitValue() (any, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}

	switch tok.kind {
	case tokNumber:
		n, err := strconv.Atoi(tok.val)
		if err != nil {
			return nil, p.errorf(tok.pos, "invalid number in default value: %s", tok.val)
		}
		return map[string]any{"num": n}, nil

	case tokMinus:
		// Negative number
		numTok, err := p.expect(tokNumber)
		if err != nil {
			return nil, p.errorf(tok.pos, "expected number after '-' in default value")
		}
		n, err := strconv.Atoi(numTok.val)
		if err != nil {
			return nil, p.errorf(numTok.pos, "invalid number in default value: %s", numTok.val)
		}
		return map[string]any{"num": -n}, nil

	case tokIdent:
		// Entity ID, possibly followed by a number
		id := tok.val
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokNumber {
			n, err := strconv.Atoi(peek.val)
			if err != nil {
				return nil, p.errorf(peek.pos, "invalid number in default value: %s", peek.val)
			}
			return map[string]any{"id": id, "num": n}, nil
		}
		if peek.kind == tokMinus {
			// Entity ID followed by negative number
			numTok, err := p.expect(tokNumber)
			if err != nil {
				return nil, p.errorf(peek.pos, "expected number after '-' in default value")
			}
			n, err := strconv.Atoi(numTok.val)
			if err != nil {
				return nil, p.errorf(numTok.pos, "invalid number in default value: %s", numTok.val)
			}
			return map[string]any{"id": id, "num": -n}, nil
		}
		p.unget(peek)
		return map[string]any{"id": id}, nil

	default:
		return nil, p.errorf(tok.pos, "expected number or identifier in default value, got %s", tok.describe())
	}
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
func (p *parser) checkVarName(name string, pos int) error {
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

// hasContinuePlaceholder reports whether any @continue placeholder exists
// in b.frames[from:].
func hasContinuePlaceholder(b *frameBuilder, from int) bool {
	for j := from; j < len(b.frames); j++ {
		if op, _ := b.frames[j]["op"].(string); op == "@continue" {
			return true
		}
	}
	return false
}

// patchContinuePlaceholders replaces @continue placeholder frames in
// b.frames[from:] with a noop jump to nextVal. nextVal is frameRef for
// loop/while/counted loops, or false for iterator-backed for loops
// (re-dispatch).
func patchContinuePlaceholders(b *frameBuilder, from int, nextVal any) {
	for j := from; j < len(b.frames); j++ {
		if op, _ := b.frames[j]["op"].(string); op == "@continue" {
			b.frames[j] = map[string]any{
				"op":   "set_reg",
				"0":    false,
				"1":    false,
				"next": nextVal,
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
					"0":    false,
					"1":    false,
					"next": target,
				}
			}
		}
	}
}

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

// emitJumpFallthroughError emits notify + exit frames for jump fallthrough
// protection. The notify displays an error message and the expected label
// value. Returns the frameRef of the notify frame.
func emitJumpFallthroughError(b *frameBuilder, labelVal any) frameRef {
	ref := frameRef(b.pos())
	b.emit(map[string]any{
		"op":  "notify",
		"txt": "jump: no matching label",
		"0":   labelVal,
	})
	b.emit(map[string]any{"op": "exit"})
	return ref
}

// patchJumpBreakPlaceholders replaces @jumpbreak placeholder frames in
// b.frames[from:] whose label matches with jump instructions targeting a
// compiler-generated label, then emits a label instruction at the current
// position. Used for cross-exec-block-boundary break: the break emits
// jump (escaping the detached block at the VM level) and the label marks
// the continuation point after the target loop.
func patchJumpBreakPlaceholders(b *frameBuilder, from int, label string) {
	// Scan for matching @jumpbreak placeholders
	found := false
	for j := from; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@jumpbreak" {
			if fLabel, _ := f["label"].(string); fLabel == label {
				found = true
				break
			}
		}
	}
	if !found {
		return
	}

	// Allocate a single compiler label for all matching breaks
	labelNum := b.allocLabels(1)
	target := compilerLabel(labelNum)

	var patchedFrames []map[string]any
	for j := from; j < len(b.frames); j++ {
		f := b.frames[j]
		if op, _ := f["op"].(string); op == "@jumpbreak" {
			if fLabel, _ := f["label"].(string); fLabel == label {
				newFrame := map[string]any{
					"op": "jump",
					"0":  target,
				}
				b.frames[j] = newFrame
				patchedFrames = append(patchedFrames, newFrame)
			}
		}
	}

	// Emit the label at the current position (after the loop)
	labelPos := b.emit(map[string]any{
		"op": "label",
		"0":  target,
	})

	// Emit fallthrough error handler
	errorRef := emitJumpFallthroughError(b, target)

	// Set label's "next" to skip over the error handler
	b.frames[labelPos]["next"] = frameRef(b.pos())

	// Set "next" on all patched jumps to the error handler
	for _, f := range patchedFrames {
		f["next"] = errorRef
	}
}

// emitLoopBackEdge emits the back-edge jump for while and infinite loops.
// If the last body frame is @break or @continue, a noop jump is emitted
// (the placeholder frame will be patched separately). If the last frame has
// no "next", it gets one. Otherwise a noop jump is emitted to avoid
// clobbering inner control flow.
// The bodyStart parameter guards against empty bodies (no frames emitted).
func emitLoopBackEdge(b *frameBuilder, bodyStart int, target frameRef) {
	if b.pos() <= bodyStart {
		return
	}
	lastFrame := b.get(b.pos() - 1)
	if op, _ := lastFrame["op"].(string); op == "@break" || op == "@continue" {
		b.emit(map[string]any{"op": "set_reg", "0": false, "1": false, "next": target})
	} else if _, hasNext := lastFrame["next"]; !hasNext {
		lastFrame["next"] = target
	} else {
		b.emit(map[string]any{"op": "set_reg", "0": false, "1": false, "next": target})
	}
}

// patchLastBodyNext sets "next" on the last body frame to point to nextFrame
// (typically the INCR frame in counted/for loops). Skips if the body is
// empty, or if the last frame is @break/@continue (will be patched
// separately) or already has a "next". bodyStart is the frame index where
// the body began.
func patchLastBodyNext(b *frameBuilder, bodyStart int, nextFrame int) {
	if nextFrame-1 < bodyStart {
		return
	}
	lastBodyFrame := b.get(nextFrame - 1)
	if op, _ := lastBodyFrame["op"].(string); op != "@break" && op != "@continue" {
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
		ctx.b.emit(map[string]any{"op": "set_reg", "0": false, "1": retVals[i]})
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

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())
	patchContinuePlaceholders(ctx.b, origLen, frameRef(loopStart))
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
		"1":  map[string]any{"num": 0},
		"2":  counterVar,
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
		"0":    counterVar,
		"1":    map[string]any{"num": 1},
		"2":    counterVar,
		"next": frameRef(checkFrame),
	})

	patchLastBodyNext(ctx.b, origLen, incrFrame)

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())
	check := ctx.b.get(checkFrame)
	check[checkLarger] = afterLoop
	check["next"] = afterLoop

	patchContinuePlaceholders(ctx.b, origLen, frameRef(incrFrame))
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

	patchJumpBreakPlaceholders(ctx.b, origLen, s.Label)
	afterLoop := frameRef(ctx.b.pos())
	patchFalseBranches(ctx.b, checkStart, checkCount, falsePlaceholder, afterLoop)

	patchContinuePlaceholders(ctx.b, origLen, frameRef(loopStart))
	patchBreakPlaceholders(ctx.b, origLen, s.Label, afterLoop)

	return nil
}

// consumeOptionalOpenParen peeks for '(' and, if found, reads the next token.
// Returns (true, nextToken, nil) when parens are present, or (false, nextToken, nil)
// when absent. The caller must call p.expect(tokRParen) after parsing args if paren is true.
func (p *parser) consumeOptionalOpenParen() (bool, token, error) {
	tok, err := p.next()
	if err != nil {
		return false, tok, err
	}
	if tok.kind == tokLParen {
		next, err := p.next()
		return true, next, err
	}
	return false, tok, nil
}

// consumeOptionalEmptyParens peeks for '()' after a zero-arg keyword.
// Consumes both tokens if found, otherwise ungets the peeked token.
func (p *parser) consumeOptionalEmptyParens() error {
	peek, err := p.next()
	if err != nil {
		return err
	}
	if peek.kind != tokLParen {
		p.unget(peek)
		return nil
	}
	if _, err := p.expect(tokRParen); err != nil {
		return err
	}
	return nil
}

// parseLabelStmt parses `label 'name` or `label <expr>`, with optional parens.
func (p *parser) parseLabelStmt(ctx *parseContext, comment string) (*LabelStmt, error) {
	paren, peek, err := p.consumeOptionalOpenParen()
	if err != nil {
		return nil, err
	}
	var stmt *LabelStmt
	if peek.kind == tokLabel {
		stmt = &LabelStmt{Name: peek.val, Comment: comment}
	} else if peek.kind == tokString {
		return nil, p.errorf(peek.pos, "string literals not allowed; use 'name for named labels or a numeric/variable expression")
	} else {
		expr, err := p.parseSimpleExpr(peek, ctx.resolve, "label expression after 'label'")
		if err != nil {
			return nil, err
		}
		stmt = &LabelStmt{Label: expr, Comment: comment}
	}
	if paren {
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
	}
	return stmt, nil
}

// parseJumpStmt parses `jump 'name` or `jump <expr>`, with optional parens.
func (p *parser) parseJumpStmt(ctx *parseContext, comment string) (*JumpStmt, error) {
	paren, peek, err := p.consumeOptionalOpenParen()
	if err != nil {
		return nil, err
	}
	var stmt *JumpStmt
	if peek.kind == tokLabel {
		stmt = &JumpStmt{Name: peek.val, Comment: comment}
	} else if peek.kind == tokString {
		return nil, p.errorf(peek.pos, "string literals not allowed; use 'name for named labels or a numeric/variable expression")
	} else {
		expr, err := p.parseSimpleExpr(peek, ctx.resolve, "label expression after 'jump'")
		if err != nil {
			return nil, err
		}
		stmt = &JumpStmt{Label: expr, Comment: comment}
	}
	if paren {
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
	}
	return stmt, nil
}

// emitLabelStmt emits a label statement (not terminal — execution continues past it).
func (p *parser) emitLabelStmt(s *LabelStmt, ctx *emitContext, comment string) error {
	var labelVal any
	if s.Name != "" {
		labelVal = ctx.b.resolveNamedLabel(s.Name)
		if err := ctx.b.markLabelEmitted(s.Name); err != nil {
			return err
		}
	} else {
		var err error
		labelVal, err = ctx.exprGetValue(s.Label, "")
		if err != nil {
			return err
		}
	}
	f := map[string]any{"op": "label", "0": labelVal}
	setComment(f, comment)
	ctx.b.emit(f)
	return nil
}

// emitJumpStmt emits a jump statement (terminal, no successor).
func (p *parser) emitJumpStmt(s *JumpStmt, ctx *emitContext, comment string) error {
	var labelVal any
	if s.Name != "" {
		labelVal = ctx.b.resolveNamedLabel(s.Name)
		ctx.b.markJumpEmitted(s.Name)
	} else {
		var err error
		labelVal, err = ctx.exprGetValue(s.Label, "")
		if err != nil {
			return err
		}
	}
	f := map[string]any{"op": "jump", "0": labelVal}
	setComment(f, comment)
	ctx.b.emit(f)
	// Named labels are compiler-validated; add fallthrough error in case
	// the jump doesn't match at runtime (shouldn't happen, but defensive).
	if s.Name != "" {
		f["next"] = emitJumpFallthroughError(ctx.b, labelVal)
	}
	return nil
}

// emitWaitStmt emits a wait statement.
func (p *parser) emitWaitStmt(s *WaitStmt, ctx *emitContext, comment string) error {
	ticksVal, err := ctx.exprGetValue(s.Ticks, "")
	if err != nil {
		return err
	}

	if s.Tail == nil {
		f := map[string]any{"op": "wait", "0": ticksVal}
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
			"0":  ticksVal,
			"1":  tmp,
		})
		ticksVar = tmp
	}

	waitFrame := map[string]any{"op": "wait", "0": ticksVar}
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
	origLen := len(ctx.b.frames)
	savedMode := emitModeEntry(ctx.b, e.Unlock, mbeComment)
	ctx.pushScope()

	// Set breakRetVals so break-with-value inside the body can write to retVals
	savedBreakRetVals := p.breakRetVals
	p.breakRetVals = retVals
	defer func() { p.breakRetVals = savedBreakRetVals }()

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
	modeExitTarget := frameRef(ctx.b.pos())
	emitModeExit(ctx.b, savedMode)
	patchBreakPlaceholders(ctx.b, origLen, "", modeExitTarget)
	return nil
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
		return p.errorf(0, "unknown iterator %q", s.IterName)
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
			frameIdx int // last frame of this yield's assignments
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
		"op":          "check_number",
		checkValue:    iterVar,
		checkTarget:   stopVal,
		"next":        bodyRef, // equal → body (inclusive range)
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
				"0":    false,
				"1":    false,
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

// ---------------------------------------------------------------------------
// Unified statement parsers (via parseContext)
// ---------------------------------------------------------------------------

// parseIfStmt parses an if/else-if/else statement.
func (p *parser) parseIfStmt(ctx *parseContext, comment string) (*IfStmt, error) {
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	body, err := ctx.parseBody(false)
	if err != nil {
		return nil, err
	}

	stmt := &IfStmt{Cond: cond, Body: body, Comment: comment}

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
			if err := p.parseElseIfChain(ctx, stmt); err != nil {
				return nil, err
			}
		} else {
			p.unget(peek)
			if _, err := p.expect(tokLBrace); err != nil {
				return nil, err
			}
			elseBody, err := ctx.parseBody(false)
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

// parseElseIfChain parses the else-if / else chain and attaches them
// to the given IfStmt.
func (p *parser) parseElseIfChain(ctx *parseContext, stmt *IfStmt) error {
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return err
	}
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	body, err := ctx.parseBody(false)
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
			return p.parseElseIfChain(ctx, stmt)
		}
		// Plain else
		p.unget(peek)
		if _, err := p.expect(tokLBrace); err != nil {
			return err
		}
		elseBody, err := ctx.parseBody(false)
		if err != nil {
			return err
		}
		stmt.Else = elseBody
	} else {
		p.unget(tok)
	}

	return nil
}

// parseWhileStmt parses a while loop with full boolean expression support.
func (p *parser) parseWhileStmt(ctx *parseContext, comment string, label ...string) (*WhileStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	p.enterLoop(lbl)
	body, err := ctx.parseBody(false)
	p.exitLoop(lbl)
	if err != nil {
		return nil, err
	}

	return &WhileStmt{Label: lbl, Cond: cond, Body: body, Comment: comment}, nil
}

// parseLoopStmt parses a loop { ... } or loop N { ... } block.
func (p *parser) parseLoopStmt(ctx *parseContext, comment string, label ...string) (*LoopStmt, error) {
	lbl := ""
	if len(label) > 0 {
		lbl = label[0]
	}

	// Peek for count expression
	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	if peek.kind == tokLBrace {
		// Infinite loop: loop { ... }
		p.enterLoop(lbl)
		body, err := ctx.parseBody(false)
		p.exitLoop(lbl)
		if err != nil {
			return nil, err
		}
		return &LoopStmt{Label: lbl, Body: body, Comment: comment}, nil
	}

	// Counted loop: parse count expression
	count, err := p.parseSimpleExpr(peek, ctx.resolve, "'{' or count expression after 'loop'")
	if err != nil {
		return nil, err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}
	p.enterLoop(lbl)
	body, err := ctx.parseBody(false)
	p.exitLoop(lbl)
	if err != nil {
		return nil, err
	}
	return &LoopStmt{Label: lbl, Count: count, Body: body, Comment: comment}, nil
}

// parseWaitStmt parses a wait statement: `wait <ticks>` or `wait <ticks> { body; cond }`.
func (p *parser) parseWaitStmt(ctx *parseContext, comment string) (*WaitStmt, error) {
	// Parse ticks expression
	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	ticks, err := p.parseSimpleExpr(peek, ctx.resolve, "ticks expression after 'wait'")
	if err != nil {
		return nil, err
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
	stmts, err := ctx.parseBody(true)
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

// parseAssertStmt parses an assert statement. Two forms:
//
//	Expression form: assert <cond>, "message", value: <expr>
//	Block form:      assert "message", value: <expr> { ...; <cond> }
//
// With parens: assert(<cond>, "message", value: <expr>) or
// assert("message", value: <expr>) { ...; <cond> }
func (p *parser) parseAssertStmt(ctx *parseContext, comment string, assertPos int) (*AssertStmt, error) {
	// Record source file and line for the notify text.
	file := p.sourcePath
	if file == "" {
		file = "<input>"
	}
	line, _ := p.posToLineCol(assertPos)

	stmt := &AssertStmt{
		File:    file,
		Line:    line,
		Comment: comment,
	}

	peek, err := p.next()
	if err != nil {
		return nil, err
	}

	if peek.kind == tokLParen {
		// Parenthesized form: parse arguments inside parens, then check for block.
		return p.parseAssertParens(ctx, stmt, peek.pos)
	}

	// No-parens disambiguation:
	// 1. If next is `{` → block form, no pre-block args.
	if peek.kind == tokLBrace {
		return p.parseAssertBlock(ctx, stmt, peek.pos)
	}

	// 2. If next is a string literal → message. Parse optional `, value: expr`. Require `{`.
	if peek.kind == tokString {
		stmt.Message = peek.val
		if err := p.parseAssertKwArgs(ctx, stmt); err != nil {
			return nil, err
		}
		brace, err := p.next()
		if err != nil {
			return nil, err
		}
		if brace.kind != tokLBrace {
			return nil, p.errorf(brace.pos, "assert with message but no condition — add a condition expression or use block form")
		}
		return p.parseAssertBlock(ctx, stmt, brace.pos)
	}

	// 3. If next is identifier `value` followed by `:` → keyword arg, no message.
	if peek.kind == tokIdent && peek.val == "value" {
		saved := p.save()
		colon, err := p.next()
		if err != nil {
			return nil, err
		}
		if colon.kind == tokColon {
			valExpr, err := p.parseBoolExpr(ctx.resolve)
			if err != nil {
				return nil, err
			}
			if truthy, ok := valExpr.(*TruthyExpr); ok {
				valExpr = truthy.Value
			}
			stmt.Value = valExpr
			brace, err := p.next()
			if err != nil {
				return nil, err
			}
			if brace.kind != tokLBrace {
				return nil, p.errorf(brace.pos, "assert with value but no condition — add a condition expression or use block form")
			}
			return p.parseAssertBlock(ctx, stmt, brace.pos)
		}
		// Not `value:`, restore and fall through to expression form.
		p.restore(saved)
	}

	// 4. Otherwise → expression form. Parse condition expr.
	p.unget(peek)
	condStart := peek.pos
	cond, err := p.parseBoolExpr(ctx.resolve)
	if err != nil {
		return nil, err
	}
	// Capture source text of condition: from the start of the first condition
	// token to the position of the next peeked token.
	condEnd := p.pos
	if p.ungot != nil {
		condEnd = p.ungot.pos
	}
	stmt.ConditionText = strings.TrimSpace(p.src[condStart:condEnd])
	stmt.Condition = cond

	// Parse optional trailing `, "message"` and/or `, value: <expr>`.
	if err := p.parseAssertTrailingArgs(ctx, stmt); err != nil {
		return nil, err
	}

	return stmt, nil
}

// parseAssertBlock parses the block body of an assert statement.
// The opening `{` has already been consumed.
func (p *parser) parseAssertBlock(ctx *parseContext, stmt *AssertStmt, bracePos int) (*AssertStmt, error) {
	stmts, err := ctx.parseBody(true)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(bracePos, "empty assert block")
	}
	last := stmts[len(stmts)-1]
	tail, ok := last.(*exprTailStmt)
	if !ok {
		return nil, p.errorf(bracePos, "last item in assert block must be a condition expression")
	}
	stmt.Body = stmts[:len(stmts)-1]

	// Capture condition text for the tail expression.
	// For block form, we use the expression node's string representation.
	stmt.Condition = tail.Expr
	// ConditionText for block form: we don't have byte offsets for the tail
	// inside the block, so use the message if available, otherwise a generic text.
	if stmt.ConditionText == "" {
		stmt.ConditionText = "assertion"
	}

	return stmt, nil
}

// parseAssertKwArgs parses optional `, value: <expr>` after a message string.
func (p *parser) parseAssertKwArgs(ctx *parseContext, stmt *AssertStmt) error {
	peek, err := p.next()
	if err != nil {
		return err
	}
	if peek.kind != tokComma {
		p.unget(peek)
		return nil
	}
	// Expect `value:`
	kw, err := p.next()
	if err != nil {
		return err
	}
	if kw.kind != tokIdent || kw.val != "value" {
		return p.errorf(kw.pos, "expected 'value' keyword argument, got %s", kw.describe())
	}
	if _, err := p.expect(tokColon); err != nil {
		return err
	}
	valExpr, err := p.parseBoolExpr(ctx.resolve)
	if err != nil {
		return err
	}
	if truthy, ok := valExpr.(*TruthyExpr); ok {
		valExpr = truthy.Value
	}
	stmt.Value = valExpr
	return nil
}

// parseAssertTrailingArgs parses optional `, "message"` and/or `, value: <expr>`
// after the condition in expression form.
func (p *parser) parseAssertTrailingArgs(ctx *parseContext, stmt *AssertStmt) error {
	for {
		peek, err := p.next()
		if err != nil {
			return err
		}
		if peek.kind != tokComma {
			p.unget(peek)
			return nil
		}

		next, err := p.next()
		if err != nil {
			return err
		}

		// String literal → message
		if next.kind == tokString {
			stmt.Message = next.val
			continue
		}

		// `value:` → keyword argument
		if next.kind == tokIdent && next.val == "value" {
			colon, err := p.next()
			if err != nil {
				return err
			}
			if colon.kind != tokColon {
				return p.errorf(colon.pos, "expected ':' after 'value'")
			}
			valExpr, err := p.parseBoolExpr(ctx.resolve)
			if err != nil {
				return err
			}
			if truthy, ok := valExpr.(*TruthyExpr); ok {
				valExpr = truthy.Value
			}
			stmt.Value = valExpr
			continue
		}

		return p.errorf(next.pos, "unexpected %s in assert arguments — expected string message or 'value:' keyword", next.describe())
	}
}

// parseAssertParens parses a parenthesized assert statement.
// The opening `(` has already been consumed.
func (p *parser) parseAssertParens(ctx *parseContext, stmt *AssertStmt, lparenPos int) (*AssertStmt, error) {
	// Parse arguments inside parens: positional args and optional value: keyword.
	var positionalArgs []token // track kind/val for reinterpretation
	var positionalExprs []Expr
	var hasValue bool

	for {
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokRParen {
			break
		}
		if len(positionalExprs) > 0 || hasValue {
			// Expect comma between args
			if peek.kind != tokComma {
				return nil, p.errorf(peek.pos, "expected ',' or ')' in assert arguments")
			}
			peek, err = p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokRParen {
				break
			}
		}

		// Check for `value:` keyword arg
		if peek.kind == tokIdent && peek.val == "value" {
			saved := p.save()
			colon, err := p.next()
			if err != nil {
				return nil, err
			}
			if colon.kind == tokColon {
				valExpr, err := p.parseBoolExpr(ctx.resolve)
				if err != nil {
					return nil, err
				}
				if truthy, ok := valExpr.(*TruthyExpr); ok {
					valExpr = truthy.Value
				}
				stmt.Value = valExpr
				hasValue = true
				continue
			}
			p.restore(saved)
		}

		// Parse as expression (could be string literal or condition)
		p.unget(peek)
		condStart := p.pos
		if p.ungot != nil {
			condStart = p.ungot.pos
		}
		expr, err := p.parseBoolExpr(ctx.resolve)
		if err != nil {
			return nil, err
		}
		condEnd := p.pos
		if p.ungot != nil {
			condEnd = p.ungot.pos
		}

		positionalArgs = append(positionalArgs, peek)
		positionalExprs = append(positionalExprs, expr)
		// Store condition text for first non-string positional
		if peek.kind != tokString && stmt.ConditionText == "" {
			stmt.ConditionText = strings.TrimSpace(p.src[condStart:condEnd])
		}
	}

	// Check for block form after `)`
	blockPeek, err := p.next()
	if err != nil {
		return nil, err
	}

	if blockPeek.kind == tokLBrace {
		// Block form: positional args are message (strings only)
		for i, arg := range positionalArgs {
			if arg.kind == tokString {
				stmt.Message = arg.val
			} else {
				_ = positionalExprs[i] // suppress unused
				return nil, p.errorf(arg.pos, "assert block form only accepts a string message as positional argument, got %s", arg.describe())
			}
		}
		return p.parseAssertBlock(ctx, stmt, blockPeek.pos)
	}

	// Expression form: first positional = condition (error if string literal)
	p.unget(blockPeek)

	if len(positionalExprs) == 0 {
		return nil, p.errorf(lparenPos, "assert requires a condition expression")
	}

	if positionalArgs[0].kind == tokString {
		return nil, p.errorf(positionalArgs[0].pos, "string literal cannot be used as assert condition")
	}

	stmt.Condition = positionalExprs[0]

	// Second positional string = message
	for i := 1; i < len(positionalExprs); i++ {
		if positionalArgs[i].kind == tokString {
			stmt.Message = positionalArgs[i].val
		} else {
			return nil, p.errorf(positionalArgs[i].pos, "unexpected positional argument in assert — only condition and message string are allowed")
		}
	}

	return stmt, nil
}

// emitAssertStmt emits an assert statement. The condition is checked with a
// boolean check frame. The true branch (condition met) falls through to
// continuation. The false branch (assertion failed) emits a notify + exit pair.
func (p *parser) emitAssertStmt(s *AssertStmt, ctx *emitContext, comment string) error {
	// Step 1: If value expr present, resolve it first (may emit frames).
	var val any
	if s.Value != nil {
		var err error
		val, err = ctx.exprGetValue(s.Value, "")
		if err != nil {
			return err
		}
	}

	// Step 2: If block form, emit the body statements first.
	if s.Body != nil {
		ctx.pushScope()
		if err := ctx.emitBody(s.Body); err != nil {
			ctx.popScope()
			return err
		}
		ctx.popScope()
	}

	// Step 3: Resolve the boolean condition.
	resolved, err := ctx.resolveBool(s.Condition)
	if err != nil {
		return err
	}

	// Step 4: Compute frame positions.
	checkStart := ctx.b.pos()
	checkCount := resolved.frameCount()
	// Error handler is always exactly 2 frames (notify + exit)
	// since value was pre-resolved above.
	trueBranch := frameRef(checkStart + checkCount + 2) // skip notify+exit
	falseBranch := frameRef(checkStart + checkCount)    // fall into notify

	// Step 5: Emit check frames. For assert, TRUE means condition holds
	// (skip error handler), FALSE means assertion failed (fall into notify).
	if resolved.isLeaf() {
		p.emitBoolCheckFrame(resolved.term, trueBranch, falseBranch, ctx.b, comment)
	} else {
		p.emitResolvedBoolFrames(resolved, trueBranch, falseBranch, ctx.b, comment)
	}
	stripFallThrough(ctx.b, checkStart, checkCount)

	// Step 6: Build notify text and emit notify frame.
	message := s.Message
	if message == "" {
		message = "Assertion failed: " + s.ConditionText
	}
	notifyText := s.File + ":" + strconv.Itoa(s.Line) + " " + message
	notifyFrame := map[string]any{
		"op":  "notify",
		"txt": notifyText,
	}
	if val != nil {
		notifyFrame["0"] = val
	}
	ctx.b.emit(notifyFrame)

	// Step 7: Emit exit frame.
	ctx.b.emit(map[string]any{"op": "exit"})

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

	return &ForStmt{Label: lbl, IterVars: []string{iterTok.val}, Range: rangeExpr, Body: body, Comment: comment}, nil
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

// parseModeBlockExpr parses a locked/unlocked block used as an expression.
func (p *parser) parseModeBlockExpr(unlock bool, ctx *parseContext, comment string) (*ModeBlockExpr, error) {
	lbrace, err := p.expect(tokLBrace)
	if err != nil {
		return nil, err
	}
	p.modeBlockDepth++
	stmts, err := ctx.parseBody(true)
	p.modeBlockDepth--
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, p.errorf(lbrace.pos, "empty mode block expression")
	}
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

// parseIfExprBranch parses a brace-delimited expression block (statements +
// tail expression) for an if-expression branch.
func (p *parser) parseIfExprBranch(ctx *parseContext) ([]Stmt, Expr, error) {
	lbrace, err := p.expect(tokLBrace)
	if err != nil {
		return nil, nil, err
	}
	stmts, err := ctx.parseBody(true)
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

// parseIfExpr parses an if-expression after the 'if' keyword has been
// consumed. Uses the full boolean expression parser for conditions.
func (p *parser) parseIfExpr(ctx *parseContext, comment string) (*IfExpr, error) {
	cond, err := p.parseBoolPrimary(ctx.resolve)
	if err != nil {
		return nil, err
	}
	cond, err = p.parseBoolChain(cond, ctx.resolve)
	if err != nil {
		return nil, err
	}

	body, tail, err := p.parseIfExprBranch(ctx)
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
			p.unget(tok)
			return expr, nil
		}
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent && peek.val == "if" {
			eiCond, err := p.parseBoolPrimary(ctx.resolve)
			if err != nil {
				return nil, err
			}
			eiCond, err = p.parseBoolChain(eiCond, ctx.resolve)
			if err != nil {
				return nil, err
			}
			eiBody, eiTail, err := p.parseIfExprBranch(ctx)
			if err != nil {
				return nil, err
			}
			expr.ElseIfs = append(expr.ElseIfs, ElseIfExprClause{
				Cond: eiCond,
				Body: eiBody,
				Tail: eiTail,
			})
		} else {
			p.unget(peek)
			elsBody, elsTail, err := p.parseIfExprBranch(ctx)
			if err != nil {
				return nil, err
			}
			expr.ElsBody = elsBody
			expr.ElsTail = elsTail
			return expr, nil
		}
	}
}

// --- Behavior subroutine calls (call keyword) ---

// resolveCallBehaviorName resolves a behavior name for a `call` statement.
// Handles direct lookup and namespace-qualified names (ns.bhv).
func (p *parser) resolveCallBehaviorName(tok token) (string, *bhvDef, error) {
	// Direct lookup
	if b := p.bhvs[tok.val]; b != nil {
		return tok.val, b, nil
	}
	// Namespace lookup
	ns := p.namespaceSets[tok.val]
	if ns != nil {
		peek, err := p.next()
		if err != nil {
			return "", nil, err
		}
		if peek.kind == tokDot {
			memberTok, err := p.expect(tokIdent)
			if err != nil {
				return "", nil, err
			}
			qualName := tok.val + "." + memberTok.val
			if ns.isPrivate(memberTok.val) {
				return "", nil, p.errorf(memberTok.pos, "cannot access private symbol %q in namespace %q", memberTok.val, tok.val)
			}
			if b, ok := ns.bhvs[memberTok.val]; ok {
				p.bhvs[qualName] = b
				return qualName, b, nil
			}
			return "", nil, p.errorf(memberTok.pos, "%q is not a behavior in namespace %q", memberTok.val, tok.val)
		}
		p.unget(peek)
	}
	return "", nil, p.errorf(tok.pos, "unknown behavior %q", tok.val)
}

// parseCallBehaviorArgs parses keyword arguments for a `call` statement.
// Syntax: name: expr, name: out var, name: inout var
// Returns a map from parameter name to CallBhvArg.
func (p *parser) parseCallBehaviorArgs(bhv *bhvDef, resolve operandResolver, pos int) (map[string]*CallBhvArg, error) {
	args := map[string]*CallBhvArg{}

	// Build param lookup for validation
	paramByName := map[string]*paramDef{}
	for i := range bhv.params {
		paramByName[bhv.params[i].keyword] = &bhv.params[i]
	}

	// Check for parens
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	hasParen := peek.kind == tokLParen
	if !hasParen {
		p.unget(peek)
		// Check if there's anything to parse (next token is an identifier followed by colon)
		// Use save/restore since we need 2-token lookahead
		saved := p.save()
		peek, err = p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind != tokIdent {
			p.restore(saved)
			return args, nil // no args
		}
		peek2, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek2.kind != tokColon {
			p.restore(saved)
			return args, nil
		}
		// Restore and parse as keyword args
		p.restore(saved)
	}

	// Parse keyword arguments
	first := true
	for {
		if hasParen {
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokRParen {
				break
			}
			if !first {
				if peek.kind != tokComma {
					return nil, p.errorf(peek.pos, "expected ',' or ')' in call arguments, got %s", peek.describe())
				}
				peek, err = p.next()
				if err != nil {
					return nil, err
				}
				if peek.kind == tokRParen {
					break // trailing comma
				}
			}
			p.unget(peek)
		} else if !first {
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind != tokComma {
				p.unget(peek)
				break
			}
		}
		first = false

		// Parse: name: [direction] expr
		nameTok, err := p.expect(tokIdent)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokColon); err != nil {
			return nil, p.errorf(nameTok.pos, "expected ':' after parameter name in call arguments")
		}

		// Validate parameter name
		param := paramByName[nameTok.val]
		if param == nil {
			return nil, p.errorf(nameTok.pos, "unknown parameter %q in call target", nameTok.val)
		}
		if args[nameTok.val] != nil {
			return nil, p.errorf(nameTok.pos, "duplicate argument %q in call", nameTok.val)
		}

		// Check for direction keyword (out, inout)
		dirTok, err := p.next()
		if err != nil {
			return nil, err
		}

		direction := ""
		if dirTok.kind == tokIdent && (dirTok.val == "out" || dirTok.val == "inout") {
			direction = dirTok.val
		} else {
			p.unget(dirTok)
		}

		// Validate direction matches parameter
		switch {
		case param.direction == "in" && direction != "":
			return nil, p.errorf(nameTok.pos, "parameter %q is 'in' but was passed as %q", nameTok.val, direction)
		case param.direction == "out" && direction != "out":
			return nil, p.errorf(nameTok.pos, "parameter %q is 'out' and must be passed with 'out' keyword", nameTok.val)
		case param.direction == "inout" && direction != "inout":
			return nil, p.errorf(nameTok.pos, "parameter %q is 'inout' and must be passed with 'inout' keyword", nameTok.val)
		}

		// Parse value expression
		var valExpr Expr
		if direction == "out" || direction == "inout" {
			// For out/inout, parse an lvalue directly (variable, $param, or $register)
			// Don't go through the full expression parser which does readability checks
			lvalTok, err := p.next()
			if err != nil {
				return nil, err
			}
			if lvalTok.kind != tokIdent {
				return nil, p.errorf(lvalTok.pos, "%s parameter %q requires a variable or register", direction, nameTok.val)
			}
			name := lvalTok.val
			if strings.HasPrefix(name, "$") {
				if reg, ok := unitRegisters[name]; ok {
					valExpr = &LiteralExpr{Value: reg}
				} else {
					// $param reference — keep as IdentExpr, resolved at emit time
					valExpr = &IdentExpr{Name: name}
				}
			} else {
				valExpr = &IdentExpr{Name: name}
			}
		} else {
			valExpr, err = p.parseArithExpr(resolve)
			if err != nil {
				return nil, err
			}
		}

		args[nameTok.val] = &CallBhvArg{
			Direction: direction,
			Value:     valExpr,
		}
	}

	return args, nil
}

// resolveCallSub resolves a behavior name to its "sub" value for the call instruction.
// Returns -1 for self-recursion, or a positive 1-based index into the dependencies array.
func (p *parser) resolveCallSub(behaviorID string, pos int) (int, error) {
	// Self-recursion
	if behaviorID == p.selfBehaviorID {
		return -1, nil
	}

	// Already compiled
	if idx, ok := p.depIndex[behaviorID]; ok {
		return idx, nil
	}

	// Cycle detection
	if p.depCompiling[behaviorID] {
		return 0, p.errorf(pos, "recursive call cycle involving %q", behaviorID)
	}

	// Compile on demand
	compiled, err := p.compileDependency(behaviorID, pos)
	if err != nil {
		return 0, err
	}

	// Add to dependencies array
	p.dependencies = append(p.dependencies, compiled)
	idx := len(p.dependencies) // 1-based
	p.depIndex[behaviorID] = idx
	return idx, nil
}

// compileDependency compiles a callee behavior on demand for use as a subroutine.
// Returns the compiled behavior value map (without dependencies — those go on root only).
func (p *parser) compileDependency(behaviorID string, pos int) (map[string]any, error) {
	bhv := p.bhvs[behaviorID]
	if bhv == nil {
		return nil, p.errorf(pos, "unknown behavior %q", behaviorID)
	}

	// Mark as compiling (cycle detection)
	p.depCompiling[behaviorID] = true
	defer delete(p.depCompiling, behaviorID)

	// Determine source offset
	sourceOffset := 0
	if bhv.prelude != "" {
		sourceOffset = len(bhv.prelude)
	}

	sourceDir := ""
	if bhv.sourcePath != "" {
		sourceDir = path.Dir(bhv.sourcePath)
		if sourceDir == "." {
			sourceDir = ""
		}
	}

	// Create a sub-parser for the callee's source
	dp := &parser{
		scanner: scanner{
			src:          bhv.sourceText,
			locale:       p.locale,
			sourceFile:   bhv.sourcePath,
			sourceOffset: sourceOffset,
		},
		fns:         maps.Clone(p.fns),
		iters:       maps.Clone(p.iters),
		consts:      maps.Clone(p.consts),
		enums:       maps.Clone(p.enums),
		bhvs:        maps.Clone(p.bhvs),
		target:      behaviorID,
		loopLabels:  map[string]bool{},
		prelude:     bhv.prelude,
		sourceFS:    bhv.sourceFS,
		sourcePath:  bhv.sourcePath,
		sourceDir:   sourceDir,
		stdlibFS:    p.stdlibFS,
		releaseMode: p.releaseMode,
		// Share dependency tracking with root
		dependencies:   p.dependencies,
		depIndex:       p.depIndex,
		depCompiling:   p.depCompiling,
		selfBehaviorID: behaviorID,
	}

	// Skip pass 1 — the sub-parser already has all symbols cloned from the root.
	// Just find and compile the target behavior.
	dp.pos = 0
	dp.ungot = nil
	for {
		tok, err := dp.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokEOF {
			return nil, p.errorf(pos, "behavior %q not found in source", behaviorID)
		}
		if tok.kind != tokIdent {
			continue
		}
		switch tok.val {
		case "behavior":
			idTok, err := dp.parseBehaviorID()
			if err != nil {
				return nil, err
			}
			if idTok.val == behaviorID {
				obj, err := dp.parseBehaviorBody(behaviorID)
				if err != nil {
					return nil, err
				}
				// Sync shared state back
				p.dependencies = dp.dependencies
				p.warnings = append(p.warnings, dp.warnings...)

				value := obj.Value.(map[string]any)
				delete(value, "dependencies") // dependencies are flat on root
				return value, nil
			}
			if err := dp.skipBraceBlock(); err != nil {
				return nil, err
			}
		case "fn", "iter":
			if err := dp.skipFnDef(); err != nil {
				return nil, err
			}
		case "const", "import", "skip":
			if err := dp.skipToNextDecl(); err != nil {
				return nil, err
			}
		case "private":
			privTok, err := dp.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			switch privTok.val {
			case "fn", "iter":
				if err := dp.skipFnDef(); err != nil {
					return nil, err
				}
			case "const":
				if err := dp.skipToNextDecl(); err != nil {
					return nil, err
				}
			case "enum":
				if _, err := dp.expect(tokIdent); err != nil {
					return nil, err
				}
				if err := dp.skipBraceBlock(); err != nil {
					return nil, err
				}
			}
		case "enum":
			if _, err := dp.expect(tokIdent); err != nil {
				return nil, err
			}
			if err := dp.skipBraceBlock(); err != nil {
				return nil, err
			}
		}
	}
}

// emitBhvCallBehavior emits a call instruction frame at behavior level.
// resolveBhvCallArgValue resolves a call argument expression to a wire-format value
// at behavior level. Handles $param references (which resolve to parameter indices)
// and delegates other expressions to emitBhvExprGetValue.
func (p *parser) resolveBhvCallArgValue(expr Expr, syms *symbolTable, b *frameBuilder) (any, error) {
	if ident, ok := expr.(*IdentExpr); ok && strings.HasPrefix(ident.Name, "$") {
		// Check unit registers first
		if reg, ok := unitRegisters[ident.Name]; ok {
			return reg, nil
		}
		// Check param map
		if idx, ok := syms.paramMap[ident.Name]; ok {
			return idx, nil
		}
		return nil, fmt.Errorf("unknown register %q", ident.Name)
	}
	return p.emitBhvExprGetValue(expr, syms, b, "")
}

func (p *parser) emitBhvCallBehavior(stmt *CallBehaviorStmt, b *frameBuilder, syms *symbolTable) error {
	bhv := p.bhvs[stmt.BehaviorName]

	sub, err := p.resolveCallSub(stmt.BehaviorName, stmt.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	// Wire numbered slots to args (0-based, matching instruction slot numbering)
	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := stmt.Args[param.keyword]
		if !ok {
			continue // omitted param
		}

		val, err := p.resolveBhvCallArgValue(arg.Value, syms, b)
		if err != nil {
			return err
		}
		f[slotKey] = val
	}

	setComment(f, stmt.Comment)
	b.emit(f)

	// After a call, execution mode is unknown (callee may have changed it)
	b.mode = modeUnknown
	return nil
}

// emitBhvCallBehaviorExpr emits a call instruction for expression-form behavior calls.
// Unbound out params are captured as return values into the given target registers.
func (p *parser) emitBhvCallBehaviorExpr(expr *CallBehaviorExpr, retVals []any, syms *symbolTable, b *frameBuilder, comment string) error {
	bhv := p.bhvs[expr.BehaviorName]

	sub, err := p.resolveCallSub(expr.BehaviorName, expr.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	// Collect unbound out params for return value mapping
	retIdx := 0
	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := expr.Args[param.keyword]
		if ok {
			val, err := p.resolveBhvCallArgValue(arg.Value, syms, b)
			if err != nil {
				return err
			}
			f[slotKey] = val
		} else if param.direction == "out" || param.direction == "inout" {
			// Unbound out/inout param — map to return value
			if retIdx < len(retVals) {
				f[slotKey] = retVals[retIdx]
				retIdx++
			}
			// else: unbound with no target — omit (discarded)
		}
		// else: unbound in param — omit (null)
	}

	setComment(f, comment)
	b.emit(f)

	b.mode = modeUnknown
	return nil
}

// emitFnBodyCallBehavior emits a call instruction frame within a function body context.
func (p *parser) emitFnBodyCallBehavior(stmt *CallBehaviorStmt, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string) error {
	bhv := p.bhvs[stmt.BehaviorName]

	sub, err := p.resolveCallSub(stmt.BehaviorName, stmt.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := stmt.Args[param.keyword]
		if !ok {
			continue
		}

		val, err := p.emitExprGetValue(arg.Value, b, paramMap, usedVars, "", stmt.Pos)
		if err != nil {
			return err
		}
		f[slotKey] = val
	}

	setComment(f, comment)
	b.emit(f)
	b.mode = modeUnknown
	return nil
}

// emitFnBodyCallBehaviorExpr emits a call instruction for expression-form behavior calls in fn body.
func (p *parser) emitFnBodyCallBehaviorExpr(expr *CallBehaviorExpr, retVals []any, b *frameBuilder, paramMap map[string]any, usedVars map[string]bool, comment string) error {
	bhv := p.bhvs[expr.BehaviorName]

	sub, err := p.resolveCallSub(expr.BehaviorName, expr.Pos)
	if err != nil {
		return err
	}

	f := map[string]any{"op": "call", "sub": sub}

	retIdx := 0
	for i, param := range bhv.params {
		slotKey := strconv.Itoa(i) // 0-based
		arg, ok := expr.Args[param.keyword]
		if ok {
			val, err := p.emitExprGetValue(arg.Value, b, paramMap, usedVars, "", expr.Pos)
			if err != nil {
				return err
			}
			f[slotKey] = val
		} else if param.direction == "out" || param.direction == "inout" {
			if retIdx < len(retVals) {
				f[slotKey] = retVals[retIdx]
				retIdx++
			}
		}
	}

	setComment(f, comment)
	b.emit(f)
	b.mode = modeUnknown
	return nil
}
