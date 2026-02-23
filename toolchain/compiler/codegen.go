package compiler

import (
	"fmt"
	"sort"
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
		if vi, ok := syms.vars[v]; ok {
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

	// State for break target patching after a loop
	breakTargetFrame := -1
	resumeFrame := -1

	// Deferred bodies from if statements, emitted after all main-line code
	var deferred []deferredBody

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
		comment := p.docComment

		switch tok.val {
		case "instruction":
			hasInstruction = true
			rawFrame, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			if err := p.checkInstructionDirections(rawFrame, syms, tok.pos); err != nil {
				return nil, err
			}
			resolved := resolveInstructionFrame(rawFrame, nil, nil, nil, comment)
			b.emit(resolved)

		case "var":
			hasInstruction = true
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			if nameTok.val == "_" {
				// var _, ... = fn → multi-return with first discard
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind != tokComma {
					return nil, p.errorf(nameTok.pos, "'_' cannot be used as a variable name")
				}
				if err := p.compileMultiReturn(nameTok, true, true, b, comment, syms); err != nil {
					return nil, err
				}
			} else {
				if err := p.checkVarName(nameTok.val, syms, nameTok.pos); err != nil {
					return nil, err
				}
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind == tokComma {
					if err := p.compileMultiReturn(nameTok, true, false, b, comment, syms); err != nil {
						return nil, err
					}
				} else if sep.kind == tokEquals {
					if err := p.compileVarInit(nameTok, true, b, comment, syms); err != nil {
						return nil, err
					}
				} else {
					return nil, p.errorf(sep.pos, "expected ',' or '=' after var identifier, got %s", sep.describe())
				}
			}

		case "let":
			hasInstruction = true
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			if nameTok.val == "_" {
				// let _, ... = fn → multi-return with first discard
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind != tokComma {
					return nil, p.errorf(nameTok.pos, "'_' cannot be used as a variable name")
				}
				if err := p.compileMultiReturn(nameTok, false, true, b, comment, syms); err != nil {
					return nil, err
				}
			} else {
				if err := p.checkVarName(nameTok.val, syms, nameTok.pos); err != nil {
					return nil, err
				}
				sep, err := p.next()
				if err != nil {
					return nil, err
				}
				if sep.kind == tokComma {
					if err := p.compileMultiReturn(nameTok, false, false, b, comment, syms); err != nil {
						return nil, err
					}
				} else if sep.kind == tokEquals {
					if err := p.compileVarInit(nameTok, false, b, comment, syms); err != nil {
						return nil, err
					}
				} else {
					return nil, p.errorf(sep.pos, "expected ',' or '=' after let identifier, got %s", sep.describe())
				}
			}

		case "_":
			hasInstruction = true
			// _ starts a binding list: _, let a, var b = fn args
			// or just _ = fn args (discard all returns, equivalent to bare call)
			sep, err := p.next()
			if err != nil {
				return nil, err
			}
			if sep.kind == tokComma {
				if err := p.compileMultiReturn(tok, false, true, b, comment, syms); err != nil {
					return nil, err
				}
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
				args, kwArgs, err := p.parseFnCallArgs(fn, calleeTok, syms, b, comment)
				if err != nil {
					return nil, err
				}
				if err := p.expandCall(calleeTok.val, args, kwArgs, nil, b, calleeTok.pos, comment, syms.usedVars); err != nil {
					return nil, err
				}
			} else {
				return nil, p.errorf(sep.pos, "expected ',' or '=' after '_', got %s", sep.describe())
			}

		case "loop":
			hasInstruction = true
			checkFrame, err := p.compileLoop(b, syms)
			if err != nil {
				return nil, err
			}
			if checkFrame >= 0 {
				breakTargetFrame = checkFrame + 1
				resumeFrame = b.pos()
				b.seek(breakTargetFrame)
			}

		case "if":
			hasInstruction = true
			if err := p.compileIfStmt(b, &deferred, comment, syms); err != nil {
				return nil, err
			}

		case "while":
			hasInstruction = true
			if err := p.compileWhile(b, comment, syms); err != nil {
				return nil, err
			}

		default:
			hasInstruction = true
			if err := p.compileDefaultStatement(tok, b, comment, syms); err != nil {
				return nil, err
			}
		}

		// After emitting an instruction, check if we need to patch a break target
		if breakTargetFrame >= 0 && b.pos()-1 == breakTargetFrame {
			instr := b.get(breakTargetFrame)
			// Peek to see if there are more statements
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokRBrace {
				// Last instruction in behavior; stop execution
				instr["next"] = false
				b.seek(resumeFrame)
				p.unget(peek)
			} else {
				// More instructions follow; skip over loop body frames
				instr["next"] = frameRef(resumeFrame)
				b.seek(resumeFrame)
				p.unget(peek)
			}
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
			for _, f := range d.frames {
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

// compileLoop compiles a loop body. It returns the check frame index (or -1
// if the loop contains no if/break).
func (p *parser) compileLoop(b *frameBuilder, syms *symbolTable) (int, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return -1, err
	}

	loopStart := b.pos()
	checkFrame := -1

	for {
		tok, err := p.next()
		if err != nil {
			return -1, err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind == tokEOF {
			return -1, p.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
		if tok.kind != tokIdent {
			return -1, p.errorf(tok.pos, "expected statement, got %s", tok.describe())
		}

		comment := p.docComment

		switch tok.val {
		case "if":
			cf, err := p.compileIfBreak(b, comment, syms)
			if err != nil {
				return -1, err
			}
			checkFrame = cf

		default:
			if err := p.compileDefaultStatement(tok, b, comment, syms); err != nil {
				return -1, err
			}
		}
	}

	// Set next on last loop body instruction to loop back to start of body
	if checkFrame >= 0 {
		lastInstr := b.get(b.pos() - 1)
		lastInstr["next"] = frameRef(loopStart)
	}

	return checkFrame, nil
}

// compileIfBreak compiles `if lhs >= rhs { break }` inside a loop body.
// It emits a check_number instruction and reserves the next frame for the
// break target. Returns the check frame index.
func (p *parser) compileIfBreak(b *frameBuilder, comment string, syms *symbolTable) (int, error) {
	lhsTok, err := p.expect(tokIdent)
	if err != nil {
		return -1, err
	}
	if err := p.checkReadable(lhsTok.val, syms, lhsTok.pos); err != nil {
		return -1, err
	}
	if _, err := p.expect(tokGreaterEquals); err != nil {
		return -1, err
	}
	rhsTok, err := p.expect(tokNumber)
	if err != nil {
		return -1, err
	}
	rhsNum, _ := strconv.Atoi(rhsTok.val)

	if _, err := p.expect(tokLBrace); err != nil {
		return -1, err
	}
	breakTok, err := p.expect(tokIdent)
	if err != nil {
		return -1, err
	}
	if breakTok.val != "break" {
		return -1, p.errorf(breakTok.pos, "expected 'break', got %q", breakTok.val)
	}
	if _, err := p.expect(tokRBrace); err != nil {
		return -1, err
	}

	f := map[string]any{
		"op":        "check_number",
		checkValue:  lhsTok.val,
		checkTarget: map[string]any{"num": rhsNum},
	}
	setComment(f, comment)
	checkFrame := b.emit(f)

	// Reserve the next frame for the break target (filled by caller).
	b.emit(nil)

	// Set if_smaller to skip past break target to the next loop body frame.
	f[checkSmaller] = frameRef(b.pos())

	return checkFrame, nil
}

// compileWhile compiles `while ident <= number { body }`.
// It emits a check_number and the body. The body's last instruction loops back
// to the check, and the check's if_larger slot exits to the continuation.
func (p *parser) compileWhile(b *frameBuilder, comment string, syms *symbolTable) error {
	varTok, err := p.expect(tokIdent)
	if err != nil {
		return err
	}
	if err := p.checkReadable(varTok.val, syms, varTok.pos); err != nil {
		return err
	}
	if _, err := p.expect(tokLessEquals); err != nil {
		return err
	}
	limitTok, err := p.expect(tokNumber)
	if err != nil {
		return err
	}
	limitNum, _ := strconv.Atoi(limitTok.val)

	// Emit check_number: equal and smaller fall through to body.
	check := map[string]any{
		"op":        "check_number",
		checkValue:  varTok.val,
		checkTarget: map[string]any{"num": limitNum},
	}
	setComment(check, comment)
	checkFrame := b.emit(check)

	// Compile body.
	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	bodyFrames, err := p.compileBody(syms)
	if err != nil {
		return err
	}
	for _, f := range bodyFrames {
		b.emit(f)
	}

	// Loop back: set "next" on the body's last instruction.
	lastBody := b.get(b.pos() - 1)
	lastBody["next"] = frameRef(checkFrame)

	// Patch check's if_larger to exit to the continuation.
	check[checkLarger] = frameRef(b.pos())

	return nil
}

// resolveAssignTarget resolves an assignment target identifier through the
// symbol table: $register → unit register int, param → index, else → variable name.
// Returns an error if the target is an immutable let variable or a parameter
// with incompatible direction. compound indicates a read+write operation (++, +=).
func (p *parser) resolveAssignTarget(name string, syms *symbolTable, pos int, compound bool) (any, error) {
	if vi, ok := syms.vars[name]; ok {
		if !vi.mutable {
			return nil, p.errorf(pos, "cannot assign to immutable variable %q", name)
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
	return name, nil // regular variable name
}

// parseArgValue parses a single argument value at behavior level.
// Accepts strings, numbers, null, $register, param names, variable names,
// type constructors (Item, Component, Technology, Value, Coordinate),
// and the & operator for attaching numeric components.
// The b and comment params are needed for runtime constructors that emit frames.
func (p *parser) parseArgValue(syms *symbolTable, b *frameBuilder, comment string) (any, error) {
	tok, err := p.next()
	if err != nil {
		return nil, err
	}
	var base any
	switch tok.kind {
	case tokString:
		base = tok.val
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		base = map[string]any{"num": num}
	case tokIdent:
		if tok.val == "localize" {
			resolved, err := p.parseLocalize()
			if err != nil {
				return nil, err
			}
			base = resolved
		} else if tok.val == "null" {
			base = false
		} else if isConstructor(tok.val) {
			val, err := p.parseConstructor(tok, syms, b, comment)
			if err != nil {
				return nil, err
			}
			base = val
		} else if strings.HasPrefix(tok.val, "$") {
			if reg, ok := unitRegisters[tok.val]; ok {
				base = reg
			} else if idx, ok := syms.paramMap[tok.val]; ok {
				base = idx
			} else {
				return nil, p.errorf(tok.pos, "unknown register %q", tok.val)
			}
		} else {
			base = tok.val // variable name
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
		return p.parseAmpersand(base, tok.pos, syms, b, comment)
	}
	p.unget(peek)
	return base, nil
}

// parseConstructor parses a type constructor call: Name(args).
func (p *parser) parseConstructor(nameTok token, syms *symbolTable, b *frameBuilder, comment string) (any, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return nil, p.errorf(nameTok.pos, "expected '(' after %s", nameTok.val)
	}

	switch nameTok.val {
	case "Item":
		return p.parseSimpleConstructor("", nameTok.pos)
	case "Component":
		return p.parseSimpleConstructor("c_", nameTok.pos)
	case "Technology":
		return p.parseSimpleConstructor("t_", nameTok.pos)
	case "Value":
		return p.parseSimpleConstructor("v_", nameTok.pos)
	case "Coordinate":
		return p.parseCoordinateConstructor(nameTok.pos, syms, b, comment)
	}
	return nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
}

// parseSimpleConstructor parses Item/Component/Technology/Value("id").
// The '(' has already been consumed. prefix is prepended to the id.
func (p *parser) parseSimpleConstructor(prefix string, pos int) (any, error) {
	argTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if argTok.kind != tokString {
		return nil, p.errorf(argTok.pos, "expected string argument, got %s", argTok.describe())
	}
	// Expect closing paren
	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return map[string]any{"id": prefix + argTok.val}, nil
}

// parseCoordinateConstructor parses Coordinate(x, y).
// The '(' has already been consumed.
func (p *parser) parseCoordinateConstructor(pos int, syms *symbolTable, b *frameBuilder, comment string) (any, error) {
	xVal, err := p.parseArgValue(syms, b, comment)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokComma); err != nil {
		return nil, err
	}
	yVal, err := p.parseArgValue(syms, b, comment)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}

	// Check if both are compile-time numeric literals
	xMap, xIsMap := xVal.(map[string]any)
	yMap, yIsMap := yVal.(map[string]any)
	if xIsMap && yIsMap {
		xNum, xHasNum := xMap["num"]
		yNum, yHasNum := yMap["num"]
		if xHasNum && yHasNum && len(xMap) == 1 && len(yMap) == 1 {
			return map[string]any{
				"coord": map[string]any{"x": xNum, "y": yNum},
			}, nil
		}
	}

	// Runtime: emit combine_coordinate via expandCall
	tmpVar := allocUniqueVar("@coord", syms.usedVars)
	if err := p.expandCall("combine_coordinate", []any{xVal, yVal}, nil, []any{tmpVar}, b, pos, comment, syms.usedVars); err != nil {
		return nil, err
	}
	return tmpVar, nil
}

// parseAmpersand handles the & operator after a base value.
// Merges a numeric component into the base value (compile-time)
// or emits set_number (runtime).
func (p *parser) parseAmpersand(base any, basePos int, syms *symbolTable, b *frameBuilder, comment string) (any, error) {
	rhs, err := p.parseArgValue(syms, b, comment)
	if err != nil {
		return nil, err
	}

	// Check if both are compile-time values
	baseMap, baseIsMap := base.(map[string]any)
	rhsMap, rhsIsMap := rhs.(map[string]any)
	if baseIsMap && rhsIsMap {
		rhsNum, rhsHasNum := rhsMap["num"]
		if rhsHasNum && len(rhsMap) == 1 {
			// Merge "num" into base map
			result := make(map[string]any, len(baseMap)+1)
			for k, v := range baseMap {
				result[k] = v
			}
			result["num"] = rhsNum
			return result, nil
		}
	}

	// Runtime: emit set_number via expandCall
	tmpVar := allocUniqueVar("@amp", syms.usedVars)
	if err := p.expandCall("set_number", []any{base, rhs}, nil, []any{tmpVar}, b, basePos, comment, syms.usedVars); err != nil {
		return nil, err
	}
	return tmpVar, nil
}

// parseConstructorForTarget parses a constructor expression when the output
// target variable is known. For compile-time constructors, returns the literal
// map (caller emits set_reg). For runtime constructors, emits frames directly
// targeting the variable and returns nil.
func (p *parser) parseConstructorForTarget(target string, syms *symbolTable, b *frameBuilder, comment string) (any, error) {
	ctorTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokLParen); err != nil {
		return nil, p.errorf(ctorTok.pos, "expected '(' after %s", ctorTok.val)
	}

	var base any
	switch ctorTok.val {
	case "Item":
		lit, err := p.parseSimpleConstructor("", ctorTok.pos)
		if err != nil {
			return nil, err
		}
		base = lit
	case "Component":
		lit, err := p.parseSimpleConstructor("c_", ctorTok.pos)
		if err != nil {
			return nil, err
		}
		base = lit
	case "Technology":
		lit, err := p.parseSimpleConstructor("t_", ctorTok.pos)
		if err != nil {
			return nil, err
		}
		base = lit
	case "Value":
		lit, err := p.parseSimpleConstructor("v_", ctorTok.pos)
		if err != nil {
			return nil, err
		}
		base = lit
	case "Coordinate":
		xVal, err := p.parseArgValue(syms, b, comment)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokComma); err != nil {
			return nil, err
		}
		yVal, err := p.parseArgValue(syms, b, comment)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		xMap, xIsMap := xVal.(map[string]any)
		yMap, yIsMap := yVal.(map[string]any)
		if xIsMap && yIsMap {
			xNum, xHasNum := xMap["num"]
			yNum, yHasNum := yMap["num"]
			if xHasNum && yHasNum && len(xMap) == 1 && len(yMap) == 1 {
				base = map[string]any{"coord": map[string]any{"x": xNum, "y": yNum}}
				break
			}
		}
		// Runtime: emit combine_coordinate directly into target
		if err := p.expandCall("combine_coordinate", []any{xVal, yVal}, nil, []any{target}, b, ctorTok.pos, comment, syms.usedVars); err != nil {
			return nil, err
		}
		// Check for & operator
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokAmpersand {
			rhs, err := p.parseArgValue(syms, b, comment)
			if err != nil {
				return nil, err
			}
			if err := p.expandCall("set_number", []any{target, rhs}, nil, []any{target}, b, ctorTok.pos, comment, syms.usedVars); err != nil {
				return nil, err
			}
		} else {
			p.unget(peek)
		}
		return nil, nil // frames already emitted
	default:
		return nil, p.errorf(ctorTok.pos, "unknown constructor %q", ctorTok.val)
	}

	// For non-Coordinate constructors, check for & operator
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if peek.kind == tokAmpersand {
		rhsTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if rhsTok.kind == tokNumber {
			num, _ := strconv.Atoi(rhsTok.val)
			baseMap := base.(map[string]any)
			result := make(map[string]any, len(baseMap)+1)
			for k, v := range baseMap {
				result[k] = v
			}
			result["num"] = num
			return result, nil
		}
		// Runtime &: emit set_reg for base, then set_number
		p.unget(rhsTok)
		rhs, err := p.parseArgValue(syms, b, comment)
		if err != nil {
			return nil, err
		}
		f := map[string]any{
			"op": "set_reg",
			"1":  base,
			"2":  target,
		}
		setComment(f, comment)
		b.emit(f)
		if err := p.expandCall("set_number", []any{target, rhs}, nil, []any{target}, b, ctorTok.pos, comment, syms.usedVars); err != nil {
			return nil, err
		}
		return nil, nil
	}
	p.unget(peek)
	return base, nil // compile-time literal
}

// compileDefaultStatement compiles a function call or compound assignment.
func (p *parser) compileDefaultStatement(tok token, b *frameBuilder, comment string, syms *symbolTable) error {
	// Peek to distinguish function call from compound assignment
	tok2, err := p.next()
	if err != nil {
		return err
	}

	if tok2.kind == tokPlusPlus {
		target, err := p.resolveAssignTarget(tok.val, syms, tok.pos, true)
		if err != nil {
			return err
		}
		f := map[string]any{
			"op": "add",
			"1":  target,
			"2":  map[string]any{"num": 1},
			"3":  target,
		}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	if tok2.kind == tokEquals {
		target, err := p.resolveAssignTarget(tok.val, syms, tok.pos, false)
		if err != nil {
			return err
		}
		rhsTok, err := p.next()
		if err != nil {
			return err
		}
		if rhsTok.kind == tokNumber {
			num, _ := strconv.Atoi(rhsTok.val)
			f := map[string]any{
				"op": "set_number",
				"2":  map[string]any{"num": num},
				"3":  target,
			}
			setComment(f, comment)
			b.emit(f)
			return nil
		}
		if rhsTok.kind == tokIdent && isConstructor(rhsTok.val) {
			p.unget(rhsTok)
			val, err := p.parseArgValue(syms, b, comment)
			if err != nil {
				return err
			}
			if m, ok := val.(map[string]any); ok {
				f := map[string]any{
					"op": "set_reg",
					"1":  m,
					"2":  target,
				}
				setComment(f, comment)
				b.emit(f)
				return nil
			}
			// Runtime constructor already emitted frames; copy to target
			f := map[string]any{
				"op": "set_reg",
				"1":  val,
				"2":  target,
			}
			setComment(f, comment)
			b.emit(f)
			return nil
		}
		if rhsTok.kind == tokIdent && rhsTok.val == "instruction" {
			rawFrame, err := p.parseInstruction()
			if err != nil {
				return err
			}
			if !frameHasReturnSlot(rawFrame) {
				return p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
			}
			if err := p.checkInstructionDirections(rawFrame, syms, rhsTok.pos); err != nil {
				return err
			}
			resolved := resolveInstructionFrame(rawFrame, []any{target}, nil, nil, comment)
			b.emit(resolved)
			return nil
		}
		if rhsTok.kind == tokIdent {
			fn := p.fns[rhsTok.val]
			if fn == nil {
				return p.errorf(rhsTok.pos, "unknown function %q", rhsTok.val)
			}
			if !fn.hasReturn() {
				return p.errorf(rhsTok.pos, "function %q has no return value", rhsTok.val)
			}
			args, kwArgs, err := p.parseFnCallArgs(fn, rhsTok, syms, b, comment)
			if err != nil {
				return err
			}
			if err := p.checkCallDirections(fn, rhsTok.val, args, kwArgs, syms, rhsTok.pos); err != nil {
				return err
			}
			return p.expandCall(rhsTok.val, args, kwArgs, []any{target}, b, rhsTok.pos, comment, syms.usedVars)
		}
		return p.errorf(rhsTok.pos, "expected number, function call, constructor, or instruction after '=', got %s", rhsTok.describe())
	}

	if tok2.kind == tokPlusEquals {
		target, err := p.resolveAssignTarget(tok.val, syms, tok.pos, true)
		if err != nil {
			return err
		}
		numTok, err := p.expect(tokNumber)
		if err != nil {
			return err
		}
		num, _ := strconv.Atoi(numTok.val)
		f := map[string]any{
			"op": "add",
			"1":  target,
			"2":  map[string]any{"num": num},
			"3":  target,
		}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	// Function call — push back the token we peeked
	p.unget(tok2)

	fn := p.fns[tok.val]
	if fn == nil {
		return p.errorf(tok.pos, "unknown statement %q", tok.val)
	}

	args, kwArgs, err := p.parseFnCallArgs(fn, tok, syms, b, comment)
	if err != nil {
		return err
	}
	if err := p.checkCallDirections(fn, tok.val, args, kwArgs, syms, tok.pos); err != nil {
		return err
	}

	return p.expandCall(tok.val, args, kwArgs, nil, b, tok.pos, comment, syms.usedVars)
}

// parseFnCallArgs parses the positional and keyword arguments for a function
// call at behavior level. nameTok is the function name token (used for error
// messages). The function definition fn determines argument counts and keywords.
// b and comment are threaded through for runtime constructors that emit frames.
func (p *parser) parseFnCallArgs(fn *fnDef, nameTok token, syms *symbolTable, b *frameBuilder, comment string) ([]any, map[string]any, error) {
	posCount := fn.positionalCount()
	args := make([]any, posCount)
	for i := 0; i < posCount; i++ {
		// Consume optional comma separator between positional args
		if i > 0 {
			sep, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			if sep.kind != tokComma {
				p.unget(sep)
			}
		}

		// Peek for direction annotation (in, out, inout)
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
			annotationPos = dirTok.pos
		}

		pd := fn.positionalParam(i)
		if err := p.checkCallAnnotation(annotation, pd, nameTok.val, annotationPos); err != nil {
			return nil, nil, err
		}

		val, err := p.parseArgValue(syms, b, comment)
		if err != nil {
			return nil, nil, err
		}
		args[i] = val
	}

	// Parse optional keyword args: , keyword: value
	// First check for extra positional args that should be keyword args.
	var kwArgs map[string]any
	peek, err := p.next()
	if err != nil {
		return nil, nil, err
	}
	if (peek.kind == tokString || peek.kind == tokNumber) && fn.positionalCount() < len(fn.params) {
		return nil, nil, p.errorf(peek.pos,
			"too many positional arguments for %s (remaining parameters are keyword-only)", nameTok.val)
	}
	if peek.kind == tokComma {
		kwArgs = map[string]any{}
		for {
			// Read the first ident — could be a direction annotation or keyword name
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
			val, err := p.parseArgValue(syms, b, comment)
			if err != nil {
				return nil, nil, err
			}
			kwArgs[dirOrKw.val] = val

			// Check for another comma
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


// --- If statement compilation ---

// compileBody compiles a brace-delimited block into a slice of frames.
// The opening '{' must already be consumed.
func (p *parser) compileBody(syms *symbolTable) ([]map[string]any, error) {
	b := &frameBuilder{}
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
		if err := p.compileDefaultStatement(tok, b, comment, syms); err != nil {
			return nil, err
		}
	}
	return b.frames, nil
}

// compileIfStmt compiles an if / else-if / else statement.
// The "if" keyword has already been consumed.
func (p *parser) compileIfStmt(b *frameBuilder, deferred *[]deferredBody, comment string, syms *symbolTable) error {
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
	rhsTok, err := p.expect(tokNumber)
	if err != nil {
		return err
	}
	rhsNum, _ := strconv.Atoi(rhsTok.val)

	check := map[string]any{
		"op":        "check_number",
		checkValue:  lhsTok.val,
		checkTarget: map[string]any{"num": rhsNum},
	}
	setComment(check, comment)
	checkFrame := b.emit(check)

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	bodyFrames, err := p.compileBody(syms)
	if err != nil {
		return err
	}

	switch opTok.kind {
	case tokLess:
		// a < N: body when smaller. Deferred.
		*deferred = append(*deferred, deferredBody{
			frames:     bodyFrames,
			checkFrame: checkFrame,
			slot:       checkSmaller,
		})

	case tokGreaterEquals:
		// a >= N: body when larger or equal. Inline (both fall through).
		for _, f := range bodyFrames {
			b.emit(f)
		}
		// Parse optional else
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokIdent && tok.val == "else" {
			if _, err := p.expect(tokLBrace); err != nil {
				return err
			}
			elseFrames, err := p.compileBody(syms)
			if err != nil {
				return err
			}
			*deferred = append(*deferred, deferredBody{
				frames:     elseFrames,
				checkFrame: checkFrame,
				slot:       checkSmaller,
			})
		} else {
			p.unget(tok)
		}

	case tokDoubleEquals:
		// a == N: body when equal. Inline (falls through).
		for _, f := range bodyFrames {
			b.emit(f)
		}
		// Parse else if / else
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokIdent && tok.val == "else" {
			if err := p.compileElseClauses(checkFrame, deferred, syms); err != nil {
				return err
			}
		} else {
			p.unget(tok)
		}

	case tokGreater:
		// a > N: body when larger. Deferred.
		*deferred = append(*deferred, deferredBody{
			frames:     bodyFrames,
			checkFrame: checkFrame,
			slot:       checkLarger,
		})

	default:
		return p.errorf(opTok.pos, "unsupported comparison operator %s", opTok.describe())
	}

	// Set continuation on all deferred bodies from this if block.
	continuation := b.pos()
	for i := range *deferred {
		if (*deferred)[i].continuation == 0 && (*deferred)[i].checkFrame == checkFrame {
			(*deferred)[i].continuation = continuation
		}
	}

	return nil
}

// compileElseClauses compiles the else / else-if chain after an == condition.
func (p *parser) compileElseClauses(checkFrame int, deferred *[]deferredBody, syms *symbolTable) error {
	// Check for "else if" vs plain "else"
	tok, err := p.next()
	if err != nil {
		return err
	}

	if tok.kind == tokIdent && tok.val == "if" {
		// else if: parse the condition
		if _, err := p.expect(tokIdent); err != nil {
			return err
		}
		opTok, err := p.next()
		if err != nil {
			return err
		}
		if _, err := p.expect(tokNumber); err != nil {
			return err
		}

		var slot string
		switch opTok.kind {
		case tokGreater:
			slot = checkLarger
		case tokLess:
			slot = checkSmaller
		default:
			return p.errorf(opTok.pos, "unsupported else-if operator %s", opTok.describe())
		}

		if _, err := p.expect(tokLBrace); err != nil {
			return err
		}
		frames, err := p.compileBody(syms)
		if err != nil {
			return err
		}
		*deferred = append(*deferred, deferredBody{
			frames:     frames,
			checkFrame: checkFrame,
			slot:       slot,
		})

		// Check for trailing else
		tok2, err := p.next()
		if err != nil {
			return err
		}
		if tok2.kind == tokIdent && tok2.val == "else" {
			// Determine the remaining slot
			var elseSlot string
			if slot == checkLarger {
				elseSlot = checkSmaller
			} else {
				elseSlot = checkLarger
			}
			if _, err := p.expect(tokLBrace); err != nil {
				return err
			}
			elseFrames, err := p.compileBody(syms)
			if err != nil {
				return err
			}
			*deferred = append(*deferred, deferredBody{
				frames:     elseFrames,
				checkFrame: checkFrame,
				slot:       elseSlot,
			})
		} else {
			p.unget(tok2)
		}
	} else {
		// Plain else block
		p.unget(tok)
		if _, err := p.expect(tokLBrace); err != nil {
			return err
		}
		elseFrames, err := p.compileBody(syms)
		if err != nil {
			return err
		}
		// For == with plain else: if_larger and if_smaller both go to else
		*deferred = append(*deferred, deferredBody{
			frames:     elseFrames,
			checkFrame: checkFrame,
			slot:       checkLarger,
		})
		*deferred = append(*deferred, deferredBody{
			frames:     elseFrames,
			checkFrame: checkFrame,
			slot:       checkSmaller,
		})
	}

	return nil
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

// compileVarInit compiles the right-hand side of a var/let declaration.
// The '=' has already been consumed. Accepts a number literal, a function call,
// or a type constructor (with optional & operator).
func (p *parser) compileVarInit(nameTok token, mutable bool, b *frameBuilder, comment string, syms *symbolTable) error {
	rhsTok, err := p.next()
	if err != nil {
		return err
	}

	if rhsTok.kind == tokNumber {
		num, _ := strconv.Atoi(rhsTok.val)
		// Check for & after number
		peek, err := p.next()
		if err != nil {
			return err
		}
		if peek.kind == tokAmpersand {
			return p.errorf(peek.pos, "number literal cannot be left side of '&' (use a type constructor)")
		}
		p.unget(peek)
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true
		f := map[string]any{
			"op": "set_number",
			"2":  map[string]any{"num": num},
			"3":  nameTok.val,
		}
		setComment(f, comment)
		b.emit(f)
		return nil
	}

	if rhsTok.kind == tokIdent && isConstructor(rhsTok.val) {
		p.unget(rhsTok)
		val, err := p.parseConstructorForTarget(nameTok.val, syms, b, comment)
		if err != nil {
			return err
		}
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true
		if val != nil {
			// Compile-time literal: emit set_reg
			f := map[string]any{
				"op": "set_reg",
				"1":  val,
				"2":  nameTok.val,
			}
			setComment(f, comment)
			b.emit(f)
		}
		// If val is nil, runtime frames already emitted targeting nameTok.val
		return nil
	}

	if rhsTok.kind == tokIdent && rhsTok.val == "instruction" {
		rawFrame, err := p.parseInstruction()
		if err != nil {
			return err
		}
		if !frameHasReturnSlot(rawFrame) {
			return p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
		}
		if err := p.checkInstructionDirections(rawFrame, syms, rhsTok.pos); err != nil {
			return err
		}
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true
		resolved := resolveInstructionFrame(rawFrame, []any{nameTok.val}, nil, nil, comment)
		b.emit(resolved)
		return nil
	}

	if rhsTok.kind == tokIdent {
		fn := p.fns[rhsTok.val]
		if fn == nil {
			return p.errorf(rhsTok.pos, "unknown function %q", rhsTok.val)
		}
		if !fn.hasReturn() {
			return p.errorf(rhsTok.pos, "function %q has no return value", rhsTok.val)
		}
		syms.vars[nameTok.val] = varInfo{mutable: mutable}
		syms.usedVars[nameTok.val] = true
		args, kwArgs, err := p.parseFnCallArgs(fn, rhsTok, syms, b, comment)
		if err != nil {
			return err
		}
		if err := p.checkCallDirections(fn, rhsTok.val, args, kwArgs, syms, rhsTok.pos); err != nil {
			return err
		}
		return p.expandCall(rhsTok.val, args, kwArgs, []any{nameTok.val}, b, rhsTok.pos, comment, syms.usedVars)
	}

	return p.errorf(rhsTok.pos, "expected number, function call, or constructor after '=', got %s", rhsTok.describe())
}

// compileMultiReturn compiles a multi-return binding list.
// The first binding (firstTok) and the comma after it have already been consumed.
// If firstDiscard is true, the first binding is a discard (_).
// Otherwise, firstTok is a name with the given firstMutable mutability.
//
// Parsing continues with: binding (',' binding)* '=' fnCall args
// binding ::= '_' | 'let' ident | 'var' ident | ident
//
// Modifiers (let/var) are sticky — bare idents inherit the active modifier.
// '_' does not change the active modifier. Bare idents with no active modifier
// assign to existing variables.
func (p *parser) compileMultiReturn(firstTok token, firstMutable, firstDiscard bool, b *frameBuilder, comment string, syms *symbolTable) error {
	type multiBinding struct {
		name    string // variable name, or "" for discard
		discard bool
		newVar  bool // true if this declares a new variable
		mutable bool // only meaningful if newVar
		pos     int  // token position for error reporting
	}

	var bindings []multiBinding
	if firstDiscard {
		bindings = append(bindings, multiBinding{discard: true, pos: firstTok.pos})
	} else {
		bindings = append(bindings, multiBinding{
			name:    firstTok.val,
			newVar:  true,
			mutable: firstMutable,
			pos:     firstTok.pos,
		})
	}

	// Track the active modifier: -1 = none, 0 = let (immutable), 1 = var (mutable)
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
			return err
		}

		if tok.kind == tokEquals {
			break
		}

		if tok.kind != tokIdent {
			return p.errorf(tok.pos, "expected identifier, '_', 'let', 'var', or '=' in binding list, got %s", tok.describe())
		}

		switch tok.val {
		case "_":
			bindings = append(bindings, multiBinding{discard: true, pos: tok.pos})
			// _ does not change active modifier
		case "let":
			activeModifier = 0
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			bindings = append(bindings, multiBinding{
				name:    nameTok.val,
				newVar:  true,
				mutable: false,
				pos:     nameTok.pos,
			})
		case "var":
			activeModifier = 1
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			bindings = append(bindings, multiBinding{
				name:    nameTok.val,
				newVar:  true,
				mutable: true,
				pos:     nameTok.pos,
			})
		default:
			// Bare identifier
			if activeModifier >= 0 {
				// Sticky modifier: new variable with inherited mutability
				bindings = append(bindings, multiBinding{
					name:    tok.val,
					newVar:  true,
					mutable: activeModifier == 1,
					pos:     tok.pos,
				})
			} else {
				// No active modifier: assign to existing variable
				bindings = append(bindings, multiBinding{
					name: tok.val,
					pos:  tok.pos,
				})
			}
		}

		// Expect comma or equals after each binding
		sep, err := p.next()
		if err != nil {
			return err
		}
		if sep.kind == tokEquals {
			break
		}
		if sep.kind != tokComma {
			return p.errorf(sep.pos, "expected ',' or '=' in binding list, got %s", sep.describe())
		}
	}

	// Parse the RHS: function call or instruction
	calleeTok, err := p.expect(tokIdent)
	if err != nil {
		return err
	}

	if calleeTok.val == "instruction" {
		rawFrame, err := p.parseInstruction()
		if err != nil {
			return err
		}
		if err := p.checkInstructionDirections(rawFrame, syms, calleeTok.pos); err != nil {
			return err
		}
		retCount := frameReturnCount(rawFrame)
		if retCount == 0 {
			return p.errorf(calleeTok.pos, "instruction has no return slots (@N); cannot assign its result")
		}
		if len(bindings) > retCount {
			return p.errorf(calleeTok.pos, "too many bindings (%d) for instruction which returns %d values", len(bindings), retCount)
		}
		// Validate and register new bindings
		for _, bind := range bindings {
			if bind.discard {
				continue
			}
			if bind.newVar {
				if err := p.checkVarName(bind.name, syms, bind.pos); err != nil {
					return err
				}
			}
		}
		// Build retVals slice
		retVals := make([]any, len(bindings))
		for i, bind := range bindings {
			if bind.discard {
				retVals[i] = false
			} else if bind.newVar {
				retVals[i] = bind.name
			} else {
				target, err := p.resolveAssignTarget(bind.name, syms, bind.pos, false)
				if err != nil {
					return err
				}
				retVals[i] = target
			}
		}
		// Register new variables
		for _, bind := range bindings {
			if bind.newVar {
				syms.vars[bind.name] = varInfo{mutable: bind.mutable}
				syms.usedVars[bind.name] = true
			}
		}
		resolved := resolveInstructionFrame(rawFrame, retVals, nil, nil, comment)
		b.emit(resolved)
		return nil
	}

	fn := p.fns[calleeTok.val]
	if fn == nil {
		return p.errorf(calleeTok.pos, "unknown function %q", calleeTok.val)
	}
	if !fn.hasReturn() {
		return p.errorf(calleeTok.pos, "function %q has no return value", calleeTok.val)
	}
	if len(bindings) > fn.returnCount() {
		return p.errorf(calleeTok.pos, "too many bindings (%d) for function %q which returns %d values", len(bindings), calleeTok.val, fn.returnCount())
	}

	// Validate and register new bindings
	for _, bind := range bindings {
		if bind.discard {
			continue
		}
		if bind.newVar {
			if err := p.checkVarName(bind.name, syms, bind.pos); err != nil {
				return err
			}
		}
	}

	// Build retVals slice
	retVals := make([]any, len(bindings))
	for i, bind := range bindings {
		if bind.discard {
			retVals[i] = false
		} else if bind.newVar {
			retVals[i] = bind.name
		} else {
			// Existing variable assignment
			target, err := p.resolveAssignTarget(bind.name, syms, bind.pos, false)
			if err != nil {
				return err
			}
			retVals[i] = target
		}
	}

	// Register new variables in symbol table after validation
	for _, bind := range bindings {
		if bind.newVar {
			syms.vars[bind.name] = varInfo{mutable: bind.mutable}
			syms.usedVars[bind.name] = true
		}
	}

	args, kwArgs, err := p.parseFnCallArgs(fn, calleeTok, syms, b, comment)
	if err != nil {
		return err
	}
	if err := p.checkCallDirections(fn, calleeTok.val, args, kwArgs, syms, calleeTok.pos); err != nil {
		return err
	}
	return p.expandCall(calleeTok.val, args, kwArgs, retVals, b, calleeTok.pos, comment, syms.usedVars)
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
