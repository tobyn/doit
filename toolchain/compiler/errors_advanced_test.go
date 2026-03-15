package compiler_test

import (
	"os"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/compiler"
)

func TestCompileErrorsAdvanced(t *testing.T) {
	stdlib := os.DirFS("../stdlib")


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
	for a, b, c in each_component() {
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
	for c, idx in each_component() {
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

	t.Run("iterator_instruction_missing_done", func(t *testing.T) {
		src := `behavior a {
	@name "A"
	for comp in iterator_instruction "for_component" {
		0: @1
	} {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "done") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("iterator_instruction_too_few_vars", func(t *testing.T) {
		src := `behavior a {
	@name "A"
	for comp in iterator_instruction "for_component" {
		0: @1
		1: @2
		2: @3
		done: 3
	} {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "iteration variable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("iterator_instruction_exec_binding", func(t *testing.T) {
		src := `behavior a {
	@name "A"
	for comp in iterator_instruction "for_component" {
		0: @1
		exec 1: found
		done: 2
	} {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "exec binding") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("local_block_return_reserved", func(t *testing.T) {
		src := `behavior a {
	@name "A"
	instruction "check_number" {
		exec 0: 'return
		2: $signal
		3: 5
	} {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "'return") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("local_block_detached_in_expr", func(t *testing.T) {
		src := `behavior a {
	@name "A"
	let x = instruction "check_number" {
		detach exec 0: 'larger
		2: $signal
		3: 5
	} {
		notify "hi"
	}
}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "detached") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("label_string", func(t *testing.T) {
		src := `behavior a { label "foo" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "string literals not allowed") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("jump_string", func(t *testing.T) {
		src := `behavior a { jump "foo" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "string literals not allowed") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_label", func(t *testing.T) {
		src := `behavior a { label 'x; label 'x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("orphan_jump", func(t *testing.T) {
		src := `behavior a { jump 'x }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no matching label") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("label_after_exit", func(t *testing.T) {
		src := `behavior a {
			exit
			label 'target
			notify "reached"
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("label after exit should compile: %v", err)
		}
	})

	t.Run("jump_to_label_after_exit", func(t *testing.T) {
		src := `behavior a {
			@param inout trigger
			on $trigger { jump 'target }
			exit
			label 'target
			notify "reached"
			exit
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("jump to label after exit should compile: %v", err)
		}
	})

	t.Run("faction_register_missing_ident", func(t *testing.T) {
		src := `behavior a { @name "A" var x = % }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected identifier after '%'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("event_on_non_parameter", func(t *testing.T) {
		src := `behavior a {
			@param in trigger "Trigger"
			on $nonexistent { notify "x" }
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("event_fn_non_param_arg", func(t *testing.T) {
		src := `fn setup(x) {
			on x { notify "x" }
		}
		behavior a {
			@param in trigger "Trigger"
			setup $trigger
			loop { wait 10 }
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "param modifier") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_arg_receives_non_param", func(t *testing.T) {
		src := `fn setup(param trigger) {
			on trigger { notify "x" }
		}
		behavior a {
			@param in trigger "Trigger"
			var x = $trigger
			setup x
			loop { wait 10 }
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "must be a behavior parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("event_in_nested_block", func(t *testing.T) {
		src := `behavior a {
			@param inout trigger "Trigger"
			if $trigger {
				on $trigger { notify "x" }
			}
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("param_direction_mismatch", func(t *testing.T) {
		src := `fn setup(out param trigger) {
			on trigger { notify "x" }
		}
		behavior a {
			@param in trigger "Trigger"
			setup out $trigger
			loop { wait 10 }
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "cannot pass") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("fn_param_arg_with_non_param_value", func(t *testing.T) {
		src := `fn setup(param trigger) {
			on trigger { notify "x" }
		}
		behavior a {
			@param in trigger "Trigger"
			let x = $trigger
			setup x
			loop { wait 10 }
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "must be a behavior parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("assert_string_condition", func(t *testing.T) {
		// assert("hello") — string as the only arg in parens, no block → condition is a string → error
		src := `behavior a { @name "A" assert("hello") }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		// String literals can't be conditions (error comes from expression parser)
	})

	t.Run("assert_message_no_block", func(t *testing.T) {
		src := `behavior a { @name "A" assert "hello" }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "assert with message but no condition") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("assert_empty_block", func(t *testing.T) {
		src := `behavior a { @name "A" assert {} }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty assert block") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("assert_release_omits_block_body", func(t *testing.T) {
		// In release mode, assert block body side effects are also omitted
		src := `behavior a { @name "A" @param in x "X" assert { let y = $x; y > 0 } notify "done" }`
		obj, _, err := compiler.CompileString(src, stdlib, "", "", nil, "", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v := obj.Value.(map[string]any)
		// Only the notify frame should remain
		if _, has := v["1"]; has {
			t.Fatalf("expected only 1 frame (notify), but got extra frames")
		}
		f0 := v["0"].(map[string]any)
		if f0["op"] != "notify" {
			t.Fatalf("expected frame 0 to be notify, got %v", f0["op"])
		}
	})

	// --- call keyword error tests ---

	t.Run("call_unknown_behavior", func(t *testing.T) {
		src := `behavior a { call nonexistent(x: 5) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown behavior") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("call_unknown_param", func(t *testing.T) {
		src := "behavior helper { @param in x; notify $x }\nbehavior a { call helper(wrong: 5) }"
		_, _, err := compiler.CompileString(src, stdlib, "a", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown parameter") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("call_duplicate_arg", func(t *testing.T) {
		src := "behavior helper { @param in x; notify $x }\nbehavior a { call helper(x: 5, x: 6) }"
		_, _, err := compiler.CompileString(src, stdlib, "a", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("call_wrong_direction", func(t *testing.T) {
		src := "behavior helper { @param out x; notify $x }\nbehavior a { call helper(x: 5) }"
		_, _, err := compiler.CompileString(src, stdlib, "a", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "must be passed with") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("call_in_param_as_out", func(t *testing.T) {
		src := "behavior helper { @param in x; notify $x }\nbehavior a { @param out r; call helper(x: out $r) }"
		_, _, err := compiler.CompileString(src, stdlib, "a", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "is 'in' but was passed as") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("behavior_param_not_a_behavior", func(t *testing.T) {
		src := `
		fn deploy(behavior bhv, target) { load_behavior bhv, target }
		behavior a { var x = 5; deploy x, $goto }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "must be a behavior reference") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("behavior_param_out_direction", func(t *testing.T) {
		src := `fn bad(out behavior bhv) { }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "behavior parameter cannot be out") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("behavior_param_fn_body_not_behavior", func(t *testing.T) {
		src := `
		fn deploy(behavior bhv, target) { load_behavior bhv, target }
		fn wrapper(target) { deploy target, target }
		behavior a { wrapper $goto }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "must be a behavior reference") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dot_access_invalid_field", func(t *testing.T) {
		src := `behavior a { var x = 1; notify x.foo }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected 'number' or 'value'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dot_access_invalid_field_in_fn_body", func(t *testing.T) {
		src := `fn f(v) { var x = v.xyz }
		behavior a { f 1 }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "expected 'number' or 'value'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

}
