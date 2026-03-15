package compiler_test

import (
	"os"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/compiler"
)

func TestCompileWarnings(t *testing.T) {
	stdlib := os.DirFS("../stdlib")

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

	t.Run("unreachable_after_restart", func(t *testing.T) {
		src := `behavior a {
			restart
			notify "unreachable"
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'restart'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after restart, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_restart_fn", func(t *testing.T) {
		src := `fn f() {
			restart
			notify "dead"
		}
		behavior a { f }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'restart'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after restart in fn body, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_jump", func(t *testing.T) {
		src := `behavior a {
			jump 1
			notify "unreachable"
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'jump'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after jump, got: %v", warnings)
		}
	})

	t.Run("unreachable_after_jump_fn", func(t *testing.T) {
		src := `fn f() {
			jump 1
			notify "dead"
		}
		behavior a { f }`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unreachable code after 'jump'") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unreachable code warning after jump in fn body, got: %v", warnings)
		}
	})

	t.Run("no_unreachable_after_exit_with_on_handler", func(t *testing.T) {
		src := `behavior a {
			@param inout p "P"
			exit
			on $p { notify "handled" }
			notify "reachable via handler continuation"
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "unreachable") {
				t.Fatalf("expected no unreachable warning when on handler follows exit, got: %s", w)
			}
		}
	})

	t.Run("unreachable_after_exit_no_handler", func(t *testing.T) {
		src := `behavior a {
			@param inout p "P"
			exit
			notify "truly unreachable"
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
			t.Fatalf("expected unreachable warning for code after exit without on handler, got: %v", warnings)
		}
	})

	t.Run("no_unreachable_with_on_handler_later_in_block", func(t *testing.T) {
		// on handler is not immediately after exit — code in between
		// should still not warn because the handler makes the block reachable
		src := `behavior a {
			@param inout p "P"
			exit
			notify "setup"
			on $p { notify "handled" }
			notify "reachable"
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "unreachable") {
				t.Fatalf("expected no unreachable warning when on handler exists later in block, got: %s", w)
			}
		}
	})

	t.Run("no_unreachable_with_label_later_in_block", func(t *testing.T) {
		src := `behavior a {
			exit
			notify "setup"
			label 'target
			notify "reachable via jump"
		}`
		_, warnings, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "unreachable") {
				t.Fatalf("expected no unreachable warning when label exists later in block, got: %s", w)
			}
		}
	})

	t.Run("jump_missing_expression", func(t *testing.T) {
		src := `behavior a {
			jump
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for jump without expression")
		}
		if !strings.Contains(err.Error(), "label expression after 'jump'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("label_empty_parens", func(t *testing.T) {
		src := `behavior a { label() }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for label with empty parens")
		}
	})

	t.Run("jump_empty_parens", func(t *testing.T) {
		src := `behavior a { jump() }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for jump with empty parens")
		}
	})

	t.Run("exit_with_arg", func(t *testing.T) {
		src := `behavior a { exit(1) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for exit with argument")
		}
	})

	t.Run("restart_with_arg", func(t *testing.T) {
		src := `behavior a { restart(1) }`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for restart with argument")
		}
	})

	t.Run("last_with_arg", func(t *testing.T) {
		src := `behavior a {
			sequence() {
				first { last(1) }
				done { notify "done" }
			}
		}`
		_, _, err := compiler.CompileString(src, stdlib, "", "", nil, "")
		if err == nil {
			t.Fatal("expected error for last with argument")
		}
	})
}
