# Future Ideas

Ideas to revisit later. These are not committed designs — just things
worth thinking about when the time is right.

## Compound doc comments from nested calls

When a function call has a `#!` comment and the expanded instructions also
have their own `#!` comments, it might be useful to build compound
comments that combine both levels (e.g., `"Greeting sequence / Says
hello"`). The syntax for this hasn't been decided yet.

## Parenthesized function calls

`notify("Hello")` as equivalent to `notify "Hello"`. The parser does
not currently handle `(` after a function name.

## AST optimizations

Potential optimization passes (would need a new optimization file):

- **Constant folding**: Evaluate compile-time-computable expressions.
- **Dead code elimination**: Remove unreachable statements after
  `return`/`break`.

## Mode block expression extensions

The expression form for `locked`/`unlocked` is implemented (see
decisions.md "Mode block expressions"). These extensions are deferred:

- **Continuation after mode block**: `locked { get_number v } + 1` —
  using the mode block expression result in a larger expression.
- **Mode blocks in function call arguments**: `my_fn unlocked { get_self }` —
  requires parsing mode blocks in argument position.
- **Mode blocks in `return` statement items**: `return unlocked { get_self }` —
  requires parsing mode blocks in return item position.

## If-expression extensions

If-expressions are implemented (see decisions.md "If-expressions").
These extensions are deferred:

- **Continuation after if-expression**: `if cond { a } else { b } + 1` —
  using the if-expression result in a larger expression.
- **If-expressions in function call arguments**: `my_fn if cond { a } else { b }` —
  requires parsing if-expressions in argument position.
- **If-expressions in `return` statement items**: `return if cond { a } else { b }` —
  requires parsing if-expressions in return item position.

## Extended comparison and type check expressions

- **Comparison in function arguments**: `notify (a > 5)` — needs
  parenthesized expressions to disambiguate from `notify a, ...`.
- **Constructor RHS**: `a == Item("metalbar")` — requires parsing
  type constructors in comparison RHS position.
- **Implicit `&&`/`||` precedence**: `a > 1 && b < 5 || c > 3`
  without parentheses is a compile error. Could add implicit
  precedence (`&&` binds tighter than `||`), but parenthesized
  grouping is already supported.
- **`is Number`**: `value_type` cannot distinguish numbers from null
  (both fall through to "No Match"), so `is Number` is not available.
  Could potentially be implemented with `check_number` against itself
  (nonzero = number), but the null/0 ambiguity remains.
- **Function calls in non-first boolean position**: `d || my_fn x`
  would require interleaved frame emission for proper short-circuit
  semantics.

## Extended arithmetic expressions

- **Modulo operator**: `%` → `modulo` instruction. The instruction exists
  in the stdlib but has no operator syntax yet.

