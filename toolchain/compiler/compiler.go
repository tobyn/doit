package compiler

import (
	"fmt"
	"io"
	"io/fs"

	"github.com/tobyn/doit/toolchain/codec"
)

// Compile reads doit source from r and compiles it into a codec Object.
// If behaviorID is non-empty, it selects the behavior to compile from a
// multi-behavior source file. When the source contains a single behavior,
// behaviorID may be empty to auto-select it.
func Compile(r io.Reader, stdlib fs.FS, behaviorID string) (*codec.Object, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return CompileString(string(data), stdlib, behaviorID)
}

// CompileString compiles doit source into a codec Object.
func CompileString(src string, stdlib fs.FS, behaviorID string) (*codec.Object, error) {
	fns, err := parseStdlib(stdlib)
	if err != nil {
		return nil, fmt.Errorf("stdlib: %w", err)
	}
	p := &parser{scanner: scanner{src: src}, fns: fns, target: behaviorID}
	return p.parseFile()
}

// --- Shared types ---

type fnDef struct {
	params []string
	frame  map[string]any // instruction-based (stdlib)
	body   []fnBodyCall   // call-based (user-defined)
}

type fnBodyArg struct {
	isIdent bool
	val     string
}

type fnBodyCall struct {
	name string
	args []fnBodyArg
}

type deferredBody struct {
	frames       []map[string]any
	checkFrame   int    // index of the check_number frame to patch
	slot         string // "0" (if_larger) or "1" (if_smaller)
	continuation int    // frame index of the statement after the if block
}
