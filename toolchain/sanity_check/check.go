// Command check compiles the sanity check doit source files and compares their
// JSON output against golden files. It tracks drift status in sanity_check.md.
//
// When the status line in sanity_check.md indicates no drift ("Status: clean"),
// the program compiles test.doit and listener.doit, compares the output against
// test.golden.json and listener.golden.json, and updates the status line to
// "Status: drifted after <commit>" if the output has changed.
//
// When the status line already indicates drift, the program does nothing.
//
// Usage:
//
//	go run ./sanity_check
//	go run ./sanity_check -update    # regenerate golden files and mark clean
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
	"github.com/tobyn/doit/toolchain/compiler"
)

// statusPattern matches the drift status line in sanity_check.md.
var statusPattern = regexp.MustCompile(`(?m)^> Drift status: .+$`)

const (
	statusClean   = "> Drift status: clean"
	statusDrifted = "> Drift status: drifted after %s"
)

type checkTarget struct {
	source     string // doit source file
	behaviorID string // -b flag value (empty for auto-select)
	golden     string // golden JSON file
}

var targets = []checkTarget{
	{source: "test.doit", behaviorID: "sanity_check", golden: "test.golden.json"},
	{source: "listener.doit", behaviorID: "", golden: "listener.golden.json"},
}

func main() {
	update := flag.Bool("update", false, "regenerate golden files and mark status clean")
	flag.Parse()

	if err := run(*update); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "sanity_check: %v\n", err)
		os.Exit(1)
	}
}

func run(update bool) error {
	// Resolve paths relative to this program's directory.
	dir, err := findDir()
	if err != nil {
		return err
	}

	if update {
		return doUpdate(dir)
	}
	return doCheck(dir)
}

// findDir returns the absolute path to the sanity_check directory.
func findDir() (string, error) {
	// When run via "go run ./sanity_check" from toolchain/, the working
	// directory is toolchain/. Look for the sanity_check subdirectory.
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// If we're in toolchain/, use sanity_check/ under it.
	candidate := filepath.Join(wd, "sanity_check")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate, nil
	}

	// If we're already in sanity_check/, use the current directory.
	if filepath.Base(wd) == "sanity_check" {
		return wd, nil
	}

	return "", fmt.Errorf("cannot find sanity_check directory from %s", wd)
}

func doCheck(dir string) error {
	mdPath := mdFilePath(dir)

	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		return fmt.Errorf("reading sanity_check.md: %w", err)
	}
	md := string(mdBytes)

	// If already drifted, nothing to do.
	if !strings.Contains(md, statusClean) {
		fmt.Println("already drifted, skipping check")
		return nil
	}

	// Check each target.
	stdlib, err := stdlibFS(dir)
	if err != nil {
		return err
	}

	for _, t := range targets {
		drifted, err := checkOneTarget(dir, stdlib, t)
		if err != nil {
			return err
		}
		if drifted {
			return markDrifted(mdPath, md)
		}
	}

	fmt.Println("sanity check: clean")
	return nil
}

func checkOneTarget(dir string, stdlib fs.FS, t checkTarget) (bool, error) {
	goldenPath := filepath.Join(dir, t.golden)

	// If golden file doesn't exist, it's drifted (needs initial generation).
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("%s: no golden file (run with -update to generate)\n", t.golden)
			return true, nil
		}
		return false, err
	}

	// Compile the source.
	compiled, err := compileSource(dir, stdlib, t)
	if err != nil {
		return false, err
	}

	// Parse golden JSON.
	goldenVal, err := codec.UnmarshalJSON(goldenBytes)
	if err != nil {
		return false, fmt.Errorf("parsing golden %s: %w", t.golden, err)
	}

	// Compare.
	if !reflect.DeepEqual(compiled, goldenVal) {
		fmt.Printf("%s: output differs from golden\n", t.source)
		return true, nil
	}

	return false, nil
}

func compileSource(dir string, stdlib fs.FS, t checkTarget) (any, error) {
	srcPath := filepath.Join(dir, t.source)
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", t.source, err)
	}
	defer f.Close()

	sourceFS := os.DirFS(dir)
	obj, _, err := compiler.Compile(f, stdlib, t.behaviorID, "", sourceFS, t.source)
	if err != nil {
		return nil, fmt.Errorf("compiling %s: %w", t.source, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("compiling %s: no output", t.source)
	}

	return obj.Value, nil
}

func doUpdate(dir string) error {
	stdlib, err := stdlibFS(dir)
	if err != nil {
		return err
	}

	for _, t := range targets {
		compiled, err := compileSource(dir, stdlib, t)
		if err != nil {
			return err
		}

		jsonBytes, err := json.MarshalIndent(compiled, "", "    ")
		if err != nil {
			return fmt.Errorf("marshaling %s: %w", t.golden, err)
		}
		jsonBytes = append(jsonBytes, '\n')

		goldenPath := filepath.Join(dir, t.golden)
		if err := os.WriteFile(goldenPath, jsonBytes, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", t.golden, err)
		}
		fmt.Printf("updated %s\n", t.golden)
	}

	// Mark clean in sanity_check.md.
	mdPath := mdFilePath(dir)

	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		return fmt.Errorf("reading sanity_check.md: %w", err)
	}

	md := string(mdBytes)
	if statusPattern.MatchString(md) {
		md = statusPattern.ReplaceAllString(md, statusClean)
	}

	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return fmt.Errorf("writing sanity_check.md: %w", err)
	}
	fmt.Println("marked clean in sanity_check.md")

	return nil
}

func markDrifted(mdPath, md string) error {
	commit, err := gitHEAD()
	if err != nil {
		return err
	}

	status := fmt.Sprintf(statusDrifted, commit)
	if statusPattern.MatchString(md) {
		md = statusPattern.ReplaceAllString(md, status)
	}

	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return fmt.Errorf("writing sanity_check.md: %w", err)
	}
	fmt.Printf("marked drifted after %s in sanity_check.md\n", commit)
	return nil
}

func mdFilePath(dir string) string {
	p := filepath.Join(filepath.Dir(dir), "..", ".claude", "learnings", "sanity_check.md")
	return filepath.Clean(p)
}

func stdlibFS(dir string) (fs.FS, error) {
	toolchainDir := filepath.Dir(dir)
	stdlibDir := filepath.Join(toolchainDir, "stdlib")
	if _, err := os.Stat(stdlibDir); err != nil {
		return nil, fmt.Errorf("stdlib not found at %s", stdlibDir)
	}
	return os.DirFS(stdlibDir), nil
}

func gitHEAD() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
