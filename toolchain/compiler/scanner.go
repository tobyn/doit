package compiler

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/tobyn/doit/toolchain/syntax"
)

// --- Scanner (thin wrapper around syntax.Scanner) ---

// tokenKind is an alias for syntax.TokenKind, allowing the compiler to
// reference token kinds without qualification.
type tokenKind = syntax.TokenKind

// token is a compiler-local token representation with lowercase fields.
// rawNext() converts from syntax.Token to avoid renaming every field
// access across the compiler.
type token struct {
	kind tokenKind
	val  string
	pos  int
}

// Token kind constants — aliases for syntax.Tok* values.
const (
	tokEOF            = syntax.TokEOF
	tokIdent          = syntax.TokIdent
	tokString         = syntax.TokString
	tokNumber         = syntax.TokNumber
	tokLBrace         = syntax.TokLBrace
	tokRBrace         = syntax.TokRBrace
	tokColon          = syntax.TokColon
	tokLParen         = syntax.TokLParen
	tokRParen         = syntax.TokRParen
	tokComma          = syntax.TokComma
	tokEquals         = syntax.TokEquals
	tokDoubleEquals   = syntax.TokDoubleEquals
	tokPlusEquals     = syntax.TokPlusEquals
	tokPlusPlus       = syntax.TokPlusPlus
	tokLess           = syntax.TokLess
	tokLessEquals     = syntax.TokLessEquals
	tokGreater        = syntax.TokGreater
	tokGreaterEquals  = syntax.TokGreaterEquals
	tokAt             = syntax.TokAt
	tokAmpersand      = syntax.TokAmpersand
	tokDoubleAmpersand = syntax.TokDoubleAmpersand
	tokDoublePipe     = syntax.TokDoublePipe
	tokNotEquals      = syntax.TokNotEquals
	tokPlus           = syntax.TokPlus
	tokMinus          = syntax.TokMinus
	tokStar           = syntax.TokStar
	tokSlash          = syntax.TokSlash
	tokMinusMinus     = syntax.TokMinusMinus
	tokMinusEquals    = syntax.TokMinusEquals
	tokStarEquals     = syntax.TokStarEquals
	tokSlashEquals    = syntax.TokSlashEquals
	tokPercent        = syntax.TokPercent
	tokPercentEquals  = syntax.TokPercentEquals
	tokBang           = syntax.TokBang
	tokArrow          = syntax.TokArrow
	tokDot            = syntax.TokDot
	tokDoubleColon    = syntax.TokDoubleColon
	tokLabel          = syntax.TokLabel
	tokComment        = syntax.TokComment
)

// Compiler-internal token kinds (not part of the lexical grammar).
const (
	tokIs     tokenKind = 200 // internal: 'is' operator in comparisonTerm.op
	tokTruthy tokenKind = 201 // internal: truthy check in comparisonTerm.op
)

// Re-export syntax-level definitions used by the compiler.
var (
	Keywords    = syntax.Keywords
	isConstructor = syntax.IsConstructor
	isIdentStart  = syntax.IsIdentStart
	isIdentCont   = syntax.IsIdentCont
)

type scannerState struct {
	pos          int
	ungot        *token
	docComment   string
	ungotComment string
}

type scanner struct {
	syn          syntax.Scanner
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
		pos:          s.syn.Pos,
		ungot:        ungot,
		docComment:   s.docComment,
		ungotComment: s.ungotComment,
	}
}

func (s *scanner) restore(state scannerState) {
	s.syn.Pos = state.pos
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
	modeBlockDepth int              // >0 when inside a mode block body
	forNumberDepth int              // >0 when inside a for_number-backed loop body
	breakRetVals   []any            // target registers for break-with-value; nil outside expression blocks
	loopLabels      map[string]bool // labels of enclosing loops
	outerLoopLabels map[string]bool // labels of loops beyond exec block boundaries
	warnings       []string         // compiler warnings (non-fatal)
	releaseMode    bool             // true when compiling with --release (omits assert)
	collectSymbols bool             // when true, populate symbols during collectDecls
	symbols        []Symbol         // top-level declarations (populated when collectSymbols is true)

	// callExprParser is set by behavior/fn body contexts to enable
	// function call parsing in boolean primary position (e.g., d || my_fn x).
	callExprParser func(callee *fnDef, calleeTok token) (Expr, error)

	// Behavior call support
	bhvs             map[string]*bhvDef   // behavior definitions (from this file + imports)
	dependencies     []map[string]any     // accumulated compiled dependencies for root behavior
	depIndex         map[string]int       // behavior ID → 1-based dependency index (dedup)
	depCompiling     map[string]bool      // behaviors currently being compiled (cycle detection)
	selfBehaviorID   string               // behavior currently being compiled (for self-recursion)
}

// recordSymbol adds a symbol to the symbols list if symbol collection is enabled.
// pos is the byte offset in the source; it is converted to 0-based line:col.
func (p *parser) recordSymbol(name string, kind SymbolKind, pos int) {
	if !p.collectSymbols {
		return
	}
	line, col := p.posToLineCol(pos)
	// posToLineCol returns 1-based; convert to 0-based for LSP.
	p.symbols = append(p.symbols, Symbol{Name: name, Kind: kind, Line: line - 1, Col: col - 1})
}

// warnf appends a formatted warning message with line:column prefix.
// posToLineCol converts a byte offset in the source to a 1-based line and column.
func (s *scanner) posToLineCol(pos int) (line, col int) {
	if pos < s.sourceOffset {
		return 1, 1
	}
	line, col = 1, 1
	for i := s.sourceOffset; i < pos && i < len(s.syn.Src); i++ {
		if s.syn.Src[i] == '\n' {
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
	annotation := p.sourceAnnotation(pos)
	if p.sourceFile != "" {
		p.warnings = append(p.warnings, fmt.Sprintf("%s:%d:%d: %s%s", p.sourceFile, line, col, msg, annotation))
	} else {
		p.warnings = append(p.warnings, fmt.Sprintf("%d:%d: %s%s", line, col, msg, annotation))
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

// sourceAnnotation returns source line + caret annotation for an error at pos.
// Returns empty string if pos is in the prelude or annotation can't be built.
func (s *scanner) sourceAnnotation(pos int) string {
	if pos < s.sourceOffset || pos >= len(s.syn.Src) {
		return ""
	}

	// Find line start and end.
	lineStart := pos
	for lineStart > s.sourceOffset && s.syn.Src[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := pos
	for lineEnd < len(s.syn.Src) && s.syn.Src[lineEnd] != '\n' {
		lineEnd++
	}

	sourceLine := s.syn.Src[lineStart:lineEnd]
	if strings.TrimSpace(sourceLine) == "" {
		return ""
	}

	line, _ := s.posToLineCol(pos)
	col := pos - lineStart // 0-based byte offset into the line

	// Expand tabs and compute display column.
	var display strings.Builder
	displayCol := 0
	for i, c := range sourceLine {
		if c == '\t' {
			spaces := 4 - (display.Len() % 4)
			for range spaces {
				display.WriteByte(' ')
			}
			if i < col {
				displayCol = display.Len()
			}
		} else {
			display.WriteRune(c)
			if i < col {
				displayCol = display.Len()
			}
		}
	}
	if col == 0 {
		displayCol = 0
	}

	lineNum := fmt.Sprintf("%d", line)
	padding := strings.Repeat(" ", len(lineNum))

	return fmt.Sprintf("\n  %s | %s\n  %s | %s^", lineNum, display.String(), padding, strings.Repeat(" ", displayCol))
}

func (s *scanner) errorf(pos int, format string, args ...any) error {
	line, col := s.posToLineCol(pos)
	msg := fmt.Sprintf(format, args...)
	annotation := s.sourceAnnotation(pos)
	if s.sourceFile != "" {
		return fmt.Errorf("%s:%d:%d: %s%s", s.sourceFile, line, col, msg, annotation)
	}
	return fmt.Errorf("%d:%d: %s%s", line, col, msg, annotation)
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

// rawNext delegates to syntax.Scanner.RawNext() and converts the result
// to the compiler's local token type.
func (s *scanner) rawNext() (token, error) {
	if s.syn.Error == nil {
		s.syn.Error = func(pos int, format string, args ...any) error {
			return s.errorf(pos, format, args...)
		}
	}
	tok, err := s.syn.RawNext()
	return token{kind: tok.Kind, val: tok.Val, pos: tok.Pos}, err
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

	s.docComment = ""
	var docLines []string
	for {
		tok, err := s.rawNext()
		if err != nil {
			return tok, err
		}
		if tok.kind != tokComment {
			if len(docLines) > 0 {
				if loc, rest, ok := parseLocalePrefix(docLines[0]); ok {
					s.docComment = s.resolveLocalizedDocComment(loc, rest, docLines[1:])
				} else {
					s.docComment = strings.Join(docLines, " ")
				}
			}
			return tok, nil
		}
		// Accumulate doc comment lines (#!), skip regular comments.
		if len(tok.val) >= 2 && tok.val[1] == '!' {
			docLines = append(docLines, strings.TrimSpace(tok.val[2:]))
		}
	}
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

// blockContainsReachabilityPath scans ahead through the current block
// (without consuming tokens) looking for `on` or `label` at any nesting
// depth. These keywords create non-linear control flow paths (event handler
// continuations and jump targets) that make subsequent code reachable even
// after a terminal statement.
func (s *scanner) blockContainsReachabilityPath() bool {
	saved := s.save()
	defer s.restore(saved)
	depth := 0
	for {
		tok, err := s.next()
		if err != nil {
			return false
		}
		switch tok.kind {
		case tokLBrace:
			depth++
		case tokRBrace:
			if depth == 0 {
				return false // end of current block
			}
			depth--
		case tokEOF:
			return false
		case tokIdent:
			if tok.val == "on" || tok.val == "label" {
				return true
			}
		}
	}
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
	// Handle compiler-internal token kinds.
	switch t.kind {
	case tokIs:
		return "'is'"
	case tokTruthy:
		return "truthy"
	}
	// Delegate to syntax.Token.Describe() for all standard kinds.
	return syntax.Token{Kind: t.kind, Val: t.val}.Describe()
}
