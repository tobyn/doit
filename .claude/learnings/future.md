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
