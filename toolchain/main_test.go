package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tobyn/doit/toolchain/codec"
	"github.com/tobyn/doit/toolchain/compiler"
)

func TestCompile(t *testing.T) {
	doitFiles, err := filepath.Glob("compiler/tests/*.doit")
	if err != nil {
		t.Fatal(err)
	}
	if len(doitFiles) == 0 {
		t.Fatal("no test cases found in compiler/tests/*.doit")
	}

	stdlib := os.DirFS("stdlib")

	for _, doitFile := range doitFiles {
		name := strings.TrimSuffix(filepath.Base(doitFile), ".doit")
		jsonFile := strings.TrimSuffix(doitFile, ".doit") + ".json"

		// If the name contains "__", the part after it is the behavior ID.
		behaviorID := ""
		if idx := strings.Index(name, "__"); idx >= 0 {
			behaviorID = name[idx+2:]
		}

		t.Run(name, func(t *testing.T) {
			f, err := os.Open(doitFile)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			// Check for a "# locale: <tag>" directive on the second line.
			locale := ""
			scanner := bufio.NewScanner(f)
			lineNum := 0
			for scanner.Scan() && lineNum < 2 {
				line := scanner.Text()
				if after, ok := strings.CutPrefix(line, "# locale: "); ok {
					locale = strings.TrimSpace(after)
				}
				lineNum++
			}
			if _, err := f.Seek(0, 0); err != nil {
				t.Fatal(err)
			}

			encoded, err := Compile(f, stdlib, behaviorID, locale, nil, "")
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			obj, err := codec.Decode(strings.NewReader(encoded))
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			wantBytes, err := os.ReadFile(jsonFile)
			if err != nil {
				t.Fatal(err)
			}
			wantVal, err := codec.UnmarshalJSON(wantBytes)
			if err != nil {
				t.Fatalf("UnmarshalJSON error: %v", err)
			}
			// Convert reference implementation output (0-based keys) to our
			// native format (1-based keys).
			wantVal = refToNative(wantVal)

			matchBehaviors(t, obj.Value.(map[string]any), wantVal.(map[string]any))
		})
	}
}

func TestCompileErrors(t *testing.T) {
	stdlib := os.DirFS("stdlib")

	t.Run("multiple_behaviors_no_selection", func(t *testing.T) {
		src := `behavior a { @name "A" } behavior b { @name "B" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "multiple behaviors") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("behavior_not_found", func(t *testing.T) {
		src := `behavior a { @name "A" } behavior b { @name "B" }`
		_, _, err := compiler.CompileString(src, stdlib, "c", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_name", func(t *testing.T) {
		src := `behavior a { @name "A" @name "B" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate @name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no_behaviors", func(t *testing.T) {
		src := `fn greet() { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no behavior") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("localized_name_with_locale", func(t *testing.T) {
		src := `behavior a { @name localize { en_US "English" ja "日本語" } notify "hi" }`
		obj, _, err := compiler.CompileString(src, stdlib, "", "ja", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		name := obj.Value.(map[string]any)["name"]
		if name != "日本語" {
			t.Fatalf("expected name %q, got %q", "日本語", name)
		}
	})

	t.Run("localized_name_no_match", func(t *testing.T) {
		src := `behavior a { @name localize { en_US "English" ja "日本語" } notify "hi" }`
		obj, _, err := compiler.CompileString(src, stdlib, "", "fr", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		name := obj.Value.(map[string]any)["name"]
		if name != "English" {
			t.Fatalf("expected name %q, got %q", "English", name)
		}
	})

	t.Run("localized_name_empty_block", func(t *testing.T) {
		src := `behavior a { @name localize {} notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty localize block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("localize_missing_brace", func(t *testing.T) {
		src := `behavior a { @name localize "Foo" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("doc_comment_inherit", func(t *testing.T) {
		src := "fn greet() { notify \"Hello\" }\nbehavior a {\n#! Greeting\ngreet\n}"
		obj, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if frame["cmt"] != "Greeting" {
			t.Fatalf("expected cmt %q, got %v", "Greeting", frame["cmt"])
		}
	})

	t.Run("doc_comment_override", func(t *testing.T) {
		src := "fn greet() {\n#! Inner\nnotify \"Hello\"\nnotify \"World\"\n}\nbehavior a {\n#! Outer\ngreet\n}"
		obj, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v := obj.Value.(map[string]any)
		f1 := v["1"].(map[string]any)
		f2 := v["2"].(map[string]any)
		if f1["cmt"] != "Inner" {
			t.Fatalf("frame 1: expected cmt %q, got %v", "Inner", f1["cmt"])
		}
		if f2["cmt"] != "Outer" {
			t.Fatalf("frame 2: expected cmt %q, got %v", "Outer", f2["cmt"])
		}
	})

	t.Run("no_doc_comment", func(t *testing.T) {
		src := `behavior a { notify "Hello" }`
		obj, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if _, exists := frame["cmt"]; exists {
			t.Fatalf("expected no cmt field, got %v", frame["cmt"])
		}
	})

	t.Run("unknown_keyword_arg", func(t *testing.T) {
		src := `behavior a { notify "Hello", unknown: "x" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown keyword argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("positional_param_after_keyword", func(t *testing.T) {
		src := "fn bad(value v, txt) {}\nbehavior a { notify \"hi\" }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "positional parameter after keyword parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_keyword_arg", func(t *testing.T) {
		src := `behavior a { notify "Hello", timeout: "5", timeout: "10" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate keyword argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_keyword_arg_in_fn_body", func(t *testing.T) {
		src := "fn bad(txt) { notify txt, timeout: \"5\", timeout: \"10\" }\nbehavior a { bad \"hi\" }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate keyword argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("extra_positional_arg", func(t *testing.T) {
		src := `behavior a { notify "Hello" "extra" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many positional arguments") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unterminated_string", func(t *testing.T) {
		src := `behavior a { notify "Hello }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unterminated string") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_escape", func(t *testing.T) {
		src := `behavior a { notify "Hello\q" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown escape") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unexpected_character", func(t *testing.T) {
		src := `behavior a { ~ }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected character") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_function_in_behavior", func(t *testing.T) {
		src := `behavior a { nonexistent "x" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_function_in_fn_body", func(t *testing.T) {
		src := "fn foo() { nonexistent \"x\" }\nbehavior a { foo }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_closing_brace", func(t *testing.T) {
		src := `behavior a { notify "Hello"`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_attribute", func(t *testing.T) {
		src := `behavior a { @foo "bar" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown attribute") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_function_arg", func(t *testing.T) {
		src := `behavior a { check_number "x" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// check_number expects 2 positional args; providing only 1 should fail
		if err.Error() == "" {
			t.Fatalf("expected non-empty error")
		}
	})

	t.Run("invalid_top_level", func(t *testing.T) {
		src := `foobar {}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty_source", func(t *testing.T) {
		src := ``
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no behavior") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_reassign", func(t *testing.T) {
		src := `behavior a { let x = 5; x = 10 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_plus_equals", func(t *testing.T) {
		src := `behavior a { let x = 5; x += 1 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_plus_plus", func(t *testing.T) {
		src := `behavior a { let x = 5; x++ }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_register", func(t *testing.T) {
		src := `behavior a { domove $foo }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown register") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_after_instruction", func(t *testing.T) {
		src := `behavior a { notify "hi"; @param in x "X" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "@param must be declared before any instructions") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_builtin_conflict", func(t *testing.T) {
		src := `behavior a { @param in signal "Signal" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with a built-in register") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_duplicate_name", func(t *testing.T) {
		src := `behavior a { @param in x "X1"; @param out x "X2" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_invalid_direction", func(t *testing.T) {
		src := `behavior a { @param rw x "X" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected parameter direction") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_count_limit", func(t *testing.T) {
		src := `behavior a {
			@param in a "A"
			@param in b "B"
			@param in c "C"
			@param in d "D"
			@param in e "E"
			@param in f "F"
			@param in g "G"
			@param in h "H"
			@param in i "I"
			@param in j "J"
			@param in k "K"
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many parameters") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dollar_sign_alone", func(t *testing.T) {
		src := `behavior a { domove $ }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected register name after '$'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_no_return", func(t *testing.T) {
		src := `behavior a { let x = notify }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_unknown", func(t *testing.T) {
		src := `behavior a { let x = nonexistent }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function or variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_string_rhs", func(t *testing.T) {
		src := `behavior a { let x = "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "strings have no runtime representation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("var_fn_no_return", func(t *testing.T) {
		src := `behavior a { var x = notify }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("assign_fn_no_return", func(t *testing.T) {
		src := `behavior a { var x = 5; x = notify }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_return_immutable", func(t *testing.T) {
		src := `behavior a { let me = get_self; me = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_body_no_return", func(t *testing.T) {
		src := "fn bad() { let x = notify }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_slot_at0", func(t *testing.T) {
		stdlibSrc := "fn bad() { instruction \"test\" { 0: @0 } }"
		err := compiler.TestParseStdlibFile(stdlibSrc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "@N return index must be >= 1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_slot_multiple", func(t *testing.T) {
		stdlibSrc := "fn multi() { return instruction \"test\" { 0: @1  1: @2 } }"
		err := compiler.TestParseStdlibFile(stdlibSrc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_slot_gap", func(t *testing.T) {
		stdlibSrc := "fn bad() { instruction \"test\" { 0: @1  1: @3 } }"
		err := compiler.TestParseStdlibFile(stdlibSrc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing @2") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_slot_no_at1", func(t *testing.T) {
		stdlibSrc := "fn bad() { instruction \"test\" { 0: @2 } }"
		err := compiler.TestParseStdlibFile(stdlibSrc)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing @1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_instruction_syntax", func(t *testing.T) {
		stdlibSrc := "fn my_get() { return instruction \"get_self\" { 0: @1 } }"
		err := compiler.TestParseStdlibFile(stdlibSrc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_variable_in_stdlib", func(t *testing.T) {
		// After unification, return <ident> is valid fn body syntax
		stdlibSrc := "fn wrapper(foo) { return foo }"
		err := compiler.TestParseStdlibFile(stdlibSrc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("localized_doc_comment_with_locale", func(t *testing.T) {
		src := "behavior a {\n#! (en) English comment\n#! (ja) 日本語コメント\nnotify \"Hello\"\n}"
		obj, _, err := compiler.CompileString(src, stdlib, "", "ja", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if frame["cmt"] != "日本語コメント" {
			t.Fatalf("expected cmt %q, got %v", "日本語コメント", frame["cmt"])
		}
	})

	t.Run("localized_doc_comment_no_locale", func(t *testing.T) {
		src := "behavior a {\n#! (en) English comment\n#! (ja) 日本語コメント\nnotify \"Hello\"\n}"
		obj, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if frame["cmt"] != "English comment" {
			t.Fatalf("expected cmt %q, got %v", "English comment", frame["cmt"])
		}
	})

	t.Run("localized_doc_comment_fallback", func(t *testing.T) {
		src := "behavior a {\n#! (en) English comment\n#! (ja) 日本語コメント\nnotify \"Hello\"\n}"
		obj, _, err := compiler.CompileString(src, stdlib, "", "fr", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if frame["cmt"] != "English comment" {
			t.Fatalf("expected cmt %q, got %v", "English comment", frame["cmt"])
		}
	})

	t.Run("localized_doc_comment_multiline_continuation", func(t *testing.T) {
		src := "behavior a {\n#! (en) line one\n#! continued\n#! (ja) 日本語\nnotify \"Hello\"\n}"
		obj, _, err := compiler.CompileString(src, stdlib, "", "en", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if frame["cmt"] != "line one continued" {
			t.Fatalf("expected cmt %q, got %v", "line one continued", frame["cmt"])
		}
	})

	t.Run("plain_doc_comment_not_affected", func(t *testing.T) {
		// A plain doc comment (no locale prefix) should work as before
		src := "behavior a {\n#! plain comment\nnotify \"Hello\"\n}"
		obj, _, err := compiler.CompileString(src, stdlib, "", "ja", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if frame["cmt"] != "plain comment" {
			t.Fatalf("expected cmt %q, got %v", "plain comment", frame["cmt"])
		}
	})

	t.Run("multi_return_too_many_bindings", func(t *testing.T) {
		src := "behavior a {\n  let me = get_self\n  let coord = get_location me\n  let x, y, z = separate_coordinate coord\n}"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many bindings") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("multi_return_no_return_fn", func(t *testing.T) {
		src := `behavior a { let x, y = notify "Hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("underscore_as_var_name", func(t *testing.T) {
		src := `behavior a { var _ = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'_' cannot be used as a variable name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("underscore_as_let_name", func(t *testing.T) {
		src := `behavior a { let _ = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'_' cannot be used as a variable name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("multi_return_fn_body_too_many", func(t *testing.T) {
		src := "fn bad() {\n  let me = get_self\n  let coord = get_location me\n  let x, y, z = separate_coordinate coord\n  return x\n}\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many bindings") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("constructor_missing_lparen", func(t *testing.T) {
		src := `behavior a { notify "hi", value: Item "metalbar" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected '(' after Item") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("coordinate_wrong_arg_count", func(t *testing.T) {
		src := `behavior a { notify "hi", value: Coordinate(1) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// Coordinate expects 2 args; providing 1 means the comma is missing
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("item_non_string_arg", func(t *testing.T) {
		src := `behavior a { notify "hi", value: Item(42) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected string argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("item_wrong_arg_count", func(t *testing.T) {
		src := `behavior a { notify "hi", value: Item("a", "b") }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// Item expects 1 string then ')'; the comma triggers an error
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("constructor_as_variable_name", func(t *testing.T) {
		src := `behavior a { let Item = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "type constructor") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_let_no_return_slot", func(t *testing.T) {
		src := "behavior a { let x = instruction \"test\" { 0: foo } }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no return slots") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_multi_return_too_many_bindings", func(t *testing.T) {
		src := "behavior a { let x, y = instruction \"test\" { 0: @1 } }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many bindings") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_fn_body_let_no_return_slot", func(t *testing.T) {
		src := "fn bad() { let x = instruction \"test\" { 0: foo } }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no return slots") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_fn_body_multi_return_too_many", func(t *testing.T) {
		src := "fn bad() { let x, y = instruction \"test\" { 0: @1 } }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many bindings") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Parameter direction enforcement ---

	t.Run("param_in_assign", func(t *testing.T) {
		src := `behavior a { @param in x "X"; $x = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_in_plusplus", func(t *testing.T) {
		src := `behavior a { @param in x "X"; $x++ }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_in_plusequals", func(t *testing.T) {
		src := `behavior a { @param in x "X"; $x += 1 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_read_fn_arg", func(t *testing.T) {
		src := `behavior a { @param out x "X"; domove $x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass out parameter to in parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_in_to_out_fn", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { @param in x \"X\"; writer out $x }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass in parameter to out parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_in_condition", func(t *testing.T) {
		src := `behavior a { @param out x "X"; if $x >= 5 { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_plusplus", func(t *testing.T) {
		src := `behavior a { @param out x "X"; $x++ }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_in_param_to_out", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nfn caller(x) { writer out x }\nbehavior a { caller 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass in parameter to out parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_out_param_as_input", func(t *testing.T) {
		src := "fn caller(out x) { notify x }\nbehavior a { var z = 5; caller z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass out parameter to in parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_to_out_param", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { let x = 5; writer out x }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass in parameter to out parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_out_param_in_instruction_input", func(t *testing.T) {
		// out param used in non-@N instruction slot (treated as input read)
		src := "fn bad(out x) { instruction \"notify\" { txt: x } }\nbehavior a { var z = 5; bad out z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_out_param_read_in_call", func(t *testing.T) {
		// out param used as input in a fn body call (notify reads x)
		src := "fn bad(out x) { notify x }\nbehavior a { var z = 5; bad out z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass out parameter to in parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// Positive tests: should compile successfully

	t.Run("param_out_assign_ok", func(t *testing.T) {
		src := `behavior a { @param out x "X"; $x = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_inout_both_ok", func(t *testing.T) {
		src := `behavior a { @param inout x "X"; $x += 1; domove $x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_direction_default_in", func(t *testing.T) {
		// Omitting direction defaults to "in" — passing a literal to an unadorned param should work
		src := "fn reader(x) { notify x }\nbehavior a { reader 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_out_param_ok", func(t *testing.T) {
		// out param in fn: callee writes to it via let binding, fine with a var argument + out annotation
		src := "fn writer(out target) { let target = get_self }\nbehavior a { var z = 5; writer out z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_in_while_condition", func(t *testing.T) {
		src := `behavior a { @param out x "X"; while $x <= 5 { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_in_instruction_input", func(t *testing.T) {
		src := `behavior a { @param out x "X"; instruction "notify" { 0: $x } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Call-site direction annotation enforcement ---

	t.Run("missing_out_annotation", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { var z = 5; writer z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing 'out' annotation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_inout_annotation", func(t *testing.T) {
		src := "fn updater(inout target) { instruction \"get_self\" { 0: target } }\nbehavior a { var z = 5; updater z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing 'inout' annotation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong_annotation_out_for_in", func(t *testing.T) {
		src := "fn reader(x) { notify x }\nbehavior a { reader out 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "argument has 'out' annotation but parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong_annotation_in_for_out", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { var z = 5; writer in z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "argument has 'in' annotation but parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_out_annotation_keyword", func(t *testing.T) {
		src := "fn my_fn(x, out kw result) { let result = get_self }\nbehavior a { var z = 5; my_fn 1, kw: z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing 'out' annotation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_out_annotation_fn_body", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nfn caller(x) { writer x }\nbehavior a { caller 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing 'out' annotation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// Call-site annotation positive tests

	t.Run("out_annotation_ok", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { var z = 5; writer out z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("inout_annotation_ok", func(t *testing.T) {
		src := "fn updater(inout target) { instruction \"add\" { 0: target  1: target  2: target } }\nbehavior a { var z = 5; updater inout z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("explicit_in_annotation_ok", func(t *testing.T) {
		// explicit "in" for an "in" param should be accepted
		src := "fn reader(x) { notify x }\nbehavior a { reader in 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_out_annotation_ok", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nfn caller(inout x) { writer out x }\nbehavior a { var z = 5; caller inout z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_out_annotation_ok", func(t *testing.T) {
		src := "fn my_fn(x, out kw result) { let result = get_self }\nbehavior a { var z = 5; my_fn 1, out kw: z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_var_name_null", func(t *testing.T) {
		src := `behavior a { var null = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_var_name_return", func(t *testing.T) {
		src := `behavior a { var return = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_var_name_true", func(t *testing.T) {
		src := `behavior a { var true = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_var_name_false", func(t *testing.T) {
		src := `behavior a { var false = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_assign_no_return_slot", func(t *testing.T) {
		src := `behavior a { var x = 5; x = instruction "test" { 0: x } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no return slots") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("direction_keyword_as_var_name", func(t *testing.T) {
		src := `behavior a { let out = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unit_as_var_name", func(t *testing.T) {
		src := `behavior a { let Unit = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unit_constructor", func(t *testing.T) {
		src := `behavior a { let x = Unit("foo") }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Unit has no constructor") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unit_constructor_fn_body", func(t *testing.T) {
		src := `fn f() { let x = Unit("foo") }
behavior a { f }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Unit has no constructor") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Scanner errors ---

	t.Run("single_pipe_error", func(t *testing.T) {
		src := `behavior a { let x = a | b }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "use '||' for logical OR") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_at_behavior_level", func(t *testing.T) {
		src := `behavior a { return }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "only be used inside function bodies") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_in_behavior_block", func(t *testing.T) {
		src := `behavior a { if true { return } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "only be used inside function bodies") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("else_without_if", func(t *testing.T) {
		src := `behavior a { else { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'else' without matching 'if'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_nested_in_fn_body", func(t *testing.T) {
		src := `behavior a { f } fn f() { fn g() { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot be nested") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_nested_in_behavior", func(t *testing.T) {
		src := `behavior a { fn g() { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot be nested") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("behavior_nested_in_behavior", func(t *testing.T) {
		src := `behavior a { behavior b { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot be nested") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("double_slash_comment", func(t *testing.T) {
		src := `behavior a { // this is a comment }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "use '#' for comments") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("single_equals_in_condition", func(t *testing.T) {
		src := `behavior a { var x = 1; if x = 5 { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "use '==' for comparison") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("single_equals_in_while", func(t *testing.T) {
		src := `behavior a { var x = 1; while x = 5 { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "use '==' for comparison") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Comparison expression errors ---

	t.Run("comparison_string_rhs", func(t *testing.T) {
		src := `behavior a { var x = 5; let r = x > "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("comparison_out_param_lhs", func(t *testing.T) {
		src := `behavior a { @param out x "X"; let r = $x > 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("comparison_assign_string_rhs", func(t *testing.T) {
		src := `behavior a { var x = 5; var r = 0; r = x > "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("comparison_out_param_rhs", func(t *testing.T) {
		src := `behavior a { @param out x "X"; var a = 5; let r = a > $x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Logical operator errors ---

	t.Run("logical_mixed_operators", func(t *testing.T) {
		// Implicit precedence: && binds tighter than ||
		src := `behavior a { var x = 5; var y = 3; var z = 1; let r = x > 2 && y < 10 || z > 0 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_number_truthy", func(t *testing.T) {
		// Numbers and bare variables are now valid as truthy terms in && / ||
		src := `behavior a { var x = 5; let r = x > 2 && 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_var_truthy", func(t *testing.T) {
		// Bare variable is a valid truthy term in &&
		src := `behavior a { var x = 5; var y = 3; let r = x > 2 && y }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_out_param_second", func(t *testing.T) {
		src := `behavior a { @param out x "X"; var a = 5; let r = a > 2 && $x < 10 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Equality operator errors ---

	t.Run("equality_string_rhs", func(t *testing.T) {
		src := `behavior a { var a = 5; let r = a != "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("negation_compiles", func(t *testing.T) {
		src := `behavior a { var x = 5; let r = !x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("double_negation_compiles", func(t *testing.T) {
		src := `behavior a { var x = 5; let r = !!x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("negation_in_if_compiles", func(t *testing.T) {
		src := `behavior a { var x = 5; if !x { notify "empty" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("negation_in_while_compiles", func(t *testing.T) {
		src := `behavior a { var x = 5; while !x { notify "waiting" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("negation_in_fn_body_compiles", func(t *testing.T) {
		src := `fn check(v) { let r = !v; return r } behavior a { var x = 5; let r = check x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("negation_assign_compiles", func(t *testing.T) {
		src := `behavior a { var x = 5; var r = 0; r = !x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("negation_chain_compiles", func(t *testing.T) {
		src := `behavior a { var x = 5; var y = 3; let r = !(x > 0 && y < 10) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("negation_is_compiles", func(t *testing.T) {
		src := `behavior a { let me = get_self; let r = !(me is Unit) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Type check (is) operator errors ---

	t.Run("is_unknown_type", func(t *testing.T) {
		src := `behavior a { let me = get_self; let a = me is Foo }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown type") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("is_missing_type", func(t *testing.T) {
		src := `behavior a { let me = get_self; let a = me is }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected type name after 'is'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("locked_as_variable_name", func(t *testing.T) {
		src := `behavior a { let locked = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unlocked_as_variable_name", func(t *testing.T) {
		src := `behavior a { let unlocked = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bare_lock_not_valid", func(t *testing.T) {
		src := `behavior a { lock }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			return // error expected — "lock" is no longer a keyword
		}
		t.Fatal("expected error for bare 'lock'")
	})

	t.Run("bare_unlock_not_valid", func(t *testing.T) {
		src := `behavior a { unlock }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			return // error expected — "unlock" is no longer a keyword
		}
		t.Fatal("expected error for bare 'unlock'")
	})

	t.Run("arith_string_rhs", func(t *testing.T) {
		src := `behavior a { var b = 5; let a = b + "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_out_param_lhs", func(t *testing.T) {
		src := `behavior a { @param out x "X"; let r = $x + 1 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_let_compound_assign", func(t *testing.T) {
		src := `behavior a { let x = 5; x -= 1 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_let_decrement", func(t *testing.T) {
		src := `behavior a { let x = 5; x-- }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Parenthesized boolean expression errors ---

	t.Run("paren_unclosed", func(t *testing.T) {
		src := `behavior a { var x = 5; var y = 3; let r = (x > 2 && y < 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected '}'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_empty", func(t *testing.T) {
		src := `behavior a { let r = () }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected identifier, number, or '('") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_mixed_suggests_parens", func(t *testing.T) {
		// Implicit precedence: && binds tighter than ||
		src := `behavior a { var x = 5; var y = 3; var z = 1; let r = x > 2 && y < 10 || z > 0 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Expression priority error tests ---

	t.Run("compound_assign_arith_string", func(t *testing.T) {
		src := `behavior a { var x = 5; x += "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "strings have no runtime representation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_chain_string_rhs", func(t *testing.T) {
		src := `behavior a { var b = 5; let a = b + 1 + "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("constructor_arith_not_supported", func(t *testing.T) {
		// Constructor followed by arithmetic operator is not a valid expression
		src := `behavior a { var b = 5; let a = Item("metalbar") + 1 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected statement") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// Positive test: parenthesized single value compiles as value passthrough
	t.Run("paren_value_passthrough", func(t *testing.T) {
		src := `behavior a { var x = 5; let a = (x) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- fn body expression and control flow error cases ---

	t.Run("fn_body_assign_to_let", func(t *testing.T) {
		src := "fn bad(x) { let a = x; a = 5 }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_assign_to_in_param", func(t *testing.T) {
		src := "fn bad(x) { x = 5 }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_compound_assign_to_let", func(t *testing.T) {
		src := "fn bad(x) { let a = x; a += 1 }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_read_out_param", func(t *testing.T) {
		src := "fn bad(out x) { let a = x }\nbehavior a { var z = 5; bad out z }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_var_underscore", func(t *testing.T) {
		src := "fn bad() { var _ = 5 }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot be used as a variable name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_let_keyword", func(t *testing.T) {
		src := "fn bad() { let true = 5 }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_var_constructor", func(t *testing.T) {
		src := "fn bad() { var Item = 5 }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "type constructor") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_for_keyword_iter", func(t *testing.T) {
		src := "fn bad() { for null in Range(5) { notify \"hi\" } }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_multi_let_keyword", func(t *testing.T) {
		src := "fn bad(x) { let false, b = separate_coordinate x }\nbehavior a { let c = Coordinate(1,2); bad c }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_keyword_assign", func(t *testing.T) {
		src := "behavior a { true = 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_constructor_increment", func(t *testing.T) {
		src := "behavior a { Item++ }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "type constructor") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_keyword_assign", func(t *testing.T) {
		src := "fn bad(x) { false = x }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_constructor_compound_assign", func(t *testing.T) {
		src := "fn bad(x) { Range += x }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "type constructor") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_continue", func(t *testing.T) {
		src := "behavior a { loop { continue } }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'continue' is not supported") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_continue", func(t *testing.T) {
		src := "fn bad() { loop { continue } }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'continue' is not supported") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_range_ampersand", func(t *testing.T) {
		src := "behavior a { set_reg Range(5) & 3 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'&' cannot be used with Range") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_range_ampersand_let", func(t *testing.T) {
		src := "behavior a { let x = Range(5) & 3 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'&' cannot be used with Range") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_range_ampersand", func(t *testing.T) {
		src := "fn bad() { let x = Range(5) & 3 }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'&' cannot be used with Range") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_range_ampersand_arg", func(t *testing.T) {
		src := "fn bad(x) { set_reg Range(5) & 3 }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'&' cannot be used with Range") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_ampersand_variable_let", func(t *testing.T) {
		src := "behavior a { var x = 1\nlet y = x & 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'&' requires a type constructor on the left side") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_ampersand_variable_assign", func(t *testing.T) {
		src := "behavior a { var x = 1\nx = x & 5 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'&' requires a type constructor on the left side") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_ampersand_variable", func(t *testing.T) {
		src := "fn bad(x) { let y = x & 5 }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'&' requires a type constructor on the left side") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_let_string_error", func(t *testing.T) {
		src := `behavior a { let x = "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "strings have no runtime representation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_assign_string_error", func(t *testing.T) {
		src := `behavior a { var x = 1` + "\n" + `x = "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "strings have no runtime representation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_let_string_error", func(t *testing.T) {
		src := `fn bad() { let x = "hello" }` + "\n" + `behavior a { bad }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "strings have no runtime representation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_let_var_copy", func(t *testing.T) {
		src := "behavior a { var x = 1\nlet y = x }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("let y = x should compile: %v", err)
		}
	})

	t.Run("bhv_assign_var_copy", func(t *testing.T) {
		src := "behavior a { var x = 1\nvar y = 2\ny = x }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("y = x should compile: %v", err)
		}
	})

	t.Run("bhv_let_param_copy", func(t *testing.T) {
		src := "behavior a { @param in x \"X\"\nlet y = $x }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("let y = $x should compile: %v", err)
		}
	})

	t.Run("bhv_let_unknown_still_errors", func(t *testing.T) {
		src := "behavior a { let y = unknown }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function or variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("undeclared_var_in_call_arg", func(t *testing.T) {
		src := `behavior a { @name "A"; set_reg undeclared }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function or variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("undeclared_var_assign_target", func(t *testing.T) {
		src := `behavior a { @name "A"; undeclared = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "undeclared variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("undeclared_var_fn_body_operand", func(t *testing.T) {
		src := "fn f() { set_reg undeclared }\nbehavior a { @name \"A\"; f }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function or variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("undeclared_var_fn_body_assign", func(t *testing.T) {
		src := "fn f() { undeclared = 5 }\nbehavior a { @name \"A\"; f }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "undeclared variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("is_number_error", func(t *testing.T) {
		src := "behavior a { let x = 1\nlet b = x is Number }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'is Number' is not supported") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("is_range_error", func(t *testing.T) {
		src := "fn bad(x) { let b = x is Range }\nbehavior a { bad 1 }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'is Range' is not supported") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_break_outside_loop", func(t *testing.T) {
		src := "behavior a { break }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// At behavior level, break outside loop should fail
	})

	t.Run("expr_list_too_many_expressions", func(t *testing.T) {
		src := `behavior a { let a, b = 1, 2, 3 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many expressions") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("expr_list_fn_no_return", func(t *testing.T) {
		src := `behavior a { let a, b = notify "hi", 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("expr_list_fn_body_too_many_expressions", func(t *testing.T) {
		src := "fn bad() { let a, b = 1, 2, 3 }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many expressions") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("expr_list_fn_body_fn_no_return", func(t *testing.T) {
		src := "fn bad() { let a, b = notify \"hi\", 5 }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("mode_block_expr_empty", func(t *testing.T) {
		src := `behavior a { let x = unlocked { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty mode block expression") {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.HasPrefix(err.Error(), "1:1:") {
			t.Fatalf("error should not report position 1:1: %v", err)
		}
	})

	t.Run("mode_block_expr_non_value_tail", func(t *testing.T) {
		src := `behavior a { let x = unlocked { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in mode block expression must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("mode_block_expr_fn_body_empty", func(t *testing.T) {
		src := `behavior a { let x = f } fn f() { let x = unlocked { }; return x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty mode block expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("mode_block_expr_fn_body_non_value_tail", func(t *testing.T) {
		src := `behavior a { let x = f } fn f() { let x = unlocked { notify "hi" }; return x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in mode block expression must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_in_while_compiles", func(t *testing.T) {
		src := `behavior a { var i = 1; while i <= 5 { if i >= 3 { break }; i += 1 } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("counted_loop_missing_brace", func(t *testing.T) {
		src := `behavior a { loop 5 notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// Should fail because 'notify' is not '{'
	})

	t.Run("fn_body_counted_loop_missing_brace", func(t *testing.T) {
		src := `behavior a { f } fn f() { loop 5 notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// Should fail because 'notify' is not '{'
	})

	t.Run("break_outside_loop_behavior", func(t *testing.T) {
		src := `behavior a { var i = 0 if i >= 1 { break } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'break' outside of loop or exec block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_outside_loop_fn_body", func(t *testing.T) {
		src := `behavior a { f } fn f() { break }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'break' outside of loop or exec block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("labeled_break_across_exec_boundary", func(t *testing.T) {
		src := `
fn my_iter(e) exec(body) {
    instruction "for_component" {
        0: e
        detach next: body
    }
}
behavior a {
    let e = get_self
    'outer: loop {
        my_iter(e) {
            body { break 'outer }
        }
    }
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown loop label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_loop_label_behavior", func(t *testing.T) {
		src := `behavior a { 'x: loop { 'x: loop { break } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate loop label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_loop_label_fn_body", func(t *testing.T) {
		src := `behavior a { f } fn f() { 'x: loop { 'x: loop { break } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate loop label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("labeled_break_compiles", func(t *testing.T) {
		src := `behavior a { var i = 0 'outer: loop { loop { if i >= 5 { break 'outer } i += 1 } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("labeled_break_fn_body_compiles", func(t *testing.T) {
		src := `behavior a { f } fn f() { var i = 0 'outer: loop { loop { if i >= 5 { break 'outer } i += 1 } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("break_unknown_label_behavior", func(t *testing.T) {
		src := `behavior a { var i = 0 'outer: loop { loop { if i >= 5 { break 'typo } i += 1 } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown loop label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_unknown_label_fn_body", func(t *testing.T) {
		src := `behavior a { f } fn f() { var i = 0 'outer: loop { loop { if i >= 5 { break 'typo } i += 1 } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown loop label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_label_shadows_fn_behavior", func(t *testing.T) {
		src := `fn f() {} behavior a { 'f: loop { loop { break 'f } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("break_label_shadows_fn_fn_body", func(t *testing.T) {
		src := `fn g() {} behavior a { h } fn h() { 'g: loop { loop { break 'g } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("if_expr_no_else_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "X" let r = if $x > 1 { 5 } notify "done", value: r }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("if_expr_empty_branch", func(t *testing.T) {
		src := `behavior a { @param in x "X" let r = if $x > 1 { } else { 5 } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty if-expression branch") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("if_expr_non_value_tail", func(t *testing.T) {
		src := `behavior a { @param in x "X" let r = if $x > 1 { notify "hi" } else { 5 } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in if-expression branch must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("if_expr_fn_body_no_else_compiles", func(t *testing.T) {
		src := `behavior a { let r = f } fn f() { let x = if 1 > 0 { 5 }; return x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("if_expr_fn_body_empty_branch", func(t *testing.T) {
		src := `behavior a { let r = f } fn f() { let x = if 1 > 0 { } else { 5 }; return x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty if-expression branch") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("if_expr_fn_body_non_value_tail", func(t *testing.T) {
		src := `behavior a { let r = f } fn f() { let x = if 1 > 0 { notify "hi" } else { 5 }; return x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in if-expression branch must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Range and for loop errors ---

	t.Run("range_step_zero", func(t *testing.T) {
		src := `behavior a { for i in Range(0, 10, 0) { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Range step cannot be zero") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("range_step_zero_fn_body", func(t *testing.T) {
		src := `fn f() { for i in Range(0, 10, 0) { notify "hi" } } behavior a { f }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "Range step cannot be zero") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("for_missing_in", func(t *testing.T) {
		src := `behavior a { for i Range(5) { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected 'in'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("for_non_ident_iter_var", func(t *testing.T) {
		src := `behavior a { for "x" in Range(5) { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// Parser expects an identifier for the iteration variable
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("for_keyword_iter_var", func(t *testing.T) {
		src := `behavior a { for if in Range(5) { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("for_assign_to_iter_var", func(t *testing.T) {
		src := `behavior a { for i in Range(5) { i = 3 } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("for_duplicate_label", func(t *testing.T) {
		src := `behavior a { 'x: for i in Range(5) { 'x: for j in Range(3) { notify "hi" } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate loop label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Wait errors ---

	t.Run("wait_empty_block", func(t *testing.T) {
		src := `behavior a { wait 5 { } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty wait block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wait_non_value_tail", func(t *testing.T) {
		src := `behavior a { wait 5 { notify "hi" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in wait block must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wait_empty_block_fn_body", func(t *testing.T) {
		src := `fn f() { wait 5 { } } behavior a { f }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty wait block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wait_non_value_tail_fn_body", func(t *testing.T) {
		src := `fn f() { wait 5 { notify "hi" } } behavior a { f }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in wait block must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_call_missing_rparen", func(t *testing.T) {
		src := `behavior a { notify("Hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_call_missing_rparen_fn_body", func(t *testing.T) {
		src := `fn f() { notify("Hello" } behavior a { f }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_call_compiles", func(t *testing.T) {
		src := `behavior a { notify("Hello"); let me = get_self(); notify("Hi", value: me) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_cmp_arg_unclosed", func(t *testing.T) {
		src := `behavior a { @param in x "x"; let r = set_reg ($x > 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for unclosed parenthesized expression")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_cmp_fn_arg_unclosed", func(t *testing.T) {
		src := `
fn helper(val) { return instruction "set_reg" { 0: val; 1: @1 } }
fn caller(x) { let r = helper (x > 5; return r }
behavior a { @param in x "x"; let r = caller $x }
`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for unclosed parenthesized expression in fn body")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_cmp_arg_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "x"; let r = set_reg ($x > 5) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bool_fn_call_compiles", func(t *testing.T) {
		src := `
fn get_flag() { return instruction "get_var" { 0: "flag"; 1: @1 } }
behavior a { @param in d "d"; let r = $d || get_flag }
`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Behavior-level if/while full expression tests ---

	t.Run("bhv_if_var_rhs_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "X"; @param in y "Y"; if $x > $y { notify "yes" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_if_bool_chain_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "X"; @param in y "Y"; if $x > 5 && $y < 10 { notify "yes" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_if_is_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "X"; if $x is Unit { notify "unit" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_if_truthy_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "X"; if $x { notify "yes" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_if_equality_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "X"; if $x == null { notify "null" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_while_var_rhs_compiles", func(t *testing.T) {
		src := `behavior a { @param in limit "Limit"; var i = 0; while i < $limit { notify "tick"; i++ } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_while_bool_chain_compiles", func(t *testing.T) {
		src := `behavior a { var i = 0; var active = 1; while i < 10 && active { notify "tick"; i++ } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_if_fn_call_condition_compiles", func(t *testing.T) {
		src := `
fn get_count(in v) { return instruction "get_number" { 0: v; 1: @1 } }
behavior a { @param in x "X"; if get_count $x > 5 { notify "big" } }
`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_if_arith_condition_compiles", func(t *testing.T) {
		src := `behavior a { @param in x "X"; @param in y "Y"; if $x + 1 >= $y - 2 { notify "yes" } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_if_break_chain_compiles", func(t *testing.T) {
		src := `behavior a { var i = 0; var j = 0; loop { if i > 5 && j > 3 { break }; i++; j++ } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Unary minus / negative number tests ---

	t.Run("neg_let_literal_compiles", func(t *testing.T) {
		src := `behavior a { let x = -5; set_reg x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("neg_let_variable_compiles", func(t *testing.T) {
		src := `behavior a { @param in p "P"; let x = -$p; set_reg x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("neg_assign_compiles", func(t *testing.T) {
		src := `behavior a { var x = 0; x = -5; set_reg x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("neg_fn_body_compiles", func(t *testing.T) {
		src := `fn neg(p) { let x = -p; return x }; behavior a { @param in p "P"; let x = neg $p; set_reg x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("neg_in_call_arg_compiles", func(t *testing.T) {
		src := `behavior a { @param in p "P"; set_reg -$p }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("neg_in_comparison_compiles", func(t *testing.T) {
		src := `behavior a { @param in p "P"; let x = $p > -5; set_reg x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("neg_string_error", func(t *testing.T) {
		src := `behavior a { let x = -"hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for negating a string")
		}
	})

	t.Run("fn_mixed_modifier_immutable_assign", func(t *testing.T) {
		src := `fn f(coord) { var a, let b = separate_coordinate coord; b = a }
		        behavior a { @name "A" f Coordinate(1, 2) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error assigning to immutable let binding in mixed modifier list")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_underscore_not_variable", func(t *testing.T) {
		src := `fn f() { let _ = 5 }
		        behavior a { @name "A" f }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error using _ as variable name")
		}
		if !strings.Contains(err.Error(), "'_' cannot be used as a variable name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Block scoping tests ---

	t.Run("block_scoping_let_redeclare_after_block", func(t *testing.T) {
		// let x in if-block should go out of scope, allowing re-declaration after
		src := `behavior a {
			@name "A"
			@param in p "P"
			if $p {
				let x = 5
				set_reg x
			}
			let x = 10
			set_reg x
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected block-scoped let to allow re-declaration after block, got: %v", err)
		}
	})

	t.Run("block_scoping_assign_after_let_block", func(t *testing.T) {
		// let x in if-block should not prevent assignment to x after block (behavior level)
		src := `behavior a {
			@name "A"
			@param in p "P"
			if $p {
				let x = get_self
			}
			var x = get_self
			set_reg x
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected let in block not to leak immutability, got: %v", err)
		}
	})

	t.Run("block_scoping_sibling_branches", func(t *testing.T) {
		// Same name in sibling if/else branches — both scopes are independent
		src := `behavior a {
			@name "A"
			@param in p "P"
			if $p {
				let x = 5
				set_reg x
			} else {
				let x = 10
				set_reg x
			}
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected same name in sibling branches to work, got: %v", err)
		}
	})

	t.Run("fn_block_scoping_let_redeclare_after_block", func(t *testing.T) {
		// fn body: let x in if-block should go out of scope
		src := `fn f(in cond) {
			if cond {
				let x = 5
				set_reg x
			}
			let x = 10
			set_reg x
		}
		behavior a { @name "A" @param in p "P" f $p }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected fn body block-scoped let to allow re-declaration, got: %v", err)
		}
	})

	t.Run("fn_block_scoping_immutability_contained", func(t *testing.T) {
		// fn body: let x in block should not prevent var x after block
		src := `fn f(in cond) {
			if cond {
				let x = 5
				set_reg x
			}
			var x = 10
			x = 20
			set_reg x
		}
		behavior a { @name "A" @param in p "P" f $p }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected let immutability to be contained in block, got: %v", err)
		}
	})

	t.Run("keyword_import_as_var", func(t *testing.T) {
		src := `behavior a { let import = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_from_as_var", func(t *testing.T) {
		src := `behavior a { let from = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_as_var", func(t *testing.T) {
		src := `behavior a { let as = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_import_as_fn_name", func(t *testing.T) {
		src := `fn import() { notify "hi" } behavior a { import }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_from_as_fn_name", func(t *testing.T) {
		src := `fn from() { notify "hi" } behavior a { from }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_as_fn_name", func(t *testing.T) {
		src := `fn as() { notify "hi" } behavior a { as }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_after_fn", func(t *testing.T) {
		src := `fn my_fn() { notify "hi" }
		import hello from "./lib"
		behavior a { my_fn }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "import statements must appear before") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_after_behavior", func(t *testing.T) {
		src := `behavior a { notify "hi" }
		import hello from "./lib"`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "import statements must appear before") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_duplicate_names", func(t *testing.T) {
		src := `import hello, hello from "./lib"
		behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate import name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_duplicate_names_across_stmts", func(t *testing.T) {
		src := `import hello from "./a"
		import hello from "./b"
		behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate import name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_named_collides_with_namespace", func(t *testing.T) {
		src := `import hello from "./a"
		import "./b" as hello
		behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate import name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_keyword_alias", func(t *testing.T) {
		src := `import hello as fn from "./lib"
		behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_keyword_namespace", func(t *testing.T) {
		src := `import "./lib" as fn
		behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_bad_path", func(t *testing.T) {
		src := `import hello from "lib"
		behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "import path must start with") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_empty_path", func(t *testing.T) {
		src := `import hello from ""
		behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "import path cannot be empty") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Duplicate function errors ---

	t.Run("fn_duplicate_name", func(t *testing.T) {
		src := `fn greet() { notify "hi" }
fn greet() { notify "hello" }
behavior a { greet }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_duplicate_private", func(t *testing.T) {
		src := `fn greet() { notify "hi" }
private fn greet() { notify "hello" }
behavior a { greet }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_override_stdlib_ok", func(t *testing.T) {
		// Overriding a stdlib function with a same-name user fn should be allowed
		src := `fn notify(txt) { instruction "notify" { txt: txt } }
behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Constant declaration errors ---

	t.Run("const_duplicate_name", func(t *testing.T) {
		src := `const X = 5
const X = 10
behavior a { let x = X }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate constant") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_collides_with_fn", func(t *testing.T) {
		src := `fn greet() { notify "hi" }
const greet = 5
behavior a { let x = greet }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with a function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_collides_with_const", func(t *testing.T) {
		src := `const greet = 5
fn greet() { notify "hi" }
behavior a { let x = greet }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with a constant") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_forward_reference", func(t *testing.T) {
		src := `const A = B
const B = 5
behavior a { let x = A }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not a compile-time constant") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_runtime_variable", func(t *testing.T) {
		src := `const X = y
behavior a { let y = 5 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not a compile-time constant") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_keyword_name", func(t *testing.T) {
		src := `const if = 5
behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_underscore_name", func(t *testing.T) {
		src := `const _ = 5
behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot be used as a constant name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Compile-time evaluator errors ---

	t.Run("const_fn_call_bail", func(t *testing.T) {
		// Function that hits instruction → should bail
		src := `fn runtime_fn(x) {
    return instruction "set_number" {
        2: x
        3: @1
    }
}
const X = runtime_fn(5)
behavior a { notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not compile-time evaluable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_fn_call_ok", func(t *testing.T) {
		// Pure function call should succeed
		src := `fn double(x) { return x * 2 }
const X = double(5)
behavior a { set_reg X }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("const_fn_call_transitive", func(t *testing.T) {
		// Function calling another function should work
		src := `fn double(x) { return x * 2 }
fn quadruple(x) { return double(double(x)) }
const X = quadruple(3)
behavior a { set_reg X }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	// --- Enum error tests ---

	t.Run("enum_duplicate_member", func(t *testing.T) {
		src := `enum Dir { North; South; North }
behavior a { let x = Dir::North }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate enum member") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enum_duplicate_value", func(t *testing.T) {
		src := `enum Dir { North; South = 0 }
behavior a { let x = Dir::North }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "same value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enum_collides_with_fn", func(t *testing.T) {
		src := `fn Dir() { notify "hi" }
enum Dir { North }
behavior a { let x = Dir::North }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with a function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_collides_with_enum", func(t *testing.T) {
		src := `enum Dir { North }
fn Dir() { notify "hi" }
behavior a { let x = Dir::North }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with an enum") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enum_collides_with_const", func(t *testing.T) {
		src := `const Dir = 5
enum Dir { North }
behavior a { let x = Dir::North }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with a constant") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_collides_with_enum", func(t *testing.T) {
		src := `enum Dir { North }
const Dir = 5
behavior a { let x = Dir::North }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with an enum") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enum_unknown_member", func(t *testing.T) {
		src := `enum Dir { North; South }
behavior a { let x = Dir::West }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no member") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enum_bare_name", func(t *testing.T) {
		src := `enum Dir { North; South }
behavior a { let x = Dir }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "requires '::'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enum_bare_name_fn_body", func(t *testing.T) {
		src := `enum Dir { North; South }
fn use_dir(x) { return Dir }
behavior a { let x = use_dir 1 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "requires '::'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enum_empty", func(t *testing.T) {
		src := `enum Dir {}
behavior a { @name "A" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no members") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- exec / continuation error cases ---

	t.Run("exec_empty", func(t *testing.T) {
		src := `fn f(x) exec() { instruction "nop" }
behavior a { @name "A" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "exec() requires at least one") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_duplicate_name", func(t *testing.T) {
		src := `fn f(x) exec(a, a) { instruction "nop" }
behavior a { @name "A" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate continuation name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_collides_with_param", func(t *testing.T) {
		src := `fn f(x) exec(x) { instruction "nop" }
behavior a { @name "A" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with parameter name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_unknown_continuation_at_call", func(t *testing.T) {
		src := `fn f(x) exec(a, b) {
	instruction "nop" { exec 0: a; next: b }
}
behavior t { @name "T"; f(get_self) { c { notify "bad" } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown continuation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_duplicate_continuation_block", func(t *testing.T) {
		src := `fn f(x) exec(a, b) {
	instruction "nop" { exec 0: a; next: b }
}
behavior t { @name "T"; f(get_self) { a { notify "1" } a { notify "2" } } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate continuation block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_binding_not_in_exec_list", func(t *testing.T) {
		src := `fn f(x) exec(a) {
	instruction "nop" { exec 0: z }
}
behavior t { @name "T" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not declared") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_binding_args_empty_parens", func(t *testing.T) {
		src := `fn f(x) exec(a) {
	instruction "nop" { exec 0: a() }
}
behavior t { @name "T" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty exec binding") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_binding_args_invalid_at_index", func(t *testing.T) {
		src := `fn f(x) exec(a) {
	instruction "nop" { exec 0: a(@0) }
}
behavior t { @name "T" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "@N index must be >= 1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_cont_data_empty_parens", func(t *testing.T) {
		src := `fn f(a) exec(yes) {
	if a > 5 { return yes() }
	return yes
}
behavior a { @name "A" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty continuation arg list") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_cont_data_inconsistent_count", func(t *testing.T) {
		src := `fn f(a) exec(yes, no) {
	if a > 5 { return yes(a, 1) }
	return yes(a)
}
behavior a { @name "A" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "inconsistent arg count") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_block_params_exceed_data_none", func(t *testing.T) {
		src := `fn f(a) exec(yes, no) {
	if a > 5 { return yes }
	return no
}
behavior t {
	@name "T"
	var x = 10
	f(x) { yes { v -> notify "oops", value: v } }
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "does not provide data") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_block_params_exceed_data_count", func(t *testing.T) {
		src := `fn f(a) exec(yes, no) {
	if a > 5 { return yes(a) }
	return no
}
behavior t {
	@name "T"
	var x = 10
	f(x) { yes { v, extra -> notify "oops" } }
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "provides 1 data arg(s), but block has 2 binding(s)") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("exec_block_params_exceed_instruction_data", func(t *testing.T) {
		src := `fn f(value, target) exec(larger, smaller, equal) {
	instruction "check_number" {
		exec 0: larger(@1)
		exec 1: smaller
		2: value
		3: target
		4: @1
		next: equal
	}
}
behavior t {
	@name "T"
	let a = get_self
	let b = get_self
	f(a, b) { smaller { x -> notify "oops" } }
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "does not provide data") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Iterator error cases ---

	t.Run("iter_too_many_vars", func(t *testing.T) {
		src := `behavior a {
	@name "A"
	for a, b, c in for_component() {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many variables") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("iter_range_multiple_vars", func(t *testing.T) {
		src := `behavior a {
	@name "A"
	for a, b in Range(5) {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "binds exactly one variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("iter_yield_outside_iter", func(t *testing.T) {
		src := `fn f() {
	yield 1
}
behavior a {
	@name "A"
	f
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "yield") && !strings.Contains(err.Error(), "iter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("iter_for_in_with_fn", func(t *testing.T) {
		src := `fn f() exec(body, done) {
	instruction "for_component" {
		0: @1
		1: @2
		detach next: body(@1, @2)
		exec 2: done
	}
}
behavior a {
	@name "A"
	for x in f() {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not an iterator") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("iter_yield_count_mismatch", func(t *testing.T) {
		src := `iter my_iter() -> a, b {
	for c, idx in for_component() {
		yield c
	}
}
behavior a {
	@name "A"
	for x, y in my_iter() {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "yield produces") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

}

func TestCompileWarnings(t *testing.T) {
	stdlib := os.DirFS("stdlib")

	t.Run("same_scope_shadow_unused", func(t *testing.T) {
		src := `behavior a {
			@name "A"
			let x = 5
			let x = 10
			set_reg x
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(warnings) == 0 {
			t.Fatal("expected a shadowing warning for unused let re-declaration")
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "shadows a previous declaration") && strings.Contains(w, `"x"`) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected shadowing warning for x, got warnings: %v", warnings)
		}
	})

	t.Run("same_scope_shadow_used_no_warning", func(t *testing.T) {
		src := `behavior a {
			@name "A"
			let x = 5
			set_reg x
			let x = 10
			set_reg x
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "shadows") && strings.Contains(w, `"x"`) {
				t.Fatalf("expected no shadowing warning when variable was used, got: %s", w)
			}
		}
	})

	t.Run("child_scope_no_warning", func(t *testing.T) {
		src := `behavior a {
			@name "A"
			@param in p "P"
			let x = 5
			if $p {
				let x = 10
				set_reg x
			}
			set_reg x
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "shadows") && strings.Contains(w, `"x"`) {
				t.Fatalf("expected no shadowing warning for child scope, got: %s", w)
			}
		}
	})

	t.Run("fn_same_scope_shadow_unused", func(t *testing.T) {
		src := `fn f() {
			let x = 5
			let x = 10
			set_reg x
		}
		behavior a { @name "A" f }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(warnings) == 0 {
			t.Fatal("expected a shadowing warning in fn body")
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "shadows a previous declaration") && strings.Contains(w, `"x"`) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected shadowing warning for x in fn body, got warnings: %v", warnings)
		}
	})

	t.Run("fn_same_scope_shadow_used_no_warning", func(t *testing.T) {
		src := `fn f() {
			let x = 5
			set_reg x
			let x = 10
			set_reg x
		}
		behavior a { @name "A" f }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "shadows") && strings.Contains(w, `"x"`) {
				t.Fatalf("expected no shadowing warning in fn body when variable was used, got: %s", w)
			}
		}
	})

	t.Run("fn_child_scope_no_warning", func(t *testing.T) {
		src := `fn f(in cond) {
			let x = 5
			if cond {
				let x = 10
				set_reg x
			}
			set_reg x
		}
		behavior a { @name "A" @param in p "P" f $p }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "shadows") && strings.Contains(w, `"x"`) {
				t.Fatalf("expected no shadowing warning for child scope in fn body, got: %s", w)
			}
		}
	})

	t.Run("var_shadow_unused", func(t *testing.T) {
		src := `behavior a {
			@name "A"
			var x = 5
			var x = 10
			set_reg x
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(warnings) == 0 {
			t.Fatal("expected a shadowing warning for unused var re-declaration")
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "shadows a previous declaration") && strings.Contains(w, `"x"`) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected shadowing warning for var x, got warnings: %v", warnings)
		}
	})

	t.Run("unreachable_after_exit_bhv", func(t *testing.T) {
		src := `behavior a {
			exit
			notify "dead"
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(warnings) == 0 {
			t.Fatal("expected unreachable code warning")
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'exit'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after exit, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_exit_fn", func(t *testing.T) {
		src := `fn f() {
			exit
			notify "dead"
		}
		behavior a { f }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'exit'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after exit in fn body, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_break", func(t *testing.T) {
		src := `behavior a {
			loop {
				break
				notify "dead"
			}
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'break'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after break, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_return", func(t *testing.T) {
		src := `fn f() {
			return 1
			notify "dead"
		}
		behavior a { let x = f }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'return'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after return, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_exit_in_nested_block", func(t *testing.T) {
		src := `behavior a {
			@param in p "P"
			if $p {
				exit
				notify "dead"
			}
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'exit'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning in nested block, got: %v", warnings)
		}
	})

	t.Run("no_unreachable_warning_when_terminal_is_last", func(t *testing.T) {
		src := `behavior a {
			notify "hello"
			exit
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "unreachable") {
				t.Fatalf("expected no unreachable warning when exit is last, got: %s", w)
			}
		}
	})

	t.Run("unreachable_after_last_bhv", func(t *testing.T) {
		src := `behavior a {
			last
			notify "dead"
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'last'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after last, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_last_fn", func(t *testing.T) {
		src := `fn f() {
			last
			notify "dead"
		}
		behavior a { f }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'last'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after last in fn body, got: %v", warnings)
		}
	})
}

func TestImports(t *testing.T) {
	stdlib := os.DirFS("stdlib")

	t.Run("named_import", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./lib"
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("named_import_with_alias", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet as hello from "./lib"
behavior a { hello }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_import", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./lib"
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }
private fn secret() { notify "Secret" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_no_private", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./lib"
behavior a { secret }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }
private fn secret() { notify "Secret" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error — private fn should not be imported by glob")
		}
	})

	t.Run("private_import_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import secret from "./lib"
behavior a { secret }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`private fn secret() { notify "Secret" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "private") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("circular_import_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"a.doit": &fstest.MapFile{Data: []byte(`import b_fn from "./b"
fn a_fn() { notify "A" }
behavior a { b_fn }`)},
			"b.doit": &fstest.MapFile{Data: []byte(`import a_fn from "./a"
fn b_fn() { notify "B" }`)},
		}
		src, _ := sourceFS.ReadFile("a.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "a.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "circular import") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("self_import_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./main"
fn greet() { notify "Hello" }
behavior a { greet }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "import itself") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_collides_with_named_import", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./lib"
fn greet() { notify "Local" }
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with a named import") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_collides_with_namespace", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
fn lib() { notify "Local" }
behavior a { lib }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with an import namespace") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_shadowed_by_same_file", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./lib"
fn greet() { notify "Local" }
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_last_wins", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./a"
import * from "./b"
behavior a { greet }`)},
			"a.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "A" }`)},
			"b.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "B" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_rename_replaces_original", func(t *testing.T) {
		// import *, greet as hi — greet should only be accessible as hi
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import *, greet as hi from "./lib"
behavior a { hi }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }
fn farewell() { notify "Bye" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_rename_original_not_accessible", func(t *testing.T) {
		// The original name should not be accessible when renamed via glob+alias
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import *, greet as hi from "./lib"
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatalf("expected error for accessing renamed function by original name")
		}
	})

	t.Run("glob_rename_other_fns_still_accessible", func(t *testing.T) {
		// Other glob-imported functions (not renamed) should still be accessible
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import *, greet as hi from "./lib"
behavior a { farewell }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }
fn farewell() { notify "Bye" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("import_behaviors_ignored", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./lib"
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }
behavior ignored { notify "Ignored" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("not_found_import", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./lib"
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn other() { notify "Other" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_file_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./nonexistent"
behavior a { notify "hi" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read import") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("parent_path_import", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"sub/main.doit": &fstest.MapFile{Data: []byte(`import greet from "../lib"
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("sub/main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "sub/main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("imported_file_parse_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./lib"
behavior a { greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet( { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "lib.doit") {
			t.Fatalf("expected error mentioning lib.doit, got: %v", err)
		}
	})

	t.Run("stdin_import_error", func(t *testing.T) {
		src := `import greet from "./lib"
behavior a { greet }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "imports require a source file path") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("named_import_with_return_value", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import get_val from "./lib"
behavior a { let x = get_val; set_reg x }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn get_val() {
	return instruction "get_self" { 0: @1 }
}`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("transitive_import", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./a"
behavior main { greet }`)},
			"a.doit": &fstest.MapFile{Data: []byte(`import say from "./b"
fn greet() { say }`)},
			"b.doit": &fstest.MapFile{Data: []byte(`fn say() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_qualified_call", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main { lib.greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_qualified_call_with_return", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main { let me = lib.get_me }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn get_me() { return instruction "get_self" { 0: @1 } }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_qualified_call_in_fn_body", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
fn do_greet() { lib.greet }
behavior main { do_greet }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_private_fn_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main { lib.secret }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`private fn secret() { notify "Secret" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error for private function access")
		}
		if !strings.Contains(err.Error(), "private") {
			t.Fatalf("expected private function error, got: %v", err)
		}
	})

	t.Run("namespace_fn_not_found_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main { lib.nonexistent }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error for nonexistent function")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected function not found error, got: %v", err)
		}
	})

	t.Run("namespace_qualified_in_let_rhs", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
fn wrapper() { let me = lib.get_me; return me }
behavior main { let u = wrapper }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn get_me() { return instruction "get_self" { 0: @1 } }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_qualified_in_assignment", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main {
	var me = lib.get_me
	me = lib.get_me
}`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn get_me() { return instruction "get_self" { 0: @1 } }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_qualified_in_call_arg", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main { set_reg lib.get_me }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn get_me() { return instruction "get_self" { 0: @1 } }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_with_named_imports", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import greet from "./lib" as lib
behavior main {
	greet
	lib.greet
}`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Constant import tests ---

	t.Run("named_import_const", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import COUNT from "./lib"
behavior a { let x = COUNT }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`const COUNT = 42`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_import_const", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./lib"
behavior a { let x = COUNT }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`const COUNT = 42
private const SECRET = 99`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_no_private_const", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./lib"
behavior a { let x = SECRET }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`const COUNT = 42
private const SECRET = 99`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error — private const should not be imported by glob")
		}
	})

	t.Run("private_const_import_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import SECRET from "./lib"
behavior a { let x = SECRET }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`private const SECRET = 99`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "private") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_const", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior a { let x = lib.COUNT }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`const COUNT = 42`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_private_const_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior a { let x = lib.SECRET }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`private const SECRET = 99`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error for private constant access")
		}
		if !strings.Contains(err.Error(), "private") {
			t.Fatalf("expected private constant error, got: %v", err)
		}
	})

	t.Run("namespace_discard_call_in_nested_block", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main {
	let x = 1
	if x > 0 {
		_ = lib.get_me
	}
}`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn get_me() { return instruction "get_self" { 0: @1 } }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_call_in_nested_block", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main {
	let x = 1
	if x > 0 {
		lib.greet
	}
}`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn greet() { notify "Hello" }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("const_via_imported_fn", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import double from "./lib"
const X = double(5)
behavior a { set_reg X }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`fn double(x) { return x * 2 }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Enum import tests ---

	t.Run("named_import_enum", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import Dir from "./lib"
behavior a { let x = Dir::North }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`enum Dir { North; South; East; West }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_import_enum", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./lib"
behavior a { let x = Dir::South }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`enum Dir { North; South; East; West }
private enum Secret { A; B }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("glob_no_private_enum", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import * from "./lib"
behavior a { let x = Secret::A }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`enum Dir { North }
private enum Secret { A; B }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error — private enum should not be imported by glob")
		}
	})

	t.Run("private_enum_import_error", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import Secret from "./lib"
behavior a { let x = Secret::A }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`private enum Secret { A; B }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "private") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_enum", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior a { let x = lib.Dir::East }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`enum Dir { North; South; East; West }`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestCompileErrorFlag(t *testing.T) {
	// Write source that produces a shadowing warning.
	src := "behavior a {\n\t@name \"A\"\n\tlet x = 5\n\tlet x = 10\n\tset_reg x\n}\n"
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "warn.doit")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("without_flag_succeeds", func(t *testing.T) {
		outPath := filepath.Join(tmpDir, "out1.txt")
		err := cmdCompile([]string{"-o", outPath, srcPath})
		if err != nil {
			t.Fatalf("expected success without -e flag, got: %v", err)
		}
	})

	t.Run("short_flag_fails", func(t *testing.T) {
		outPath := filepath.Join(tmpDir, "out2.txt")
		err := cmdCompile([]string{"-e", "-o", outPath, srcPath})
		if err == nil {
			t.Fatal("expected error with -e flag")
		}
		if !strings.Contains(err.Error(), "shadows") {
			t.Fatalf("expected shadowing warning as error, got: %v", err)
		}
	})

	t.Run("long_flag_fails", func(t *testing.T) {
		outPath := filepath.Join(tmpDir, "out3.txt")
		err := cmdCompile([]string{"-error", "-o", outPath, srcPath})
		if err == nil {
			t.Fatal("expected error with --error flag")
		}
		if !strings.Contains(err.Error(), "shadows") {
			t.Fatalf("expected shadowing warning as error, got: %v", err)
		}
	})
}

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

func TestCodec(t *testing.T) {
	encodedFiles, err := filepath.Glob("codec/tests/*.encoded")
	if err != nil {
		t.Fatal(err)
	}
	if len(encodedFiles) == 0 {
		t.Fatal("no test cases found in codec/tests/*.encoded")
	}

	for _, encodedFile := range encodedFiles {
		name := strings.TrimSuffix(filepath.Base(encodedFile), ".encoded")
		decodedFile := strings.TrimSuffix(encodedFile, ".encoded") + ".decoded"

		f, err := os.Open(encodedFile)
		if err != nil {
			t.Fatal(err)
		}
		obj, err := codec.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s: Decode error: %v", name, err)
		}

		decodedBytes, err := os.ReadFile(decodedFile)
		if err != nil {
			t.Fatal(err)
		}
		wantType, wantVal := parseDecodedFile(t, string(decodedBytes))
		// Convert reference implementation output (0-based keys) to our
		// native format (1-based keys).
		wantVal = refToNative(wantVal)

		t.Run(name+"/decode", func(t *testing.T) {
			if obj.Type != wantType {
				t.Errorf("type: got %s, want %s", obj.Type, wantType)
			}
			if !reflect.DeepEqual(obj.Value, wantVal) {
				gotJSON, _ := json.MarshalIndent(obj.Value, "", "    ")
				t.Errorf("value mismatch.\ngot:\n%s", string(gotJSON))
			}
		})

		t.Run(name+"/roundtrip", func(t *testing.T) {
			encoded, err := codec.EncodeString(obj)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			obj2, err := codec.Decode(strings.NewReader(encoded))
			if err != nil {
				t.Fatalf("Decode(re-encoded) error: %v", err)
			}

			if obj2.Type != wantType {
				t.Errorf("type: got %s, want %s", obj2.Type, wantType)
			}
			if !reflect.DeepEqual(obj2.Value, wantVal) {
				gotJSON, _ := json.MarshalIndent(obj2.Value, "", "    ")
				t.Errorf("value mismatch.\ngot:\n%s", string(gotJSON))
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	t.Run("empty_input", func(t *testing.T) {
		_, err := codec.DecodeString("")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too short") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("too_short", func(t *testing.T) {
		_, err := codec.DecodeString("DS")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too short") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_prefix", func(t *testing.T) {
		_, err := codec.DecodeString("XXCabcde")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "does not begin with") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid_base62_character", func(t *testing.T) {
		_, err := codec.DecodeString("DSC!!!!!!!!!")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("bad_checksum", func(t *testing.T) {
		// Take a valid-looking string and corrupt the checksum (last char)
		// The format is DS + type + base62_u32(decompressLen) + base62_data + checksum_char
		// Construct a minimal one with a wrong checksum
		_, err := codec.DecodeString("DSC0a0b0c0d0")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("corrupted_zlib", func(t *testing.T) {
		// A string that claims to have compressed data but the data is garbage.
		// The decompressLen > 0 triggers zlib decompression.
		// "DSC" + base62 for a non-zero decompressLen + some base62 data
		// We'll use a known-encoded string and corrupt the data portion.
		_, err := codec.DecodeString("DSCz00000000000z")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func parseDecodedFile(t *testing.T, content string) (codec.ObjectType, any) {
	t.Helper()
	s := strings.TrimSpace(content)
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) != 2 {
		t.Fatal("decoded file must have type line + JSON")
	}
	typ := codec.ObjectType(lines[0][0])
	val, err := codec.UnmarshalJSON([]byte(strings.TrimSpace(lines[1])))
	if err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	return typ, val
}

// refToNative converts reference implementation decoded output to match our
// codec's native format. The reference JS codec stores Lua integer table keys
// as 0-based strings (k - 1); our codec preserves Lua's native 1-based
// numbering. This function shifts all numeric string keys in maps by +1.
func refToNative(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, child := range val {
			newKey := k
			if n, err := strconv.Atoi(k); err == nil {
				newKey = strconv.Itoa(n + 1)
			}
			result[newKey] = refToNative(child)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = refToNative(elem)
		}
		return result
	default:
		return v
	}
}

// matchBehaviors compares two behavior maps using graph isomorphism.
// Frame numbers may differ between got and want; the comparison builds a
// bijective frame-number mapping via BFS from frame "1".
func matchBehaviors(t *testing.T, got, want map[string]any) {
	t.Helper()

	// 1. Compare "name" directly.
	if got["name"] != want["name"] {
		t.Errorf("name mismatch: got %v, want %v", got["name"], want["name"])
		return
	}

	// Compare "parameters" and "pnames" if present.
	for _, key := range []string{"parameters", "pnames"} {
		gv, gok := got[key]
		wv, wok := want[key]
		if gok != wok {
			t.Errorf("%s presence mismatch: got %v, want %v", key, gok, wok)
			return
		}
		if gok && !reflect.DeepEqual(gv, wv) {
			t.Errorf("%s mismatch: got %v, want %v", key, gv, wv)
			return
		}
	}

	// 2. Count numeric-string keys (frames). Must be equal.
	gotFrames := numericKeys(got)
	wantFrames := numericKeys(want)
	if len(gotFrames) != len(wantFrames) {
		gotJSON, _ := json.MarshalIndent(got, "", "    ")
		t.Errorf("frame count mismatch: got %d, want %d\ngot:\n%s",
			len(gotFrames), len(wantFrames), gotJSON)
		return
	}

	if len(gotFrames) == 0 {
		return
	}

	// 3. BFS from ("1", "1").
	g2w := map[string]string{} // got frame key → want frame key
	w2g := map[string]string{} // want frame key → got frame key

	type framePair struct{ g, w string }
	var queue []framePair
	failed := false

	addMapping := func(g, w string) {
		if ew, ok := g2w[g]; ok {
			if ew != w {
				t.Errorf("mapping conflict: got frame %s already mapped to want frame %s, cannot map to %s", g, ew, w)
				failed = true
			}
			return
		}
		if eg, ok := w2g[w]; ok {
			if eg != g {
				t.Errorf("mapping conflict: want frame %s already mapped to got frame %s, cannot map from %s", w, eg, g)
				failed = true
			}
			return
		}
		g2w[g] = w
		w2g[w] = g
		queue = append(queue, framePair{g, w})
	}

	addMapping("1", "1")

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		gFrame, gOk := got[p.g].(map[string]any)
		wFrame, wOk := want[p.w].(map[string]any)
		if !gOk && !wOk {
			// Both reference nonexistent frames (e.g., dangling "next" past
			// the last frame). This is fine — both behaviors end the same way.
			continue
		}
		if !gOk || !wOk {
			t.Errorf("frames got:%s / want:%s: expected maps", p.g, p.w)
			failed = true
			continue
		}

		// Verify same set of keys.
		if !sameKeySet(gFrame, wFrame) {
			t.Errorf("frames got:%s / want:%s: key sets differ: got %v, want %v",
				p.g, p.w, sortedKeys(gFrame), sortedKeys(wFrame))
			failed = true
			continue
		}

		// Compare "op".
		if gFrame["op"] != wFrame["op"] {
			t.Errorf("frames got:%s / want:%s: op mismatch: got %v, want %v",
				p.g, p.w, gFrame["op"], wFrame["op"])
			failed = true
		}

		// Handle "next" field.
		gNext, gHas := gFrame["next"]
		wNext, wHas := wFrame["next"]

		if !gHas && !wHas {
			// Implicit sequential: enqueue (gKey+1, wKey+1) if next frames exist.
			gn := strconv.Itoa(mustAtoi(p.g) + 1)
			wn := strconv.Itoa(mustAtoi(p.w) + 1)
			_, gnExists := got[gn].(map[string]any)
			_, wnExists := want[wn].(map[string]any)
			if gnExists && wnExists {
				addMapping(gn, wn)
			} else if gnExists != wnExists {
				t.Errorf("frames got:%s / want:%s: implicit next existence mismatch", p.g, p.w)
				failed = true
			}
		} else if gHas && wHas {
			switch gv := gNext.(type) {
			case bool:
				if wv, ok := wNext.(bool); !ok || gv != wv {
					t.Errorf("frames got:%s / want:%s: next mismatch: got %v, want %v",
						p.g, p.w, gNext, wNext)
					failed = true
				}
			case int:
				if wv, ok := wNext.(int); ok {
					addMapping(strconv.Itoa(gv), strconv.Itoa(wv))
				} else {
					t.Errorf("frames got:%s / want:%s: next type mismatch: got int, want %T",
						p.g, p.w, wNext)
					failed = true
				}
			default:
				t.Errorf("frames got:%s / want:%s: unexpected next type %T",
					p.g, p.w, gNext)
				failed = true
			}
		} else {
			t.Errorf("frames got:%s / want:%s: next field presence mismatch", p.g, p.w)
			failed = true
		}

		// Compare remaining fields.
		for k, gVal := range gFrame {
			if k == "op" || k == "next" {
				continue
			}
			wVal := wFrame[k]

			switch gv := gVal.(type) {
			case string:
				if wv, ok := wVal.(string); !ok || gv != wv {
					t.Errorf("frames got:%s / want:%s field %q: got %q, want %v",
						p.g, p.w, k, gv, wVal)
					failed = true
				}
			case bool:
				if wv, ok := wVal.(bool); !ok || gv != wv {
					t.Errorf("frames got:%s / want:%s field %q: got %v, want %v",
						p.g, p.w, k, gv, wVal)
					failed = true
				}
			case map[string]any:
				if !reflect.DeepEqual(gVal, wVal) {
					t.Errorf("frames got:%s / want:%s field %q: got %v, want %v",
						p.g, p.w, k, gVal, wVal)
					failed = true
				}
			case int:
				wv, ok := wVal.(int)
				if !ok {
					t.Errorf("frames got:%s / want:%s field %q: type mismatch: got int, want %T",
						p.g, p.w, k, wVal)
					failed = true
				} else if gv != wv {
					// Unequal integers → treat as frame references.
					addMapping(strconv.Itoa(gv), strconv.Itoa(wv))
				}
				// Equal integers → data values, OK.
			default:
				if !reflect.DeepEqual(gVal, wVal) {
					t.Errorf("frames got:%s / want:%s field %q: got %v, want %v",
						p.g, p.w, k, gVal, wVal)
					failed = true
				}
			}
		}
	}

	// 4. Check for orphaned frames not reached by BFS.
	var unmappedGot, unmappedWant []string
	for _, k := range gotFrames {
		if _, ok := g2w[k]; !ok {
			unmappedGot = append(unmappedGot, k)
		}
	}
	for _, k := range wantFrames {
		if _, ok := w2g[k]; !ok {
			unmappedWant = append(unmappedWant, k)
		}
	}

	// Match orphans by content: same op + same non-integer fields.
	for _, gk := range unmappedGot {
		gFrame := got[gk].(map[string]any)
		matched := false
		for i, wk := range unmappedWant {
			wFrame := want[wk].(map[string]any)
			if frameContentMatches(gFrame, wFrame, g2w) {
				g2w[gk] = wk
				w2g[wk] = gk
				unmappedWant = append(unmappedWant[:i], unmappedWant[i+1:]...)
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("orphaned got frame %s: no matching want frame", gk)
			failed = true
		}
	}
	if len(unmappedWant) > 0 {
		t.Errorf("orphaned want frames with no match: %v", unmappedWant)
		failed = true
	}

	if failed {
		gotJSON, _ := json.MarshalIndent(got, "", "    ")
		t.Errorf("full got:\n%s", gotJSON)
	}
}

// numericKeys returns the keys of m that are valid integer strings, sorted numerically.
func numericKeys(m map[string]any) []string {
	var keys []string
	for k := range m {
		if _, err := strconv.Atoi(k); err == nil {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	return keys
}

func sameKeySet(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// frameContentMatches checks if two frames have the same op and identical
// non-integer fields. For integer fields, it checks consistency with the
// existing mapping.
func frameContentMatches(gFrame, wFrame map[string]any, g2w map[string]string) bool {
	if !sameKeySet(gFrame, wFrame) {
		return false
	}
	if gFrame["op"] != wFrame["op"] {
		return false
	}
	for k, gVal := range gFrame {
		if k == "op" {
			continue
		}
		wVal := wFrame[k]
		switch gv := gVal.(type) {
		case int:
			wv, ok := wVal.(int)
			if !ok {
				return false
			}
			if gv == wv {
				continue
			}
			// Check mapping consistency.
			gs := strconv.Itoa(gv)
			ws := strconv.Itoa(wv)
			if ew, ok := g2w[gs]; ok && ew != ws {
				return false
			}
		default:
			if !reflect.DeepEqual(gVal, wVal) {
				return false
			}
		}
	}
	return true
}
