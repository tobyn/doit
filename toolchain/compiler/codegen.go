package compiler

import (
	"sort"
	"strconv"

	"github.com/tobyn/doit/toolchain/codec"
)

func (p *parser) parseBehaviorBody(behaviorID string) (*codec.Object, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	value := map[string]any{}
	frame := 0

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
				str, err := p.expect(tokString)
				if err != nil {
					return nil, err
				}
				value["name"] = str.val
			default:
				return nil, p.errorf(attr.pos, "unknown attribute @%s", attr.val)
			}
			continue
		}

		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected statement, got %s", tok.describe())
		}

		switch tok.val {
		case "instruction":
			instr, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			value[strconv.Itoa(frame)] = instr
			frame++

		case "var":
			nameTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tokEquals); err != nil {
				return nil, err
			}
			numTok, err := p.expect(tokNumber)
			if err != nil {
				return nil, err
			}
			num, _ := strconv.Atoi(numTok.val)
			value[strconv.Itoa(frame)] = map[string]any{
				"op": "set_number",
				"1":  map[string]any{"num": num},
				"2":  nameTok.val,
			}
			frame++

		case "loop":
			checkFrame, err := p.compileLoop(value, &frame)
			if err != nil {
				return nil, err
			}
			if checkFrame >= 0 {
				breakTargetFrame = checkFrame + 1
				resumeFrame = frame
				frame = breakTargetFrame
			}

		case "if":
			if err := p.compileIfStmt(value, &frame, &deferred); err != nil {
				return nil, err
			}

		case "while":
			if err := p.compileWhile(value, &frame); err != nil {
				return nil, err
			}

		default:
			if err := p.compileDefaultStatement(tok, value, &frame); err != nil {
				return nil, err
			}
		}

		// After emitting an instruction, check if we need to patch a break target
		if breakTargetFrame >= 0 && frame-1 == breakTargetFrame {
			instr := value[strconv.Itoa(breakTargetFrame)].(map[string]any)
			// Peek to see if there are more statements
			peek, err := p.next()
			if err != nil {
				return nil, err
			}
			if peek.kind == tokRBrace {
				// Last instruction in behavior; stop execution
				instr["next"] = false
				frame = resumeFrame
				p.ungot = &peek
			} else {
				// More instructions follow; skip over loop body frames
				instr["next"] = resumeFrame
				frame = resumeFrame
				p.ungot = &peek
			}
			breakTargetFrame = -1
			resumeFrame = -1
		}
	}

	// Emit deferred bodies after all main-line frames.
	mainFrameCount := frame
	if len(deferred) > 0 {
		// Prevent the last main-line frame from falling into deferred frames.
		if mainFrameCount > 0 {
			lastInstr := value[strconv.Itoa(mainFrameCount-1)].(map[string]any)
			if _, hasNext := lastInstr["next"]; !hasNext {
				lastInstr["next"] = false
			}
		}

		// Sort: reverse chronological by check frame, slot "0" before "1".
		sort.SliceStable(deferred, func(i, j int) bool {
			if deferred[i].checkFrame != deferred[j].checkFrame {
				return deferred[i].checkFrame > deferred[j].checkFrame
			}
			return deferred[i].slot < deferred[j].slot
		})

		for i := range deferred {
			d := &deferred[i]
			bodyFrame := frame
			for _, f := range d.frames {
				value[strconv.Itoa(frame)] = f
				frame++
			}
			// Set "next" on the body's last frame.
			lastBody := value[strconv.Itoa(frame-1)].(map[string]any)
			if d.continuation < mainFrameCount {
				lastBody["next"] = d.continuation + 1
			} else {
				lastBody["next"] = false
			}
			// Patch the check_number's branch slot.
			checkInstr := value[strconv.Itoa(d.checkFrame)].(map[string]any)
			checkInstr[d.slot] = bodyFrame + 1
		}
	}

	if _, exists := value["name"]; !exists {
		value["name"] = behaviorID
	}

	return &codec.Object{Type: codec.Behavior, Value: value}, nil
}

// compileLoop compiles a loop body. It returns the check frame index (or -1
// if the loop contains no if/break).
func (p *parser) compileLoop(value map[string]any, frame *int) (int, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return -1, err
	}

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

		switch tok.val {
		case "if":
			cf, err := p.compileIfBreak(value, frame)
			if err != nil {
				return -1, err
			}
			checkFrame = cf

		default:
			if err := p.compileDefaultStatement(tok, value, frame); err != nil {
				return -1, err
			}
		}
	}

	// Set next on last loop body instruction to jump back to check
	if checkFrame >= 0 {
		lastInstr := value[strconv.Itoa(*frame-1)].(map[string]any)
		lastInstr["next"] = checkFrame
	}

	return checkFrame, nil
}

// compileIfBreak compiles `if lhs >= rhs { break }` inside a loop body.
// It emits a check_number instruction and reserves the next frame for the
// break target. Returns the check frame index.
func (p *parser) compileIfBreak(value map[string]any, frame *int) (int, error) {
	lhsTok, err := p.expect(tokIdent)
	if err != nil {
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

	checkFrame := *frame
	value[strconv.Itoa(*frame)] = map[string]any{
		"op": "check_number",
		"1":  5, // >= comparison
		"2":  lhsTok.val,
		"3":  map[string]any{"num": rhsNum},
	}
	*frame++

	// Reserve the next frame for the break target (filled by caller)
	*frame++

	return checkFrame, nil
}

// compileWhile compiles `while ident <= number { body }`.
// It emits a check_number and the body. The body's last instruction loops back
// to the check, and the check's if_larger slot exits to the continuation.
func (p *parser) compileWhile(value map[string]any, frame *int) error {
	varTok, err := p.expect(tokIdent)
	if err != nil {
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
	checkFrame := *frame
	check := map[string]any{
		"op": "check_number",
		"2":  varTok.val,
		"3":  map[string]any{"num": limitNum},
	}
	value[strconv.Itoa(*frame)] = check
	*frame++

	// Compile body.
	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	bodyFrames, err := p.compileBody()
	if err != nil {
		return err
	}
	for _, f := range bodyFrames {
		value[strconv.Itoa(*frame)] = f
		*frame++
	}

	// Loop back: set "next" on the body's last instruction.
	lastBody := value[strconv.Itoa(*frame-1)].(map[string]any)
	lastBody["next"] = checkFrame + 1

	// Patch check's if_larger to exit to the continuation.
	check["0"] = *frame + 1

	return nil
}

// compileDefaultStatement compiles a function call or compound assignment.
func (p *parser) compileDefaultStatement(tok token, value map[string]any, frame *int) error {
	// Peek to distinguish function call from compound assignment
	tok2, err := p.next()
	if err != nil {
		return err
	}

	if tok2.kind == tokPlusPlus {
		value[strconv.Itoa(*frame)] = map[string]any{
			"op": "add",
			"0":  tok.val,
			"1":  map[string]any{"num": 1},
			"2":  tok.val,
		}
		*frame++
		return nil
	}

	if tok2.kind == tokEquals {
		numTok, err := p.expect(tokNumber)
		if err != nil {
			return err
		}
		num, _ := strconv.Atoi(numTok.val)
		value[strconv.Itoa(*frame)] = map[string]any{
			"op": "set_number",
			"1":  map[string]any{"num": num},
			"2":  tok.val,
		}
		*frame++
		return nil
	}

	if tok2.kind == tokPlusEquals {
		numTok, err := p.expect(tokNumber)
		if err != nil {
			return err
		}
		num, _ := strconv.Atoi(numTok.val)
		value[strconv.Itoa(*frame)] = map[string]any{
			"op": "add",
			"0":  tok.val,
			"1":  map[string]any{"num": num},
			"2":  tok.val,
		}
		*frame++
		return nil
	}

	// Function call — push back the token we peeked
	p.ungot = &tok2

	fn := p.fns[tok.val]
	if fn == nil {
		return p.errorf(tok.pos, "unknown statement %q", tok.val)
	}

	args := make([]string, len(fn.params))
	for i := range fn.params {
		str, err := p.expect(tokString)
		if err != nil {
			return err
		}
		args[i] = str.val
	}

	return p.expandCall(tok.val, args, value, frame, tok.pos)
}

// --- If statement compilation ---

// compileBody compiles a brace-delimited block into a slice of frames.
// The opening '{' must already be consumed.
func (p *parser) compileBody() ([]map[string]any, error) {
	tmp := map[string]any{}
	frame := 0
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
		if err := p.compileDefaultStatement(tok, tmp, &frame); err != nil {
			return nil, err
		}
	}
	frames := make([]map[string]any, frame)
	for i := 0; i < frame; i++ {
		frames[i] = tmp[strconv.Itoa(i)].(map[string]any)
	}
	return frames, nil
}

// compileIfStmt compiles an if / else-if / else statement.
// The "if" keyword has already been consumed.
func (p *parser) compileIfStmt(value map[string]any, frame *int, deferred *[]deferredBody) error {
	lhsTok, err := p.expect(tokIdent)
	if err != nil {
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

	checkFrame := *frame
	check := map[string]any{
		"op": "check_number",
		"2":  lhsTok.val,
		"3":  map[string]any{"num": rhsNum},
	}
	value[strconv.Itoa(*frame)] = check
	*frame++

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	bodyFrames, err := p.compileBody()
	if err != nil {
		return err
	}

	switch opTok.kind {
	case tokLess:
		// a < N: body when smaller. Deferred.
		*deferred = append(*deferred, deferredBody{
			frames:     bodyFrames,
			checkFrame: checkFrame,
			slot:       "1",
		})

	case tokGreaterEquals:
		// a >= N: body when larger or equal. Inline (both fall through).
		for _, f := range bodyFrames {
			value[strconv.Itoa(*frame)] = f
			*frame++
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
			elseFrames, err := p.compileBody()
			if err != nil {
				return err
			}
			*deferred = append(*deferred, deferredBody{
				frames:     elseFrames,
				checkFrame: checkFrame,
				slot:       "1",
			})
		} else {
			p.ungot = &tok
		}

	case tokDoubleEquals:
		// a == N: body when equal. Inline (falls through).
		for _, f := range bodyFrames {
			value[strconv.Itoa(*frame)] = f
			*frame++
		}
		// Parse else if / else
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokIdent && tok.val == "else" {
			if err := p.compileElseClauses(checkFrame, deferred); err != nil {
				return err
			}
		} else {
			p.ungot = &tok
		}

	case tokGreater:
		// a > N: body when larger. Deferred.
		*deferred = append(*deferred, deferredBody{
			frames:     bodyFrames,
			checkFrame: checkFrame,
			slot:       "0",
		})

	default:
		return p.errorf(opTok.pos, "unsupported comparison operator %s", opTok.describe())
	}

	// Set continuation on all deferred bodies from this if block.
	continuation := *frame
	for i := range *deferred {
		if (*deferred)[i].continuation == 0 && (*deferred)[i].checkFrame == checkFrame {
			(*deferred)[i].continuation = continuation
		}
	}

	return nil
}

// compileElseClauses compiles the else / else-if chain after an == condition.
func (p *parser) compileElseClauses(checkFrame int, deferred *[]deferredBody) error {
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
			slot = "0"
		case tokLess:
			slot = "1"
		default:
			return p.errorf(opTok.pos, "unsupported else-if operator %s", opTok.describe())
		}

		if _, err := p.expect(tokLBrace); err != nil {
			return err
		}
		frames, err := p.compileBody()
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
			if slot == "0" {
				elseSlot = "1"
			} else {
				elseSlot = "0"
			}
			if _, err := p.expect(tokLBrace); err != nil {
				return err
			}
			elseFrames, err := p.compileBody()
			if err != nil {
				return err
			}
			*deferred = append(*deferred, deferredBody{
				frames:     elseFrames,
				checkFrame: checkFrame,
				slot:       elseSlot,
			})
		} else {
			p.ungot = &tok2
		}
	} else {
		// Plain else block
		p.ungot = &tok
		if _, err := p.expect(tokLBrace); err != nil {
			return err
		}
		elseFrames, err := p.compileBody()
		if err != nil {
			return err
		}
		// For == with plain else: if_larger and if_smaller both go to else
		*deferred = append(*deferred, deferredBody{
			frames:     elseFrames,
			checkFrame: checkFrame,
			slot:       "0",
		})
		*deferred = append(*deferred, deferredBody{
			frames:     elseFrames,
			checkFrame: checkFrame,
			slot:       "1",
		})
	}

	return nil
}
