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

## Continuations and branching instructions

Currently ~70 stdlib instructions are control-flow stubs because they
have `[exec]` branch slots the compiler can't express. The design
uses **continuations** as the unifying concept — every path out of a
branching instruction (exec slots AND `"next"`) is a continuation.
None is inherently special.

### Core model

The `instruction` intrinsic needs to be enhanced to express the full
range of VM control flow. The key concepts:

- **Continuation blocks**: Named blocks of code that exec slots or
  `"next"` can be wired to. At the call site, the caller provides
  blocks for whichever continuations they care about.
- **`return`**: A reserved name (already a keyword) representing the
  continuation where the construct exits. Any slot can be bound to
  `return`. When no slot is explicitly bound to `return`, the return
  point is the implicit join point after all blocks.
- **Default behavior**: `next: return` is implicit when `"next"` isn't
  explicitly bound. This preserves backward compatibility — today's
  non-branching instructions work unchanged.

### Two kinds of continuation connections

**Bridging continuations** (e.g., check_number's larger, smaller,
equal): The block runs, then control bridges to `return`. The
instruction fires once, picks a path, and that path merges back into
the main flow. The compiler adds a jump to the return point after the
block's last frame.

**Detaching continuations** (e.g., for_number's body): The block
runs, then falls off — `"next": false`. The instruction retains
control internally. It decides whether to dispatch to the body again
or fire another continuation. The body is subordinate to the
instruction, like a sub-behavior. Verified via decoded `for_number`
JSON: the loop body ends with `"next": false` and has NO explicit
back-edge to the iterator instruction. The VM handles re-dispatch
internally for iterator instructions.

These two types are exhaustive — every exec slot in every known
instruction (including modded instructions examined) is cleanly
bridging or detaching. No third category was found.

### Function signatures

Functions declare their continuations after the param list with the
`exec` keyword. All continuations are listed — including the one
mapped to `"next"`. No path is privileged:

```
fn check_number(value, target) exec(larger, smaller, equal) { ... }
fn for_component(entity) exec(body, done) { ... }
fn my_branch(a) exec(next_one, next_two) { ... }
```

The signature does not distinguish bridging from detaching. The
connection type is an implementation detail determined by the
`instruction` block bindings. For pure-logic functions (no
`instruction`), continuations are always bridging.

### Instruction block bindings

Inside `instruction { }`, exec slots and `"next"` are wired to
continuation names. The `exec` keyword distinguishes exec slot keys
from parameter slot keys. `next` and `detach` both imply `exec`, so
the `exec` keyword is only required for bare numeric keys:

```
fn check_number(value, target) exec(larger, smaller, equal) {
    instruction "check_number" {
        2: value,
        3: target,
        exec 0: larger,       // exec required (bare numeric key)
        exec 1: smaller,
        next: equal,           // implies exec
    }
}
```

The `detach` modifier marks a detaching continuation. It also
implies `exec`:

```
fn for_component(entity) exec(body, done) {
    instruction "for_component" {
        0: @1,
        1: @2,
        detach next: body(@1, @2),  // detach implies exec
        exec 2: done,
    }
}
```

Redundant `exec` is always accepted for clarity: `exec next:` is
the same as `next:`, and `detach exec 2:` is the same as
`detach 2:`.

### Data flow: `@N` binding to continuations

`@N` markers define output data slots on the instruction (as they do
today). Continuations receive data by listing `@N` references in an
argument list:

```
detach next: body(@1, @2),     // body receives two values
exec 0: a_closer(@1),          // a_closer receives one value
```

Pure conditionals have no `@N` markers — continuations are pure
control flow.

The same `@N` can appear in multiple continuation argument lists.
This handles cases like `select_nearest` where output is available
regardless of which path fires.

**`return` continuation**: Controls what the function returns to the
caller. `return(@2, @4)` returns specific outputs. Bare `return` or
absent `return` defaults to all `@N` in order — this preserves
backward compatibility with today's non-branching instructions.

**Value arguments in continuation lists**: Argument lists support
literal values and expressions alongside `@N` references. This
enables several patterns:

- **Internalizing failure**: A function wraps a failable instruction
  and returns defaults on the failure path instead of exposing the
  continuation: `exec 0: return(null)`.
- **Discriminated merging**: Two exec paths route to the same
  continuation with a different literal tag:
  `exec 0: handler(@1, MyEnum::PathA)`,
  `exec 1: handler(@1, MyEnum::PathB)`.
- **Default fill**: A continuation passes through some outputs and
  fills the rest: `exec 0: result(@1, 0)`.

At minimum, literals (numbers, null, enum values, constructors) are
supported. Full expressions are desirable but not required.

### Pure-logic functions can branch too

`return <continuation_name>` routes execution to a caller-provided
continuation without any instruction involvement:

```
fn my_branch(a) exec(next_one, next_two) {
    if a > 3 {
        return next_one
    } else {
        return next_two
    }
}
```

### Call-site syntax

Continuation blocks trail the function call. Bare blocks are
**bridging** (run once). Blocks prefixed with `for` are
**detaching** (iterate). This distinction is always visible at the
call site — the programmer never has to look up a function
definition to know whether their code runs once or in a loop.

Unprovided continuations bridge directly to `return`. Order of
named blocks doesn't matter — matched by name.

**Three syntactic forms:**

**Multi-block** — grouping braces contain named blocks:

```
check_number(a, b) {
    larger { do_something() }
    smaller { do_other() }
    equal { do_equal() }
}

// Mixed bridging and detaching:
do_deep_analysis(input) {
    unit_analysis { unit -> process(unit) }
    for coord_analysis { coord -> analyze_tile(coord) }
    area_analysis { summarize_area() }
    for number_analysis { n -> count(n) }
    sequence_analysis { summarize_sequence() }
    no_match { error() }
}
```

**Single named block** — no grouping braces needed:

```
check_number(a, b) larger { do_something() }

get_inventory_item() no_items { handle_empty() }

for_number_split(0, 10, 1) for even { i -> use_even(i) }
```

**Collapsed unnamed block** — name omitted, binds to the
leftmost continuation in the function's `exec(...)` list.
Remaining continuations are empty (bridge to return):

```
let item = get_inventory_item() { null }

check_number(a, b) { do_something() }
// equivalent to: check_number(a, b) larger { do_something() }
```

**Parser disambiguation** for `fn() {`: if the token after `{`
is an identifier followed by `{`, or `for` followed by an
identifier, it's the multi-block form. Otherwise it's the
collapsed unnamed form. `{ var -> body }` (Kotlin binding) is
always collapsed since `->` cannot follow a continuation name.

**Data binding** uses Kotlin-style arrow syntax. When a
continuation receives `@N` data, the caller binds it with `->`:

```
select_nearest(a, b) {
    a_closer { closest -> use(closest) }
    b_closer { closest -> use(closest) }
}
```

Blocks with no data have no `->`:

```
check_number(a, b) {
    larger { do_something() }
}
```

**`break` semantics** depend on block type:

- **Bridging blocks** are transparent to `break` — like `if`
  bodies, `break` passes through to the enclosing loop.
- **`for` blocks** (detaching) are break targets. `break`
  compiles to the VM's `last` instruction, stopping the iterator.
- Labeled `break` can target an enclosing loop from inside any
  block type.

**Expression form** — each block has a tail value, following
if-expression rules:

```
let result = check_number(a, b) {
    larger { 10 }
    smaller { 20 }
}
// equal unprovided → null (like if-expr without else)

// Collapsed:
let item = get_inventory_item() { null }
```

### Categories of branching instructions

The ~70 control-flow stubs fall into five categories:

1. **Pure conditionals** (`check_number`, `compare_register`,
   `value_type`, `compare_entity`, `compare_item`, `is_a`,
   `unit_type`, `is_unit_a`, `is_empty`, `is_daynight`,
   `get_season`, `check_altitude`, `check_blightness`,
   `check_health`, `check_battery`, `check_grid_effeciency`,
   `is_logistics`, `is_same_grid`, `is_moving`, `is_passable`,
   `is_fixed`, `is_unlocked`, `have_item`, `checkfreespace`,
   `can_produce`, `gettrust`, `match`, `switch`, `check_bit`) —
   Route execution to one of N paths. No data output. All
   continuations are bridging.

2. **Iterators** (`for_component`, `for_unit`, `for_inventory_item`,
   `for_entities_in_range`, `for_number`, `for_producers`,
   `for_recipe_ingredients`, `for_repair_ingredients`, `for_research`,
   `for_research_ingredients`, `for_research_unlocks`, `for_signal`,
   `for_signal_match`, `for_count_resources`, `memory_loop`) —
   Stateful instructions with a detaching body continuation and a
   bridging "done" continuation. Produce output data each iteration.
   Will be handled by generalized `for` loops (see separate section).

3. **Failable getters** (`get_inventory_item`,
   `get_inventory_item_index`, `get_resource_item`,
   `get_reg_remotely`, `faction_item_amount`, `scan`, `solve`,
   `is_docked`, `is_equipped`, `is_working`) — Output on success
   (fall-through), bridging continuation on failure.

4. **Action outcomes** (`build`, `build_registered`,
   `produce_registered`, `mine`, `equip_component`,
   `unequip_component`, `equip_component_remotely`,
   `unequip_component_remotely`, `set_reg_remotely`, `make_carrier`,
   `make_miner`, `make_producer`, `make_turret_bots`,
   `serve_construction`, `wait_component`) — No data output.
   Bridging continuations for status (success/failure,
   working/blocked).

5. **Conditional with output** (`select_nearest`) — Bridging
   continuations AND data output. Output available regardless of
   which path is taken. Rare (one known case).

All categories use the same call-site syntax. Categories 1, 3, 4, 5
use bare (bridging) blocks. Category 2 uses `for`-prefixed
(detaching) blocks. Mixed instructions (hypothetical or modded) can
combine both in a single call.

### Expression semantics

Branching calls work as expressions, following the same rules as
if-expressions:

- Each continuation path has a value: tail expressions for provided
  blocks, `@N` values for the `return` path, `null` for unprovided
  blocks.
- Expression arity = max across all paths. Shorter paths are
  null-filled.
- Arity depends on the call-site blocks, not the function signature
  alone.

### Continuation passthrough

Wrappers forward continuations to inner calls via
`return <continuation_name>` inside a block. The inner call's
continuation fires, then control transfers to the wrapper's
named continuation.

### Relationship to `match` / `when`

The multi-block syntax naturally resembles a match statement.
`value_type` with continuation blocks is a type switch:

```
value_type(data) {
    item { ... }
    unit { ... }
    component { ... }
}
```

A dedicated `match` expression could still be desirable as sugar
over `if`/`else if` chains for non-continuation contexts. The two
features complement each other.

### Design status

Semantics and syntax are settled. Connection type taxonomy is
complete (bridging and detaching only).

**Settled syntax:**

- **Function signatures**: `exec(...)` after param list, no
  connection type annotation
- **Instruction blocks**: `exec N:` / `next:` / `detach` modifiers,
  `detach` and `next` imply `exec`
- **`return` continuation**: exit point, `return(...)` controls
  return values, bare/absent defaults to all `@N` in order
- **`@N` data binding**: argument lists on continuation references,
  value arguments supported
- **Call-site blocks**: bare = bridging, `for`-prefixed = detaching,
  three forms (multi-block with grouping braces, single named,
  collapsed unnamed with leftmost default)
- **Data binding at call site**: Kotlin-style `{ var -> body }`
- **`break`**: transparent through bridging blocks, targets `for`
  (detaching) blocks via `last` instruction
- **Expression form**: tail values, arity follows if-expression rules
- **Continuation passthrough**: `return <name>` inside blocks
- **Iterator sugar**: `for vars in iterator() { ... }` for
  single-body iterators

**Remaining work**: implementation, iterator `for` loop
generalization (see separate section), resolution of open issues
below.

### Open issues

Issues identified during syntax design that need resolution before
or during implementation.

**1. `for` keyword overload**: `for` now has three meanings:
compiler-generated counted loops (`for i in Range(5)`), iterator
sugar (`for comp in for_component()`), and detaching block prefix
(`for coord_analysis { coord -> ... }`). The first two share
`for ... in` syntax; the third is `for name { ... }`. Syntactically
distinguishable but semantically overloaded. May be acceptable
(many languages overload `for`) but worth monitoring for confusion.

**2. Two binding mechanisms**: Iterator sugar binds in the header
(`for comp, idx in ...`), while general call-site syntax uses
Kotlin-style bindings (`for body { comp, idx -> ... }`). Both bind
`@N` data to variables but look completely different. The sugar is
a convenience over the general form — document clearly that they're
equivalent.

**3. Parser ambiguity with `for` after a call**: After `fn()`, the
parser may see `for`. This could be a detaching continuation clause
or a new `for` loop statement. Distinguishable via lookahead:
`for name {` = continuation clause, `for name in` = for loop. The
collapsed unnamed form `fn() for { ... }` is trickier — `for {`
could be a detaching clause or a `for` loop with a block
expression. May need a rule that collapsed `for` requires a name,
or that `for {` is always parsed as a continuation clause after a
call to a function with `exec` continuations.

**4. Expression semantics for `for` blocks**: A detaching block
runs N times — its "tail value" is ambiguous (last iteration?
accumulated? undefined?). Proposed resolution: **expression form
is restricted to all-bridging calls.** If any `for` block is
present, the call cannot be used as an expression — compile error.
This is clean and avoids semantic confusion.

**5. `return` inside `for` blocks and iterator cleanup**: `return`
from the enclosing function while inside a detaching body leaves
the iterator on the VM's block stack. The behavior restart likely
clears `state.blocks` (execution restarts fresh), but this needs
verification. If not, `return` inside a `for` block may need to
emit `last` before `@return` to clean up the iterator.

**6. Labeled `break` from `for` block to enclosing loop**: This
requires two operations — `last` (clean up the iterator) AND a
jump past the loop. The compiler would need to emit `last` followed
by `@break`. Verify that `last` + `@break` compiles correctly and
that the block stack is in the right state after `last`. For nested
iterators, multiple `last` instructions may be needed (one per
iterator level being crossed).

**7. Compiler enforcement of `for` correctness**: The compiler
should validate that call-site `for` blocks match `detach` bindings
in the function definition. `for` on a bridging continuation →
compile error. Bare block on a detaching continuation → compile
error. This ensures the call site always reflects the actual
semantics.

**8. Multi-value Kotlin bindings**: Continuation blocks receiving
multiple `@N` values use comma-separated bindings:
`for body { item, count, extra -> ... }`. Consistent with
multi-return prefix matching elsewhere in the language.

**9. `match` / `switch` integration**: The `switch` instruction
(5 cases + default) may benefit from dedicated `match` syntax
rather than 5+ named continuation blocks. This is complementary
to the continuation model — `match` could desugar to `switch`
with continuation blocks internally.

**10. Continuation passthrough in `for` blocks**: Whether
`return <continuation_name>` (passthrough) works inside a
detaching block is unclear. It would need to stop the iteration
AND forward to the continuation. May be unsupported initially.

## Generalized `for` loops and iterators

Iterator instructions (Category 2 — stateful, looping, with
detaching body continuations) will be handled by generalizing the
`for` loop. This builds on top of the continuation model but is
deferred to a separate implementation phase.

Variable binding uses multi-return prefix matching, so varying
output counts (1–5) are handled naturally:

```
for comp, idx in for_component() { ... }
for item, count in for_inventory_item(entity) { ... }
for i in for_number(0, 10, 1) { ... }
```

The `for` loop provides the ergonomic layer: natural nesting,
`break`/labeled `break`, and `let`-style variable binding. The
underlying mechanism is the same continuation model — `for` just
adds iteration semantics on top. The iterator's "done" continuation
is implicitly bound to `return`, so code after the `for` loop runs
when iteration completes.

**`for_number` replaces Range compilation**: The current `for i in
Range(start, stop, step)` compiles to 3–4 overhead frames (INIT,
CHECK via `check_number`, BODY, INCR via `add`). The `for_number`
VM instruction does all of this in a single frame with internal
state. Generalizing `for` to use iterator instructions makes Range
loops compile to `for_number` + body — a significant efficiency win.

### `break` in iterator loops

`break` inside an iterator-backed `for` loop compiles to the VM's
`last` instruction — NOT a `@break` placeholder jump like in
Range-based loops. The two mechanisms are completely different at the
VM level, even though they mean the same thing to the programmer.

**How `last` works**: The VM maintains a `state.blocks` stack. Each
iterator pushes a record when it starts (via `BeginBlock`), capturing
the iterator's instruction index and internal state. The `last`
instruction pops the top record, looks up the original iterator, and
calls that iterator's `last` handler. The handler does any cleanup
(some iterators clear output variables) and sets `state.counter` to
the done exec slot.

**Body re-dispatch**: When the body's last frame has `"next": false`
and the block stack is non-empty, the VM calls `BeginBlock` again,
which calls `next` to advance the iterator. If exhausted, the
iterator's `last` handler fires automatically (same as `break`).

**Always innermost**: `last` pops the top of the block stack — it
always targets the innermost iterator loop. There is no VM mechanism
to break a specific outer iterator. This means labeled `break`
targeting an outer iterator-backed loop from inside an inner
iterator-backed loop would require emitting multiple `last`
instructions (one per nesting level to pop through). Worth
investigating when implementing — may need special handling or may
simply be unsupported for iterator-to-iterator labeled breaks.

## Subroutine calls instead of inlining

Functions are currently always inlined — every call site duplicates the
function body. The behavior VM has a `call` instruction that supports
subroutines, which could allow true function calls without duplication.
This would also enable recursion. Needs investigation into how the
`call` instruction works (it's currently a not-implementable stub in
the stdlib).
