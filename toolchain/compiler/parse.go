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

		if tok.kind != tokIdent || tok.val != "instruction" {
			return p.errorf(tok.pos, "expected 'instruction' or '}', got %s", tok.describe())
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
	var ret []string
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

		// Handle return statement: return ident
		if tok.val == "return" {
			retTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			ret = []string{retTok.val}
			continue
		}

		// Handle let statements in fn bodies: let varName = fnCall args...
		if tok.val == "let" {
			varTok, err := p.expect(tokIdent)
			if err != nil {
				return err
			}
			if _, err := p.expect(tokEquals); err != nil {
				return err
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
			retArg := fnBodyArg{isIdent: true, val: varTok.val}
			call.retArg = &retArg
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

	p.fns[nameTok.val] = &fnDef{params: params, ret: ret, body: body}
	return nil
}

// parseFnBodyArgValue parses a single argument value in a function body call.
// Accepts strings, identifiers, numbers, null, and $register references.
func (p *parser) parseFnBodyArgValue() (fnBodyArg, error) {
	tok, err := p.next()
	if err != nil {
		return fnBodyArg{}, err
	}
	switch tok.kind {
	case tokString:
		return fnBodyArg{val: tok.val}, nil
	case tokNumber:
		num, _ := strconv.Atoi(tok.val)
		return fnBodyArg{literal: map[string]any{"num": num}}, nil
	case tokIdent:
		if tok.val == "localize" {
			resolved, err := p.parseLocalize()
			if err != nil {
				return fnBodyArg{}, err
			}
			return fnBodyArg{val: resolved}, nil
		}
		if tok.val == "null" {
			return fnBodyArg{literal: false}, nil
		}
		if strings.HasPrefix(tok.val, "$") {
			if reg, ok := unitRegisters[tok.val]; ok {
				return fnBodyArg{literal: reg}, nil
			}
			return fnBodyArg{}, p.errorf(tok.pos, "unknown unit register %q", tok.val)
		}
		return fnBodyArg{isIdent: true, val: tok.val}, nil
	default:
		return fnBodyArg{}, p.errorf(tok.pos, "expected argument value, got %s", tok.describe())
	}
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
			if n != 1 {
				return nil, p.errorf(numTok.pos, "only @1 is supported (single return value)")
			}
			frame[key] = returnSlot(n)
		default:
			return nil, p.errorf(valTok.pos, "expected string, identifier, or @N, got %s", valTok.describe())
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

func (p *parser) expandCall(name string, args []any, kwArgs map[string]any, retVal any, b *frameBuilder, pos int, comment string) error {
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

	if len(fn.ret) > 0 {
		if retVal != nil {
			paramMap[fn.ret[0]] = retVal
		} else {
			paramMap[fn.ret[0]] = false
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
			if _, ok := v.(returnSlot); ok {
				if retVal != nil {
					instr[nativeKey] = retVal
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
		if comment != "" {
			instr["cmt"] = comment
		}
		b.emit(instr)
		return nil
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
		var resolvedRet any
		if call.retArg != nil {
			resolvedRet = resolveBodyArg(*call.retArg, paramMap)
		}
		callComment := call.comment
		if callComment == "" {
			callComment = comment
		}
		if err := p.expandCall(call.name, resolvedArgs, resolvedKwArgs, resolvedRet, b, pos, callComment); err != nil {
			return err
		}
	}
	return nil
}
