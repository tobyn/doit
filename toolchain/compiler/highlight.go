package compiler

// --- Semantic Token Types ---
//
// Tokenize produces classified token spans from doit source code for use
// in syntax highlighting. It uses the compiler's scanner with lightweight
// context tracking to classify tokens semantically without requiring a
// full parse.

// SemanticTokenType represents an LSP semantic token type.
type SemanticTokenType int

const (
	TokenComment    SemanticTokenType = iota // comments (#, #!)
	TokenKeyword                             // control flow, declarations
	TokenFunction                            // function/iterator names
	TokenVariable                            // local variables
	TokenParameter                           // behavior parameters ($name in directives)
	TokenProperty                            // dot accessors (.number, .value)
	TokenString                              // string literals
	TokenNumber                              // numeric literals
	TokenOperator                            // operators and punctuation
	TokenType                                // type constructors (Item, Unit, etc.)
	TokenEnum                                // enum type names
	TokenEnumMember                          // enum values (after ::)
	TokenDecorator                           // directives (@name, @param, etc.)
	TokenLabel                               // label sigils ('name)
	TokenRegister                            // unit registers ($name)
	TokenNamespace                           // namespace in qualified access
)

// SemanticTokenTypes returns the LSP semantic token type names in order,
// matching the SemanticTokenType constants. Used by the language server
// to register capabilities.
func SemanticTokenTypes() []string {
	return []string{
		"comment",
		"keyword",
		"function",
		"variable",
		"parameter",
		"property",
		"string",
		"number",
		"operator",
		"type",
		"enum",
		"enumMember",
		"decorator",
		"label",
		"variable",  // register — mapped to variable with modifier
		"namespace", // namespace — for qualified access
	}
}

// SemanticTokenModifier represents LSP semantic token modifiers (bitmask).
type SemanticTokenModifier int

const (
	ModDeclaration   SemanticTokenModifier = 1 << iota // at definition site
	ModReadonly                                         // let (not var)
	ModDocumentation                                    // doc comments (#!)
	ModDefaultLibrary                                   // stdlib functions
)

// SemanticTokenModifiers returns the LSP semantic token modifier names
// in order, matching the SemanticTokenModifier bit positions.
func SemanticTokenModifiers() []string {
	return []string{
		"declaration",
		"readonly",
		"documentation",
		"defaultLibrary",
	}
}

// SemanticToken represents a classified token span in source code.
type SemanticToken struct {
	Offset    int                   // byte offset in source
	Length    int                   // byte length
	Type      SemanticTokenType     // token classification
	Modifiers SemanticTokenModifier // modifier flags
}

// highlightContext tracks state for context-dependent token classification.
type highlightContext int

const (
	ctxNone           highlightContext = iota
	ctxAfterFn                        // after 'fn' keyword
	ctxAfterIter                      // after 'iter' keyword
	ctxAfterBehavior                  // after 'behavior' keyword
	ctxAfterEnum                      // after 'enum' keyword
	ctxAfterConst                     // after 'const' keyword
	ctxAfterLet                       // after 'let' keyword
	ctxAfterVar                       // after 'var' keyword
	ctxAfterAt                        // after '@' token
	ctxAfterAtParam                   // after '@param'
	ctxAfterAtParamDir                // after '@param in/out/inout'
	ctxAfterAtParamName               // after '@param in/out/inout name'
	ctxAfterPrivate                   // after 'private' keyword
	ctxAfterDoubleColon               // after '::'
	ctxAfterImport                    // after 'import'
	ctxAfterFor                       // after 'for'
	ctxAfterForIdents                 // after 'for x, y'
	ctxAfterOn                        // after 'on'
	ctxInEnumBody                     // inside enum { ... }
	ctxAfterDot                       // after '.' operator
)

// Tokenize produces semantic tokens for the given doit source code.
// It uses the compiler's scanner (rawNext) with lightweight context
// tracking to classify tokens semantically without requiring a full
// parse. Tokens are returned in source order.
func Tokenize(src string) []SemanticToken {
	t := &tokenizer{
		s:      scanner{src: src},
		tokens: make([]SemanticToken, 0, 256),
	}
	t.tokenize()
	return t.tokens
}

type tokenizer struct {
	s      scanner
	tokens []SemanticToken
	ctx    highlightContext
	// Track brace depth for enum body detection.
	enumBraceDepth int
	braceDepth     int
	// Pending @ token for @N slot reference detection.
	pendingAt int // byte offset of '@', or -1 if none
}

func (t *tokenizer) emit(offset, length int, typ SemanticTokenType, mods SemanticTokenModifier) {
	t.tokens = append(t.tokens, SemanticToken{
		Offset:    offset,
		Length:    length,
		Type:      typ,
		Modifiers: mods,
	})
}

// flushPendingAt emits a pending '@' as a decorator token.
func (t *tokenizer) flushPendingAt() {
	if t.pendingAt >= 0 {
		t.emit(t.pendingAt, 1, TokenDecorator, 0)
		t.ctx = ctxAfterAt
		t.pendingAt = -1
	}
}

func (t *tokenizer) tokenize() {
	t.pendingAt = -1
	for {
		prevPos := t.s.pos
		tok, err := t.s.rawNext()
		if err != nil {
			// Ensure progress on scanner errors (some don't advance pos).
			if t.s.pos <= prevPos {
				t.s.pos = prevPos + 1
			}
			continue
		}
		if tok.kind == tokEOF {
			t.flushPendingAt()
			break
		}

		rawLen := t.s.pos - tok.pos

		// Handle @N slot reference detection: if we have a pending @
		// and this token is a number immediately after it, combine them.
		if t.pendingAt >= 0 {
			if tok.kind == tokNumber && tok.pos == t.pendingAt+1 {
				t.emit(t.pendingAt, t.s.pos-t.pendingAt, TokenVariable, 0)
				t.pendingAt = -1
				t.ctx = ctxNone
				continue
			}
			t.flushPendingAt()
		}

		switch tok.kind {
		case tokComment:
			if len(tok.val) >= 2 && tok.val[1] == '!' {
				t.emit(tok.pos, rawLen, TokenComment, ModDocumentation)
			} else {
				t.emit(tok.pos, rawLen, TokenComment, 0)
			}

		case tokString:
			t.emit(tok.pos, rawLen, TokenString, 0)
			t.ctx = ctxNone

		case tokNumber:
			t.emit(tok.pos, rawLen, TokenNumber, 0)
			t.ctx = ctxNone

		case tokIdent:
			if len(tok.val) > 0 && tok.val[0] == '$' {
				// Register reference ($ident).
				if t.ctx == ctxAfterOn {
					t.emit(tok.pos, rawLen, TokenParameter, 0)
				} else {
					t.emit(tok.pos, rawLen, TokenRegister, 0)
				}
				t.ctx = ctxNone
			} else {
				t.classifyIdent(tok.pos, tok.val)
			}

		case tokLabel:
			t.emit(tok.pos, rawLen, TokenLabel, 0)
			t.ctx = ctxNone

		case tokAt:
			// Defer emission — might be @N (slot reference).
			t.pendingAt = tok.pos

		case tokDoubleColon:
			t.emit(tok.pos, rawLen, TokenOperator, 0)
			t.ctx = ctxAfterDoubleColon

		case tokLBrace:
			t.braceDepth++
			if t.ctx == ctxAfterEnum {
				t.enumBraceDepth = t.braceDepth
				t.ctx = ctxInEnumBody
			} else if t.ctx == ctxAfterImport {
				// Stay in import context for named imports.
			} else {
				t.ctx = ctxNone
			}
			t.emit(tok.pos, rawLen, TokenOperator, 0)

		case tokRBrace:
			if t.ctx == ctxInEnumBody && t.braceDepth == t.enumBraceDepth {
				t.ctx = ctxNone
				t.enumBraceDepth = 0
			}
			t.braceDepth--
			t.emit(tok.pos, rawLen, TokenOperator, 0)

		case tokDot:
			t.emit(tok.pos, rawLen, TokenOperator, 0)
			t.ctx = ctxAfterDot

		case tokComma:
			t.emit(tok.pos, rawLen, TokenOperator, 0)
			// Don't reset context (preserves for-loop ident context, import context).

		case tokLParen, tokRParen, tokColon:
			t.emit(tok.pos, rawLen, TokenOperator, 0)
			if t.ctx != ctxAfterForIdents && t.ctx != ctxAfterImport {
				t.ctx = ctxNone
			}

		default:
			// All other operators.
			t.emit(tok.pos, rawLen, TokenOperator, 0)
			t.ctx = ctxNone
		}
	}
}

func (t *tokenizer) classifyIdent(offset int, word string) {
	length := len(word)

	// Context-dependent classification.
	switch t.ctx {
	case ctxAfterFn:
		t.emit(offset, length, TokenFunction, ModDeclaration)
		t.ctx = ctxNone
		return
	case ctxAfterIter:
		t.emit(offset, length, TokenFunction, ModDeclaration)
		t.ctx = ctxNone
		return
	case ctxAfterBehavior:
		t.emit(offset, length, TokenType, ModDeclaration)
		t.ctx = ctxNone
		return
	case ctxAfterEnum:
		t.emit(offset, length, TokenEnum, ModDeclaration)
		// Stay in ctxAfterEnum — the { will transition to ctxInEnumBody.
		return
	case ctxAfterConst:
		t.emit(offset, length, TokenVariable, ModDeclaration|ModReadonly)
		t.ctx = ctxNone
		return
	case ctxAfterLet:
		t.emit(offset, length, TokenVariable, ModDeclaration|ModReadonly)
		// After the name, might be a comma for multi-binding.
		t.ctx = ctxNone
		return
	case ctxAfterVar:
		t.emit(offset, length, TokenVariable, ModDeclaration)
		t.ctx = ctxNone
		return
	case ctxAfterAt:
		t.emit(offset, length, TokenDecorator, 0)
		if word == "param" {
			t.ctx = ctxAfterAtParam
		} else {
			t.ctx = ctxNone
		}
		return
	case ctxAfterAtParam:
		// Expect direction: in, out, inout
		if word == "in" || word == "out" || word == "inout" {
			t.emit(offset, length, TokenKeyword, 0)
			t.ctx = ctxAfterAtParamDir
		} else {
			t.emit(offset, length, TokenParameter, ModDeclaration)
			t.ctx = ctxAfterAtParamName
		}
		return
	case ctxAfterAtParamDir:
		t.emit(offset, length, TokenParameter, ModDeclaration)
		t.ctx = ctxAfterAtParamName
		return
	case ctxAfterAtParamName:
		// Could be display name string (handled by string scanner) or default
		t.ctx = ctxNone
		t.classifyIdentDefault(offset, word)
		return
	case ctxAfterPrivate:
		if word == "fn" {
			t.emit(offset, length, TokenKeyword, 0)
			t.ctx = ctxAfterFn
		} else if word == "iter" {
			t.emit(offset, length, TokenKeyword, 0)
			t.ctx = ctxAfterIter
		} else {
			t.classifyIdentDefault(offset, word)
		}
		return
	case ctxAfterDoubleColon:
		t.emit(offset, length, TokenEnumMember, 0)
		t.ctx = ctxNone
		return
	case ctxAfterImport:
		// Inside import { ... } — these are function names
		if word == "from" {
			t.emit(offset, length, TokenKeyword, 0)
			t.ctx = ctxNone
		} else if word == "as" {
			t.emit(offset, length, TokenKeyword, 0)
		} else {
			t.emit(offset, length, TokenFunction, 0)
		}
		return
	case ctxAfterFor:
		// First ident after 'for' is a loop variable
		t.emit(offset, length, TokenVariable, ModDeclaration)
		t.ctx = ctxAfterForIdents
		return
	case ctxAfterForIdents:
		if word == "in" {
			t.emit(offset, length, TokenKeyword, 0)
			t.ctx = ctxNone
		} else {
			// Additional loop variables (multi-return)
			t.emit(offset, length, TokenVariable, ModDeclaration)
		}
		return
	case ctxInEnumBody:
		t.emit(offset, length, TokenEnumMember, ModDeclaration)
		return
	case ctxAfterOn:
		// After 'on', expect $param or 'radio' keyword
		if word == "radio" {
			t.emit(offset, length, TokenKeyword, 0)
		} else {
			t.classifyIdentDefault(offset, word)
		}
		t.ctx = ctxNone
		return
	case ctxAfterDot:
		if word == "number" || word == "value" {
			t.emit(offset, length, TokenProperty, 0)
		} else {
			t.emit(offset, length, TokenVariable, 0)
		}
		t.ctx = ctxNone
		return
	}

	t.classifyIdentDefault(offset, word)
}

func (t *tokenizer) classifyIdentDefault(offset int, word string) {
	length := len(word)

	// Type constructors.
	if isConstructor(word) || word == "Unit" {
		t.emit(offset, length, TokenType, 0)
		t.ctx = ctxNone
		return
	}

	// Keywords that set context.
	switch word {
	case "fn":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterFn
		return
	case "iter":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterIter
		return
	case "behavior":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterBehavior
		return
	case "enum":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterEnum
		return
	case "const":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterConst
		return
	case "let":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterLet
		return
	case "var":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterVar
		return
	case "private":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterPrivate
		return
	case "import":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterImport
		return
	case "for":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterFor
		return
	case "on":
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxAfterOn
		return
	}

	// Other keywords.
	if Keywords[word] {
		t.emit(offset, length, TokenKeyword, 0)
		t.ctx = ctxNone
		return
	}

	// Identifiers that look like enum type names (capitalized, before ::).
	// Can't determine this without lookahead, so treat as variable.
	// The enum-access case is handled by ctxAfterDoubleColon.

	// Plain identifier — could be a function call or variable reference.
	// Without full parsing, we classify as variable. The language server's
	// AST-based pass will reclassify call targets as functions.
	t.emit(offset, length, TokenVariable, 0)
	t.ctx = ctxNone
}
