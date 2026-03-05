package compiler

import (
	"fmt"
	"io/fs"
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
	tokAmpersand
	tokDoubleAmpersand
	tokDoublePipe
	tokNotEquals
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokMinusMinus
	tokMinusEquals
	tokStarEquals
	tokSlashEquals
	tokPercent
	tokPercentEquals
	tokBang
	tokArrow // ->
	tokDot
	tokDoubleColon
	tokLabel  // 'identifier — loop label sigil
	tokIs     // internal-only: represents the 'is' operator in comparisonTerm.op
	tokTruthy // internal-only: represents a truthy check in comparisonTerm.op
)

type token struct {
	kind tokenKind
	val  string
	pos  int
}

// Keywords lists all reserved keywords in the doit language.
var Keywords = map[string]bool{
	"as":          true,
	"behavior":    true,
	"break":       true,
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
	"out":         true,
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

// isConstructor reports whether an identifier is a type constructor name.
func isConstructor(name string) bool {
	switch name {
	case "Coordinate", "Item", "Component", "Technology", "Value", "Range":
		return true
	}
	return false
}

type scannerState struct {
	pos          int
	ungot        *token
	docComment   string
	ungotComment string
}

type scanner struct {
	src          string
	pos          int
	ungot        *token
	docComment   string // accumulated #! lines before the current token
	ungotComment string // saved docComment for ungotten token
	locale       string // BCP 47 locale tag; empty = use first entry
	sourceFile   string // source file path for error messages (empty = main file)
	sourceOffset int    // byte offset of user source (after prepended prelude)
}

func (s *scanner) save() scannerState {
	var ungot *token
	if s.ungot != nil {
		t := *s.ungot
		ungot = &t
	}
	return scannerState{
		pos:          s.pos,
		ungot:        ungot,
		docComment:   s.docComment,
		ungotComment: s.ungotComment,
	}
}

func (s *scanner) restore(state scannerState) {
	s.pos = state.pos
	s.ungot = state.ungot
	s.docComment = state.docComment
	s.ungotComment = state.ungotComment
}

type parser struct {
	scanner
	fns         map[string]*fnDef
	iters       map[string]*iterDef
	target      string   // behavior ID to compile ("" = auto-select)
	behaviorIDs []string // collected during pass 1
	imports     []ImportStmt     // parsed import statements
	prelude    string   // prelude text (propagated to sub-parsers for imports)
	fileDecls  []string // names declared in this file (populated by collectDecls)
	sourceFS       fs.FS                      // file system for resolving imports (nil = no imports)
	sourcePath     string                     // path of the source file within sourceFS
	sourceDir      string                     // directory of the source file (derived from sourcePath)
	stdlibFS       fs.FS                      // stdlib file system for std: imports
	stdlibFns      map[string]*fnDef          // parsed stdlib fns (shared across imports)
	stdlibEnums    map[string]*enumDef       // parsed stdlib enums (shared across imports)
	stdlibIters    map[string]*iterDef       // parsed stdlib iters (shared across imports)
	importStack    []string                   // import path stack for cycle detection
	namedImports   map[string]bool            // names from named imports (for collision checking)
	namespaceNames map[string]bool            // namespace names (for collision checking)
	importedNames  map[string]bool            // all names from imports: glob + named (for dup detection)
	namespaceSets  map[string]*symbolSet      // namespace → symbolSet (fns, consts, enums)
	consts         map[string]*constDef       // compile-time constants
	enums          map[string]*enumDef        // compile-time enums
	evalStepLimit  int                               // step limit for compile-time evaluation
	loopDepth      int              // >0 when inside a loop body
	execBlockDepth int              // >0 when inside an exec block body
	loopLabels      map[string]bool // labels of enclosing loops
	outerLoopLabels map[string]bool // labels of loops beyond exec block boundaries
	warnings    []string         // compiler warnings (non-fatal)

	// callExprParser is set by behavior/fn body contexts to enable
	// function call parsing in boolean primary position (e.g., d || my_fn x).
	callExprParser func(callee *fnDef, calleeTok token) (Expr, error)
}

// warnf appends a formatted warning message with line:column prefix.
// posToLineCol converts a byte offset in the source to a 1-based line and column.
func (s *scanner) posToLineCol(pos int) (line, col int) {
	if pos < s.sourceOffset {
		return 1, 1
	}
	line, col = 1, 1
	for i := s.sourceOffset; i < pos && i < len(s.src); i++ {
		if s.src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func (p *parser) warnf(pos int, format string, args ...any) {
	line, col := p.posToLineCol(pos)
	msg := fmt.Sprintf(format, args...)
	if p.sourceFile != "" {
		p.warnings = append(p.warnings, fmt.Sprintf("%s:%d:%d: %s", p.sourceFile, line, col, msg))
	} else {
		p.warnings = append(p.warnings, fmt.Sprintf("%d:%d: %s", line, col, msg))
	}
}

// enterLoop records entry into a loop body. If label is non-empty, it is
// registered so that `break label` can target this loop.
func (p *parser) enterLoop(label string) {
	p.loopDepth++
	if label != "" {
		p.loopLabels[label] = true
	}
}

// exitLoop records exit from a loop body and unregisters the label.
func (p *parser) exitLoop(label string) {
	p.loopDepth--
	if label != "" {
		delete(p.loopLabels, label)
	}
}

// enterExecBlock saves and resets loop state for an exec block boundary.
// Current loopLabels and outerLoopLabels are merged into a new
// outerLoopLabels so that cross-boundary `break 'label` can find them.
// Returns a restore function for use with defer.
func (p *parser) enterExecBlock() func() {
	savedLoopDepth := p.loopDepth
	savedLoopLabels := p.loopLabels
	savedOuterLoopLabels := p.outerLoopLabels

	newOuter := map[string]bool{}
	for k := range p.outerLoopLabels {
		newOuter[k] = true
	}
	for k := range p.loopLabels {
		newOuter[k] = true
	}

	p.outerLoopLabels = newOuter
	p.loopDepth = 0
	p.loopLabels = map[string]bool{}
	p.execBlockDepth++

	return func() {
		p.loopDepth = savedLoopDepth
		p.loopLabels = savedLoopLabels
		p.outerLoopLabels = savedOuterLoopLabels
		p.execBlockDepth--
	}
}

func (s *scanner) errorf(pos int, format string, args ...any) error {
	line, col := s.posToLineCol(pos)
	msg := fmt.Sprintf(format, args...)
	if s.sourceFile != "" {
		return fmt.Errorf("%s:%d:%d: %s", s.sourceFile, line, col, msg)
	}
	return fmt.Errorf("%d:%d: %s", line, col, msg)
}

// parseLocalePrefix checks if line starts with a (locale) prefix.
// Returns the locale code, the remaining text, and whether a prefix was found.
func parseLocalePrefix(line string) (locale, rest string, ok bool) {
	if !strings.HasPrefix(line, "(") {
		return "", "", false
	}
	idx := strings.IndexByte(line, ')')
	if idx < 0 {
		return "", "", false
	}
	locale = strings.TrimSpace(line[1:idx])
	if locale == "" {
		return "", "", false
	}
	for _, c := range locale {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-') {
			return "", "", false
		}
	}
	rest = strings.TrimSpace(line[idx+1:])
	return locale, rest, true
}

func (s *scanner) skipWhitespaceAndComments() {
	s.docComment = ""
	var docLines []string

	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ';' {
			s.pos++
		} else if c == '#' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '!' {
			s.pos += 2 // skip #!
			start := s.pos
			for s.pos < len(s.src) && s.src[s.pos] != '\n' {
				s.pos++
			}
			docLines = append(docLines, strings.TrimSpace(s.src[start:s.pos]))
		} else if c == '#' {
			for s.pos < len(s.src) && s.src[s.pos] != '\n' {
				s.pos++
			}
		} else {
			break
		}
	}

	if len(docLines) == 0 {
		return
	}

	if loc, rest, ok := parseLocalePrefix(docLines[0]); ok {
		s.docComment = s.resolveLocalizedDocComment(loc, rest, docLines[1:])
	} else {
		s.docComment = strings.Join(docLines, " ")
	}
}

func (s *scanner) resolveLocalizedDocComment(firstLocale, firstText string, remaining []string) string {
	type entry struct {
		locale string
		text   string
	}
	entries := []entry{{firstLocale, firstText}}

	for _, line := range remaining {
		if loc, rest, ok := parseLocalePrefix(line); ok {
			entries = append(entries, entry{loc, rest})
		} else {
			last := &entries[len(entries)-1]
			if last.text != "" {
				last.text += " " + line
			} else {
				last.text = line
			}
		}
	}

	locales := make([]string, len(entries))
	for i, e := range entries {
		locales[i] = e.locale
	}
	idx := matchLocale(s.locale, locales)
	return entries[idx].text
}

func (s *scanner) unget(tok token) {
	t := tok
	s.ungot = &t
	s.ungotComment = s.docComment
}

func (s *scanner) next() (token, error) {
	if s.ungot != nil {
		tok := *s.ungot
		s.ungot = nil
		s.docComment = s.ungotComment
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
	case c == ':' && s.pos+1 < len(s.src) && s.src[s.pos+1] == ':':
		s.pos += 2
		return token{tokDoubleColon, "::", start}, nil
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
	case c == '+':
		s.pos++
		return token{tokPlus, "+", start}, nil
	case c == '-' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '-':
		s.pos += 2
		return token{tokMinusMinus, "--", start}, nil
	case c == '-' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokMinusEquals, "-=", start}, nil
	case c == '-' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '>':
		s.pos += 2
		return token{tokArrow, "->", start}, nil
	case c == '-':
		s.pos++
		return token{tokMinus, "-", start}, nil
	case c == '*' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokStarEquals, "*=", start}, nil
	case c == '*':
		s.pos++
		return token{tokStar, "*", start}, nil
	case c == '/' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '/':
		return token{}, s.errorf(start, "unexpected '//' — use '#' for comments")
	case c == '/' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokSlashEquals, "/=", start}, nil
	case c == '/':
		s.pos++
		return token{tokSlash, "/", start}, nil
	case c == '%' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokPercentEquals, "%=", start}, nil
	case c == '%':
		s.pos++
		return token{tokPercent, "%", start}, nil
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
	case c == '&' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '&':
		s.pos += 2
		return token{tokDoubleAmpersand, "&&", start}, nil
	case c == '&':
		s.pos++
		return token{tokAmpersand, "&", start}, nil
	case c == '|' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '|':
		s.pos += 2
		return token{tokDoublePipe, "||", start}, nil
	case c == '|':
		return token{}, s.errorf(start, "unexpected '|' — use '||' for logical OR")
	case c == '!' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '=':
		s.pos += 2
		return token{tokNotEquals, "!=", start}, nil
	case c == '!':
		s.pos++
		return token{tokBang, "!", start}, nil
	case c == '.':
		s.pos++
		return token{tokDot, ".", start}, nil
	case c == '$':
		s.pos++
		if s.pos >= len(s.src) || !isIdentStart(s.src[s.pos]) {
			return token{}, s.errorf(start, "expected register name after '$'")
		}
		for s.pos < len(s.src) && isIdentCont(s.src[s.pos]) {
			s.pos++
		}
		return token{tokIdent, s.src[start:s.pos], start}, nil
	case c == '\'':
		s.pos++
		if s.pos >= len(s.src) || !isIdentStart(s.src[s.pos]) {
			return token{}, s.errorf(start, "expected label name after '\\''")
		}
		nameStart := s.pos
		for s.pos < len(s.src) && isIdentCont(s.src[s.pos]) {
			s.pos++
		}
		return token{tokLabel, s.src[nameStart:s.pos], start}, nil
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

// skipToCloseBrace consumes tokens until the matching '}' is found,
// accounting for nested brace pairs. Used to skip unreachable code.
func (s *scanner) skipToCloseBrace() error {
	depth := 1
	for depth > 0 {
		tok, err := s.next()
		if err != nil {
			return err
		}
		switch tok.kind {
		case tokLBrace:
			depth++
		case tokRBrace:
			depth--
		case tokEOF:
			return s.errorf(tok.pos, "unexpected end of file (missing '}')")
		}
	}
	return nil
}

func (t token) describe() string {
	switch t.kind {
	case tokEOF:
		return "end of file"
	case tokIdent:
		if Keywords[t.val] {
			return fmt.Sprintf("keyword %q", t.val)
		}
		return fmt.Sprintf("identifier %q", t.val)
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
	case tokAmpersand:
		return "'&'"
	case tokDoubleAmpersand:
		return "'&&'"
	case tokDoublePipe:
		return "'||'"
	case tokNotEquals:
		return "'!='"
	case tokPlus:
		return "'+'"
	case tokMinus:
		return "'-'"
	case tokStar:
		return "'*'"
	case tokSlash:
		return "'/'"
	case tokMinusMinus:
		return "'--'"
	case tokMinusEquals:
		return "'-='"
	case tokStarEquals:
		return "'*='"
	case tokSlashEquals:
		return "'/='"
	case tokPercent:
		return "'%'"
	case tokPercentEquals:
		return "'%='"
	case tokBang:
		return "'!'"
	case tokArrow:
		return "'->'"
	case tokDot:
		return "'.'"
	case tokDoubleColon:
		return "'::'"
	case tokLabel:
		return fmt.Sprintf("label '%s", t.val)
	case tokIs:
		return "'is'"
	case tokTruthy:
		return "truthy"
	}
	return t.val
}
