package compiler

import (
	"fmt"
	"io"
	"io/fs"
	"strconv"

	"github.com/tobyn/doit/toolchain/codec"
)

// Compile reads doit source from r and compiles it into a codec Object.
// If behaviorID is non-empty, it selects the behavior to compile from a
// multi-behavior source file. When the source contains a single behavior,
// behaviorID may be empty to auto-select it. The locale parameter is a BCP 47
// tag used to resolve localized @name blocks; if empty, the first entry is used.
func Compile(r io.Reader, stdlib fs.FS, behaviorID, locale string) (*codec.Object, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return CompileString(string(data), stdlib, behaviorID, locale)
}

// CompileString compiles doit source into a codec Object.
func CompileString(src string, stdlib fs.FS, behaviorID, locale string) (*codec.Object, error) {
	fns, err := parseStdlib(stdlib)
	if err != nil {
		return nil, fmt.Errorf("stdlib: %w", err)
	}
	p := &parser{scanner: scanner{src: src, locale: locale}, fns: fns, target: behaviorID}
	return p.parseFile()
}

// TestParseStdlibFile is a test helper that parses a stdlib source string.
func TestParseStdlibFile(src string) error {
	return parseStdlibFile(src, map[string]*fnDef{})
}

// TestParseLocalePrefix is a test helper that exposes parseLocalePrefix.
func TestParseLocalePrefix(line string) (locale, rest string, ok bool) {
	return parseLocalePrefix(line)
}

// --- check_number instruction slots (1-based wire format) ---

const (
	checkLarger  = "1" // exec branch: value > target
	checkSmaller = "2" // exec branch: value < target
	checkValue   = "3" // input: value to compare
	checkTarget  = "4" // input: comparison target
)

// setComment sets the "cmt" field on a frame if comment is non-empty.
func setComment(frame map[string]any, comment string) {
	if comment != "" {
		frame["cmt"] = comment
	}
}

// --- Shared types ---

type paramDef struct {
	name    string // variable name used in the function body
	keyword string // "" for positional, keyword name for keyword params
}

type fnDef struct {
	params []paramDef
	ret    string         // return parameter name (empty = no return)
	frame  map[string]any // instruction-based (stdlib)
	body   []fnBodyCall   // call-based (user-defined)
}

// positionalCount returns the number of positional params.
func (f *fnDef) positionalCount() int {
	n := 0
	for _, p := range f.params {
		if p.keyword == "" {
			n++
		}
	}
	return n
}

// keywordVarNames returns the set of variable names that belong to
// keyword params.
func (f *fnDef) keywordVarNames() map[string]bool {
	m := map[string]bool{}
	for _, p := range f.params {
		if p.keyword != "" {
			m[p.name] = true
		}
	}
	return m
}

// keywordByName returns the paramDef for the given keyword, or nil.
func (f *fnDef) keywordByName(keyword string) *paramDef {
	for i := range f.params {
		if f.params[i].keyword == keyword {
			return &f.params[i]
		}
	}
	return nil
}

// returnSlot marks an instruction output slot as a return value.
// The int is the 1-based return index (@1 = first return value).
type returnSlot int

// hasReturn reports whether the function produces a return value.
// This is true if the function has a named return parameter, a return
// statement (user-defined fns), or if its instruction frame contains
// a returnSlot (@1 marker).
func (f *fnDef) hasReturn() bool {
	if f.ret != "" {
		return true
	}
	for _, v := range f.frame {
		if _, ok := v.(returnSlot); ok {
			return true
		}
	}
	return false
}

type fnBodyArg struct {
	isIdent bool
	val     string
	literal any // non-nil for numbers, null, $register (pre-resolved)
}

// unitRegisters maps $name identifiers to their wire-format integers.
var unitRegisters = map[string]int{
	"$signal": -4,
	"$visual": -3,
	"$store":  -2,
	"$goto":   -1,
}

type varInfo struct {
	mutable bool // true for var, false for let
}

type paramInfo struct {
	index     int    // 1-based parameter index
	name      string // display name
	direction string // "in", "out", or "inout"
}

type symbolTable struct {
	params   []paramInfo
	paramMap map[string]int     // "$name" → 1-based index
	vars     map[string]varInfo // declared variables
}

func newSymbolTable() *symbolTable {
	return &symbolTable{
		paramMap: map[string]int{},
		vars:     map[string]varInfo{},
	}
}

type fnBodyCall struct {
	name    string
	args    []fnBodyArg          // positional args
	kwArgs  map[string]fnBodyArg // keyword -> value
	retArg  *fnBodyArg           // return value target (nil = no return)
	comment string               // #! doc comment
}

type deferredBody struct {
	frames       []map[string]any
	checkFrame   int    // index of the check_number frame to patch
	slot         string // checkLarger or checkSmaller
	continuation int    // frame index of the statement after the if block
}

// frameRef marks an integer as a reference to a 0-based index in a
// frameBuilder's slice. finalize converts these to 1-based wire format indices.
type frameRef int

type frameBuilder struct {
	frames []map[string]any
	cursor int
}

func (b *frameBuilder) emit(f map[string]any) int {
	idx := b.cursor
	if idx < len(b.frames) {
		b.frames[idx] = f
	} else {
		b.frames = append(b.frames, f)
	}
	b.cursor++
	return idx
}

func (b *frameBuilder) pos() int { return b.cursor }

func (b *frameBuilder) seek(pos int) { b.cursor = pos }

func (b *frameBuilder) get(idx int) map[string]any {
	return b.frames[idx]
}

// finalize writes 1-based frame entries into value, converting
// any frameRef values to 1-based integers.
func (b *frameBuilder) finalize(value map[string]any) {
	for i, f := range b.frames {
		resolved := make(map[string]any, len(f))
		for k, v := range f {
			if ref, ok := v.(frameRef); ok {
				resolved[k] = int(ref) + 1
			} else {
				resolved[k] = v
			}
		}
		value[strconv.Itoa(i+1)] = resolved
	}
}
