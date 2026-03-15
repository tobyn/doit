// Package syntax defines the lexical grammar of the doit language.
// It provides the scanner (tokenizer), token types, keyword definitions,
// and semantic token classification used by both the compiler and
// language tooling.
package syntax

import (
	"fmt"
	"strings"
)

// TokenKind represents the type of a scanned token.
type TokenKind byte

const (
	TokEOF TokenKind = iota
	TokIdent
	TokString
	TokNumber
	TokLBrace
	TokRBrace
	TokColon
	TokLParen
	TokRParen
	TokComma
	TokEquals
	TokDoubleEquals
	TokPlusEquals
	TokPlusPlus
	TokLess
	TokLessEquals
	TokGreater
	TokGreaterEquals
	TokAt
	TokAmpersand
	TokDoubleAmpersand
	TokDoublePipe
	TokNotEquals
	TokPlus
	TokMinus
	TokStar
	TokSlash
	TokMinusMinus
	TokMinusEquals
	TokStarEquals
	TokSlashEquals
	TokPercent
	TokPercentEquals
	TokBang
	TokArrow // ->
	TokDot
	TokDoubleColon
	TokLabel   // 'identifier — loop label sigil
	TokComment // # or #! comment line
)

// Token represents a scanned token.
type Token struct {
	Kind TokenKind
	Val  string
	Pos  int // byte offset in source
}

// Describe returns a human-readable description of the token for
// use in error messages.
func (t Token) Describe() string {
	switch t.Kind {
	case TokEOF:
		return "end of file"
	case TokIdent:
		if Keywords[t.Val] {
			return fmt.Sprintf("keyword %q", t.Val)
		}
		return fmt.Sprintf("identifier %q", t.Val)
	case TokString:
		return fmt.Sprintf("string %q", t.Val)
	case TokLBrace:
		return "'{'"
	case TokRBrace:
		return "'}'"
	case TokColon:
		return "':'"
	case TokLParen:
		return "'('"
	case TokRParen:
		return "')'"
	case TokComma:
		return "','"
	case TokNumber:
		return fmt.Sprintf("number %s", t.Val)
	case TokEquals:
		return "'='"
	case TokDoubleEquals:
		return "'=='"
	case TokPlusPlus:
		return "'++'"
	case TokPlusEquals:
		return "'+='"
	case TokLess:
		return "'<'"
	case TokLessEquals:
		return "'<='"
	case TokGreater:
		return "'>'"
	case TokGreaterEquals:
		return "'>='"
	case TokAt:
		return "'@'"
	case TokAmpersand:
		return "'&'"
	case TokDoubleAmpersand:
		return "'&&'"
	case TokDoublePipe:
		return "'||'"
	case TokNotEquals:
		return "'!='"
	case TokPlus:
		return "'+'"
	case TokMinus:
		return "'-'"
	case TokStar:
		return "'*'"
	case TokSlash:
		return "'/'"
	case TokMinusMinus:
		return "'--'"
	case TokMinusEquals:
		return "'-='"
	case TokStarEquals:
		return "'*='"
	case TokSlashEquals:
		return "'/='"
	case TokPercent:
		return "'%'"
	case TokPercentEquals:
		return "'%='"
	case TokBang:
		return "'!'"
	case TokArrow:
		return "'->'"
	case TokDot:
		return "'.'"
	case TokDoubleColon:
		return "'::'"
	case TokLabel:
		return fmt.Sprintf("label '%s", t.Val)
	case TokComment:
		return fmt.Sprintf("comment %q", t.Val)
	}
	return t.Val
}

// Keywords lists all reserved keywords in the doit language.
var Keywords = map[string]bool{
	"as":          true,
	"assert":      true,
	"behavior":    true,
	"break":       true,
	"call":        true,
	"const":       true,
	"continue":    true,
	"else":        true,
	"enum":        true,
	"exec":        true,
	"exit":        true,
	"false":       true,
	"fn":          true,
	"for":         true,
	"from":        true,
	"if":          true,
	"import":      true,
	"in":          true,
	"infinity":    true,
	"not_equal":   true,
	"inout":       true,
	"instruction":          true,
	"jump":        true,
	"label":       true,
	"iterator_instruction": true,
	"iter":                 true,
	"last":        true,
	"let":         true,
	"locked":      true,
	"localize":    true,
	"loop":        true,
	"null":        true,
	"on":          true,
	"out":         true,
	"param":       true,
	"private":     true,
	"restart":     true,
	"return":      true,
	"skip":        true,
	"true":        true,
	"unlocked":    true,
	"var":         true,
	"wait":        true,
	"is":          true,
	"while":       true,
	"yield":       true,
	// Type constructors
	"Coordinate": true,
	"Component":  true,
	"Item":       true,
	"Range":      true,
	"Technology": true,
	"Unit":       true,
	"Value":      true,
}

// IsConstructor reports whether an identifier is a type constructor name.
func IsConstructor(name string) bool {
	switch name {
	case "Coordinate", "Item", "Component", "Technology", "Value", "Range":
		return true
	}
	return false
}

// IsIdentStart reports whether c can start an identifier.
func IsIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

// IsIdentCont reports whether c can continue an identifier.
func IsIdentCont(c byte) bool {
	return IsIdentStart(c) || c >= '0' && c <= '9'
}

// Scanner tokenizes doit source code.
type Scanner struct {
	Src string
	Pos int
	// Error is called to format scanner errors. If nil, a basic
	// position-prefixed message is produced.
	Error func(pos int, format string, args ...any) error
}

func (s *Scanner) errorf(pos int, format string, args ...any) error {
	if s.Error != nil {
		return s.Error(pos, format, args...)
	}
	return fmt.Errorf("%d: %s", pos, fmt.Sprintf(format, args...))
}

func (s *Scanner) skipWhitespace() {
	for s.Pos < len(s.Src) {
		c := s.Src[s.Pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ';' {
			s.Pos++
		} else {
			break
		}
	}
}

// RawNext scans the next token including comments. Comments are returned
// as TokComment tokens (with the full line from '#' to end-of-line in Val).
// This is the low-level scanning primitive used by both the compiler's
// next() method (which filters comments) and the semantic tokenizer
// (which emits comment tokens directly).
func (s *Scanner) RawNext() (Token, error) {
	s.skipWhitespace()
	if s.Pos >= len(s.Src) {
		return Token{Kind: TokEOF, Pos: s.Pos}, nil
	}

	start := s.Pos
	c := s.Src[s.Pos]

	// Comments (both # and #!).
	if c == '#' {
		for s.Pos < len(s.Src) && s.Src[s.Pos] != '\n' {
			s.Pos++
		}
		return Token{TokComment, s.Src[start:s.Pos], start}, nil
	}

	switch {
	case c == '{':
		s.Pos++
		return Token{TokLBrace, "{", start}, nil
	case c == '}':
		s.Pos++
		return Token{TokRBrace, "}", start}, nil
	case c == ':' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == ':':
		s.Pos += 2
		return Token{TokDoubleColon, "::", start}, nil
	case c == ':':
		s.Pos++
		return Token{TokColon, ":", start}, nil
	case c == '(':
		s.Pos++
		return Token{TokLParen, "(", start}, nil
	case c == ')':
		s.Pos++
		return Token{TokRParen, ")", start}, nil
	case c == ',':
		s.Pos++
		return Token{TokComma, ",", start}, nil
	case c == '=' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokDoubleEquals, "==", start}, nil
	case c == '=':
		s.Pos++
		return Token{TokEquals, "=", start}, nil
	case c == '+' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '+':
		s.Pos += 2
		return Token{TokPlusPlus, "++", start}, nil
	case c == '+' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokPlusEquals, "+=", start}, nil
	case c == '+':
		s.Pos++
		return Token{TokPlus, "+", start}, nil
	case c == '-' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '-':
		s.Pos += 2
		return Token{TokMinusMinus, "--", start}, nil
	case c == '-' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokMinusEquals, "-=", start}, nil
	case c == '-' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '>':
		s.Pos += 2
		return Token{TokArrow, "->", start}, nil
	case c == '-':
		s.Pos++
		return Token{TokMinus, "-", start}, nil
	case c == '*' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokStarEquals, "*=", start}, nil
	case c == '*':
		s.Pos++
		return Token{TokStar, "*", start}, nil
	case c == '/' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '/':
		return Token{}, s.errorf(start, "unexpected '//' — use '#' for comments")
	case c == '/' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokSlashEquals, "/=", start}, nil
	case c == '/':
		s.Pos++
		return Token{TokSlash, "/", start}, nil
	case c == '%' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokPercentEquals, "%=", start}, nil
	case c == '%':
		s.Pos++
		return Token{TokPercent, "%", start}, nil
	case c == '>' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokGreaterEquals, ">=", start}, nil
	case c == '>':
		s.Pos++
		return Token{TokGreater, ">", start}, nil
	case c == '<' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokLessEquals, "<=", start}, nil
	case c == '<':
		s.Pos++
		return Token{TokLess, "<", start}, nil
	case c == '@':
		s.Pos++
		return Token{TokAt, "@", start}, nil
	case c == '&' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '&':
		s.Pos += 2
		return Token{TokDoubleAmpersand, "&&", start}, nil
	case c == '&':
		s.Pos++
		return Token{TokAmpersand, "&", start}, nil
	case c == '|' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '|':
		s.Pos += 2
		return Token{TokDoublePipe, "||", start}, nil
	case c == '|':
		return Token{}, s.errorf(start, "unexpected '|' — use '||' for logical OR")
	case c == '!' && s.Pos+1 < len(s.Src) && s.Src[s.Pos+1] == '=':
		s.Pos += 2
		return Token{TokNotEquals, "!=", start}, nil
	case c == '!':
		s.Pos++
		return Token{TokBang, "!", start}, nil
	case c == '.':
		s.Pos++
		return Token{TokDot, ".", start}, nil
	case c == '$':
		s.Pos++
		if s.Pos >= len(s.Src) || !IsIdentStart(s.Src[s.Pos]) {
			return Token{}, s.errorf(start, "expected register name after '$'")
		}
		for s.Pos < len(s.Src) && IsIdentCont(s.Src[s.Pos]) {
			s.Pos++
		}
		return Token{TokIdent, s.Src[start:s.Pos], start}, nil
	case c == '\'':
		s.Pos++
		if s.Pos >= len(s.Src) || !IsIdentStart(s.Src[s.Pos]) {
			return Token{}, s.errorf(start, "expected label name after '\\''")
		}
		nameStart := s.Pos
		for s.Pos < len(s.Src) && IsIdentCont(s.Src[s.Pos]) {
			s.Pos++
		}
		return Token{TokLabel, s.Src[nameStart:s.Pos], start}, nil
	case c == '"':
		return s.scanString()
	case c >= '0' && c <= '9':
		return s.scanNumber(), nil
	case IsIdentStart(c):
		return s.scanIdent(), nil
	default:
		return Token{}, s.errorf(start, "unexpected character %q", c)
	}
}

func (s *Scanner) scanString() (Token, error) {
	start := s.Pos
	s.Pos++ // skip opening quote
	var b strings.Builder
	for s.Pos < len(s.Src) {
		c := s.Src[s.Pos]
		s.Pos++
		if c == '"' {
			return Token{TokString, b.String(), start}, nil
		}
		if c == '\\' {
			if s.Pos >= len(s.Src) {
				return Token{}, s.errorf(s.Pos-1, "unterminated escape sequence")
			}
			esc := s.Src[s.Pos]
			s.Pos++
			switch esc {
			case '"', '\\':
				b.WriteByte(esc)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return Token{}, s.errorf(s.Pos-1, "unknown escape \\%c", esc)
			}
		} else {
			b.WriteByte(c)
		}
	}
	return Token{}, s.errorf(start, "unterminated string")
}

func (s *Scanner) scanIdent() Token {
	start := s.Pos
	for s.Pos < len(s.Src) && IsIdentCont(s.Src[s.Pos]) {
		s.Pos++
	}
	return Token{TokIdent, s.Src[start:s.Pos], start}
}

func (s *Scanner) scanNumber() Token {
	start := s.Pos
	for s.Pos < len(s.Src) && s.Src[s.Pos] >= '0' && s.Src[s.Pos] <= '9' {
		s.Pos++
	}
	return Token{TokNumber, s.Src[start:s.Pos], start}
}
