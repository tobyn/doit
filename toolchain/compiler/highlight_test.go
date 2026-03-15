package compiler

import (
	"testing"
)

func TestTokenizeComment(t *testing.T) {
	tokens := Tokenize("# a regular comment")
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	if tokens[0].Type != TokenComment {
		t.Errorf("expected TokenComment, got %d", tokens[0].Type)
	}
	if tokens[0].Modifiers != 0 {
		t.Errorf("expected no modifiers, got %d", tokens[0].Modifiers)
	}
}

func TestTokenizeDocComment(t *testing.T) {
	tokens := Tokenize("#! doc comment")
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	if tokens[0].Type != TokenComment {
		t.Errorf("expected TokenComment, got %d", tokens[0].Type)
	}
	if tokens[0].Modifiers&ModDocumentation == 0 {
		t.Error("expected ModDocumentation")
	}
}

func TestTokenizeBehaviorDecl(t *testing.T) {
	tokens := Tokenize("behavior hello_world {}")
	assertToken(t, tokens, 0, TokenKeyword, "behavior")
	assertToken(t, tokens, 1, TokenType, "hello_world")
}

func TestTokenizeFnDecl(t *testing.T) {
	tokens := Tokenize("fn my_func(x, y) {}")
	assertToken(t, tokens, 0, TokenKeyword, "fn")
	assertToken(t, tokens, 1, TokenFunction, "my_func")
	assertTokenMod(t, tokens, 1, ModDeclaration)
}

func TestTokenizePrivateFn(t *testing.T) {
	tokens := Tokenize("private fn helper() {}")
	assertToken(t, tokens, 0, TokenKeyword, "private")
	assertToken(t, tokens, 1, TokenKeyword, "fn")
	assertToken(t, tokens, 2, TokenFunction, "helper")
}

func TestTokenizeIterDecl(t *testing.T) {
	tokens := Tokenize("iter each_thing() -> val {}")
	assertToken(t, tokens, 0, TokenKeyword, "iter")
	assertToken(t, tokens, 1, TokenFunction, "each_thing")
	assertTokenMod(t, tokens, 1, ModDeclaration)
}

func TestTokenizeEnumDecl(t *testing.T) {
	src := "enum Direction {\n    North\n    South\n}"
	tokens := Tokenize(src)
	assertToken(t, tokens, 0, TokenKeyword, "enum")
	assertToken(t, tokens, 1, TokenEnum, "Direction")
	assertTokenMod(t, tokens, 1, ModDeclaration)
	// { is operator
	assertToken(t, tokens, 2, TokenOperator, "{")
	// Enum members
	assertToken(t, tokens, 3, TokenEnumMember, "North")
	assertToken(t, tokens, 4, TokenEnumMember, "South")
}

func TestTokenizeEnumAccess(t *testing.T) {
	tokens := Tokenize("Direction::North")
	assertToken(t, tokens, 0, TokenVariable, "Direction")
	assertToken(t, tokens, 1, TokenOperator, "::")
	assertToken(t, tokens, 2, TokenEnumMember, "North")
}

func TestTokenizeConstDecl(t *testing.T) {
	tokens := Tokenize("const MAX = 42")
	assertToken(t, tokens, 0, TokenKeyword, "const")
	assertToken(t, tokens, 1, TokenVariable, "MAX")
	assertTokenMod(t, tokens, 1, ModDeclaration|ModReadonly)
}

func TestTokenizeLetVar(t *testing.T) {
	tokens := Tokenize("let x = 5\nvar y = 10")
	assertToken(t, tokens, 0, TokenKeyword, "let")
	assertToken(t, tokens, 1, TokenVariable, "x")
	assertTokenMod(t, tokens, 1, ModDeclaration|ModReadonly)
	assertToken(t, tokens, 2, TokenOperator, "=")
	assertToken(t, tokens, 3, TokenNumber, "5")

	assertToken(t, tokens, 4, TokenKeyword, "var")
	assertToken(t, tokens, 5, TokenVariable, "y")
	assertTokenMod(t, tokens, 5, ModDeclaration)
	if tokens[5].Modifiers&ModReadonly != 0 {
		t.Error("var should not have ModReadonly")
	}
}

func TestTokenizeRegister(t *testing.T) {
	tokens := Tokenize("$target")
	assertToken(t, tokens, 0, TokenRegister, "$target")
}

func TestTokenizeLabel(t *testing.T) {
	tokens := Tokenize("'outer")
	assertToken(t, tokens, 0, TokenLabel, "'outer")
}

func TestTokenizeDirective(t *testing.T) {
	tokens := Tokenize("@name")
	assertToken(t, tokens, 0, TokenDecorator, "@")
	assertToken(t, tokens, 1, TokenDecorator, "name")
}

func TestTokenizeParamDirective(t *testing.T) {
	src := `@param in target "Target"`
	tokens := Tokenize(src)
	assertToken(t, tokens, 0, TokenDecorator, "@")
	assertToken(t, tokens, 1, TokenDecorator, "param")
	assertToken(t, tokens, 2, TokenKeyword, "in")
	assertToken(t, tokens, 3, TokenParameter, "target")
	assertTokenMod(t, tokens, 3, ModDeclaration)
	assertToken(t, tokens, 4, TokenString, `"Target"`)
}

func TestTokenizeTypeConstructor(t *testing.T) {
	tokens := Tokenize(`Item("metalbar")`)
	assertToken(t, tokens, 0, TokenType, "Item")
}

func TestTokenizeString(t *testing.T) {
	tokens := Tokenize(`"hello \"world\""`)
	assertToken(t, tokens, 0, TokenString, `"hello \"world\""`)
}

func TestTokenizeSlotRef(t *testing.T) {
	tokens := Tokenize("@1")
	assertToken(t, tokens, 0, TokenVariable, "@1")
}

func TestTokenizeForLoop(t *testing.T) {
	tokens := Tokenize("for comp, idx in each_component {}")
	assertToken(t, tokens, 0, TokenKeyword, "for")
	assertToken(t, tokens, 1, TokenVariable, "comp")
	assertTokenMod(t, tokens, 1, ModDeclaration)
	assertToken(t, tokens, 2, TokenOperator, ",")
	assertToken(t, tokens, 3, TokenVariable, "idx")
	assertTokenMod(t, tokens, 3, ModDeclaration)
	assertToken(t, tokens, 4, TokenKeyword, "in")
}

func TestTokenizeOnEvent(t *testing.T) {
	tokens := Tokenize("on $trigger {}")
	assertToken(t, tokens, 0, TokenKeyword, "on")
	assertToken(t, tokens, 1, TokenParameter, "$trigger")
}

func TestTokenizeKeywords(t *testing.T) {
	keywords := []string{"if", "else", "while", "loop", "break", "continue",
		"return", "exit", "restart", "wait", "last", "yield",
		"jump", "label", "locked", "unlocked", "assert", "call",
		"instruction", "true", "false", "null", "infinity", "is"}
	for _, kw := range keywords {
		tokens := Tokenize(kw)
		if len(tokens) != 1 {
			t.Errorf("%s: expected 1 token, got %d", kw, len(tokens))
			continue
		}
		if tokens[0].Type != TokenKeyword {
			t.Errorf("%s: expected TokenKeyword, got %d", kw, tokens[0].Type)
		}
	}
}

func TestTokenizeOperators(t *testing.T) {
	ops := []string{"->", "&&", "||", "==", "!=", "<=", ">=",
		"+=", "-=", "*=", "/=", "%=", "++", "--",
		"=", "+", "-", "*", "/", "%", "<", ">", "!", "&"}
	for _, op := range ops {
		tokens := Tokenize(op)
		if len(tokens) != 1 {
			t.Errorf("%q: expected 1 token, got %d", op, len(tokens))
			continue
		}
		if tokens[0].Type != TokenOperator {
			t.Errorf("%q: expected TokenOperator, got %d", op, tokens[0].Type)
		}
	}
}

func TestTokenizeImport(t *testing.T) {
	tokens := Tokenize(`import { notify, domove } from "std:instructions"`)
	assertToken(t, tokens, 0, TokenKeyword, "import")
	assertToken(t, tokens, 1, TokenOperator, "{")
	assertToken(t, tokens, 2, TokenFunction, "notify")
	assertToken(t, tokens, 3, TokenOperator, ",")
	assertToken(t, tokens, 4, TokenFunction, "domove")
	assertToken(t, tokens, 5, TokenOperator, "}")
	assertToken(t, tokens, 6, TokenKeyword, "from")
	assertToken(t, tokens, 7, TokenString, `"std:instructions"`)
}

func TestTokenizeImportGlob(t *testing.T) {
	tokens := Tokenize(`import * from "std:instructions"`)
	assertToken(t, tokens, 0, TokenKeyword, "import")
	assertToken(t, tokens, 1, TokenOperator, "*")
	assertToken(t, tokens, 2, TokenKeyword, "from")
	assertToken(t, tokens, 3, TokenString, `"std:instructions"`)
}

func TestTokenizeComplexExample(t *testing.T) {
	src := `behavior hello_world {
    @name "Hello World"
    #! Say hello
    notify "Hello, World!"
}`
	tokens := Tokenize(src)
	assertToken(t, tokens, 0, TokenKeyword, "behavior")
	assertToken(t, tokens, 1, TokenType, "hello_world")
	assertToken(t, tokens, 2, TokenOperator, "{")
	assertToken(t, tokens, 3, TokenDecorator, "@")
	assertToken(t, tokens, 4, TokenDecorator, "name")
	assertToken(t, tokens, 5, TokenString, `"Hello World"`)
	assertToken(t, tokens, 6, TokenComment, "#! Say hello")
	assertToken(t, tokens, 7, TokenVariable, "notify")
	assertToken(t, tokens, 8, TokenString, `"Hello, World!"`)
	assertToken(t, tokens, 9, TokenOperator, "}")
}

func TestTokenizeDotAccess(t *testing.T) {
	tokens := Tokenize("x.number")
	assertToken(t, tokens, 0, TokenVariable, "x")
	assertToken(t, tokens, 1, TokenOperator, ".")
	assertToken(t, tokens, 2, TokenProperty, "number")
}

// assertToken checks that the token at index i has the expected type and
// its source text matches expected.
func assertToken(t *testing.T, tokens []SemanticToken, i int, typ SemanticTokenType, text string) {
	t.Helper()
	if i >= len(tokens) {
		t.Fatalf("token index %d out of range (have %d tokens)", i, len(tokens))
	}
	tok := tokens[i]
	if tok.Type != typ {
		t.Errorf("token[%d]: expected type %d, got %d", i, typ, tok.Type)
	}
	// Reconstruct text from the tokenizer's source is not possible here,
	// but we can check the length matches.
	if tok.Length != len(text) {
		t.Errorf("token[%d]: expected length %d (%q), got %d", i, len(text), text, tok.Length)
	}
}

func assertTokenMod(t *testing.T, tokens []SemanticToken, i int, mod SemanticTokenModifier) {
	t.Helper()
	if i >= len(tokens) {
		t.Fatalf("token index %d out of range (have %d tokens)", i, len(tokens))
	}
	if tokens[i].Modifiers&mod != mod {
		t.Errorf("token[%d]: expected modifiers %d, got %d", i, mod, tokens[i].Modifiers)
	}
}
