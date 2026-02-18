package compiler

import (
	"fmt"
	"io/fs"
	"strconv"

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

func parseStdlibFile(src string, fns map[string]*fnDef) error {
	p := &parser{src: src}
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
		if _, err := p.expect(tokLParen); err != nil {
			return err
		}

		var params []string
		for {
			tok, err := p.next()
			if err != nil {
				return err
			}
			if tok.kind == tokRParen {
				break
			}
			if len(params) > 0 {
				if tok.kind != tokComma {
					return p.errorf(tok.pos, "expected ',' or ')', got %s", tok.describe())
				}
				tok, err = p.expect(tokIdent)
				if err != nil {
					return err
				}
			}
			if tok.kind != tokIdent {
				return p.errorf(tok.pos, "expected parameter name, got %s", tok.describe())
			}
			params = append(params, tok.val)
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

func (p *parser) parseFile() (*codec.Object, error) {
	// Pass 1: collect user-defined function definitions
	if err := p.collectUserFns(); err != nil {
		return nil, err
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
			return p.parseBehaviorBody()
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
			if _, err := p.expect(tokIdent); err != nil {
				return err
			}
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
	if _, err := p.expect(tokLParen); err != nil {
		return err
	}

	var params []string
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		if tok.kind == tokRParen {
			break
		}
		if len(params) > 0 {
			if tok.kind != tokComma {
				return p.errorf(tok.pos, "expected ',' or ')', got %s", tok.describe())
			}
			tok, err = p.expect(tokIdent)
			if err != nil {
				return err
			}
		}
		if tok.kind != tokIdent {
			return p.errorf(tok.pos, "expected parameter name, got %s", tok.describe())
		}
		params = append(params, tok.val)
	}

	if _, err := p.expect(tokLBrace); err != nil {
		return err
	}

	var body []fnBodyCall
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

		callee := p.fns[tok.val]
		if callee == nil {
			return p.errorf(tok.pos, "unknown function %q", tok.val)
		}

		args := make([]fnBodyArg, len(callee.params))
		for i := range callee.params {
			argTok, err := p.next()
			if err != nil {
				return err
			}
			switch argTok.kind {
			case tokString:
				args[i] = fnBodyArg{val: argTok.val}
			case tokIdent:
				args[i] = fnBodyArg{isIdent: true, val: argTok.val}
			default:
				return p.errorf(argTok.pos, "expected string or identifier, got %s", argTok.describe())
			}
		}

		body = append(body, fnBodyCall{name: tok.val, args: args})
	}

	p.fns[nameTok.val] = &fnDef{params: params, body: body}
	return nil
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
		if tok.kind != tokIdent {
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
		default:
			return nil, p.errorf(valTok.pos, "expected string or identifier, got %s", valTok.describe())
		}
	}
	return frame, nil
}

func (p *parser) expandCall(name string, args []string, value map[string]any, frame *int, pos int) error {
	fn := p.fns[name]
	if fn == nil {
		return p.errorf(pos, "unknown statement %q", name)
	}

	paramMap := map[string]string{}
	for i, pname := range fn.params {
		paramMap[pname] = args[i]
	}

	if fn.frame != nil {
		instr := make(map[string]any, len(fn.frame))
		for k, v := range fn.frame {
			if s, ok := v.(string); ok {
				if arg, ok := paramMap[s]; ok {
					instr[k] = arg
					continue
				}
			}
			instr[k] = v
		}
		value[strconv.Itoa(*frame)] = instr
		*frame++
		return nil
	}

	for _, call := range fn.body {
		resolvedArgs := make([]string, len(call.args))
		for i, arg := range call.args {
			if arg.isIdent {
				if val, ok := paramMap[arg.val]; ok {
					resolvedArgs[i] = val
				} else {
					resolvedArgs[i] = arg.val
				}
			} else {
				resolvedArgs[i] = arg.val
			}
		}
		if err := p.expandCall(call.name, resolvedArgs, value, frame, pos); err != nil {
			return err
		}
	}
	return nil
}
