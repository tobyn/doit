package compiler_test

import (
	"testing"

	"github.com/tobyn/doit/toolchain/compiler"
)

func TestParseLocalePrefix(t *testing.T) {
	tests := []struct {
		input      string
		wantLocale string
		wantRest   string
		wantOK     bool
	}{
		{"(en) text", "en", "text", true},
		{"(en_US) text", "en_US", "text", true},
		{"(zh-Hans) text", "zh-Hans", "text", true},
		{"plain text", "", "", false},
		{"(en)", "en", "", true},
		{"()", "", "", false},
		{"(123!) bad", "", "", false},
		{"(en)no space", "en", "no space", true},
		{"( en ) text", "en", "text", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			locale, rest, ok := compiler.TestParseLocalePrefix(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if locale != tt.wantLocale {
				t.Fatalf("locale: got %q, want %q", locale, tt.wantLocale)
			}
			if rest != tt.wantRest {
				t.Fatalf("rest: got %q, want %q", rest, tt.wantRest)
			}
		})
	}
}
