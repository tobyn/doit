package compiler

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestTextMateKeywordSync verifies that every keyword in the scanner's
// Keywords map appears in the TextMate grammar, and vice versa. This
// catches drift between the two sources of truth for language syntax.
func TestTextMateKeywordSync(t *testing.T) {
	data, err := os.ReadFile("../../editors/doit.tmLanguage.json")
	if err != nil {
		t.Fatalf("reading TextMate grammar: %v", err)
	}

	var grammar struct {
		Repository map[string]struct {
			Match string `json:"match"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &grammar); err != nil {
		t.Fatalf("parsing TextMate grammar: %v", err)
	}

	// Collect keywords from all TextMate keyword pattern groups.
	keywordPatterns := []string{
		"control-flow-keywords",
		"declaration-keywords",
		"mode-keywords",
		"literal-keywords",
		"operator-keywords",
		"other-keywords",
	}
	tmKeywords := map[string]bool{}
	for _, name := range keywordPatterns {
		pat, ok := grammar.Repository[name]
		if !ok {
			t.Errorf("TextMate grammar missing pattern group %q", name)
			continue
		}
		for _, word := range extractAlternation(pat.Match) {
			tmKeywords[word] = true
		}
	}

	// Keywords that appear in structural declaration patterns rather
	// than keyword groups. These are matched by more specific regexes
	// (e.g., behavior-declaration) and don't need to be in the keyword
	// groups, but must still be in the scanner's Keywords map.
	declKeywords := []string{"behavior", "fn", "iter", "enum", "const", "import", "from"}
	for _, kw := range declKeywords {
		tmKeywords[kw] = true
	}

	// Check: every non-constructor keyword in Keywords is in the grammar.
	for kw := range Keywords {
		if isConstructor(kw) || kw == "Unit" {
			continue // checked separately below
		}
		if !tmKeywords[kw] {
			t.Errorf("keyword %q in scanner.Keywords but not in TextMate grammar", kw)
		}
	}

	// Check: every TextMate keyword is in the scanner's Keywords map.
	for kw := range tmKeywords {
		if !Keywords[kw] {
			t.Errorf("keyword %q in TextMate grammar but not in scanner.Keywords", kw)
		}
	}
}

// TestTextMateTypeConstructorSync verifies that the TextMate grammar's
// type-constructors pattern matches the scanner's isConstructor function.
func TestTextMateTypeConstructorSync(t *testing.T) {
	data, err := os.ReadFile("../../editors/doit.tmLanguage.json")
	if err != nil {
		t.Fatalf("reading TextMate grammar: %v", err)
	}

	var grammar struct {
		Repository map[string]struct {
			Match string `json:"match"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &grammar); err != nil {
		t.Fatalf("parsing TextMate grammar: %v", err)
	}

	pat, ok := grammar.Repository["type-constructors"]
	if !ok {
		t.Fatal("TextMate grammar missing type-constructors pattern")
	}
	tmTypes := map[string]bool{}
	for _, word := range extractAlternation(pat.Match) {
		tmTypes[word] = true
	}

	// Check: every type constructor (plus "Unit") is in the grammar.
	constructors := []string{"Coordinate", "Component", "Item", "Range", "Technology", "Value", "Unit"}
	for _, c := range constructors {
		if !tmTypes[c] {
			t.Errorf("type constructor %q in scanner but not in TextMate grammar", c)
		}
	}

	// Check: every TextMate type constructor is known to the scanner.
	for ty := range tmTypes {
		if !isConstructor(ty) && ty != "Unit" {
			t.Errorf("type %q in TextMate grammar but not in scanner.isConstructor", ty)
		}
	}
}

// TestTextMateEscapeSync verifies that the TextMate grammar's string
// escape pattern matches the scanner's supported escape sequences.
func TestTextMateEscapeSync(t *testing.T) {
	data, err := os.ReadFile("../../editors/doit.tmLanguage.json")
	if err != nil {
		t.Fatalf("reading TextMate grammar: %v", err)
	}

	var grammar struct {
		Repository map[string]struct {
			Patterns []struct {
				Match string `json:"match"`
			} `json:"patterns"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &grammar); err != nil {
		t.Fatalf("parsing TextMate grammar: %v", err)
	}

	// Extract escape characters from the TextMate strings pattern.
	// The escape pattern is like \\[\"\\nt] — a character class after \\.
	strPat, ok := grammar.Repository["strings"]
	if !ok {
		t.Fatal("TextMate grammar missing strings pattern")
	}
	tmEscapes := map[byte]bool{}
	for _, p := range strPat.Patterns {
		// Match pattern like \\[chars] or \\\\[chars]
		// After JSON parsing: \\[\"\\nt]
		re := regexp.MustCompile(`\\\\\[([^\]]+)\]`)
		m := re.FindStringSubmatch(p.Match)
		if m == nil {
			continue
		}
		charClass := m[1]
		// Parse the character class: handle escaped chars like \"
		for i := 0; i < len(charClass); i++ {
			if charClass[i] == '\\' && i+1 < len(charClass) {
				tmEscapes[charClass[i+1]] = true
				i++
			} else {
				tmEscapes[charClass[i]] = true
			}
		}
	}

	// The scanner's scanString supports these escape characters.
	scannerEscapes := map[byte]bool{
		'"':  true,
		'\\': true,
		'n':  true,
		't':  true,
	}

	for esc := range scannerEscapes {
		if !tmEscapes[esc] {
			t.Errorf("escape '\\%c' in scanner but not in TextMate grammar", esc)
		}
	}
	for esc := range tmEscapes {
		if !scannerEscapes[esc] {
			t.Errorf("escape '\\%c' in TextMate grammar but not in scanner", esc)
		}
	}
}

// extractAlternation extracts words from a regex alternation group.
// Given a pattern like `\b(word1|word2|word3)\b`, it returns
// ["word1", "word2", "word3"].
func extractAlternation(pattern string) []string {
	re := regexp.MustCompile(`\(([^)]+)\)`)
	m := re.FindStringSubmatch(pattern)
	if m == nil {
		return nil
	}
	return strings.Split(m[1], "|")
}
