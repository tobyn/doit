// Package formatter implements canonical formatting for doit source code.
// It normalizes indentation, operator spacing, semicolons, and blank lines
// while preserving the user's line-break decisions.
package formatter

import (
	"strings"

	"github.com/tobyn/doit/toolchain/syntax"
)

const indent = "    " // 4 spaces per indent level

// Format formats doit source code and returns the formatted result.
func Format(src string) (string, error) {
	toks, err := scan(src)
	if err != nil {
		return "", err
	}
	return render(src, toks), nil
}

// token pairs a scanned token with its raw source text.
type token struct {
	syntax.Token
	raw    string // verbatim source text of this token
	endPos int    // byte offset after this token in source
}

// scan tokenizes the source, capturing raw text for each token.
func scan(src string) ([]token, error) {
	s := &syntax.Scanner{Src: src}
	var toks []token
	for {
		t, err := s.RawNext()
		if err != nil {
			return nil, err
		}
		toks = append(toks, token{
			Token:  t,
			raw:    src[t.Pos:s.Pos],
			endPos: s.Pos,
		})
		if t.Kind == syntax.TokEOF {
			break
		}
	}
	return toks, nil
}

// render rebuilds the source from tokens with normalized formatting.
func render(src string, toks []token) string {
	if len(toks) == 0 {
		return ""
	}

	var out strings.Builder
	depth := 0   // brace nesting depth
	prevEnd := 0 // end position of previous token in source
	var prev, prevprev *syntax.Token

	for i := range toks {
		t := &toks[i]
		if t.Kind == syntax.TokEOF {
			break
		}

		// Analyze the whitespace gap between the previous token and this one.
		gap := src[prevEnd:t.Pos]
		newlines := strings.Count(gap, "\n")
		hasSemi := strings.ContainsRune(gap, ';')

		// Closing brace decreases indent before the line is written.
		if t.Kind == syntax.TokRBrace {
			depth--
			if depth < 0 {
				depth = 0
			}
		}

		if i == 0 {
			// First token — write leading indentation only.
			writeIndent(&out, depth)
		} else if newlines > 0 {
			// Token is on a new line. Collapse 2+ blank lines to 1.
			blank := newlines - 1
			if blank > 1 {
				blank = 1
			}
			for j := 0; j <= blank; j++ {
				out.WriteByte('\n')
			}
			writeIndent(&out, depth)
			// Reset line context so cross-line tokens don't influence
			// unary/binary disambiguation on the new line.
			prev = nil
			prevprev = nil
		} else {
			// Same line — apply spacing rules.
			if hasSemi && t.Kind != syntax.TokRBrace {
				out.WriteString("; ")
			} else if needsSpace(prevprev, prev, &t.Token) {
				out.WriteByte(' ')
			}
		}

		out.WriteString(t.raw)

		// Opening brace increases indent after being written.
		if t.Kind == syntax.TokLBrace {
			depth++
		}

		prevEnd = t.endPos
		prevprev = prev
		prev = &toks[i].Token
	}

	result := out.String()
	if len(result) == 0 {
		return ""
	}
	if result[len(result)-1] != '\n' {
		result += "\n"
	}
	return result
}

// blockKeywords are keywords where a space before '(' is natural
// because they introduce blocks rather than being called like functions.
var blockKeywords = map[string]bool{
	"if": true, "else": true, "while": true, "for": true,
	"loop": true, "on": true, "fn": true, "iter": true,
	"behavior": true,
}

// needsSpace reports whether a space is needed between prev and cur
// on the same line. prevprev is used for unary/binary disambiguation.
func needsSpace(prevprev, prev, cur *syntax.Token) bool {
	if prev == nil {
		return false
	}

	p, c := prev.Kind, cur.Kind

	// No space around . and ::
	if p == syntax.TokDot || c == syntax.TokDot {
		return false
	}
	if p == syntax.TokDoubleColon || c == syntax.TokDoubleColon {
		return false
	}

	// No space after ( or before )
	if p == syntax.TokLParen {
		return false
	}
	if c == syntax.TokRParen {
		return false
	}

	// No space before ,
	if c == syntax.TokComma {
		return false
	}

	// No space before :
	if c == syntax.TokColon {
		return false
	}

	// No space after @
	if p == syntax.TokAt {
		return false
	}

	// No space after !
	if p == syntax.TokBang {
		return false
	}

	// No space before ++ and --
	if c == syntax.TokPlusPlus || c == syntax.TokMinusMinus {
		return false
	}

	// Empty block: {} stays tight
	if p == syntax.TokLBrace && c == syntax.TokRBrace {
		return false
	}

	// Label before ( — no space: 'larger(@1)
	if p == syntax.TokLabel && c == syntax.TokLParen {
		return false
	}

	// Identifier before ( — space for block keywords, no space for calls
	if p == syntax.TokIdent && c == syntax.TokLParen {
		return blockKeywords[prev.Val]
	}

	// Unary minus: no space after - when nothing before it ends an expression
	if p == syntax.TokMinus && !canEndExpr(prevprev) {
		return false
	}

	// Faction register prefix: no space after % when it isn't modulo
	if p == syntax.TokPercent && !canEndExpr(prevprev) {
		return false
	}

	return true
}

// canEndExpr reports whether tok could be the last token of an expression.
// Used to disambiguate binary minus/percent from unary minus/faction prefix.
func canEndExpr(tok *syntax.Token) bool {
	if tok == nil {
		return false
	}
	switch tok.Kind {
	case syntax.TokNumber, syntax.TokString, syntax.TokRParen, syntax.TokRBrace,
		syntax.TokPlusPlus, syntax.TokMinusMinus:
		return true
	case syntax.TokIdent:
		switch tok.Val {
		case "true", "false", "null", "infinity", "not_equal":
			return true
		}
		return !syntax.Keywords[tok.Val]
	}
	return false
}

func writeIndent(b *strings.Builder, level int) {
	for i := 0; i < level; i++ {
		b.WriteString(indent)
	}
}
