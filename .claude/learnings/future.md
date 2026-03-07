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
- **Developer tools** — language server, syntax highlighting for
  VS Code and JetBrains IDEs.
- **Website** — static marketing/download page, web-hosted manual,
  syntax highlighting.
- **Desynced 1.0 stdlib update** — see `.claude/learnings/desynced_1_0.md`
  for full analysis. Key items: add 8 new instructions to stdlib
  (+ 2 event instructions needing design), remove `unpackage_all` /
  `package_all`, add "Path Blocked" exec branches to `dodrop` /
  `dopickup` / `domove`, expand `BitwiseMode` enum (8 new ops),
  design faction register syntax, design event instruction support,
  handle `wait 0` behavior change.

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

Post-1.0. The `call` stub has been removed from the stdlib.

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
