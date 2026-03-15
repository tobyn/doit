package compiler

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"strconv"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
	"github.com/tobyn/doit/toolchain/syntax"
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
// When releaseMode is true, assert statements are omitted from the output.
func Compile(r io.Reader, stdlib fs.FS, behaviorID, locale string, sourceFS fs.FS, sourcePath string, releaseMode ...bool) (*codec.Object, []string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	return CompileString(string(data), stdlib, behaviorID, locale, sourceFS, sourcePath, releaseMode...)
}

// CompileString compiles doit source into a codec Object.
// sourceFS and sourcePath provide file system context for resolving imports.
// The returned warnings slice contains non-fatal compiler warnings (nil if none).
// When releaseMode is true, assert statements are omitted from the output.
func CompileString(src string, stdlib fs.FS, behaviorID, locale string, sourceFS fs.FS, sourcePath string, releaseMode ...bool) (*codec.Object, []string, error) {
	sourceDir := ""
	if sourcePath != "" {
		sourceDir = path.Dir(sourcePath)
		if sourceDir == "." {
			sourceDir = ""
		}
	}

	// Read the prelude and prepend it to the source unless opted out.
	preludeText := ""
	sourceOffset := 0
	if stdlib != nil {
		data, err := fs.ReadFile(stdlib, "prelude.doit")
		if err != nil {
			return nil, nil, fmt.Errorf("stdlib: reading prelude: %w", err)
		}
		preludeText = string(data)
	}
	if preludeText != "" && !hasSkipPrelude(src) {
		src = preludeText + src
		sourceOffset = len(preludeText)
	}

	release := len(releaseMode) > 0 && releaseMode[0]

	p := &parser{
		scanner:    scanner{syn: syntax.Scanner{Src: src}, locale: locale, sourceOffset: sourceOffset},
		fns:        map[string]*fnDef{},
		iters:      map[string]*iterDef{},
		target:     behaviorID,
		loopLabels: map[string]bool{},
		consts:     map[string]*constDef{},
		enums:      map[string]*enumDef{},
		bhvs:       map[string]*bhvDef{},
		prelude:    preludeText,
		sourceFS:    sourceFS,
		sourcePath:  sourcePath,
		sourceDir:   sourceDir,
		stdlibFS:    stdlib,
		releaseMode: release,
	}
	obj, err := p.parseFile()
	if err != nil {
		return nil, nil, err
	}
	return obj, p.warnings, nil
}

// hasSkipPrelude reports whether the source starts with a `skip prelude` directive.
func hasSkipPrelude(src string) bool {
	s := &scanner{syn: syntax.Scanner{Src: src}}
	tok1, err := s.next()
	if err != nil || tok1.kind != tokIdent || tok1.val != "skip" {
		return false
	}
	tok2, err := s.next()
	if err != nil || tok2.kind != tokIdent || tok2.val != "prelude" {
		return false
	}
	return true
}

// TestParseLocalePrefix is a test helper that exposes parseLocalePrefix.
func TestParseLocalePrefix(line string) (locale, rest string, ok bool) {
	return parseLocalePrefix(line)
}

// --- check_number instruction slots ---

const (
	checkLarger  = "0" // exec branch: value > target
	checkSmaller = "1" // exec branch: value < target
	checkValue   = "2" // input: value to compare
	checkTarget  = "3" // input: comparison target
)

// --- compare_register instruction slots ---

const (
	compareRegDifferent = "0" // exec branch: If Different
	compareRegValue1    = "1" // input: value_1
	compareRegValue2    = "2" // input: value_2
)

// --- value_type instruction slots ---

const (
	valueTypeInput = "0" // input: value to check
	valueTypeItem  = "1" // exec branch: Item
	valueTypeUnit  = "2" // exec branch: Unit
	valueTypeComp  = "3" // exec branch: Component
	valueTypeTech  = "4" // exec branch: Tech
	valueTypeValue = "5" // exec branch: Value
	valueTypeCoord = "6" // exec branch: Coord
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
	name       string // variable name used in the function body
	keyword    string // "" for positional, keyword name for keyword params
	direction  string // "" or "in", "out", "inout"; "" defaults to "in"
	isParam    bool   // true for param modifier (requires behavior parameter at call site)
	isBehavior bool   // true for behavior modifier (requires behavior reference at call site)
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

// enumDef holds a compile-time enum definition with named integer values.
type enumDef struct {
	values  map[string]int // member name → integer value
	members []string       // declaration order (for error messages)
	private bool           // true for private enum (not visible as import)
}

// bhvDef holds the metadata for a behavior that can be called via `call`.
type bhvDef struct {
	params     []paramDef // from @param declarations
	sourceFS   fs.FS      // nil = same file as caller
	sourcePath string     // file path within sourceFS (empty = same file)
	sourceText string     // cached source (for cross-file compilation)
	prelude    string     // prelude text to prepend (for cross-file compilation)
}

// symbolSet groups the top-level declaration maps (functions, iterators,
// constants, enums, behaviors) that share a namespace. Used by the import
// system and namespace storage to operate on all uniformly.
type symbolSet struct {
	fns    map[string]*fnDef
	iters  map[string]*iterDef
	consts map[string]*constDef
	enums  map[string]*enumDef
	bhvs   map[string]*bhvDef
}

// newSymbolSet creates an empty symbolSet.
func newSymbolSet() *symbolSet {
	return &symbolSet{
		fns:    map[string]*fnDef{},
		iters:  map[string]*iterDef{},
		consts: map[string]*constDef{},
		enums:  map[string]*enumDef{},
		bhvs:   map[string]*bhvDef{},
	}
}

// has reports whether name exists in any of the maps.
func (s *symbolSet) has(name string) bool {
	if _, ok := s.fns[name]; ok {
		return true
	}
	if _, ok := s.iters[name]; ok {
		return true
	}
	if _, ok := s.consts[name]; ok {
		return true
	}
	if _, ok := s.enums[name]; ok {
		return true
	}
	if _, ok := s.bhvs[name]; ok {
		return true
	}
	return false
}

// isPrivate reports whether name exists and is private.
func (s *symbolSet) isPrivate(name string) bool {
	if fn, ok := s.fns[name]; ok {
		return fn.private
	}
	if it, ok := s.iters[name]; ok {
		return it.private
	}
	if c, ok := s.consts[name]; ok {
		return c.private
	}
	if e, ok := s.enums[name]; ok {
		return e.private
	}
	// behaviors are never private (for now)
	return false
}

// mergeNonPrivate copies all non-private entries from src into s.
func (s *symbolSet) mergeNonPrivate(src *symbolSet) {
	for name, fn := range src.fns {
		if !fn.private {
			s.fns[name] = fn
		}
	}
	for name, it := range src.iters {
		if !it.private {
			s.iters[name] = it
		}
	}
	for name, c := range src.consts {
		if !c.private {
			s.consts[name] = c
		}
	}
	for name, e := range src.enums {
		if !e.private {
			s.enums[name] = e
		}
	}
	for name, b := range src.bhvs {
		s.bhvs[name] = b
	}
}

// clone returns a shallow copy of the symbolSet (maps are cloned).
func (s *symbolSet) clone() *symbolSet {
	return &symbolSet{
		fns:    maps.Clone(s.fns),
		iters:  maps.Clone(s.iters),
		consts: maps.Clone(s.consts),
		enums:  maps.Clone(s.enums),
		bhvs:   maps.Clone(s.bhvs),
	}
}

// deleteAll removes name from all maps.
func (s *symbolSet) deleteAll(name string) {
	delete(s.fns, name)
	delete(s.iters, name)
	delete(s.consts, name)
	delete(s.enums, name)
	delete(s.bhvs, name)
}

// addNonPrivateNames adds all non-private symbol names to the provided set.
func (s *symbolSet) addNonPrivateNames(names map[string]bool) {
	for name, fn := range s.fns {
		if !fn.private {
			names[name] = true
		}
	}
	for name, it := range s.iters {
		if !it.private {
			names[name] = true
		}
	}
	for name, c := range s.consts {
		if !c.private {
			names[name] = true
		}
	}
	for name, e := range s.enums {
		if !e.private {
			names[name] = true
		}
	}
	for name := range s.bhvs {
		names[name] = true
	}
}

// iterDef holds a parsed iter declaration.
type iterDef struct {
	params   []paramDef
	outputs  []string       // yielded output names (from -> ...)
	astBody  []Stmt         // body (may contain yield/for)
	frame    map[string]any // promoted instruction frame (stdlib)
	doneSlot string         // instruction exec slot key for exhaustion (0-based ref key)
	private   bool                // true for private iter
	scope     map[string]*fnDef  // functions available when this iter was defined
	iterScope map[string]*iterDef // iterators available when this iter was defined
}

// execBinding marks an instruction block slot as wired to a continuation.
type execBinding struct {
	name     string // continuation name (or "return")
	detached bool   // true if marked with `detach`
	local    bool   // true if marked with `'` sigil (instruction-level block)
	args     []any  // @N references (returnSlot) and literals; nil = no data
}

type fnDef struct {
	params       []paramDef
	rets         []string        // return names (nil = no return)
	frame        map[string]any  // instruction-based (stdlib)
	astBody      []Stmt          // call-based (user-defined) — AST IR
	execNames    []string        // ordered continuation names from exec(...)
	execDetached map[string]bool // which continuations are detached (derived from instruction block)
	execContArgs map[string]int  // continuation name → arg count (pure-logic data dispatch)
	private      bool                // true for private fn (not visible as import)
	scope        map[string]*fnDef  // functions available when this fn was defined (for imports)
	iterScope    map[string]*iterDef // iterators available when this fn was defined (for imports)
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

// behaviorRef marks a resolved behavior dependency reference.
// The int is the value for the "sub" field: 1-based index into
// dependencies, or -1 for self-recursion.
type behaviorRef int

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

// hasExec reports whether the function declares continuations.
func (f *fnDef) hasExec() bool {
	return len(f.execNames) > 0
}

// unitRegisters maps $name identifiers to their wire-format integers.
var unitRegisters = map[string]int{
	"$signal": -4,
	"$visual": -3,
	"$store":  -2,
	"$goto":   -1,
}

type varInfo struct {
	mutable bool   // true for var, false for let
	depth   int    // scope depth at declaration (for shadowing warnings)
	used    bool   // whether the variable has been read since declaration
	regName string // register name in compiled output (may differ from user name when shadowing)
}

type paramInfo struct {
	index     int    // 1-based parameter index
	name      string // display name
	direction string // "in", "out", or "inout"
	initValue any    // initial value for pinits (nil = no default)
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
// The register name matches the user-visible name. Use declareVarScoped
// for user var/let declarations that should isolate shadowed registers.
func (s *symbolTable) declareVar(name string, mutable bool) {
	s.vars[name] = varInfo{mutable: mutable, depth: s.scopeDepth, regName: name}
	s.usedVars[name] = true
}

// declareVarScoped registers a variable and allocates a unique register
// name when the variable shadows an outer-scope variable. This prevents
// an inner var/let from overwriting the outer variable's register value.
func (s *symbolTable) declareVarScoped(name string, mutable bool) {
	regName := name
	if existing, ok := s.vars[name]; ok && existing.depth < s.scopeDepth {
		regName = s.allocUniqueShadow(name)
	}
	s.vars[name] = varInfo{mutable: mutable, depth: s.scopeDepth, regName: regName}
	s.usedVars[regName] = true
}

// allocUniqueShadow generates a unique register name for a shadowed variable.
func (s *symbolTable) allocUniqueShadow(name string) string {
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s$%d", name, i)
		if !s.usedVars[candidate] {
			return candidate
		}
	}
}

// resolveReg returns the register name for a user-visible variable name.
func (s *symbolTable) resolveReg(name string) string {
	if vi, ok := s.vars[name]; ok {
		return vi.regName
	}
	return name
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
// frame if the current mode differs. When saved is modeUnknown (top-level
// mode block), defaults to locked for the restore.
func emitModeExit(b *frameBuilder, saved execMode) {
	if saved == modeUnknown {
		if b.mode != modeLocked {
			b.emit(map[string]any{"op": "lock"})
		}
		b.mode = modeLocked
		return
	}
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
	modeUnknown
)

// frameRef marks an integer as a reference to a 0-based index in a
// frameBuilder's slice. finalize converts these to 1-based integers
// for the output JSON (matching Lua's 1-based table indexing).
type frameRef int

type frameBuilder struct {
	frames        []map[string]any
	cursor        int
	mode          execMode
	nextLabel     int
	namedLabels   map[string]int  // name → allocated label index
	emittedLabels map[string]bool // names with label instruction emitted
	emittedJumps  map[string]bool // names with jump instruction emitted
	deferredEvents []deferredEvent // events to emit after main flow
}

// allocLabels reserves n label indices and returns the base index.
func (b *frameBuilder) allocLabels(n int) int {
	base := b.nextLabel
	b.nextLabel += n
	return base
}

// compilerLabel returns a composite register value for a compiler-
// generated label. Uses v_letter_L + large negative numbers to avoid
// collision with user labels.
func compilerLabel(n int) map[string]any {
	return map[string]any{"id": "v_letter_L", "num": -1000001 - n}
}

// resolveNamedLabel lazily allocates a compiler label for the given name.
// Idempotent: returns the same label value on subsequent calls with the same name.
func (b *frameBuilder) resolveNamedLabel(name string) map[string]any {
	if b.namedLabels == nil {
		b.namedLabels = map[string]int{}
	}
	if idx, ok := b.namedLabels[name]; ok {
		return compilerLabel(idx)
	}
	idx := b.allocLabels(1)
	b.namedLabels[name] = idx
	return compilerLabel(idx)
}

// markLabelEmitted records that a label instruction was emitted for the given name.
// Returns an error if a label with that name was already emitted (duplicate).
func (b *frameBuilder) markLabelEmitted(name string) error {
	if b.emittedLabels == nil {
		b.emittedLabels = map[string]bool{}
	}
	if b.emittedLabels[name] {
		return fmt.Errorf("duplicate label '%s", name)
	}
	b.emittedLabels[name] = true
	return nil
}

// markJumpEmitted records that a jump instruction targeting the given name was emitted.
func (b *frameBuilder) markJumpEmitted(name string) {
	if b.emittedJumps == nil {
		b.emittedJumps = map[string]bool{}
	}
	b.emittedJumps[name] = true
}

// validateNamedLabels checks that every named jump has a matching label.
// Returns an error listing orphan jump names.
func (b *frameBuilder) validateNamedLabels() error {
	if b.emittedJumps == nil {
		return nil
	}
	var orphans []string
	for name := range b.emittedJumps {
		if b.emittedLabels == nil || !b.emittedLabels[name] {
			orphans = append(orphans, "'"+name)
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	if len(orphans) == 1 {
		return fmt.Errorf("jump to %s with no matching label", orphans[0])
	}
	return fmt.Errorf("jumps to %s with no matching labels", strings.Join(orphans, ", "))
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

// isNoopBridge reports whether f is a set_reg false, false frame
// serving only as a control-flow redirect (no side effects).
// Frames with extra keys (e.g., "cmt") are conservatively kept —
// a comment is player-visible in the game's behavior editor.
//
// Re-dispatch noops ("next": false) ARE eligible for elimination.
// When a numbered exec slot references a noop, replacing the ref
// with false produces correct re-dispatch semantics — the VM treats
// false in any exec slot (numbered or "next") as "no connection",
// which triggers re-dispatch to the enclosing iterator or restarts
// the behavior at the top level. Absent keys (not false) cause
// fall-through to the next frame.
func isNoopBridge(f map[string]any) bool {
	if f["op"] != "set_reg" || f["0"] != false || f["1"] != false {
		return false
	}
	for k := range f {
		switch k {
		case "op", "0", "1", "next":
		default:
			return false
		}
	}
	return true
}

// resolveNoopTarget follows a chain of noop bridges starting at idx
// to find the final non-noop target. Returns a frameRef for a real
// frame or false for re-dispatch.
func resolveNoopTarget(frames []map[string]any, idx int, noops map[int]bool) any {
	visited := map[int]bool{idx: true}
	for {
		f := frames[idx]
		next, hasNext := f["next"]
		if !hasNext {
			// Fall-through to idx+1.
			target := idx + 1
			if noops[target] {
				if visited[target] {
					return frameRef(target)
				}
				visited[target] = true
				idx = target
				continue
			}
			return frameRef(target)
		}
		if next == false {
			return false
		}
		if ref, ok := next.(frameRef); ok {
			target := int(ref)
			if noops[target] {
				if visited[target] {
					return next
				}
				visited[target] = true
				idx = target
				continue
			}
			return next
		}
		return next
	}
}

// eliminateNoopBridges removes set_reg false, false frames that serve
// only as control-flow redirects, adjusting all references to maintain
// correct control flow. Runs after all emission/patching, before finalize.
func (b *frameBuilder) eliminateNoopBridges() {
	// Phase 1: Identify noop frames.
	noops := map[int]bool{}
	for i, f := range b.frames {
		if isNoopBridge(f) {
			noops[i] = true
		}
	}
	if len(noops) == 0 {
		return
	}

	// Phase 2: Resolve targets transitively.
	targets := make(map[int]any, len(noops))
	for i := range noops {
		targets[i] = resolveNoopTarget(b.frames, i, noops)
	}

	// Phase 3: Redirect references in all non-noop frames.
	for i, f := range b.frames {
		if noops[i] {
			continue
		}
		for k, v := range f {
			if ref, ok := v.(frameRef); ok {
				if target, ok := targets[int(ref)]; ok {
					f[k] = target
				}
			}
		}
	}

	// Phase 4: Fix fall-through predecessors. When a non-noop frame at
	// position I-1 falls through to a noop at position I, the noop's
	// removal changes what the predecessor falls through to. If the
	// noop's target differs from the natural fall-through after removal,
	// we must add explicit branches. This restores branch slots that
	// stripFallThrough deleted (because they pointed to the immediately
	// following frame — the noop — which is now being removed).
	for i := range noops {
		if i > 0 && !noops[i-1] {
			pred := b.frames[i-1]
			target := targets[i]

			// Find where fall-through will go after all noops are removed:
			// the first non-noop frame after position i.
			nextReal := i + 1
			for nextReal < len(b.frames) && noops[nextReal] {
				nextReal++
			}

			// If the noop's target is the natural fall-through successor,
			// the stripped slots are still correct — no fix needed.
			if target == frameRef(nextReal) {
				continue
			}

			if _, hasNext := pred["next"]; !hasNext {
				pred["next"] = target
			}
			// Restore stripped branch slots on check frames.
			op, _ := pred["op"].(string)
			switch op {
			case "check_number":
				if _, has := pred[checkLarger]; !has {
					pred[checkLarger] = target
				}
				if _, has := pred[checkSmaller]; !has {
					pred[checkSmaller] = target
				}
			case "compare_register":
				if _, has := pred[compareRegDifferent]; !has {
					pred[compareRegDifferent] = target
				}
			case "value_type":
				for _, slot := range allTypeSlots {
					if _, has := pred[slot]; !has {
						pred[slot] = target
					}
				}
			}
		}
	}

	// Phase 5: Remove and reindex.
	// Build offset table: offsets[i] = count of noops before index i.
	offsets := make([]int, len(b.frames)+1)
	count := 0
	for i := range b.frames {
		offsets[i] = count
		if noops[i] {
			count++
		}
	}
	offsets[len(b.frames)] = count

	// Build new frame slice without noops.
	newFrames := make([]map[string]any, 0, len(b.frames)-len(noops))
	for i, f := range b.frames {
		if !noops[i] {
			newFrames = append(newFrames, f)
		}
	}

	// Adjust remaining frameRef values to account for removed frames.
	for _, f := range newFrames {
		for k, v := range f {
			if ref, ok := v.(frameRef); ok {
				idx := int(ref)
				if idx < len(offsets) {
					f[k] = frameRef(idx - offsets[idx])
				}
			}
		}
	}

	// Strip redundant "next" that now points to the natural successor.
	for i, f := range newFrames {
		if next, ok := f["next"]; ok {
			if ref, ok := next.(frameRef); ok && int(ref) == i+1 {
				delete(f, "next")
			}
		}
	}

	b.frames = newFrames
	b.cursor = len(newFrames)
}

// reorderEventHandlers moves event handler chains adjacent to their
// continuation targets. The Desynced visual editor has an exponential-cost
// auto-layout algorithm that hangs when exec slot connections span large
// frame index gaps. Event handlers are emitted at the end of the frame
// list but their continuation "next" points back to an early frame,
// creating a large gap. This pass eliminates the gap by relocating each
// event chain (event instruction + handler body) to just before its
// continuation target.
func (b *frameBuilder) reorderEventHandlers() {
	// Phase 1: Identify event chains.
	// An event chain starts with an event_parameter or event_radio frame,
	// followed by its handler body frames up to and including the terminal
	// frame (one with an explicit "next" that is a frameRef, or "next": false,
	// or an exit instruction).
	type eventChain struct {
		start       int      // index of event instruction
		end         int      // index of last handler frame (inclusive)
		contTarget  int      // 0-based frame index the handler continues to (-1 if none)
	}
	var chains []eventChain

	for i, f := range b.frames {
		op, _ := f["op"].(string)
		if op != "event_parameter" && op != "event_radio" {
			continue
		}

		// Walk the handler body to find its end. The body is contiguous,
		// ending at the next event instruction or the end of the frame list.
		// The continuation "next" is on the terminal frame (patched by
		// patchHandlerEnd), but intermediate frames may also have "next"
		// values (e.g., if/else branches), so we must walk to the actual end.
		end := i
		contTarget := -1
		for j := i + 1; j < len(b.frames); j++ {
			hf := b.frames[j]
			// Stop before the next event instruction
			hop, _ := hf["op"].(string)
			if hop == "event_parameter" || hop == "event_radio" {
				break
			}
			end = j
		}

		// Check the terminal frame for a backward-pointing continuation
		if end > i {
			lastFrame := b.frames[end]
			if next, hasNext := lastFrame["next"]; hasNext {
				if ref, ok := next.(frameRef); ok {
					target := int(ref)
					if target < i {
						contTarget = target
					}
				}
			}
		}

		if contTarget >= 0 {
			chains = append(chains, eventChain{start: i, end: end, contTarget: contTarget})
		}
	}

	if len(chains) == 0 {
		return
	}

	// Phase 2: Build new frame ordering.
	// Mark all event chain frames.
	isEventFrame := make([]bool, len(b.frames))
	for _, ch := range chains {
		for j := ch.start; j <= ch.end; j++ {
			isEventFrame[j] = true
		}
	}

	// Build insertion map: continuation target -> chains to insert before it.
	// Only insert if the predecessor frame is terminal (won't fall through),
	// so the event chain stays unreachable from normal flow. If the
	// continuation target is frame 0 or its predecessor falls through,
	// leave the chain in its original position.
	insertBefore := map[int][]eventChain{}
	for _, ch := range chains {
		target := ch.contTarget
		if target > 0 {
			pred := b.frames[target-1]
			op, _ := pred["op"].(string)
			// Only safe to insert if the predecessor truly can't fall
			// through: exit instructions are always terminal, and frames
			// with "next": false have no successor. Branching instructions
			// (compare_register, check_number, value_type) may have absent
			// exec slots that fall through even if "next" is explicit.
			safe := op == "exit" || op == "jump" || pred["next"] == false
			if safe {
				insertBefore[target] = append(insertBefore[target], ch)
				continue
			}
		}
		// Can't safely insert — unmark these frames so they stay in place.
		for j := ch.start; j <= ch.end; j++ {
			isEventFrame[j] = false
		}
	}

	if len(insertBefore) == 0 {
		return
	}

	// Build new order: walk frames, skip event frames, insert chains at targets.
	newOrder := make([]int, 0, len(b.frames))
	for i := range b.frames {
		if chs, ok := insertBefore[i]; ok {
			for _, ch := range chs {
				for j := ch.start; j <= ch.end; j++ {
					newOrder = append(newOrder, j)
				}
			}
		}
		if !isEventFrame[i] {
			newOrder = append(newOrder, i)
		}
	}

	// Phase 3: Remap all frameRef values.
	// Build old→new index mapping.
	remap := make([]int, len(b.frames))
	for newIdx, oldIdx := range newOrder {
		remap[oldIdx] = newIdx
	}

	// Build a map from past-the-end sentinel targets to their chain's
	// continuation target. Event handler if/else branches may jump past the
	// handler body using a frameRef one past the last handler frame. After
	// reordering, these must point to the continuation target instead.
	sentinelToCont := map[int]int{}
	for _, ch := range chains {
		if _, ok := insertBefore[ch.contTarget]; ok {
			sentinel := ch.end + 1
			sentinelToCont[sentinel] = ch.contTarget
		}
	}

	newFrames := make([]map[string]any, len(b.frames))
	for newIdx, oldIdx := range newOrder {
		f := b.frames[oldIdx]
		for k, v := range f {
			if ref, ok := v.(frameRef); ok {
				idx := int(ref)
				if idx < len(remap) {
					f[k] = frameRef(remap[idx])
				} else if cont, ok := sentinelToCont[idx]; ok {
					f[k] = frameRef(remap[cont])
				}
				// Other out-of-bounds refs are left as-is
			}
		}
		newFrames[newIdx] = f
	}

	// Phase 4: Strip redundant "next" that now points to the natural successor.
	// Skip terminal instructions (exit, jump) — their "next" is a continuation
	// pointer (e.g., event handler resume target), not flow-through.
	for i, f := range newFrames {
		if next, ok := f["next"]; ok {
			if ref, ok := next.(frameRef); ok && int(ref) == i+1 {
				op, _ := f["op"].(string)
				if op != "exit" && op != "jump" {
					delete(f, "next")
				}
			}
		}
	}

	b.frames = newFrames
	b.cursor = len(newFrames)
}

// finalize writes frame entries into value, converting any frameRef
// values to plain integers.
func (b *frameBuilder) finalize(value map[string]any) {
	for i, f := range b.frames {
		resolved := make(map[string]any, len(f))
		for k, v := range f {
			if ref, ok := v.(frameRef); ok {
				resolved[k] = int(ref) + 1 // 1-based: frame refs are data values, not table keys
			} else {
				resolved[k] = v
			}
		}
		value[strconv.Itoa(i)] = resolved
	}
}

// deferredEvent holds an event statement and the context needed to emit it
// after the main flow. Both behavior-level and inlined function events use
// this struct.
type deferredEvent struct {
	stmt            *OnEventStmt
	paramMap        map[string]any // resolved param mappings (nil for behavior-level)
	syms            *symbolTable   // behavior-level symbol table
	frameAtDeferral frameRef       // b.pos() when the event was deferred
}

// emitContext abstracts the differences between behavior-level and fn body
// emission paths, allowing unified control flow emitters.
type emitContext struct {
	b                *frameBuilder
	usedVars         map[string]bool
	resolveBool      func(expr Expr) (*resolvedBoolExpr, error)
	emitBody         func(stmts []Stmt) error
	exprGetValue     func(expr Expr, comment string) (any, error)
	exprTo           func(expr Expr, target any, comment string) error
	expandCallExpr   func(ce *CallExpr, retVals []any, comment string) error
	resolveInstrFrame func(frame map[string]any, retVals []any, comment string) map[string]any
	pushScope        func()
	popScope         func()
	declareIterVar   func(name string) string
}

// parseMode identifies the parsing context for statement blocks.
type parseMode int

const (
	modeBehavior parseMode = iota // behavior body
	modeFunction                 // function body
	modeIterator                 // iterator body
)

// parseContext abstracts the differences between behavior-level and fn body
// parsing paths, allowing unified statement parsers.
type parseContext struct {
	mode             parseMode
	resolve          operandResolver
	parseBody        func(exprTail bool) ([]Stmt, error)
	pushScope        func()
	popScope         func()
	declareIterVar   func(name string)
	parseConstructor func(nameTok token) (Expr, error)

	// Statement-level callbacks for unified parsing.
	parseLetVar      func(mutable bool, comment string) ([]Stmt, error)
	parseOnEvent     func(comment string) (*OnEventStmt, error)
	parseDefaultStmt func(tok token, comment string, exprTail bool) (stmts []Stmt, done bool, err error)
	checkInstrDirs   func(frame map[string]any, pos int) error
	parseLocalBlocks func(frame map[string]any) ([]*ContinuationBlock, error)
	parseValueExpr   func() (Expr, error) // for break/return values

	// fn-body-specific state (nil in behavior mode).
	fnCtx *fnBodyContext
}
