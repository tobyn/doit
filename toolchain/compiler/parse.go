package compiler

import (
	"fmt"
	"io/fs"
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

		// Peek: if next is an identifier, this is a keyword param
		peek, err := p.next()
		if err != nil {
			return nil, err
		}
		if peek.kind == tokIdent {
			// keyword param: tok is keyword, peek is variable name
			seenKeyword = true
			params = append(params, paramDef{
				name: peek.val, keyword: tok.val,
			})
		} else {
			// positional param
			p.unget(peek)
			if seenKeyword {
				return nil, p.errorf(tok.pos, "positional parameter after keyword parameter")
			}
			params = append(params, paramDef{name: tok.val})
		}
	}
	return params, nil
}

func parseStdlibFile(src string, fns map[string]*fnDef) error {
	p := &parser{scanner: scanner{src: src}}
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

		// Parse body — look for an instruction statement or empty body
		tok, err = p.next()
		if err != nil {
			return err
		}

		if tok.kind == tokRBrace {
			// Empty body — skip this function
			continue
		}

		// Support both "instruction ..." and "return instruction ..."
		if tok.kind == tokIdent && tok.val == "return" {
			tok, err = p.next()
			if err != nil {
				return err
			}
		}

		if tok.kind != tokIdent || tok.val != "instruction" {
			return p.errorf(tok.pos, "expected 'instruction', 'return', or '}', got %s", tok.describe())
		}

		frame, err := p.parseInstruction()
		if err != nil {
			return err
		}

		if _, err := p.expect(tokRBrace); err != nil {
			return err
		}

		fns[nameTok.val] = &fnDef{
			params: params,
			frame:  frame,
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

	var body []fnBodyCall
	var rets []string
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

		// Handle return statement: return item (',' item)*
		// Items can be identifiers, number literals, or null.
		// Literals are desugared into synthetic body calls.
		if tok.val == "return" {
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
				call, err := p.parseFnBodyCall(callee, calleeTok)
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
				call.retArgs = retArgs
				call.comment = comment
				body = append(body, call)
				continue
			}
			// Single return: let varName = fnCall args...
			if sep.kind != tokEquals {
				return p.errorf(sep.pos, "expected ',' or '=' after let identifier, got %s", sep.describe())
			}
			calleeTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			callee := p.fns[calleeTok.val]
			if callee == nil {
				return p.errorf(calleeTok.pos, "unknown function %q", calleeTok.val)
			}
			if !callee.hasReturn() {
				return p.errorf(calleeTok.pos, "function %q has no return value", calleeTok.val)
			}
			call, err := p.parseFnBodyCall(callee, calleeTok)
			if err != nil {
				return err
			}
			call.retArgs = []fnBodyArg{{isIdent: true, val: varTok.val}}
			call.comment = comment
			body = append(body, call)
			continue
		}

		callee := p.fns[tok.val]
		if callee == nil {
			return p.errorf(tok.pos, "unknown function %q", tok.val)
		}

		call, err := p.parseFnBodyCall(callee, tok)
		if err != nil {
			return err
		}
		call.comment = comment
		body = append(body, call)
	}

	p.fns[nameTok.val] = &fnDef{params: params, rets: rets, body: body}
	return nil
}

// parseFnBodyArgValue parses a single argument value in a function body call.
// Accepts strings, identifiers, numbers, null, $register references,
// type constructors (compile-time only), and the & operator.
func (p *parser) parseFnBodyArgValue() (fnBodyArg, error) {
	tok, err := p.next()
	if err != nil {
		return fnBodyArg{}, err
	}
	var base fnBodyArg
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
				return fnBodyArg{}, err
			}
			base = fnBodyArg{val: resolved}
		} else if tok.val == "null" {
			base = fnBodyArg{literal: false}
		} else if isConstructor(tok.val) {
			lit, err := p.parseFnBodyConstructor(tok)
			if err != nil {
				return fnBodyArg{}, err
			}
			base = fnBodyArg{literal: lit}
		} else if strings.HasPrefix(tok.val, "$") {
			if reg, ok := unitRegisters[tok.val]; ok {
				base = fnBodyArg{literal: reg}
			} else {
				return fnBodyArg{}, p.errorf(tok.pos, "unknown unit register %q", tok.val)
			}
		} else {
			base = fnBodyArg{isIdent: true, val: tok.val}
		}
	default:
		return fnBodyArg{}, p.errorf(tok.pos, "expected argument value, got %s", tok.describe())
	}

	// Check for & operator
	peek, err := p.next()
	if err != nil {
		return fnBodyArg{}, err
	}
	if peek.kind == tokAmpersand {
		return p.parseFnBodyAmpersand(base, tok.pos)
	}
	p.unget(peek)
	return base, nil
}

// parseFnBodyConstructor parses a type constructor in a function body.
// Only compile-time (literal) args are allowed.
func (p *parser) parseFnBodyConstructor(nameTok token) (any, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return nil, p.errorf(nameTok.pos, "expected '(' after %s", nameTok.val)
	}

	switch nameTok.val {
	case "Item":
		return p.parseFnBodySimpleConstructor("", nameTok.pos)
	case "Component":
		return p.parseFnBodySimpleConstructor("c_", nameTok.pos)
	case "Technology":
		return p.parseFnBodySimpleConstructor("t_", nameTok.pos)
	case "Value":
		return p.parseFnBodySimpleConstructor("v_", nameTok.pos)
	case "Coordinate":
		return p.parseFnBodyCoordinateConstructor(nameTok.pos)
	}
	return nil, p.errorf(nameTok.pos, "unknown constructor %q", nameTok.val)
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
// Both args must be numeric literals.
func (p *parser) parseFnBodyCoordinateConstructor(pos int) (any, error) {
	xTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if xTok.kind != tokNumber {
		if xTok.kind == tokIdent && !isConstructor(xTok.val) {
			return nil, p.errorf(xTok.pos, "Coordinate() in function bodies requires literal arguments, got variable %q", xTok.val)
		}
		return nil, p.errorf(xTok.pos, "expected number, got %s", xTok.describe())
	}
	x, _ := strconv.Atoi(xTok.val)

	if _, err := p.expect(tokComma); err != nil {
		return nil, err
	}

	yTok, err := p.next()
	if err != nil {
		return nil, err
	}
	if yTok.kind != tokNumber {
		if yTok.kind == tokIdent && !isConstructor(yTok.val) {
			return nil, p.errorf(yTok.pos, "Coordinate() in function bodies requires literal arguments, got variable %q", yTok.val)
		}
		return nil, p.errorf(yTok.pos, "expected number, got %s", yTok.describe())
	}
	y, _ := strconv.Atoi(yTok.val)

	if _, err := p.expect(tokRParen); err != nil {
		return nil, err
	}
	return map[string]any{"coord": map[string]any{"x": x, "y": y}}, nil
}

// parseFnBodyAmpersand handles & in function bodies. Both sides must be compile-time.
func (p *parser) parseFnBodyAmpersand(base fnBodyArg, basePos int) (fnBodyArg, error) {
	rhsTok, err := p.next()
	if err != nil {
		return fnBodyArg{}, err
	}

	if base.isIdent {
		return fnBodyArg{}, p.errorf(basePos, "'&' in function bodies requires compile-time values, got variable %q", base.val)
	}
	if base.literal == nil {
		// string literal — not meaningful for &
		return fnBodyArg{}, p.errorf(basePos, "string literal cannot be left side of '&'")
	}

	// Parse RHS — must be a number literal
	if rhsTok.kind != tokNumber {
		return fnBodyArg{}, p.errorf(rhsTok.pos, "expected number after '&', got %s", rhsTok.describe())
	}
	num, _ := strconv.Atoi(rhsTok.val)

	baseMap, ok := base.literal.(map[string]any)
	if !ok {
		return fnBodyArg{}, p.errorf(basePos, "cannot use '&' with this value")
	}

	result := make(map[string]any, len(baseMap)+1)
	for k, v := range baseMap {
		result[k] = v
	}
	result["num"] = num
	return fnBodyArg{literal: result}, nil
}

// parseFnBodyCall parses the positional and keyword arguments for a function
// call in a fn body. Returns a fnBodyCall with name and args/kwArgs populated
// (but not comment or retArg — the caller sets those).
func (p *parser) parseFnBodyCall(callee *fnDef, calleeTok token) (fnBodyCall, error) {
	posCount := callee.positionalCount()
	args := make([]fnBodyArg, posCount)
	for i := 0; i < posCount; i++ {
		arg, err := p.parseFnBodyArgValue()
		if err != nil {
			return fnBodyCall{}, err
		}
		args[i] = arg
	}

	// Parse optional keyword args: , keyword: value
	var kwArgs map[string]fnBodyArg
	peek, err := p.next()
	if err != nil {
		return fnBodyCall{}, err
	}
	if (peek.kind == tokString || peek.kind == tokIdent) && callee.positionalCount() < len(callee.params) {
		if peek.kind == tokString {
			return fnBodyCall{}, p.errorf(peek.pos,
				"too many positional arguments for %s (remaining parameters are keyword-only)", calleeTok.val)
		}
		p.unget(peek)
	} else if peek.kind == tokComma {
		kwArgs = map[string]fnBodyArg{}
		for {
			kwTok, err := p.expect(tokIdent)
			if err != nil {
				return fnBodyCall{}, err
			}
			kw := callee.keywordByName(kwTok.val)
			if kw == nil {
				return fnBodyCall{}, p.errorf(kwTok.pos, "unknown keyword argument %q", kwTok.val)
			}
			if _, exists := kwArgs[kwTok.val]; exists {
				return fnBodyCall{}, p.errorf(kwTok.pos, "duplicate keyword argument %q", kwTok.val)
			}
			if _, err := p.expect(tokColon); err != nil {
				return fnBodyCall{}, err
			}
			val, err := p.parseFnBodyArgValue()
			if err != nil {
				return fnBodyCall{}, err
			}
			kwArgs[kwTok.val] = val

			next, err := p.next()
			if err != nil {
				return fnBodyCall{}, err
			}
			if next.kind != tokComma {
				p.unget(next)
				break
			}
		}
	} else {
		p.unget(peek)
	}

	return fnBodyCall{name: calleeTok.val, args: args, kwArgs: kwArgs}, nil
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
		kwVars := fn.keywordVarNames()
		instr := make(map[string]any, len(fn.frame))
		for k, v := range fn.frame {
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
				if arg, ok := paramMap[s]; ok {
					instr[nativeKey] = arg
					continue
				}
				if kwVars[s] {
					continue // omit absent keyword param
				}
			}
			instr[nativeKey] = v
		}
		setComment(instr, comment)
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
