package compiler_test

import (
	"os"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/compiler"
)

func TestCompileErrorsBasic(t *testing.T) {
	stdlib := os.DirFS("../stdlib")

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
		frame := obj.Value.(map[string]any)["0"].(map[string]any)
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
		f1 := v["0"].(map[string]any)
		f2 := v["1"].(map[string]any)
		if f1["cmt"] != "Inner" {
			t.Fatalf("frame 0: expected cmt %q, got %v", "Inner", f1["cmt"])
		}
		if f2["cmt"] != "Outer" {
			t.Fatalf("frame 1: expected cmt %q, got %v", "Outer", f2["cmt"])
		}
	})

	t.Run("no_doc_comment", func(t *testing.T) {
		src := `behavior a { notify "Hello" }`
		obj, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["0"].(map[string]any)
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

	t.Run("duplicate_keepvars", func(t *testing.T) {
		src := `behavior a { @keepvars @keepvars notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate @keepvars") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_keeparrays", func(t *testing.T) {
		src := `behavior a { @keeparrays @keeparrays notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate @keeparrays") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("out_param_default_value", func(t *testing.T) {
		src := `behavior a { @param out x "X" = 5 notify "hi" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "output parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_default_invalid_token", func(t *testing.T) {
		src := `behavior a { @param in x "X" = "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected number or identifier") {
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
		src := "skip prelude\nfn bad() { instruction \"test\" { 0: @0 } }\nbehavior a { bad() }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "@N return index must be >= 1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_slot_multiple", func(t *testing.T) {
		src := "skip prelude\nfn multi() { return instruction \"test\" { 0: @1  1: @2 } }\nbehavior a { multi() }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_slot_gap", func(t *testing.T) {
		src := "skip prelude\nfn bad() { instruction \"test\" { 0: @1  1: @3 } }\nbehavior a { bad() }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing @2") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_slot_no_at1", func(t *testing.T) {
		src := "skip prelude\nfn bad() { instruction \"test\" { 0: @2 } }\nbehavior a { bad() }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "missing @1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_instruction_syntax", func(t *testing.T) {
		src := "skip prelude\nfn my_get() { return instruction \"get_self\" { 0: @1 } }\nbehavior a { my_get() }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("return_variable_in_stdlib", func(t *testing.T) {
		src := "skip prelude\nfn wrapper(foo) { return foo }\nbehavior a { wrapper(1) }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
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
		frame := obj.Value.(map[string]any)["0"].(map[string]any)
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
		frame := obj.Value.(map[string]any)["0"].(map[string]any)
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
		frame := obj.Value.(map[string]any)["0"].(map[string]any)
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
		frame := obj.Value.(map[string]any)["0"].(map[string]any)
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
		frame := obj.Value.(map[string]any)["0"].(map[string]any)
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

}
