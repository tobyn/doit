# Future Ideas

Ideas to revisit later. These are not committed designs — just things
worth thinking about when the time is right.

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
