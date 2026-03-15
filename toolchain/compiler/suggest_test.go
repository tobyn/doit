package compiler

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"notify", "notfy", 1},
		{"domove", "domov", 1},
		{"result", "reslt", 1},
		{"kitten", "sitting", 3},
	}
	for _, tc := range cases {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestClosestMatch(t *testing.T) {
	fns := []string{"notify", "domove", "set_reg", "wait", "exit"}

	cases := []struct {
		input string
		want  string
	}{
		{"notfy", "notify"},
		{"domov", "domove"},
		{"waitt", "wait"},
		{"xyzzy_foo_bar", ""},  // too far from anything
		{"s", ""},              // too short to match meaningfully
	}
	for _, tc := range cases {
		got := closestMatch(tc.input, fns)
		if got != tc.want {
			t.Errorf("closestMatch(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSuggest(t *testing.T) {
	fns := []string{"notify", "domove"}

	if s := suggest("notfy", fns); s != "; did you mean notify?" {
		t.Errorf("suggest(notfy) = %q", s)
	}
	if s := suggest("xyzzy", fns); s != "" {
		t.Errorf("suggest(xyzzy) = %q, want empty", s)
	}
}
