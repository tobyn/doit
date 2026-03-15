package compiler_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/compiler"
)

func TestCompileErrorsExpr(t *testing.T) {
	stdlib := os.DirFS("../stdlib")

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

	t.Run("bhv_continue_outside_loop", func(t *testing.T) {
		src := "behavior a { continue }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'continue' outside of loop") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_body_continue_outside_loop", func(t *testing.T) {
		src := "fn bad() { continue }\nbehavior a { bad }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'continue' outside of loop") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("continue_in_yield_iter", func(t *testing.T) {
		src := "iter my_iter() -> v {\nfor c, idx in each_component() {\nif c != null { yield c }\n}\n}\nbehavior a { for x in my_iter() { continue } }"
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
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
		if !strings.Contains(err.Error(), "'break' outside of loop, exec block, or mode block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_outside_loop_fn_body", func(t *testing.T) {
		src := `behavior a { f } fn f() { break }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'break' outside of loop, exec block, or mode block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_in_mode_block_expr_bare_no_tail", func(t *testing.T) {
		// bare break in a mode block expression without a tail expression
		// should error — the block must produce a value
		src := `behavior a { let x = unlocked { break } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "last item in mode block expression must be a value-producing expression") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_with_value_outside_expression_block", func(t *testing.T) {
		src := `behavior a { loop { break 42 } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "break with value outside of expression block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("break_with_value_arity_mismatch", func(t *testing.T) {
		src := `behavior a { let x = unlocked { if true { break 1, 2 }; 0 } }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "break has 2 value(s) but expression block expects 1") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("labeled_break_across_exec_boundary", func(t *testing.T) {
		// Cross-boundary break compiles to jump/label pair
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
		obj, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify the output contains jump and label instructions
		v := obj.Value.(map[string]any)
		data, _ := json.Marshal(v)
		s := string(data)
		if !strings.Contains(s, `"op":"jump"`) {
			t.Fatal("expected jump instruction for cross-boundary break")
		}
		if !strings.Contains(s, `"op":"label"`) {
			t.Fatal("expected label instruction for cross-boundary break")
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
}
