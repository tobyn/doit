# Future Ideas

Ideas to revisit later. These are not committed designs — just things
worth thinking about when the time is right.

## Optional parens on zero-arg branching calls

Zero-argument branching functions like `sequence()`, `for_component()`,
and `each_component()` require empty parens today. The parser could be
extended to allow `sequence { first { ... } done { ... } }` without
parens, matching how zero-arg non-branching calls already work. The
disambiguation rule would be: if the token after the identifier is `{`
and the function has exec continuations, treat `{` as the start of
continuation blocks rather than a statement block.

## Optional static type checking (post-release priority)

Add optional type annotations that the compiler checks at compile time.
Granularity at the game-type level: `Item`, `Unit`, `Component`,
`Technology`, `Value`, `Coordinate`, `Number`, `Boolean`, `String`,
`Range`. Unannotated code stays unchecked (gradual typing). The
compiler already tracks string vs register values implicitly — this
would make that reasoning explicit and extend it to distinguish between
register types.

## 1.0 burndown

- ~~**Namespace qualification parity**~~ — Fixed. `for...in` now
  supports namespace-qualified iterators (`for v in lib.my_iter()`).
  Audit confirmed no other gaps: functions, constants, enums, and
  iterators all resolve through `resolveFnName` in all contexts.
- **Move tests into packages** — compiler tests in `compiler/`, codec
  tests in `codec/`. Only integration wiring tests stay in `main`.
- **Error message quality** — review compiler errors for clarity and
  source locations. Important for new users at 1.0.
- **Assert + debug/release modes** — `assert` statement that checks
  a condition at runtime: `assert expr, "message"` with optional value
  `assert expr, "message", val`. In debug mode, emits a truthy check
  that proceeds on success or shows a notification with the error
  message (and value) then exits on failure. In release mode, all
  `assert` statements are omitted entirely. Requires a compiler flag
  for mode selection.
- **Developer tools** — language server, syntax highlighting for
  VS Code and JetBrains IDEs.
- **Website** — static marketing/download page, web-hosted manual,
  syntax highlighting.
- ~~**Desynced 1.0 stdlib update**~~ — Done. New stdlib instructions,
  removed `unpackage_all`/`package_all`, "Path Blocked" exec branches,
  `BitwiseMode` expansion, `wait 0` behavior change, behavior
  properties (`@keepvars`, `@keeparrays`, `@param` defaults),
  faction register syntax (`%name`). `instructions.lua` updated to 1.0.
- **`call` keyword + `load_behavior` + `dependencies` format** —
  `call` needs to become a language keyword for inter-behavior calls
  (the game instruction requires a dependency index that can't be a
  stdlib parameter). `load_behavior` (remotely loads a behavior onto
  an adjacent unit) needs the same behavior-reference mechanism.
  The codec needs to handle the new `dependencies` format (flat array
  at root, preferred over legacy `subs`). See detailed analysis in
  "Subroutine calls instead of inlining" and "`call` keyword for
  inter-behavior calls" sections below.
- ~~**Event instructions**~~ — Done. `on $param { handler }` for
  parameter-change events, `on radio(band) -> signal { handler }` for
  radio events. `param` modifier on function args enforces behavior
  parameter at call sites. Events deferred and emitted after main flow.
- **Locked/unlocked block parity** — `locked { ... }` and
  `unlocked { ... }` blocks should behave like other block constructs:
  support `break` to exit early, return value(s) from the block
  expression, etc. Currently they're simpler than other blocks. Aligning
  their semantics makes the language more consistent and composable.
- ~~**Initial mode = unknown**~~ — Done. Behaviors now start in
  `modeUnknown`. The first mode block always emits its transition
  instruction. Top-level mode block exit defaults to locked for the
  restore (backward compatible with standalone behaviors while
  preparing for `call` subroutines).

## Reactive block (`watch` / `react`)

Many behaviors are driven by parameter values and radio signals from
other sources. A reactive block construct could listen to multiple
event sources and re-execute its body whenever any of them change:

```
watch $param1, $param2, radio(band) -> sig {
    # body re-runs whenever param1, param2, or the radio signal changes
    result = compute($param1, $param2, sig)
}
```

This extracts the common pattern of "set up N event handlers that all
re-trigger the same logic" into a single declarative construct. Under
the hood it would compile to multiple `event_parameter` /
`event_radio` instructions all pointing to the same handler entry.

### Polling non-event sources

Some data sources don't have event instructions — faction registers
(`%name`), unit registers (`$signal`, `$visual`), or arbitrary
expressions. A unified reactive model could support polling these on a
timer alongside true event sources:

```
watch $param1, poll(%shared_counter, ticks=5) {
    # re-runs on param1 change OR every 5 ticks if %shared_counter changed
}
```

`poll` would compile to a periodic check (e.g., `wait_ticks` +
`compare_register` against a cached previous value), while true event
sources use the native event instructions. The block body sees a
consistent snapshot regardless of the trigger source.

### Open questions

- **Syntax**: `watch`, `react`, `observe`, or something else?
- **Polling semantics**: Should polled sources use dirty-checking
  (compare current vs cached) or always re-run? Dirty-checking is
  more efficient but adds hidden state (cached values in registers).
- **Debouncing**: If multiple events fire in the same tick, does the
  body run once or multiple times? The VM's event model (events don't
  nest) may naturally deduplicate.
- **Interaction with main flow**: Does the reactive block replace the
  main loop entirely (the behavior IS the reactive block), or can it
  coexist with imperative main flow? Both patterns seem useful.
- **Scope**: Should this be a behavior-level-only construct, or should
  it work inside functions (like `on` does with `param` args)?

## Compound doc comments from nested calls

When a function call has a `#!` comment and the expanded instructions also
have their own `#!` comments, it might be useful to build compound
comments that combine both levels (e.g., `"Greeting sequence / Says
hello"`). The syntax for this hasn't been decided yet.

## AST optimizations

Constant folding is now implemented (arithmetic, comparisons, boolean
chains, negation). Remaining optimization ideas:

- **Dead code elimination**: Remove unreachable statements after
  `return`/`break`.
- **Opportunistic partial evaluation**: Extend compile-time evaluation
  beyond `const` declarations to optimize regular expressions when all
  operands happen to be known at compile time.

## Condition optimization

Deduplicate repeated type checks across `if`/`else if` chains (e.g.,
`a is Unit && ...` followed by `a is Coord || ...` should check
`value_type(a)` once when possible). Feeds directly into `match`/`when`
if that's added later.

## `match` / `when` sugar

The continuation block syntax already serves as a type switch (e.g.,
`value_type(data) { item { ... } unit { ... } }`). A dedicated `match`
expression could add sugar over `if`/`else if` chains for
non-continuation contexts. The two features would complement each other.

## Subroutine calls instead of inlining

Functions are currently always inlined — every call site duplicates the
function body. The behavior VM has a `call` instruction that supports
subroutines (see `game.md` Subroutines section for the full call
mechanism). This could allow true function calls without duplication
and would also enable recursion. The `call` instruction is fully
understood — the remaining work is compiler support: emitting
subroutine behaviors as dependencies, generating `call` instructions
instead of inlining, and managing the flat dependency list.

### Potential wins

- **Recursion**: Currently impossible — inlining would expand
  infinitely. `call` with `"sub": -1` enables self-recursion; the flat
  dependency array enables mutual recursion (A↔B). New language
  capability, not just an optimization.
- **Compiled output size**: Every inlined call duplicates the entire
  function body, scaling multiplicatively with call depth. Subroutines
  reduce N call sites to one dependency + N single-frame `call`
  instructions. This reduces compiled JSON size but does NOT help with
  the game's instruction budget — the VM still descends into the
  callee and counts every instruction executed.
- **Variable collision avoidance removal**: `collectASTOutputVars` +
  `allocUniqueVar` (~70+ lines) exist solely because inlined code
  shares the caller's namespace. The VM gives each subroutine its own
  variable namespace, eliminating the collision problem entirely for
  subroutine-compiled functions.
- **Transitive scope management simplification**: `expandCall`
  temporarily merges the called function's `scope` into the parser's
  function map so transitive callees resolve, then cleans up. With
  subroutines, each dependency compiles independently — its own
  callees become additional entries in the flat dependency array.

### Limitations and trade-offs

- **No exec slots**: `call` is a simple instruction with no branching,
  so functions with `exec(...)` signatures can't become subroutines
  directly. Branching functions must still be inlined.
- **Static lock tracking lost**: Inlining lets the compiler track
  lock/unlock state through function bodies statically. Subroutine
  calls break this — the compiler can't see inside the callee. Could
  require the programmer to manage lock state at call boundaries, or
  accept `modeUnknown` after a `call`.
- **Return path optimizations don't transfer**: The three inline
  return modes (return-instruction promotion, zero-copy, emit-and-jump)
  are inlining-specific optimizations. Subroutine returns use parameter
  passing, which isn't notably simpler — just different.
- **Import/scope machinery shifts, not disappears**: Transitive
  dependency resolution via `fnDef.scope` is still needed at compile
  time to determine which functions a subroutine calls. The scope
  management changes shape rather than going away.

### Player and developer UX downsides

These are the practical reasons `call` should be opt-in for specific
use cases rather than a wholesale replacement for inlining:

- **Behavior list pollution**: Importing a behavior that `call`s N
  subroutines means importing N+1 behaviors. All subroutine
  dependencies appear in the player's behavior list as separate
  entries, polluting it with internal implementation details.
- **Debugging blind spots**: The game's behavior editor has
  step-by-step execution and per-step register inspection. These tools
  cannot see across `call` boundaries — subroutines are effectively
  black boxes during debugging. Inlined code is fully visible.

### Relationship to iterators

`yield` works via compile-time AST rewriting (`rewriteYieldToBody`):
each `yield` site is replaced with the caller's loop body inlined.
Yield inside loops, conditionals, and even `for...in` over other
iterators works fine — the rewriting is compositional (outer yields
rewrite at AST level, inner iter expansion happens during emission).

Remaining limitations:

- **No recursive iterators** — same inlining problem as regular
  functions. A tree-walking iterator that calls itself would expand
  infinitely.
- **Yield is lexical** — can't factor out iter logic into a helper
  function that yields on the iter's behalf. `yield` must appear
  textually in the iter body.
- **Caller body duplicated per yield site** — N lexically distinct
  yield statements = N copies of the caller's loop body in compiled
  output. (A single yield inside a loop = 1 copy, executed many times.)

### `call` as the path to recursion

Given the UX downsides (behavior list pollution, debugging blind
spots) and the fact that `call` doesn't help with instruction budget,
its only unique capability is enabling recursion — for both functions
and iterators. This is low priority but worth keeping in mind as a
someday feature. The infrastructure (`call` instruction, dependency
arrays, self-call via `"sub": -1`) is fully understood.

## `call` keyword for inter-behavior calls

The `call` game instruction can't live in the stdlib because it needs
the dependency index of the callee, which can't be expressed as a
literal parameter. Instead, `call` should become a language keyword
with syntax for invoking behaviors defined in doit code.

The language already has the building blocks: behavior IDs (from file
names or declarations) and parameter definitions with directions
(in/out). A `call` keyword would let users explicitly invoke another
compiled behavior as a subroutine, with the compiler resolving the
callee to a dependency index and wiring parameter slots.

The main win is **user-driven recursion** — a behavior can `call`
itself (via `"sub": -1`) without the compiler needing to auto-detect
or optimize recursion. Users who need recursion opt into it explicitly,
accepting the trade-offs (behavior list pollution, debugging blind
spots — see "Subroutine calls instead of inlining" above).

This also opens the door to multi-behavior projects where behaviors
call each other, though the UX trade-offs (every callee appears as a
separate behavior in the player's list) mean it should be an explicit
choice rather than a default compilation strategy.

The `load_behavior` instruction (new in 1.0) also belongs here — it
remotely loads a behavior onto an adjacent unit, requiring the same
behavior-reference mechanism that `call` needs.

1.0 burndown item. The `call` stub has been removed from the stdlib.

## Visual layout (nx/ny fields)

The compiled JSON supports `"nx"` and `"ny"` fields on each frame to
control instruction placement in the game's visual behavior editor.
The compiler currently omits these, leaving layout to the game's
auto-layout algorithm. Since the compiler knows the control flow
structure it built (sequential blocks, if/else diamonds, loop bodies,
continuation branches), it could produce much cleaner layouts:

- Sequential statements in a vertical column
- Conditional branches side by side
- Loop bodies visually grouped and indented
- Continuation blocks (exec branches) laid out as parallel columns

Every compiled behavior would look better in the editor immediately.
The game's auto-layout doesn't understand structured control flow,
so it often produces tangled graphs for non-trivial behaviors.

Post-1.0. Requires reverse-engineering the coordinate system and
grid spacing the game uses for `nx`/`ny` values.

## Jump/label applications

Verified in-game: `jump` escapes `for_number` loop bodies (including
nested loops), variables survive the jump, and the VM abandons active
iterators cleanly. These primitives enable several improvements.

### Computed goto for persistent state machines

Jump/label with a variable stored in a **parameter** (which persists
across ticks) enables computed-goto state machines. A behavior can
store its current state as a label value in a parameter and `jump` to
it on entry, replacing chains of `if`/`else if` or `check_number`.
This uses the expression form (no `'` sigil) since the jump target is
dynamic.

```
behavior patrol_fsm {
    @param inout state "State"
    if state { jump state }

    # Initial state: patrol
    label 1
    domove $waypoint
    state = 2
    exit

    # State 2: engage
    label 2
    ...
}
```

This is a user-facing pattern (not a compiler feature) but worth
documenting. Useful for behaviors that need to resume mid-sequence
across ticks without re-evaluating from the top.
