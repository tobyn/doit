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

**Looping continuations** (e.g., for_number's body): The block
runs, then falls off — `"next": false`. The instruction retains
control internally. It decides whether to dispatch to the body again
or fire another continuation. The body is subordinate to the
instruction, like a sub-behavior. Verified via decoded `for_number`
JSON: the loop body ends with `"next": false` and has NO explicit
back-edge to the iterator instruction. The VM handles re-dispatch
internally for iterator instructions.

These two types are exhaustive — every exec slot in every known
instruction (including modded instructions examined) is cleanly
bridging or looping. No third category was found.

### Function signatures

Functions declare their continuations after the param list with the
`exec` keyword. All continuations are listed — including the one
mapped to `"next"`. No path is privileged:

```
fn check_number(value, target) exec(larger, smaller, equal) { ... }
fn for_component(entity) exec(body, done) { ... }
fn my_branch(a) exec(next_one, next_two) { ... }
```

The signature does not distinguish bridging from looping. The
connection type is an implementation detail determined by the
`instruction` block bindings. For pure-logic functions (no
`instruction`), continuations are always bridging.

### Instruction block bindings

Inside `instruction { }`, exec slots and `"next"` are wired to
continuation names. The `exec` keyword distinguishes exec slot keys
from parameter slot keys. `next` and `for` both imply `exec`, so
the `exec` keyword is only required for bare numeric keys:

```
fn check_number(value, target) exec(larger, smaller, equal) {
    instruction "check_number" {
        2: value,
        3: target,
        exec 0: larger,       # exec required (bare numeric key)
        exec 1: smaller,
        next: equal,           # implies exec
    }
}
```

The `for` modifier marks a looping continuation. It also implies
`exec`:

```
fn for_component(entity) exec(body, done) {
    instruction "for_component" {
        0: @1,
        1: @2,
        for next: body(@1, @2),  # for implies exec
        exec 2: done,
    }
}
```

Redundant `exec` is always accepted for clarity: `exec next:` is
the same as `next:`, and `for exec 2:` is the same as `for 2:`.

### Data flow: `@N` binding to continuations

`@N` markers define output data slots on the instruction (as they do
today). Continuations receive data by listing `@N` references in an
argument list:

```
for next: body(@1, @2),        # body receives two values
exec 0: a_closer(@1),          # a_closer receives one value
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

For v1, literals are supported (numbers, null, enum values,
constructors). Full expressions deferred until needed.

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
**looping** (iterate). This distinction is always visible at the
call site — the programmer never has to look up a function
definition to know whether their code runs once or in a loop.

Unprovided continuations bridge directly to `return`. Order of
named blocks doesn't matter — matched by name.

**Two syntactic forms:**

**Multi-block** — grouping braces contain named blocks:

```
check_number(a, b) {
    larger { do_something() }
    smaller { do_other() }
    equal { do_equal() }
}

# Mixed bridging and looping:
do_deep_analysis(input) {
    unit_analysis { unit -> process(unit) }
    for coord_analysis { coord -> analyze_tile(coord) }
    area_analysis { summarize_area() }
    for number_analysis { n -> count(n) }
    sequence_analysis { summarize_sequence() }
    no_match { error() }
}

# Single continuation — just use multi-block with one entry:
get_inventory_item() {
    no_items { handle_empty() }
}
```

**Collapsed unnamed block** — name omitted, binds to the
leftmost continuation in the function's `exec(...)` list.
Connection type (bridging/looping) is inherited from the
function definition — no `for` prefix needed. Remaining
continuations are empty (bridge to return):

```
let item = get_inventory_item() { null }

check_number(a, b) { do_something() }
# equivalent to: check_number(a, b) larger { do_something() }

for_component(entity) { comp, idx -> process(comp) }
# equivalent to: for_component(entity) { for body { comp, idx -> process(comp) } }
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
- **`for` blocks** (looping) are break targets. `break`
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
# equal unprovided → null (like if-expr without else)

# Collapsed:
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
   Stateful instructions with a looping body continuation and a
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
(looping) blocks. Mixed instructions (hypothetical or modded) can
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

### Implementation status

Phases 1–3 are implemented. Phases 4–6 remain.

**Implemented:**

- **Phase 1: Parse foundations** — `exec` keyword, `fnDef.execNames`,
  `execBinding` type, exec bindings in instruction blocks, `for`
  modifier, validation.
- **Phase 2: Call-site parsing + bridging codegen** — Continuation
  blocks at call sites (multi-block and collapsed forms),
  `expandContinuationBlocks`, bridge jumps, exec slot patching,
  `@return` patching.
- **Phase 3: Data flow** — `@N` args in exec bindings, `->` arrow
  token, scanner save/restore, Kotlin-style param bindings at call
  sites, `allocExecOutputRegs`, `findMaxExecOutputSlot`,
  `findMaxReturnSlot`, synthetic `@retN` names for exec functions
  without explicit `return`.

**Remaining:**

- **Phase 4: Looping continuations** — `for` blocks with
  `"next": false`, `break` → `last` instruction, `for`-block break
  targets.
- **Phase 5: Pure-logic branching + expression form** — `return
  <cont_name>`, expression-level branching calls, assignment form
  (`let x = fn() { ... }`).
- **Phase 6: Stdlib updates** — Replace ~70 control-flow stubs with
  real exec implementations, manual + memory updates.

### Resolved issues

Issues identified during syntax design, now resolved.

**1. `for` keyword overload**: Accepted. `for` has multiple
meanings (counted loops, looping block prefix, future iterator
sugar) but all are syntactically distinguishable. `in` separates
loop forms from block prefix. Monitor for confusion.

**2. Two binding mechanisms**: Deferred. Iterator sugar is a
separate implementation phase. Only Kotlin-style bindings
(`for body { comp, idx -> ... }`) are in scope now.

**3. Parser ambiguity with `for` after a call**: Resolved. After
`fn()`, only `{` follows (multi-block or collapsed). `for` prefix
only appears inside multi-block braces, where it's unambiguous.
Collapsed form inherits connection type from the function
definition — no `for` prefix needed or allowed.

**4. Expression semantics for `for` blocks**: Resolved. Expression
form restricted to all-bridging calls. Looping blocks don't have
expression values — compile error if any `for` block is present.

**5. `return` inside blocks**: Resolved. `return` is a compile
error inside any continuation block (bridging or looping). Blocks
are not functions.

**6. Labeled `break` across block boundaries**: Resolved. Allowed.
The compiler emits a direct jump (`"next"` past the target loop).
Stale block stack entries are harmless — cleared on behavior
restart.

**7. Compiler enforcement of `for` correctness**: Resolved. In
multi-block form: `for` on bridging → error, bare on looping →
error. Collapsed form inherits from leftmost — no annotation.

**8. Multi-value Kotlin bindings**: Resolved. Comma-separated
bindings (`for body { item, count -> ... }`), consistent with
existing prefix matching.

**9. `match` / `switch` integration**: Deferred. Works fine with
named continuation blocks. Dedicated syntax is a future ergonomic
layer.

**10. Continuation passthrough in blocks**: Resolved. `return` of
any kind inside blocks is a compile error (see #5). Wrapping a
branching function with forwarded continuations requires dropping
to the raw `instruction` block and wiring exec slots directly.

**11. Unbound numbered exec slots**: Resolved. Numbered exec slots
not explicitly bound in the instruction block default to `return`,
matching the `"next"` default behavior.

**12. Value arguments in continuation argument lists**: Resolved.
Literals only for v1 (numbers, null, enum values, constructors).
Full expressions deferred until a real use case demands them.

**13. Existing boolean/comparison compilation**: Resolved. The
hard-coded `check_number`/`compare_register`/`value_type` emission
paths for `if`/`while`/boolean expressions remain unchanged. The
continuation system adds a new calling convention alongside.

## Generalized `for` loops and iterators

Iterator instructions (Category 2 — stateful, with looping body
continuations) will be handled by generalizing the
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
