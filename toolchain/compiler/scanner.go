package compiler

import (
	"fmt"
	"strings"
)

// --- Scanner ---

type tokenKind byte

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokLBrace
	tokRBrace
	tokColon
	tokLParen
	tokRParen
	tokComma
	tokEquals
	tokDoubleEquals
	tokPlusEquals
	tokPlusPlus
	tokLess
	tokLessEquals
	tokGreater
	tokGreaterEquals
)

type token struct {
	kind tokenKind
	val  string
	pos  int
}

type parser struct {
	src         string
	pos         int
	fns         map[string]*fnDef
	ungot       *token
	target      string   // behavior ID to compile ("" = auto-select)
	behaviorIDs []string // collected during pass 1
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
	if p.ungot != nil {
		tok := *p.ungot
		p.ungot = nil
		return tok, nil
	}

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
	case c == '=' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '=':
		p.pos += 2
		return token{tokDoubleEquals, "==", start}, nil
	case c == '=':
		p.pos++
		return token{tokEquals, "=", start}, nil
	case c == '+' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '+':
		p.pos += 2
		return token{tokPlusPlus, "++", start}, nil
	case c == '+' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '=':
		p.pos += 2
		return token{tokPlusEquals, "+=", start}, nil
	case c == '>' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '=':
		p.pos += 2
		return token{tokGreaterEquals, ">=", start}, nil
	case c == '>':
		p.pos++
		return token{tokGreater, ">", start}, nil
	case c == '<' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '=':
		p.pos += 2
		return token{tokLessEquals, "<=", start}, nil
	case c == '<':
		p.pos++
		return token{tokLess, "<", start}, nil
	case c == '"':
		return p.scanString()
	case c >= '0' && c <= '9':
		return p.scanNumber(), nil
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

func (p *parser) scanNumber() token {
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	return token{tokNumber, p.src[start:p.pos], start}
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

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
	case tokNumber:
		return fmt.Sprintf("number %s", t.val)
	case tokEquals:
		return "'='"
	case tokDoubleEquals:
		return "'=='"
	case tokPlusPlus:
		return "'++'"
	case tokPlusEquals:
		return "'+='"
	case tokLess:
		return "'<'"
	case tokLessEquals:
		return "'<='"
	case tokGreater:
		return "'>'"
	case tokGreaterEquals:
		return "'>='"
	}
	return t.val
}
