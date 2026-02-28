# Future Ideas

Ideas to revisit later. These are not committed designs — just things
worth thinking about when the time is right.

## Compound doc comments from nested calls

When a function call has a `#!` comment and the expanded instructions also
have their own `#!` comments, it might be useful to build compound
comments that combine both levels (e.g., `"Greeting sequence / Says
hello"`). The syntax for this hasn't been decided yet.

## AST optimizations

Potential optimization passes (would need a new optimization file):

- **Constant folding**: Evaluate compile-time-computable expressions.
- **Dead code elimination**: Remove unreachable statements after
  `return`/`break`.

## Subroutine calls instead of inlining

Functions are currently always inlined — every call site duplicates the
function body. The behavior VM has a `call` instruction that supports
subroutines, which could allow true function calls without duplication.
This would also enable recursion. Needs investigation into how the
`call` instruction works (it's currently a not-implementable stub in
the stdlib).

## Language ergonomics audit

See `.claude/audit.md` for the full audit process, history, and open
items. The `-Werror` flag item below originated from the audit but
lives here as a general future feature.

- **`-Werror` style flag for promoting warnings to errors.** The
  compiler now supports warnings (returned alongside compiled output).
  A flag to treat warnings as errors would be useful for CI/strict
  mode. Needs a CLI flag (`-Werror` or similar) and plumbing through
  the `Compile`/`CompileString` API.
