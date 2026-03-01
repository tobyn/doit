package compiler

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"strconv"

	"github.com/tobyn/doit/toolchain/codec"
)

// Compile reads doit source from r and compiles it into a codec Object.
// If behaviorID is non-empty, it selects the behavior to compile from a
// multi-behavior source file. When the source contains a single behavior,
// behaviorID may be empty to auto-select it. The locale parameter is a BCP 47
// tag used to resolve localized @name blocks; if empty, the first entry is used.
// sourceFS and sourcePath provide file system context for resolving imports.
// When sourceFS is nil, imports are not supported (attempting to use them
// produces a compile error). sourcePath is the path of the source file
// within sourceFS, used to resolve relative import paths.
// The returned warnings slice contains non-fatal compiler warnings (nil if none).
func Compile(r io.Reader, stdlib fs.FS, behaviorID, locale string, sourceFS fs.FS, sourcePath string) (*codec.Object, []string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	return CompileString(string(data), stdlib, behaviorID, locale, sourceFS, sourcePath)
}

// CompileString compiles doit source into a codec Object.
// sourceFS and sourcePath provide file system context for resolving imports.
// The returned warnings slice contains non-fatal compiler warnings (nil if none).
func CompileString(src string, stdlib fs.FS, behaviorID, locale string, sourceFS fs.FS, sourcePath string) (*codec.Object, []string, error) {
	fns, err := parseStdlib(stdlib)
	if err != nil {
		return nil, nil, fmt.Errorf("stdlib: %w", err)
	}
	sourceDir := ""
	if sourcePath != "" {
		sourceDir = path.Dir(sourcePath)
		if sourceDir == "." {
			sourceDir = ""
		}
	}
	// Clone stdlib fns for the parser's working map (user fns will be added to it).
	// The original is kept as stdlibFns so imported files can clone it cheaply.
	workingFns := maps.Clone(fns)
	p := &parser{
		scanner:    scanner{src: src, locale: locale},
		fns:        workingFns,
		target:     behaviorID,
		loopLabels: map[string]bool{},
		consts:     map[string]*constDef{},
		sourceFS:   sourceFS,
		sourcePath: sourcePath,
		sourceDir:  sourceDir,
		stdlibFS:   stdlib,
		stdlibFns:  fns,
	}
	obj, err := p.parseFile()
	if err != nil {
		return nil, nil, err
	}
	return obj, p.warnings, nil
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

// --- compare_register instruction slots (1-based wire format) ---

const (
	compareRegDifferent = "1" // exec branch: If Different
	compareRegValue1    = "2" // input: value_1
	compareRegValue2    = "3" // input: value_2
)

// --- value_type instruction slots (1-based wire format) ---

const (
	valueTypeInput = "1" // input: value to check
	valueTypeItem  = "2" // exec branch: Item
	valueTypeUnit  = "3" // exec branch: Unit
	valueTypeComp  = "4" // exec branch: Component
	valueTypeTech  = "5" // exec branch: Tech
	valueTypeValue = "6" // exec branch: Value
	valueTypeCoord = "7" // exec branch: Coord
)

// allTypeSlots lists the 6 type branch slot keys for value_type.
var allTypeSlots = []string{
	valueTypeItem, valueTypeUnit, valueTypeComp,
	valueTypeTech, valueTypeValue, valueTypeCoord,
}

// typeCheckSlot maps a constructor keyword name to the corresponding
// value_type wire-format branch slot key.
func typeCheckSlot(name string) (string, bool) {
	switch name {
	case "Item":
		return valueTypeItem, true
	case "Unit":
		return valueTypeUnit, true
	case "Component":
		return valueTypeComp, true
	case "Technology":
		return valueTypeTech, true
	case "Value":
		return valueTypeValue, true
	case "Coordinate":
		return valueTypeCoord, true
	}
	return "", false
}

// setComment sets the "cmt" field on a frame if comment is non-empty.
func setComment(frame map[string]any, comment string) {
	if comment != "" {
		frame["cmt"] = comment
	}
}

// --- Shared types ---

type paramDef struct {
	name      string // variable name used in the function body
	keyword   string // "" for positional, keyword name for keyword params
	direction string // "" or "in", "out", "inout"; "" defaults to "in"
}

// effectiveDirection returns the direction of the parameter, defaulting to "in".
func (p *paramDef) effectiveDirection() string {
	if p.direction == "" {
		return "in"
	}
	return p.direction
}

// canPass checks if a value with direction argDir can be passed to a param
// with direction paramDir. For example, an "in" argument cannot be passed to
// an "out" parameter because the callee would write through it.
func canPass(paramDir, argDir string) bool {
	switch paramDir {
	case "in", "":
		return argDir != "out"
	case "out":
		return argDir != "in" && argDir != ""
	case "inout":
		return argDir == "inout"
	}
	return false
}

// constDef holds a compile-time constant value.
type constDef struct {
	value   any  // the resolved compile-time value
	private bool // true for private const (not visible as import)
}

type fnDef struct {
	params  []paramDef
	rets    []string       // return names (nil = no return)
	frame   map[string]any // instruction-based (stdlib)
	astBody []Stmt         // call-based (user-defined) — AST IR
	private bool           // true for private fn (not visible as import)
	scope   map[string]*fnDef // functions available when this fn was defined (for imports)
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

// positionalParam returns a pointer to the i-th positional parameter (0-based).
func (f *fnDef) positionalParam(i int) *paramDef {
	n := 0
	for j := range f.params {
		if f.params[j].keyword == "" {
			if n == i {
				return &f.params[j]
			}
			n++
		}
	}
	return nil
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

// returnCount returns the number of return values the function produces.
// For instruction-based functions, this is the count of returnSlot markers.
// For body-based functions, this is len(rets).
func (f *fnDef) returnCount() int {
	if f.frame != nil {
		count := 0
		for _, v := range f.frame {
			if _, ok := v.(returnSlot); ok {
				count++
			}
		}
		return count
	}
	return len(f.rets)
}

// hasReturn reports whether the function produces at least one return value.
func (f *fnDef) hasReturn() bool {
	return f.returnCount() > 0
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
	depth   int  // scope depth at declaration (for shadowing warnings)
	used    bool // whether the variable has been read since declaration
}

type paramInfo struct {
	index     int    // 1-based parameter index
	name      string // display name
	direction string // "in", "out", or "inout"
}

type symbolTable struct {
	params     []paramInfo
	paramMap   map[string]int     // "$name" → 1-based index
	vars       map[string]varInfo // declared variables
	usedVars   map[string]bool    // all variable names in use (for inline rename)
	scopeDepth int                // current nesting depth (0 = top-level)
}

func newSymbolTable() *symbolTable {
	return &symbolTable{
		paramMap: map[string]int{},
		vars:     map[string]varInfo{},
		usedVars: map[string]bool{},
	}
}

// pushScope saves the current vars map and increments scope depth.
// The caller must pass the returned map to popScope when the block ends.
func (s *symbolTable) pushScope() map[string]varInfo {
	saved := make(map[string]varInfo, len(s.vars))
	for k, v := range s.vars {
		saved[k] = v
	}
	s.scopeDepth++
	return saved
}

// popScope restores vars from a saved copy and decrements scope depth.
func (s *symbolTable) popScope(saved map[string]varInfo) {
	s.vars = saved
	s.scopeDepth--
}

// declareVar registers a variable at the current scope depth.
func (s *symbolTable) declareVar(name string, mutable bool) {
	s.vars[name] = varInfo{mutable: mutable, depth: s.scopeDepth}
	s.usedVars[name] = true
}

// declareVarWarn is like declareVar but also emits a warning if the name
// already exists at the same depth and was never used.
func (s *symbolTable) declareVarWarn(name string, mutable bool, p *parser, pos int) {
	if existing, ok := s.vars[name]; ok {
		if existing.depth == s.scopeDepth && !existing.used {
			p.warnf(pos, "variable %q shadows a previous declaration in the same scope that was never used", name)
		}
	}
	s.declareVar(name, mutable)
}

// lookupVar returns the varInfo for name and whether it exists.
func (s *symbolTable) lookupVar(name string) (varInfo, bool) {
	v, ok := s.vars[name]
	return v, ok
}

// markUsed marks a variable as used (read).
func (s *symbolTable) markUsed(name string) {
	if vi, ok := s.vars[name]; ok {
		vi.used = true
		s.vars[name] = vi
	}
}

// allocUniqueVar returns name if it's not in usedVars, otherwise name_2, name_3, etc.
// The chosen name is added to usedVars.
func allocUniqueVar(name string, usedVars map[string]bool) string {
	if !usedVars[name] {
		usedVars[name] = true
		return name
	}
	for i := 2; ; i++ {
		candidate := name + "_" + strconv.Itoa(i)
		if !usedVars[candidate] {
			usedVars[candidate] = true
			return candidate
		}
	}
}

// arithCounter tracks the number of @arith temp variables allocated during
// arithmetic expression emission.
type arithCounter struct {
	n int
}

func (c *arithCounter) next(usedVars map[string]bool) string {
	c.n++
	name := fmt.Sprintf("@arith%d", c.n)
	return allocUniqueVar(name, usedVars)
}

// emitModeEntry emits a mode transition frame if needed and returns the saved
// mode for later restoration via emitModeExit.
func emitModeEntry(b *frameBuilder, unlock bool, comment string) execMode {
	target := modeLocked
	if unlock {
		target = modeUnlocked
	}
	saved := b.mode
	if b.mode != target {
		op := "lock"
		if unlock {
			op = "unlock"
		}
		f := map[string]any{"op": op}
		setComment(f, comment)
		b.emit(f)
		b.mode = target
	}
	return saved
}

// emitModeExit restores the execution mode to saved, emitting a transition
// frame if the current mode differs.
func emitModeExit(b *frameBuilder, saved execMode) {
	if b.mode != saved {
		op := "lock"
		if saved == modeUnlocked {
			op = "unlock"
		}
		b.emit(map[string]any{"op": op})
		b.mode = saved
	}
}

type execMode int

const (
	modeLocked   execMode = iota
	modeUnlocked
)

// frameRef marks an integer as a reference to a 0-based index in a
// frameBuilder's slice. finalize converts these to 1-based wire format indices.
type frameRef int

type frameBuilder struct {
	frames []map[string]any
	cursor int
	mode   execMode
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

// emitContext abstracts the differences between behavior-level and fn body
// emission paths, allowing unified control flow emitters.
type emitContext struct {
	b              *frameBuilder
	usedVars       map[string]bool
	resolveBool    func(expr Expr) (*resolvedBoolExpr, error)
	emitBody       func(stmts []Stmt) error
	exprGetValue   func(expr Expr, comment string) (any, error)
	exprTo         func(expr Expr, target any, comment string) error
	expandCallExpr func(ce *CallExpr, retVals []any, comment string) error
	pushScope      func()
	popScope       func()
	declareIterVar func(name string) string
}
