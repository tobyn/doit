package compiler

import (
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
)

// Compile reads doit source from r and compiles it into a codec Object.
func Compile(r io.Reader, stdlib fs.FS) (*codec.Object, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return CompileString(string(data), stdlib)
}

// CompileString compiles doit source into a codec Object.
func CompileString(src string, stdlib fs.FS) (*codec.Object, error) {
	fns, err := parseStdlib(stdlib)
	if err != nil {
		return nil, fmt.Errorf("stdlib: %w", err)
	}
	p := &parser{src: src, fns: fns}
	return p.parseBehavior()
}

// --- Stdlib ---

type fnDef struct {
	params []string
	frame  map[string]any // the instruction template, stored as parsed
}

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

// --- Scanner ---

type tokenKind byte

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokLBrace
	tokRBrace
	tokColon
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	kind tokenKind
	val  string
	pos  int
}

type parser struct {
	src string
	pos int
	fns map[string]*fnDef
}

func (p *parser) errorf(pos int, format string, args ...any) error {
	line, col := 1, 1
	for i := 0; i < pos && i < len(p.src); i++ {
		if p.src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return fmt.Errorf("%d:%d: %s", line, col, fmt.Sprintf(format, args...))
}

func (p *parser) skipWhitespaceAndComments() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			p.pos++
		} else if c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/' {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		} else if c == '#' {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		} else {
			break
		}
	}
}

func (p *parser) next() (token, error) {
	p.skipWhitespaceAndComments()
	if p.pos >= len(p.src) {
		return token{kind: tokEOF, pos: p.pos}, nil
	}

	start := p.pos
	c := p.src[p.pos]
	switch {
	case c == '{':
		p.pos++
		return token{tokLBrace, "{", start}, nil
	case c == '}':
		p.pos++
		return token{tokRBrace, "}", start}, nil
	case c == ':':
		p.pos++
		return token{tokColon, ":", start}, nil
	case c == '(':
		p.pos++
		return token{tokLParen, "(", start}, nil
	case c == ')':
		p.pos++
		return token{tokRParen, ")", start}, nil
	case c == ',':
		p.pos++
		return token{tokComma, ",", start}, nil
	case c == '"':
		return p.scanString()
	case isIdentStart(c):
		return p.scanIdent(), nil
	default:
		return token{}, p.errorf(start, "unexpected character %q", c)
	}
}

func (p *parser) scanString() (token, error) {
	start := p.pos
	p.pos++ // skip opening quote
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		p.pos++
		if c == '"' {
			return token{tokString, b.String(), start}, nil
		}
		if c == '\\' {
			if p.pos >= len(p.src) {
				return token{}, p.errorf(p.pos-1, "unterminated escape sequence")
			}
			esc := p.src[p.pos]
			p.pos++
			switch esc {
			case '"', '\\':
				b.WriteByte(esc)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return token{}, p.errorf(p.pos-1, "unknown escape \\%c", esc)
			}
		} else {
			b.WriteByte(c)
		}
	}
	return token{}, p.errorf(start, "unterminated string")
}

func (p *parser) scanIdent() token {
	start := p.pos
	for p.pos < len(p.src) && isIdentCont(p.src[p.pos]) {
		p.pos++
	}
	return token{tokIdent, p.src[start:p.pos], start}
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

// --- Parser ---

func (p *parser) expect(kind tokenKind) (token, error) {
	tok, err := p.next()
	if err != nil {
		return tok, err
	}
	if tok.kind != kind {
		return tok, p.errorf(tok.pos, "unexpected %s", tok.describe())
	}
	return tok, nil
}

func (t token) describe() string {
	switch t.kind {
	case tokEOF:
		return "end of file"
	case tokIdent:
		return fmt.Sprintf("keyword %q", t.val)
	case tokString:
		return fmt.Sprintf("string %q", t.val)
	case tokLBrace:
		return "'{'"
	case tokRBrace:
		return "'}'"
	case tokColon:
		return "':'"
	case tokLParen:
		return "'('"
	case tokRParen:
		return "')'"
	case tokComma:
		return "','"
	}
	return t.val
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

func (p *parser) parseBehavior() (*codec.Object, error) {
	tok, err := p.expect(tokIdent)
	if err != nil {
		return nil, err
	}
	if tok.val != "behavior" {
		return nil, p.errorf(tok.pos, "expected 'behavior', got %q", tok.val)
	}

	if _, err := p.expect(tokIdent); err != nil {
		return nil, err
	}
	if _, err := p.expect(tokLBrace); err != nil {
		return nil, err
	}

	value := map[string]any{}
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

		switch tok.val {
		case "name":
			str, err := p.expect(tokString)
			if err != nil {
				return nil, err
			}
			value["name"] = str.val
		case "instruction":
			instr, err := p.parseInstruction()
			if err != nil {
				return nil, err
			}
			value[strconv.Itoa(frame)] = instr
			frame++
		default:
			// Look up stdlib function
			fn := p.fns[tok.val]
			if fn == nil {
				return nil, p.errorf(tok.pos, "unknown statement %q", tok.val)
			}

			// Parse arguments (string literals, one per parameter)
			args := make([]string, len(fn.params))
			for i := range fn.params {
				str, err := p.expect(tokString)
				if err != nil {
					return nil, err
				}
				args[i] = str.val
			}

			// Build parameter map
			paramMap := map[string]string{}
			for i, name := range fn.params {
				paramMap[name] = args[i]
			}

			// Inline the instruction: substitute parameters
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

			value[strconv.Itoa(frame)] = instr
			frame++
		}
	}

	return &codec.Object{Type: codec.Behavior, Value: value}, nil
}
