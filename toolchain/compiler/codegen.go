package compiler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
	"golang.org/x/text/language"
)

func (p *parser) parseBehaviorBody(behaviorID string) (*codec.Object, error) {
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	value := map[string]any{}
	b := &frameBuilder{}

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
			instr, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			if comment != "" {
				instr["cmt"] = comment
			}
			b.emit(instr)

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
			f := map[string]any{
				"op": "set_number",
				"2":  map[string]any{"num": num},
				"3":  nameTok.val,
			}
			if comment != "" {
				f["cmt"] = comment
			}
			b.emit(f)

		case "loop":
			checkFrame, err := p.compileLoop(b)
			if err != nil {
				return nil, err
			}
			if checkFrame >= 0 {
				breakTargetFrame = checkFrame + 1
				resumeFrame = b.pos()
				b.seek(breakTargetFrame)
			}

		case "if":
			if err := p.compileIfStmt(b, &deferred, comment); err != nil {
				return nil, err
			}

		case "while":
			if err := p.compileWhile(b, comment); err != nil {
				return nil, err
			}

		default:
			if err := p.compileDefaultStatement(tok, b, comment); err != nil {
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

	if _, exists := value["name"]; !exists {
		value["name"] = behaviorID
	}

	b.finalize(value)
	return &codec.Object{Type: codec.Behavior, Value: value}, nil
}

// compileLoop compiles a loop body. It returns the check frame index (or -1
// if the loop contains no if/break).
func (p *parser) compileLoop(b *frameBuilder) (int, error) {
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
			cf, err := p.compileIfBreak(b, comment)
			if err != nil {
				return -1, err
			}
			checkFrame = cf

		default:
			if err := p.compileDefaultStatement(tok, b, comment); err != nil {
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
func (p *parser) compileIfBreak(b *frameBuilder, comment string) (int, error) {
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

	f := map[string]any{
		"op": "check_number",
		"3":  lhsTok.val,
		"4":  map[string]any{"num": rhsNum},
	}
	if comment != "" {
		f["cmt"] = comment
	}
	checkFrame := b.emit(f)

	// Reserve the next frame for the break target (filled by caller).
	b.emit(nil)

	// Set if_smaller to skip past break target to the next loop body frame.
	f["2"] = frameRef(b.pos())

	return checkFrame, nil
}

// compileWhile compiles `while ident <= number { body }`.
// It emits a check_number and the body. The body's last instruction loops back
// to the check, and the check's if_larger slot exits to the continuation.
func (p *parser) compileWhile(b *frameBuilder, comment string) error {
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
	check := map[string]any{
		"op": "check_number",
		"3":  varTok.val,
		"4":  map[string]any{"num": limitNum},
	}
	if comment != "" {
		check["cmt"] = comment
	}
	checkFrame := b.emit(check)

	// Compile body.
	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	bodyFrames, err := p.compileBody()
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
	check["1"] = frameRef(b.pos())

	return nil
}

// compileDefaultStatement compiles a function call or compound assignment.
func (p *parser) compileDefaultStatement(tok token, b *frameBuilder, comment string) error {
	// Peek to distinguish function call from compound assignment
	tok2, err := p.next()
	if err != nil {
		return err
	}

	if tok2.kind == tokPlusPlus {
		f := map[string]any{
			"op": "add",
			"1":  tok.val,
			"2":  map[string]any{"num": 1},
			"3":  tok.val,
		}
		if comment != "" {
			f["cmt"] = comment
		}
		b.emit(f)
		return nil
	}

	if tok2.kind == tokEquals {
		numTok, err := p.expect(tokNumber)
		if err != nil {
			return err
		}
		num, _ := strconv.Atoi(numTok.val)
		f := map[string]any{
			"op": "set_number",
			"2":  map[string]any{"num": num},
			"3":  tok.val,
		}
		if comment != "" {
			f["cmt"] = comment
		}
		b.emit(f)
		return nil
	}

	if tok2.kind == tokPlusEquals {
		numTok, err := p.expect(tokNumber)
		if err != nil {
			return err
		}
		num, _ := strconv.Atoi(numTok.val)
		f := map[string]any{
			"op": "add",
			"1":  tok.val,
			"2":  map[string]any{"num": num},
			"3":  tok.val,
		}
		if comment != "" {
			f["cmt"] = comment
		}
		b.emit(f)
		return nil
	}

	// Function call — push back the token we peeked
	p.unget(tok2)

	fn := p.fns[tok.val]
	if fn == nil {
		return p.errorf(tok.pos, "unknown statement %q", tok.val)
	}

	// Parse positional args (string literals only at behavior level)
	posCount := fn.positionalCount()
	args := make([]string, posCount)
	for i := 0; i < posCount; i++ {
		str, err := p.expect(tokString)
		if err != nil {
			return err
		}
		args[i] = str.val
	}

	// Parse optional keyword args: , keyword: value
	// First check for extra positional args that should be keyword args.
	var kwArgs map[string]string
	peek, err := p.next()
	if err != nil {
		return err
	}
	if peek.kind == tokString && fn.positionalCount() < len(fn.params) {
		return p.errorf(peek.pos,
			"too many positional arguments for %s (remaining parameters are keyword-only)", tok.val)
	}
	if peek.kind == tokComma {
		kwArgs = map[string]string{}
		for {
			kwTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			kw := fn.keywordByName(kwTok.val)
			if kw == nil {
				return p.errorf(kwTok.pos, "unknown keyword argument %q", kwTok.val)
			}
			if _, exists := kwArgs[kwTok.val]; exists {
				return p.errorf(kwTok.pos, "duplicate keyword argument %q", kwTok.val)
			}
			if _, err := p.expect(tokColon); err != nil {
				return err
			}
			valTok, err := p.next()
			if err != nil {
				return err
			}
			switch valTok.kind {
			case tokString, tokIdent:
				kwArgs[kwTok.val] = valTok.val
			default:
				return p.errorf(valTok.pos, "expected string or identifier, got %s", valTok.describe())
			}

			// Check for another comma
			next, err := p.next()
			if err != nil {
				return err
			}
			if next.kind != tokComma {
				p.unget(next)
				break
			}
		}
	} else {
		p.unget(peek)
	}

	return p.expandCall(tok.val, args, kwArgs, b, tok.pos, comment)
}

// --- If statement compilation ---

// compileBody compiles a brace-delimited block into a slice of frames.
// The opening '{' must already be consumed.
func (p *parser) compileBody() ([]map[string]any, error) {
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
		if err := p.compileDefaultStatement(tok, b, comment); err != nil {
			return nil, err
		}
	}
	return b.frames, nil
}

// compileIfStmt compiles an if / else-if / else statement.
// The "if" keyword has already been consumed.
func (p *parser) compileIfStmt(b *frameBuilder, deferred *[]deferredBody, comment string) error {
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

	check := map[string]any{
		"op": "check_number",
		"3":  lhsTok.val,
		"4":  map[string]any{"num": rhsNum},
	}
	if comment != "" {
		check["cmt"] = comment
	}
	checkFrame := b.emit(check)

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
			slot:       "2",
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
			elseFrames, err := p.compileBody()
			if err != nil {
				return err
			}
			*deferred = append(*deferred, deferredBody{
				frames:     elseFrames,
				checkFrame: checkFrame,
				slot:       "2",
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
			if err := p.compileElseClauses(checkFrame, deferred); err != nil {
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
			slot:       "1",
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
			slot = "1"
		case tokLess:
			slot = "2"
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
			if slot == "1" {
				elseSlot = "2"
			} else {
				elseSlot = "1"
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
			p.unget(tok2)
		}
	} else {
		// Plain else block
		p.unget(tok)
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
			slot:       "1",
		})
		*deferred = append(*deferred, deferredBody{
			frames:     elseFrames,
			checkFrame: checkFrame,
			slot:       "2",
		})
	}

	return nil
}

// parseName parses the value of an @name attribute. It handles both the simple
// string form and the localized block form.
func (p *parser) parseName() (string, error) {
	tok, err := p.next()
	if err != nil {
		return "", err
	}
	if tok.kind == tokString {
		return tok.val, nil
	}
	if tok.kind == tokLBrace {
		return p.resolveLocalizedName()
	}
	return "", p.errorf(tok.pos, "expected string or '{' after @name, got %s", tok.describe())
}

// resolveLocalizedName parses locale/string pairs until '}' and returns the
// best match for p.locale. If p.locale is empty, the first entry is used.
func (p *parser) resolveLocalizedName() (string, error) {
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
		return "", fmt.Errorf("empty @name block")
	}

	if p.locale == "" {
		return entries[0].name, nil
	}

	tags := make([]language.Tag, len(entries))
	for i, e := range entries {
		tags[i] = language.Make(strings.ReplaceAll(e.locale, "_", "-"))
	}

	desired := language.Make(strings.ReplaceAll(p.locale, "_", "-"))
	matcher := language.NewMatcher(tags)
	_, idx, _ := matcher.Match(desired)
	return entries[idx].name, nil
}
