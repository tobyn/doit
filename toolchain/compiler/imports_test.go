package compiler_test

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tobyn/doit/toolchain/compiler"
)

func TestImports(t *testing.T) {
	stdlib := os.DirFS("../stdlib")

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

	t.Run("namespace_qualified_iterator", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main {
	for comp, idx in lib.each_comp() {
		notify "found", value: comp
	}
}`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`iter each_comp() -> comp, idx {
	instruction "for_component" {
		0: comp
		1: idx
		done: 2
	}
}`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_qualified_iterator_in_fn_body", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
fn do_scan() {
	for comp, idx in lib.each_comp() {
		notify "found", value: comp
	}
}
behavior main { do_scan }`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`iter each_comp() -> comp, idx {
	instruction "for_component" {
		0: comp
		1: idx
		done: 2
	}
}`)},
		}
		src, _ := sourceFS.ReadFile("main.doit")
		_, _, err := compiler.CompileString(string(src), stdlib, "", "", sourceFS, "main.doit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("namespace_qualified_static_iterator", func(t *testing.T) {
		sourceFS := fstest.MapFS{
			"main.doit": &fstest.MapFile{Data: []byte(`import "./lib" as lib
behavior main {
	for v in lib.things() {
		notify "got", value: v
	}
}`)},
			"lib.doit": &fstest.MapFile{Data: []byte(`iter things() -> val {
	yield 10
	yield 20
}`)},
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
