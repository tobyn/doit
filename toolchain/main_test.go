package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/codec"
)

// TestCompileIntegration verifies that the full compile-encode-decode pipeline
// produces a valid behavior. The exhaustive golden-file comparison lives in
// the compiler package tests; this is a smoke test for the wiring in main.
func TestCompileIntegration(t *testing.T) {
	stdlib := os.DirFS("stdlib")
	src := strings.NewReader(`behavior a { @name "A" notify "hello" }`)
	encoded, err := Compile(src, stdlib, "", "", nil, "")
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if encoded == "" {
		t.Fatal("Compile returned empty string")
	}

	obj, err := codec.Decode(strings.NewReader(encoded))
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	bhv, ok := obj.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", obj.Value)
	}
	if bhv["name"] != "A" {
		t.Errorf("name: got %v, want A", bhv["name"])
	}
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
