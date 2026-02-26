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

			encoded, err := Compile(f, stdlib, behaviorID, locale)
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "multiple behaviors") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("behavior_not_found", func(t *testing.T) {
		src := `behavior a { @name "A" } behavior b { @name "B" }`
		_, err := compiler.CompileString(src, stdlib, "c", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_name", func(t *testing.T) {
		src := `behavior a { @name "A" @name "B" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate @name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no_behaviors", func(t *testing.T) {
		src := `fn greet() { notify "hi" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no behavior") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("localized_name_with_locale", func(t *testing.T) {
		src := `behavior a { @name localize { en_US "English" ja "日本語" } notify "hi" }`
		obj, err := compiler.CompileString(src, stdlib, "", "ja")
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
		obj, err := compiler.CompileString(src, stdlib, "", "fr")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty localize block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("localize_missing_brace", func(t *testing.T) {
		src := `behavior a { @name localize "Foo" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("doc_comment_inherit", func(t *testing.T) {
		src := "fn greet() { notify \"Hello\" }\nbehavior a {\n#! Greeting\ngreet\n}"
		obj, err := compiler.CompileString(src, stdlib, "", "")
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
		obj, err := compiler.CompileString(src, stdlib, "", "")
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
		obj, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown keyword argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("positional_param_after_keyword", func(t *testing.T) {
		src := "fn bad(value v, txt) {}\nbehavior a { notify \"hi\" }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "positional parameter after keyword parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_keyword_arg", func(t *testing.T) {
		src := `behavior a { notify "Hello", timeout: "5", timeout: "10" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate keyword argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_keyword_arg_in_fn_body", func(t *testing.T) {
		src := "fn bad(txt) { notify txt, timeout: \"5\", timeout: \"10\" }\nbehavior a { bad \"hi\" }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate keyword argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("extra_positional_arg", func(t *testing.T) {
		src := `behavior a { notify "Hello" "extra" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many positional arguments") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unterminated_string", func(t *testing.T) {
		src := `behavior a { notify "Hello }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unterminated string") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_escape", func(t *testing.T) {
		src := `behavior a { notify "Hello\q" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown escape") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unexpected_character", func(t *testing.T) {
		src := `behavior a { ~ }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected character") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_function_in_behavior", func(t *testing.T) {
		src := `behavior a { nonexistent "x" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown statement") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_function_in_fn_body", func(t *testing.T) {
		src := "fn foo() { nonexistent \"x\" }\nbehavior a { foo }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_closing_brace", func(t *testing.T) {
		src := `behavior a { notify "Hello"`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_attribute", func(t *testing.T) {
		src := `behavior a { @foo "bar" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown attribute") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_function_arg", func(t *testing.T) {
		src := `behavior a { check_number "x" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty_source", func(t *testing.T) {
		src := ``
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no behavior") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_reassign", func(t *testing.T) {
		src := `behavior a { let x = 5; x = 10 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_plus_equals", func(t *testing.T) {
		src := `behavior a { let x = 5; x += 1 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_plus_plus", func(t *testing.T) {
		src := `behavior a { let x = 5; x++ }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown_register", func(t *testing.T) {
		src := `behavior a { domove $foo }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown register") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_after_instruction", func(t *testing.T) {
		src := `behavior a { notify "hi"; @param in x "X" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "@param must be declared before any instructions") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_builtin_conflict", func(t *testing.T) {
		src := `behavior a { @param in signal "Signal" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "conflicts with a built-in register") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_duplicate_name", func(t *testing.T) {
		src := `behavior a { @param in x "X1"; @param out x "X2" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_invalid_direction", func(t *testing.T) {
		src := `behavior a { @param rw x "X" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected parameter direction") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dollar_sign_alone", func(t *testing.T) {
		src := `behavior a { domove $ }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected register name after '$'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_no_return", func(t *testing.T) {
		src := `behavior a { let x = notify }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_unknown", func(t *testing.T) {
		src := `behavior a { let x = nonexistent }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown function") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_string_rhs", func(t *testing.T) {
		src := `behavior a { let x = "hello" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("var_fn_no_return", func(t *testing.T) {
		src := `behavior a { var x = notify }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("assign_fn_no_return", func(t *testing.T) {
		src := `behavior a { var x = 5; x = notify }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_return_immutable", func(t *testing.T) {
		src := `behavior a { let me = get_self; me = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_fn_body_no_return", func(t *testing.T) {
		src := "fn bad() { let x = notify }\nbehavior a { bad }"
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		stdlibSrc := "fn wrapper() { return foo }"
		err := compiler.TestParseStdlibFile(stdlibSrc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("localized_doc_comment_with_locale", func(t *testing.T) {
		src := "behavior a {\n#! (en) English comment\n#! (ja) 日本語コメント\nnotify \"Hello\"\n}"
		obj, err := compiler.CompileString(src, stdlib, "", "ja")
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
		obj, err := compiler.CompileString(src, stdlib, "", "")
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
		obj, err := compiler.CompileString(src, stdlib, "", "fr")
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
		obj, err := compiler.CompileString(src, stdlib, "", "en")
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
		obj, err := compiler.CompileString(src, stdlib, "", "ja")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many bindings") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("multi_return_no_return_fn", func(t *testing.T) {
		src := `behavior a { let x, y = notify "Hello" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("underscore_as_var_name", func(t *testing.T) {
		src := `behavior a { var _ = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'_' cannot be used as a variable name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("underscore_as_let_name", func(t *testing.T) {
		src := `behavior a { let _ = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'_' cannot be used as a variable name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("multi_return_fn_body_too_many", func(t *testing.T) {
		src := "fn bad() {\n  let me = get_self\n  let coord = get_location me\n  let x, y, z = separate_coordinate coord\n  return x\n}\nbehavior a { bad }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many bindings") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("constructor_missing_lparen", func(t *testing.T) {
		src := `behavior a { notify "hi", value: Item "metalbar" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected '(' after Item") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("coordinate_wrong_arg_count", func(t *testing.T) {
		src := `behavior a { notify "hi", value: Coordinate(1) }`
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected string argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("item_wrong_arg_count", func(t *testing.T) {
		src := `behavior a { notify "hi", value: Item("a", "b") }`
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "type constructor") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_let_no_return_slot", func(t *testing.T) {
		src := "behavior a { let x = instruction \"test\" { 0: foo } }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no return slots") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_multi_return_too_many_bindings", func(t *testing.T) {
		src := "behavior a { let x, y = instruction \"test\" { 0: @1 } }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many bindings") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_fn_body_let_no_return_slot", func(t *testing.T) {
		src := "fn bad() { let x = instruction \"test\" { 0: foo } }\nbehavior a { bad }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no return slots") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_fn_body_multi_return_too_many", func(t *testing.T) {
		src := "fn bad() { let x, y = instruction \"test\" { 0: @1 } }\nbehavior a { bad }"
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_in_plusplus", func(t *testing.T) {
		src := `behavior a { @param in x "X"; $x++ }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_in_plusequals", func(t *testing.T) {
		src := `behavior a { @param in x "X"; $x += 1 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_read_fn_arg", func(t *testing.T) {
		src := `behavior a { @param out x "X"; domove $x }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass out parameter to in parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_in_to_out_fn", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { @param in x \"X\"; writer out $x }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass in parameter to out parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_in_condition", func(t *testing.T) {
		src := `behavior a { @param out x "X"; if $x >= 5 { notify "hi" } }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_plusplus", func(t *testing.T) {
		src := `behavior a { @param out x "X"; $x++ }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_in_param_to_out", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nfn caller(x) { writer out x }\nbehavior a { caller 5 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass in parameter to out parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_out_param_as_input", func(t *testing.T) {
		src := "fn caller(out x) { notify x }\nbehavior a { var z = 5; caller z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass out parameter to in parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("let_to_out_param", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { let x = 5; writer out x }"
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_inout_both_ok", func(t *testing.T) {
		src := `behavior a { @param inout x "X"; $x += 1; domove $x }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_direction_default_in", func(t *testing.T) {
		// Omitting direction defaults to "in" — passing a literal to an unadorned param should work
		src := "fn reader(x) { notify x }\nbehavior a { reader 5 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_out_param_ok", func(t *testing.T) {
		// out param in fn: callee writes to it via let binding, fine with a var argument + out annotation
		src := "fn writer(out target) { let target = get_self }\nbehavior a { var z = 5; writer out z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_in_while_condition", func(t *testing.T) {
		src := `behavior a { @param out x "X"; while $x <= 5 { notify "hi" } }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_out_in_instruction_input", func(t *testing.T) {
		src := `behavior a { @param out x "X"; instruction "notify" { 0: $x } }`
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing 'out' annotation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_inout_annotation", func(t *testing.T) {
		src := "fn updater(inout target) { instruction \"get_self\" { 0: target } }\nbehavior a { var z = 5; updater z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing 'inout' annotation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong_annotation_out_for_in", func(t *testing.T) {
		src := "fn reader(x) { notify x }\nbehavior a { reader out 5 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "argument has 'out' annotation but parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong_annotation_in_for_out", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nbehavior a { var z = 5; writer in z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "argument has 'in' annotation but parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_out_annotation_keyword", func(t *testing.T) {
		src := "fn my_fn(x, out kw result) { let result = get_self }\nbehavior a { var z = 5; my_fn 1, kw: z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing 'out' annotation") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_out_annotation_fn_body", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nfn caller(x) { writer x }\nbehavior a { caller 5 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("inout_annotation_ok", func(t *testing.T) {
		src := "fn updater(inout target) { instruction \"add\" { 0: target  1: target  2: target } }\nbehavior a { var z = 5; updater inout z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("explicit_in_annotation_ok", func(t *testing.T) {
		// explicit "in" for an "in" param should be accepted
		src := "fn reader(x) { notify x }\nbehavior a { reader in 5 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_out_annotation_ok", func(t *testing.T) {
		src := "fn writer(out target) { let target = get_self }\nfn caller(inout x) { writer out x }\nbehavior a { var z = 5; caller inout z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_out_annotation_ok", func(t *testing.T) {
		src := "fn my_fn(x, out kw result) { let result = get_self }\nbehavior a { var z = 5; my_fn 1, out kw: z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_var_name_null", func(t *testing.T) {
		src := `behavior a { var null = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("keyword_as_var_name_return", func(t *testing.T) {
		src := `behavior a { var return = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("instruction_assign_no_return_slot", func(t *testing.T) {
		src := `behavior a { var x = 5; x = instruction "test" { 0: x } }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no return slots") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("direction_keyword_as_var_name", func(t *testing.T) {
		src := `behavior a { let out = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Comparison expression errors ---

	t.Run("comparison_string_rhs", func(t *testing.T) {
		src := `behavior a { var x = 5; let r = x > "hello" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("comparison_out_param_lhs", func(t *testing.T) {
		src := `behavior a { @param out x "X"; let r = $x > 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("comparison_assign_string_rhs", func(t *testing.T) {
		src := `behavior a { var x = 5; var r = 0; r = x > "hello" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("comparison_out_param_rhs", func(t *testing.T) {
		src := `behavior a { @param out x "X"; var a = 5; let r = a > $x }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Logical operator errors ---

	t.Run("logical_mixed_operators", func(t *testing.T) {
		src := `behavior a { var x = 5; var y = 3; var z = 1; let r = x > 2 && y < 10 || z > 0 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot mix") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_number_truthy", func(t *testing.T) {
		// Numbers and bare variables are now valid as truthy terms in && / ||
		src := `behavior a { var x = 5; let r = x > 2 && 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_var_truthy", func(t *testing.T) {
		// Bare variable is a valid truthy term in &&
		src := `behavior a { var x = 5; var y = 3; let r = x > 2 && y }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_out_param_second", func(t *testing.T) {
		src := `behavior a { @param out x "X"; var a = 5; let r = a > 2 && $x < 10 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bang_without_equals", func(t *testing.T) {
		src := `behavior a { var x = 5; let r = !x }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected character") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Type check (is) operator errors ---

	t.Run("is_unknown_type", func(t *testing.T) {
		src := `behavior a { let me = get_self; let a = me is Foo }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown type") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("is_missing_type", func(t *testing.T) {
		src := `behavior a { let me = get_self; let a = me is }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected type name after 'is'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("locked_as_variable_name", func(t *testing.T) {
		src := `behavior a { let locked = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unlocked_as_variable_name", func(t *testing.T) {
		src := `behavior a { let unlocked = 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "reserved keyword") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bare_lock_not_valid", func(t *testing.T) {
		src := `behavior a { lock }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			return // error expected — "lock" is no longer a keyword
		}
		t.Fatal("expected error for bare 'lock'")
	})

	t.Run("bare_unlock_not_valid", func(t *testing.T) {
		src := `behavior a { unlock }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			return // error expected — "unlock" is no longer a keyword
		}
		t.Fatal("expected error for bare 'unlock'")
	})

	t.Run("arith_string_rhs", func(t *testing.T) {
		src := `behavior a { var b = 5; let a = b + "hello" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_out_param_lhs", func(t *testing.T) {
		src := `behavior a { @param out x "X"; let r = $x + 1 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_let_compound_assign", func(t *testing.T) {
		src := `behavior a { let x = 5; x -= 1 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_let_decrement", func(t *testing.T) {
		src := `behavior a { let x = 5; x-- }`
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unexpected '}'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("paren_empty", func(t *testing.T) {
		src := `behavior a { let r = () }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected identifier, number, or '('") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("logical_mixed_suggests_parens", func(t *testing.T) {
		src := `behavior a { var x = 5; var y = 3; var z = 1; let r = x > 2 && y < 10 || z > 0 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "parentheses") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- Expression priority error tests ---

	t.Run("compound_assign_arith_string", func(t *testing.T) {
		src := `behavior a { var x = 5; x += "hello" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number, variable, or '(' in arithmetic expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("arith_chain_string_rhs", func(t *testing.T) {
		src := `behavior a { var b = 5; let a = b + 1 + "hello" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
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
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// --- fn body expression and control flow error cases ---

	t.Run("fn_body_assign_to_let", func(t *testing.T) {
		src := "fn bad(x) { let a = x; a = 5 }\nbehavior a { bad 1 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_assign_to_in_param", func(t *testing.T) {
		src := "fn bad(x) { x = 5 }\nbehavior a { bad 1 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to input parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_compound_assign_to_let", func(t *testing.T) {
		src := "fn bad(x) { let a = x; a += 1 }\nbehavior a { bad 1 }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot assign to immutable variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_read_out_param", func(t *testing.T) {
		src := "fn bad(out x) { let a = x }\nbehavior a { var z = 5; bad out z }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot read from output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_var_underscore", func(t *testing.T) {
		src := "fn bad() { var _ = 5 }\nbehavior a { bad }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot be used as a variable name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bhv_break_outside_loop", func(t *testing.T) {
		src := "behavior a { break }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		// At behavior level, break outside loop should fail
	})

	t.Run("expr_list_too_many_expressions", func(t *testing.T) {
		src := `behavior a { let a, b = 1, 2, 3 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many expressions") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("expr_list_fn_no_return", func(t *testing.T) {
		src := `behavior a { let a, b = notify "hi", 5 }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("expr_list_fn_body_too_many_expressions", func(t *testing.T) {
		src := "fn bad() { let a, b = 1, 2, 3 }\nbehavior a { bad }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too many expressions") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("expr_list_fn_body_fn_no_return", func(t *testing.T) {
		src := "fn bad() { let a, b = notify \"hi\", 5 }\nbehavior a { bad }"
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "has no return value") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("mode_block_expr_empty", func(t *testing.T) {
		src := `behavior a { let x = unlocked { } }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty mode block expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("mode_block_expr_non_value_tail", func(t *testing.T) {
		src := `behavior a { let x = unlocked { notify "hi" } }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in mode block expression must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("mode_block_expr_fn_body_empty", func(t *testing.T) {
		src := `behavior a { let x = f } fn f() { let x = unlocked { }; return x }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty mode block expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("mode_block_expr_fn_body_non_value_tail", func(t *testing.T) {
		src := `behavior a { let x = f } fn f() { let x = unlocked { notify "hi" }; return x }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in mode block expression must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
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
