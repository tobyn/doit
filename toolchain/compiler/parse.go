package compiler

import (
	"fmt"
	"io/fs"
	"maps"
	"strconv"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
)

// --- Stdlib ---

func parseStdlib(stdlib fs.FS) (map[string]*fnDef, error) {
	matches, err := fs.Glob(stdlib, "*.doit")
	if err != nil {
		return nil, err
	}

	fns := map[string]*fnDef{}
	for _, path := range matches {
		data, err := fs.ReadFile(stdlib, path)
		if err != nil {
			return nil, err
		}
		if err := parseStdlibFile(string(data), fns); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return fns, nil
}

func isDirection(val string) bool {
	return val == "in" || val == "out" || val == "inout"
}

func (p *parser) parseParamList() ([]paramDef, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return nil, err
	}
	var params []paramDef
	seenKeyword := false
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRParen {
			break
		}
		if len(params) > 0 {
			if tok.kind != tokComma {
				return nil, p.errorf(tok.pos, "expected ',' or ')', got %s", tok.describe())
			}
			tok, err = p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected parameter name, got %s", tok.describe())
		}

		// Check for direction annotation (in, out, inout)
		direction := ""
		if isDirection(tok.val) {
			direction = tok.val
			tok, err = p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
		}

		// Peek: if next is an identifier, this is a keyword param
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent {
			// keyword param: tok is keyword, peek is variable name
			seenKeyword = true
			params = append(params, paramDef{
				name: peek.val, keyword: tok.val, direction: direction,
			})
		} else {
			// positional param
			p.unget(peek)
			if seenKeyword {
				return nil, p.errorf(tok.pos, "positional parameter after keyword parameter")
			}
			params = append(params, paramDef{name: tok.val, direction: direction})
		}
	}
	return params, nil
}

func parseStdlibFile(src string, fns map[string]*fnDef) error {
	p := &parser{scanner: scanner{src: src}, fns: fns}
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			return nil
		}
		if tok.kind != tokIdent || tok.val != "fn" {
			return p.errorf(tok.pos, "expected 'fn', got %s", tok.describe())
		}
		if err := p.parseUserFn(); err != nil {
			return err
		}
	}
}

// --- Two-pass file parsing ---

func (p *parser) parseBehaviorID() (token, error) {
	tok, err := p.next()
	if err != nil {
		return tok, err
	}
	if tok.kind != tokIdent && tok.kind != tokString {
		return tok, p.errorf(tok.pos, "expected behavior id, got %s", tok.describe())
	}
	return tok, nil
}

func (p *parser) parseFile() (*codec.Object, error) {
	// Pass 1: collect user-defined function definitions
	if err := p.collectUserFns(); err != nil {
		return nil, err
	}

	// Validate behavior selection
	switch {
	case len(p.behaviorIDs) == 0:
		return nil, fmt.Errorf("source contains no behavior declarations")
	case p.target == "" && len(p.behaviorIDs) == 1:
		p.target = p.behaviorIDs[0] // auto-select
	case p.target == "":
		return nil, fmt.Errorf("source contains multiple behaviors; use -b to select one: %s",
			strings.Join(p.behaviorIDs, ", "))
	default:
		found := false
		for _, id := range p.behaviorIDs {
			if id == p.target {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("behavior %q not found; available behaviors: %s",
				p.target, strings.Join(p.behaviorIDs, ", "))
		}
	}

	// Pass 2: find and compile the behavior
	p.pos = 0
	p.ungot = nil
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokEOF {
			return nil, nil
		}
		if tok.kind != tokIdent {
			return nil, p.errorf(tok.pos, "expected declaration, got %s", tok.describe())
		}
		switch tok.val {
		case "behavior":
			idTok, err := p.parseBehaviorID()
			if err != nil {
				return nil, err
			}
			if idTok.val == p.target {
				return p.parseBehaviorBody(idTok.val)
			}
			// Skip non-matching behavior
			if err := p.skipBraceBlock(); err != nil {
				return nil, err
			}
		case "private":
			fnTok, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			if fnTok.val != "fn" {
				return nil, p.errorf(fnTok.pos, "expected 'fn' after 'private', got %q", fnTok.val)
			}
			if err := p.skipFnDef(); err != nil {
				return nil, err
			}
		case "fn":
			if err := p.skipFnDef(); err != nil {
				return nil, err
			}
		default:
			return nil, p.errorf(tok.pos, "expected 'behavior', 'fn', or 'private', got %q", tok.val)
		}
	}
}

func (p *parser) collectUserFns() error {
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			return nil
		}
		if tok.kind != tokIdent {
			return p.errorf(tok.pos, "expected declaration, got %s", tok.describe())
		}
		switch tok.val {
		case "behavior":
			idTok, err := p.parseBehaviorID()
			if err != nil {
				return err
			}
			p.behaviorIDs = append(p.behaviorIDs, idTok.val)
			if err := p.skipBraceBlock(); err != nil {
				return err
			}
		case "private":
			fnTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			if fnTok.val != "fn" {
				return p.errorf(fnTok.pos, "expected 'fn' after 'private', got %q", fnTok.val)
			}
			if err := p.parseUserFn(); err != nil {
				return err
			}
		case "fn":
			if err := p.parseUserFn(); err != nil {
				return err
			}
		default:
			return p.errorf(tok.pos, "expected 'behavior', 'fn', or 'private', got %q", tok.val)
		}
	}
}

func (p *parser) parseUserFn() error {
	nameTok, err := p.expect(tokIdent)
	if err != nil {
		return err
	}

	params, err := p.parseParamList()
	if err != nil {
		return err
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}

	// Build direction maps for enforcement in fn body
	paramDirs := map[string]string{} // param name -> effective direction
	for _, pd := range params {
		paramDirs[pd.name] = pd.effectiveDirection()
	}
	letVars := map[string]bool{} // tracks let-declared locals in fn body

	var body []fnBodyCall
	var rets []string
	synthIdx := 0
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind != tokIdent {
			return p.errorf(tok.pos, "expected function call or '}', got %s", tok.describe())
		}
		comment := p.docComment

		// Handle bare instruction statement in fn body
		if tok.val == "instruction" {
			frame, err := p.parseInstruction()
			if err != nil {
				return err
			}
			if err := p.checkFnBodyInstructionDirections(frame, paramDirs, tok.pos); err != nil {
				return err
			}
			body = append(body, fnBodyCall{frame: frame, comment: comment})
			continue
		}

		// Handle return statement: return instruction OR return item (',' item)*
		// Items can be identifiers, number literals, or null.
		// Literals are desugared into synthetic body calls.
		if tok.val == "return" {
			retPeek, err := p.next()
			if err != nil {
				return err
			}
			if retPeek.kind == tokIdent && retPeek.val == "instruction" {
				frame, err := p.parseInstruction()
				if err != nil {
					return err
				}
				if err := p.checkFnBodyInstructionDirections(frame, paramDirs, retPeek.pos); err != nil {
					return err
				}
				// Extract @N return slots and create synthetic ret names
				maxSlot := 0
				for _, v := range frame {
					if rs, ok := v.(returnSlot); ok {
						if int(rs) > maxSlot {
							maxSlot = int(rs)
						}
					}
				}
				rets = nil
				modifiedFrame := maps.Clone(frame)
				for i := 1; i <= maxSlot; i++ {
					synthName := "@ret" + strconv.Itoa(i)
					rets = append(rets, synthName)
				}
				// Replace returnSlot values with synth names in frame
				for k, v := range modifiedFrame {
					if rs, ok := v.(returnSlot); ok {
						modifiedFrame[k] = "@ret" + strconv.Itoa(int(rs))
					}
				}
				body = append(body, fnBodyCall{frame: modifiedFrame, comment: comment})
				continue
			}
			p.unget(retPeek)

			rets = nil
			retIdx := 0
			for {
				retTok, err := p.next()
				if err != nil {
					return err
				}
				switch retTok.kind {
				case tokIdent:
					if retTok.val == "null" {
						retIdx++
						synthName := "@ret" + strconv.Itoa(retIdx)
						body = append(body, fnBodyCall{
							name:    "set_reg",
							args:    []fnBodyArg{{literal: false}},
							retArgs: []fnBodyArg{{isIdent: true, val: synthName}},
						})
						rets = append(rets, synthName)
					} else {
						rets = append(rets, retTok.val)
					}
				case tokNumber:
					retIdx++
					synthName := "@ret" + strconv.Itoa(retIdx)
					num, _ := strconv.Atoi(retTok.val)
					body = append(body, fnBodyCall{
						name:    "set_number",
						args:    []fnBodyArg{{literal: false}, {literal: map[string]any{"num": num}}},
						retArgs: []fnBodyArg{{isIdent: true, val: synthName}},
					})
					rets = append(rets, synthName)
				default:
					return p.errorf(retTok.pos, "expected identifier, number, or null in return list, got %s", retTok.describe())
				}
				// Check for comma (more items) or end of list
				sep, err := p.next()
				if err != nil {
					return err
				}
				if sep.kind != tokComma {
					p.unget(sep)
					break
				}
			}
			continue
		}

		// Handle let statements in fn bodies:
		//   let varName = fnCall args...
		//   let varName = Constructor(args...)
		//   let a, b, _ = fnCall args...   (multi-return)
		if tok.val == "let" {
			varTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			// Peek for comma (multi-return) vs equals (single return)
			sep, err := p.next()
			if err != nil {
				return err
			}
			if sep.kind == tokComma {
				// Multi-return: let a, b, _ = fnCall args...
				type binding struct {
					name    string // "" for discard (_)
					discard bool
				}
				bindings := []binding{{name: varTok.val}}
				for {
					nameTok, err := p.next()
					if err != nil {
						return err
					}
					if nameTok.kind != tokIdent {
						return p.errorf(nameTok.pos, "expected identifier or '_' in binding list, got %s", nameTok.describe())
					}
					if nameTok.val == "_" {
						bindings = append(bindings, binding{discard: true})
					} else {
						bindings = append(bindings, binding{name: nameTok.val})
					}
					next, err := p.next()
					if err != nil {
						return err
					}
					if next.kind == tokEquals {
						break
					}
					if next.kind != tokComma {
						return p.errorf(next.pos, "expected ',' or '=' in binding list, got %s", next.describe())
					}
				}
				calleeTok, err := p.expect(tokIdent)
				if err != nil {
					return err
				}
				if calleeTok.val == "instruction" {
					frame, err := p.parseInstruction()
					if err != nil {
						return err
					}
					retCount := frameReturnCount(frame)
					if retCount == 0 {
						return p.errorf(calleeTok.pos, "instruction has no return slots (@N); cannot assign its result")
					}
					if len(bindings) > retCount {
						return p.errorf(calleeTok.pos, "too many bindings (%d) for instruction which returns %d values", len(bindings), retCount)
					}
					if err := p.checkFnBodyInstructionDirections(frame, paramDirs, calleeTok.pos); err != nil {
					return err
				}
				modifiedFrame := maps.Clone(frame)
					for k, v := range modifiedFrame {
						if rs, ok := v.(returnSlot); ok {
							idx := int(rs) - 1
							if idx < len(bindings) {
								if bindings[idx].discard {
									modifiedFrame[k] = "@discard"
								} else {
									modifiedFrame[k] = bindings[idx].name
								}
							} else {
								modifiedFrame[k] = "@discard"
							}
						}
					}
					for _, bind := range bindings {
						if !bind.discard {
							letVars[bind.name] = true
						}
					}
					body = append(body, fnBodyCall{frame: modifiedFrame, comment: comment})
					continue
				}
				callee := p.fns[calleeTok.val]
				if callee == nil {
					return p.errorf(calleeTok.pos, "unknown function %q", calleeTok.val)
				}
				if !callee.hasReturn() {
					return p.errorf(calleeTok.pos, "function %q has no return value", calleeTok.val)
				}
				if len(bindings) > callee.returnCount() {
					return p.errorf(calleeTok.pos, "too many bindings (%d) for function %q which returns %d values", len(bindings), calleeTok.val, callee.returnCount())
				}
				calls, err := p.parseFnBodyCall(callee, calleeTok, &synthIdx, paramDirs, letVars)
				if err != nil {
					return err
				}
				retArgs := make([]fnBodyArg, len(bindings))
				for i, b := range bindings {
					if b.discard {
						retArgs[i] = fnBodyArg{literal: false}
					} else {
						retArgs[i] = fnBodyArg{isIdent: true, val: b.name}
					}
				}
				calls[len(calls)-1].retArgs = retArgs
				calls[len(calls)-1].comment = comment
				for _, bind := range bindings {
					if !bind.discard {
						letVars[bind.name] = true
					}
				}
				body = append(body, calls...)
				continue
			}
			// Single return: let varName = fnCall args... OR let varName = Constructor(args...)
			if sep.kind != tokEquals {
				return p.errorf(sep.pos, "expected ',' or '=' after let identifier, got %s", sep.describe())
			}
			rhsTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}

			// Check for instruction RHS
			if rhsTok.val == "instruction" {
				frame, err := p.parseInstruction()
				if err != nil {
					return err
				}
				if err := p.checkFnBodyInstructionDirections(frame, paramDirs, rhsTok.pos); err != nil {
					return err
				}
				if !frameHasReturnSlot(frame) {
					return p.errorf(rhsTok.pos, "instruction has no return slots (@N); cannot assign its result")
				}
				// Replace returnSlot(1) with varTok.val in frame
				modifiedFrame := maps.Clone(frame)
				for k, v := range modifiedFrame {
					if rs, ok := v.(returnSlot); ok && int(rs) == 1 {
						modifiedFrame[k] = varTok.val
					}
				}
				letVars[varTok.val] = true
				body = append(body, fnBodyCall{frame: modifiedFrame, comment: comment})
				continue
			}


			// Check for constructor RHS
			if isConstructor(rhsTok.val) {
				arg, synthCalls, err := p.parseFnBodyConstructor(rhsTok, &synthIdx)
				if err != nil {
					return err
				}
				// Check for & operator
				peek, err := p.next()
				if err != nil {
					return err
				}
				if peek.kind == tokAmpersand {
					result, ampCalls, err := p.parseFnBodyAmpersand(arg, rhsTok.pos, &synthIdx)
					if err != nil {
						return err
					}
					synthCalls = append(synthCalls, ampCalls...)
					arg = result
				} else {
					p.unget(peek)
				}

				if arg.isIdent {
					// Runtime constructor — synthetic calls already emitted,
					// the last one writes into a temp var. We need to either
					// rewrite that last call's retArg to target varTok.val directly,
					// or add a copy. Rewriting is cleaner.
					lastCall := &synthCalls[len(synthCalls)-1]
					lastCall.retArgs = []fnBodyArg{{isIdent: true, val: varTok.val}}
					lastCall.comment = comment
					body = append(body, synthCalls...)
				} else {
					// Compile-time literal — emit set_reg
					body = append(body, synthCalls...)
					body = append(body, fnBodyCall{
						name:    "set_reg",
						args:    []fnBodyArg{arg},
						retArgs: []fnBodyArg{{isIdent: true, val: varTok.val}},
						comment: comment,
					})
				}
				letVars[varTok.val] = true
				continue
			}

			callee := p.fns[rhsTok.val]
			if callee == nil {
				return p.errorf(rhsTok.pos, "unknown function %q", rhsTok.val)
			}
			if !callee.hasReturn() {
				return p.errorf(rhsTok.pos, "function %q has no return value", rhsTok.val)
			}
			calls, err := p.parseFnBodyCall(callee, rhsTok, &synthIdx, paramDirs, letVars)
			if err != nil {
				return err
			}
			calls[len(calls)-1].retArgs = []fnBodyArg{{isIdent: true, val: varTok.val}}
			calls[len(calls)-1].comment = comment
			letVars[varTok.val] = true
			body = append(body, calls...)
			continue
		}

		callee := p.fns[tok.val]
		if callee == nil {
			return p.errorf(tok.pos, "unknown function %q", tok.val)
		}

		calls, err := p.parseFnBodyCall(callee, tok, &synthIdx, paramDirs, letVars)
		if err != nil {
			return err
		}
		calls[len(calls)-1].comment = comment
		body = append(body, calls...)
	}

	// Pure-instruction optimization: if the function body is a single
	// instruction frame, promote it to fnDef.frame for the fast direct-frame
	// expansion path. This makes stdlib functions parsed through parseUserFn
	// get the same efficient expansion as the old dedicated parseStdlibFile.
	if len(body) == 1 && body[0].frame != nil {
		frame := body[0].frame
		canPromote := true
		// Check that all string values are either the op, param names, or ret names
		opVal, _ := frame["op"].(string)
		for _, v := range frame {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if s == opVal {
				continue
			}
			isParam := false
			for _, pd := range params {
				if pd.name == s {
					isParam = true
					break
				}
			}
			if isParam {
				continue
			}
			isRet := false
			for _, r := range rets {
				if r == s {
					isRet = true
					break
				}
			}
			if isRet {
				continue
			}
			canPromote = false
			break
		}
		if canPromote {
			// Rebuild frame with returnSlots replacing ret names
			promoted := maps.Clone(frame)
			for k, v := range promoted {
				if s, ok := v.(string); ok {
					for i, r := range rets {
						if s == r {
							promoted[k] = returnSlot(i + 1)
							break
						}
					}
				}
			}
			p.fns[nameTok.val] = &fnDef{params: params, frame: promoted}
			return nil
		}
	}


	p.fns[nameTok.val] = &fnDef{params: params, rets: rets, body: body}
	return nil
}

// parseFnBodyArgValue parses a single argument value in a function body call.
// Accepts strings, identifiers, numbers, null, $register references,
// type constructors, and the & operator. Returns the argument value plus
// any synthetic body calls needed for runtime constructors/&.
func (p *parser) parseFnBodyArgValue(synthIdx *int) (fnBodyArg, []fnBodyCall, error) {
	tok, err := p.next()
	if err != nil {
		return fnBodyArg{}, nil, err
	}
	var base fnBodyArg
	var synthCalls []fnBodyCall
	switch tok.kind {
	case tokString:
		base = fnBodyArg{val: tok.val}
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		base = fnBodyArg{literal: map[string]any{"num": num}}
	case tokIdent:
		if tok.val == "localize" {
			resolved, err := p.parseLocalize()
			if err != nil {
				return fnBodyArg{}, nil, err
			}
			base = fnBodyArg{val: resolved}
		} else if tok.val == "null" {
			base = fnBodyArg{literal: false}
		} else if isConstructor(tok.val) {
			arg, calls, err := p.parseFnBodyConstructor(tok, synthIdx)
			if err != nil {
				return fnBodyArg{}, nil, err
			}
			base = arg
			synthCalls = append(synthCalls, calls...)
		} else if strings.HasPrefix(tok.val, "$") {
			if reg, ok := unitRegisters[tok.val]; ok {
				base = fnBodyArg{literal: reg}
			} else {
				return fnBodyArg{}, nil, p.errorf(tok.pos, "unknown unit register %q", tok.val)
			}
		} else {
			base = fnBodyArg{isIdent: true, val: tok.val}
		}
	default:
		return fnBodyArg{}, nil, p.errorf(tok.pos, "expected argument value, got %s", tok.describe())
	}

	// Check for & operator
	peek, err := p.next()
	if err != nil {
		return fnBodyArg{}, nil, err
	}
	if peek.kind == tokAmpersand {
		result, ampCalls, err := p.parseFnBodyAmpersand(base, tok.pos, synthIdx)
		if err != nil {
			return fnBodyArg{}, nil, err
		}
		synthCalls = append(synthCalls, ampCalls...)
		return result, synthCalls, nil
	}
	p.unget(peek)
	return base, synthCalls, nil
}

// parseFnBodyConstructor parses a type constructor in a function body.
// Returns the argument value plus any synthetic body calls for runtime constructors.
func (p *parser) parseFnBodyConstructor(nameTok token, synthIdx *int) (fnBodyArg, []fnBodyCall, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return fnBodyArg{}, nil, p.errorf(nameTok.pos, "expected '(' after %s", nameTok.val)
	}

	switch nameTok.val {
	case "Item":
		lit, err := p.parseFnBodySimpleConstructor("", nameTok.pos)
		if err != nil {
			return fnBodyArg{}, nil, err
		}
		return fnBodyArg{literal: lit}, nil, nil
	case "Component":
		lit, err := p.parseFnBodySimpleConstructor("c_", nameTok.pos)
		if err != nil {
			return fnBodyArg{}, nil, err
		}
		return fnBodyArg{literal: lit}, nil, nil
	case "Technology":
		lit, err := p.parseFnBodySimpleConstructor("t_", nameTok.pos)
		if err != nil {
			return fnBodyArg{}, nil, err
		}
		return fnBodyArg{literal: lit}, nil, nil
	case "Value":
		lit, err := p.parseFnBodySimpleConstructor("v_", nameTok.pos)
		if err != nil {
			return fnBodyArg{}, nil, err
		}
		return fnBodyArg{literal: lit}, nil, nil
	case "Coordinate":
		return p.parseFnBodyCoordinateConstructor(nameTok.pos, synthIdx)
	}
	return fnBodyArg{}, nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
}

// parseFnBodySimpleConstructor parses Item/Component/Technology/Value("id") in fn bodies.
func (p *parser) parseFnBodySimpleConstructor(prefix string, pos int) (any, error) {
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
	return map[string]any{"id": prefix + argTok.val}, nil
}

// parseFnBodyCoordinateConstructor parses Coordinate(x, y) in fn bodies.
// With literal args, returns a compile-time value. With variable args,
// emits a synthetic combine_coordinate call.
func (p *parser) parseFnBodyCoordinateConstructor(pos int, synthIdx *int) (fnBodyArg, []fnBodyCall, error) {
	xArg, xCalls, err := p.parseFnBodyArgValue(synthIdx)
	if err != nil {
		return fnBodyArg{}, nil, err
	}
	if _, err := p.expect(tokComma); err != nil {
		return fnBodyArg{}, nil, err
	}
	yArg, yCalls, err := p.parseFnBodyArgValue(synthIdx)
	if err != nil {
		return fnBodyArg{}, nil, err
	}
	if _, err := p.expect(tokRParen); err != nil {
		return fnBodyArg{}, nil, err
	}

	// Check if both are compile-time numeric literals
	if xArg.literal != nil && yArg.literal != nil && !xArg.isIdent && !yArg.isIdent {
		xMap, xIsMap := xArg.literal.(map[string]any)
		yMap, yIsMap := yArg.literal.(map[string]any)
		if xIsMap && yIsMap {
			xNum, xHasNum := xMap["num"]
			yNum, yHasNum := yMap["num"]
			if xHasNum && yHasNum && len(xMap) == 1 && len(yMap) == 1 {
				lit := map[string]any{"coord": map[string]any{"x": xNum, "y": yNum}}
				var allCalls []fnBodyCall
				allCalls = append(allCalls, xCalls...)
				allCalls = append(allCalls, yCalls...)
				return fnBodyArg{literal: lit}, allCalls, nil
			}
		}
	}

	// Runtime: emit combine_coordinate synthetic call
	*synthIdx++
	synthName := "@ctor" + strconv.Itoa(*synthIdx)
	var allCalls []fnBodyCall
	allCalls = append(allCalls, xCalls...)
	allCalls = append(allCalls, yCalls...)
	allCalls = append(allCalls, fnBodyCall{
		name:    "combine_coordinate",
		args:    []fnBodyArg{xArg, yArg},
		retArgs: []fnBodyArg{{isIdent: true, val: synthName}},
	})
	return fnBodyArg{isIdent: true, val: synthName}, allCalls, nil
}

// parseFnBodyAmpersand handles & in function bodies. If both sides are
// compile-time, merges the num field. Otherwise emits a synthetic set_number call.
func (p *parser) parseFnBodyAmpersand(base fnBodyArg, basePos int, synthIdx *int) (fnBodyArg, []fnBodyCall, error) {
	if !base.isIdent && base.literal == nil {
		// string literal — not meaningful for &
		return fnBodyArg{}, nil, p.errorf(basePos, "string literal cannot be left side of '&'")
	}

	rhsArg, rhsCalls, err := p.parseFnBodyArgValue(synthIdx)
	if err != nil {
		return fnBodyArg{}, nil, err
	}

	// Check if both are compile-time values
	if !base.isIdent && base.literal != nil && !rhsArg.isIdent && rhsArg.literal != nil {
		baseMap, baseIsMap := base.literal.(map[string]any)
		rhsMap, rhsIsMap := rhsArg.literal.(map[string]any)
		if baseIsMap && rhsIsMap {
			rhsNum, rhsHasNum := rhsMap["num"]
			if rhsHasNum && len(rhsMap) == 1 {
				result := make(map[string]any, len(baseMap)+1)
				for k, v := range baseMap {
					result[k] = v
				}
				result["num"] = rhsNum
				return fnBodyArg{literal: result}, rhsCalls, nil
			}
		}
	}

	// Runtime: emit set_number synthetic call
	*synthIdx++
	synthName := "@ctor" + strconv.Itoa(*synthIdx)
	var allCalls []fnBodyCall
	allCalls = append(allCalls, rhsCalls...)
	allCalls = append(allCalls, fnBodyCall{
		name:    "set_number",
		args:    []fnBodyArg{base, rhsArg},
		retArgs: []fnBodyArg{{isIdent: true, val: synthName}},
	})
	return fnBodyArg{isIdent: true, val: synthName}, allCalls, nil
}

// fnBodyArgDir determines the effective direction of a function body argument.
func fnBodyArgDir(arg fnBodyArg, paramDirs map[string]string, letVars map[string]bool) string {
	if !arg.isIdent {
		return "in" // literal
	}
	if dir, ok := paramDirs[arg.val]; ok {
		return dir
	}
	if letVars[arg.val] {
		return "in"
	}
	return "inout"
}

// checkFnBodyCallDirections checks direction compatibility for each argument
// at a function call site within a fn body.
func (p *parser) checkFnBodyCallDirections(callee *fnDef, calleeName string, args []fnBodyArg, kwArgs map[string]fnBodyArg, paramDirs map[string]string, letVars map[string]bool, pos int) error {
	posIdx := 0
	for _, pd := range callee.params {
		calleeDir := pd.effectiveDirection()
		if pd.keyword == "" {
			if posIdx < len(args) {
				aDir := fnBodyArgDir(args[posIdx], paramDirs, letVars)
				if !canPass(calleeDir, aDir) {
					return p.errorf(pos, "cannot pass %s parameter to %s parameter %q of %s",
						aDir, calleeDir, pd.name, calleeName)
				}
			}
			posIdx++
		} else if kwArgs != nil {
			if val, ok := kwArgs[pd.keyword]; ok {
				aDir := fnBodyArgDir(val, paramDirs, letVars)
				if !canPass(calleeDir, aDir) {
					return p.errorf(pos, "cannot pass %s parameter to %s parameter %q of %s",
						aDir, calleeDir, pd.name, calleeName)
				}
			}
		}
	}
	return nil
}

// checkFnBodyInstructionDirections verifies that non-@N slots in an instruction
// frame within a fn body don't read from out-only parameters.
func (p *parser) checkFnBodyInstructionDirections(frame map[string]any, paramDirs map[string]string, pos int) error {
	for _, v := range frame {
		if _, ok := v.(returnSlot); ok {
			continue
		}
		if name, ok := v.(string); ok {
			if dir, ok := paramDirs[name]; ok && dir == "out" {
				return p.errorf(pos, "cannot read from output parameter %q in instruction input slot", name)
			}
		}
	}
	return nil
}

// parseFnBodyCall parses the positional and keyword arguments for a function
// call in a fn body. Returns the main call plus any synthetic setup calls
// needed for runtime constructors/&. The caller sets comment and retArgs on
// the last call (the main one).
func (p *parser) parseFnBodyCall(callee *fnDef, calleeTok token, synthIdx *int, paramDirs map[string]string, letVars map[string]bool) ([]fnBodyCall, error) {
	posCount := callee.positionalCount()
	args := make([]fnBodyArg, posCount)
	var synthCalls []fnBodyCall
	for i := 0; i < posCount; i++ {
		// Peek for direction annotation (in, out, inout)
		dirTok, err := p.next()
		if err != nil {
			return nil, err
		}
		annotation := ""
		annotationPos := dirTok.pos
		if dirTok.kind == tokIdent && isDirection(dirTok.val) {
			annotation = dirTok.val
		} else {
			p.unget(dirTok)
			annotationPos = dirTok.pos
		}

		pd := callee.positionalParam(i)
		if err := p.checkCallAnnotation(annotation, pd, calleeTok.val, annotationPos); err != nil {
			return nil, err
		}

		arg, calls, err := p.parseFnBodyArgValue(synthIdx)
		if err != nil {
			return nil, err
		}
		args[i] = arg
		synthCalls = append(synthCalls, calls...)
	}

	// Parse optional keyword args: , keyword: value
	var kwArgs map[string]fnBodyArg
	peek, err := p.next()
	if err != nil {
		return nil, err
	}
	if (peek.kind == tokString || peek.kind == tokIdent) && callee.positionalCount() < len(callee.params) {
		if peek.kind == tokString {
			return nil, p.errorf(peek.pos,
				"too many positional arguments for %s (remaining parameters are keyword-only)", calleeTok.val)
		}
		p.unget(peek)
	} else if peek.kind == tokComma {
		kwArgs = map[string]fnBodyArg{}
		for {
			// Read the first ident — could be a direction annotation or keyword name
			dirOrKw, err := p.expect(tokIdent)
			if err != nil {
				return nil, err
			}
			annotation := ""
			annotationPos := dirOrKw.pos
			if isDirection(dirOrKw.val) {
				annotation = dirOrKw.val
				dirOrKw, err = p.expect(tokIdent)
				if err != nil {
					return nil, err
				}
			}

			kw := callee.keywordByName(dirOrKw.val)
			if kw == nil {
				return nil, p.errorf(dirOrKw.pos, "unknown keyword argument %q", dirOrKw.val)
			}
			if _, exists := kwArgs[dirOrKw.val]; exists {
				return nil, p.errorf(dirOrKw.pos, "duplicate keyword argument %q", dirOrKw.val)
			}
			if err := p.checkCallAnnotation(annotation, kw, calleeTok.val, annotationPos); err != nil {
				return nil, err
			}
			if _, err := p.expect(tokColon); err != nil {
				return nil, err
			}
			val, calls, err := p.parseFnBodyArgValue(synthIdx)
			if err != nil {
				return nil, err
			}
			kwArgs[dirOrKw.val] = val
			synthCalls = append(synthCalls, calls...)

			next, err := p.next()
			if err != nil {
				return nil, err
			}
			if next.kind != tokComma {
				p.unget(next)
				break
			}
		}
	} else {
		p.unget(peek)
	}

	if err := p.checkFnBodyCallDirections(callee, calleeTok.val, args, kwArgs, paramDirs, letVars, calleeTok.pos); err != nil {
		return nil, err
	}

	result := append(synthCalls, fnBodyCall{name: calleeTok.val, args: args, kwArgs: kwArgs})
	return result, nil
}

func (p *parser) skipBraceBlock() error {
	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}
	depth := 1
	for depth > 0 {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokEOF {
			return p.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
		if tok.kind == tokLBrace {
			depth++
		}
		if tok.kind == tokRBrace {
			depth--
		}
	}
	return nil
}

func (p *parser) skipFnDef() error {
	if _, err := p.expect(tokIdent); err != nil {
		return err
	}
	if _, err := p.expect(tokLParen); err != nil {
		return err
	}
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokRParen {
			break
		}
	}
	return p.skipBraceBlock()
}

func (p *parser) parseInstruction() (map[string]any, error) {
	opTok, err := p.expect(tokString)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	frame := map[string]any{"op": opTok.val}
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokRBrace {
			break
		}
		if tok.kind != tokIdent && tok.kind != tokNumber {
			return nil, p.errorf(tok.pos, "expected field name or '}', got %s", tok.describe())
		}
		key := tok.val
		if _, err := p.expect(tokColon); err != nil {
			return nil, err
		}
		valTok, err := p.next()
		if err != nil {
			return nil, err
		}
		switch valTok.kind {
		case tokString, tokIdent:
			frame[key] = valTok.val
		case tokAt:
			numTok, err := p.expect(tokNumber)
			if err != nil {
				return nil, err
			}
			n, _ := strconv.Atoi(numTok.val)
			if n < 1 {
				return nil, p.errorf(numTok.pos, "@N return index must be >= 1, got @%d", n)
			}
			frame[key] = returnSlot(n)
		default:
			return nil, p.errorf(valTok.pos, "expected string, identifier, or @N, got %s", valTok.describe())
		}
	}

	// Validate that @N return slots form a contiguous sequence from @1.
	var maxSlot int
	slots := map[int]bool{}
	for _, v := range frame {
		if rs, ok := v.(returnSlot); ok {
			n := int(rs)
			slots[n] = true
			if n > maxSlot {
				maxSlot = n
			}
		}
	}
	for i := 1; i <= maxSlot; i++ {
		if !slots[i] {
			return nil, p.errorf(opTok.pos, "instruction %q has @%d but is missing @%d — return slots must be a contiguous sequence from @1", opTok.val, maxSlot, i)
		}
	}

	return frame, nil
}

func resolveBodyArg(arg fnBodyArg, paramMap map[string]any) any {
	if arg.literal != nil {
		return arg.literal
	}
	if arg.isIdent {
		if val, ok := paramMap[arg.val]; ok {
			return val
		}
		return arg.val // variable name string
	}
	return arg.val // string literal
}

func (p *parser) expandCall(name string, args []any, kwArgs map[string]any, retVals []any, b *frameBuilder, pos int, comment string, usedVars map[string]bool) error {
	fn := p.fns[name]
	if fn == nil {
		return p.errorf(pos, "unknown statement %q", name)
	}

	paramMap := map[string]any{}
	posIdx := 0
	for _, pd := range fn.params {
		if pd.keyword == "" {
			paramMap[pd.name] = args[posIdx]
			posIdx++
		} else if kwArgs != nil {
			if val, ok := kwArgs[pd.keyword]; ok {
				paramMap[pd.name] = val
			}
		}
	}

	for i, retName := range fn.rets {
		if retVals != nil && i < len(retVals) {
			paramMap[retName] = retVals[i]
		} else {
			paramMap[retName] = false
		}
	}

	if fn.frame != nil {
		instr := resolveInstructionFrame(fn.frame, retVals, paramMap, fn.keywordVarNames(), comment)
		b.emit(instr)
		return nil
	}

	// Pre-scan: collect internal variables and rename collisions.
	for _, call := range fn.body {
		for _, arg := range call.retArgs {
			if arg.isIdent {
				if _, mapped := paramMap[arg.val]; !mapped {
					uniqueName := allocUniqueVar(arg.val, usedVars)
					paramMap[arg.val] = uniqueName
				}
			}
		}
	}

	for _, call := range fn.body {
		if call.frame != nil {
			callComment := call.comment
			if callComment == "" {
				callComment = comment
			}
			resolved := resolveInstructionFrame(call.frame, nil, paramMap, nil, callComment)
			b.emit(resolved)
			continue
		}
		resolvedArgs := make([]any, len(call.args))
		for i, arg := range call.args {
			resolvedArgs[i] = resolveBodyArg(arg, paramMap)
		}
		resolvedKwArgs := map[string]any{}
		for kw, arg := range call.kwArgs {
			resolvedKwArgs[kw] = resolveBodyArg(arg, paramMap)
		}
		var resolvedRets []any
		if len(call.retArgs) > 0 {
			resolvedRets = make([]any, len(call.retArgs))
			for i, arg := range call.retArgs {
				resolvedRets[i] = resolveBodyArg(arg, paramMap)
			}
		}
		callComment := call.comment
		if callComment == "" {
			callComment = comment
		}
		if err := p.expandCall(call.name, resolvedArgs, resolvedKwArgs, resolvedRets, b, pos, callComment, usedVars); err != nil {
			return err
		}
	}
	return nil
}
