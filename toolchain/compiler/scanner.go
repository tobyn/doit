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
	tokAt
)

type token struct {
	kind tokenKind
	val  string
	pos  int
}

// Keywords lists all reserved keywords in the doit language.
var Keywords = map[string]bool{
	"behavior":    true,
	"break":       true,
	"else":        true,
	"fn":          true,
	"if":          true,
	"instruction": true,
	"loop":        true,
	"private":     true,
	"var":         true,
	"while":       true,
}

type scanner struct {
	src   string
	pos   int
	ungot *token
}

type parser struct {
	scanner
	fns         map[string]*fnDef
	target      string   // behavior ID to compile ("" = auto-select)
	behaviorIDs []string // collected during pass 1
	locale      string   // BCP 47 locale tag; empty = use first entry
}

func (s *scanner) errorf(pos int, format string, args ...any) error {
	line, col := 1, 1
	for i := 0; i < pos && i < len(s.src); i++ {
		if s.src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return fmt.Errorf("%d:%d: %s", line, col, fmt.Sprintf(format, args...))
}

func (s *scanner) skipWhitespaceAndComments() {
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			s.pos++
		} else if c == '/' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '/' {
			for s.pos < len(s.src) && s.src[s.pos] != '\n' {
				s.pos++
			}
		} else if c == '#' {
			for s.pos < len(s.src) && s.src[s.pos] != '\n' {
				s.pos++
			}
		} else {
			break
		}
	}
}

func (s *scanner) next() (token, error) {
	if s.ungot != nil {
		tok := *s.ungot
		s.ungot = nil
		return tok, nil
	}

	s.skipWhitespaceAndComments()
	if s.pos >= len(s.src) {
		return token{kind: tokEOF, pos: s.pos}, nil
	}

	start := s.pos
	c := s.src[s.pos]
	switch {
	case c == '{':
		s.pos++
		return token{tokLBrace, "{", start}, nil
	case c == '}':
		s.pos++
		return token{tokRBrace, "}", start}, nil
	case c == ':':
		s.pos++
		return token{tokColon, ":", start}, nil
	case c == '(':
		s.pos++
		return token{tokLParen, "(", start}, nil
	case c == ')':
		s.pos++
		return token{tokRParen, ")", start}, nil
	case c == ',':
		s.pos++
		return token{tokComma, ",", start}, nil
	case c == '=' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokDoubleEquals, "==", start}, nil
	case c == '=':
		s.pos++
		return token{tokEquals, "=", start}, nil
	case c == '+' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '+':
		s.pos += 2
		return token{tokPlusPlus, "++", start}, nil
	case c == '+' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokPlusEquals, "+=", start}, nil
	case c == '>' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokGreaterEquals, ">=", start}, nil
	case c == '>':
		s.pos++
		return token{tokGreater, ">", start}, nil
	case c == '<' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokLessEquals, "<=", start}, nil
	case c == '<':
		s.pos++
		return token{tokLess, "<", start}, nil
	case c == '@':
		s.pos++
		return token{tokAt, "@", start}, nil
	case c == '"':
		return s.scanString()
	case c >= '0' && c <= '9':
		return s.scanNumber(), nil
	case isIdentStart(c):
		return s.scanIdent(), nil
	default:
		return token{}, s.errorf(start, "unexpected character %q", c)
	}
}

func (s *scanner) scanString() (token, error) {
	start := s.pos
	s.pos++ // skip opening quote
	var b strings.Builder
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		s.pos++
		if c == '"' {
			return token{tokString, b.String(), start}, nil
		}
		if c == '\\' {
			if s.pos >= len(s.src) {
				return token{}, s.errorf(s.pos-1, "unterminated escape sequence")
			}
			esc := s.src[s.pos]
			s.pos++
			switch esc {
			case '"', '\\':
				b.WriteByte(esc)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return token{}, s.errorf(s.pos-1, "unknown escape \\%c", esc)
			}
		} else {
			b.WriteByte(c)
		}
	}
	return token{}, s.errorf(start, "unterminated string")
}

func (s *scanner) scanIdent() token {
	start := s.pos
	for s.pos < len(s.src) && isIdentCont(s.src[s.pos]) {
		s.pos++
	}
	return token{tokIdent, s.src[start:s.pos], start}
}

func (s *scanner) scanNumber() token {
	start := s.pos
	for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
		s.pos++
	}
	return token{tokNumber, s.src[start:s.pos], start}
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

func (s *scanner) expect(kind tokenKind) (token, error) {
	tok, err := s.next()
	if err != nil {
		return tok, err
	}
	if tok.kind != kind {
		return tok, s.errorf(tok.pos, "unexpected %s", tok.describe())
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
	case tokAt:
		return "'@'"
	}
	return t.val
}
