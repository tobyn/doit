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

**Phase 1 implemented.** `iter` declarations, `for ... in` syntax, and
`yield` inside for-loops (wrapper iterators) are working. All stdlib
iterators converted from `fn...exec` to `iter...->`.

Remaining phases:
- **Phase 2**: Top-level `yield` (state machine compilation backed by
  `for_number`). Enables static sequences like `yield 1; yield 2; yield 3`.
- **Phase 3**: Compile `for i in Range(...)` to `for_number` internally,
  eliminating multi-frame overhead.

## Subroutine calls instead of inlining

Functions are currently always inlined — every call site duplicates the
function body. The behavior VM has a `call` instruction that supports
subroutines, which could allow true function calls without duplication.
This would also enable recursion. Needs investigation into how the
`call` instruction works (it's currently a not-implementable stub in
the stdlib).
