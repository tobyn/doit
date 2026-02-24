# Future Ideas

Ideas to revisit later. These are not committed designs — just things
worth thinking about when the time is right.

## Compound doc comments from nested calls

When a function call has a `#!` comment and the expanded instructions also
have their own `#!` comments, it might be useful to build compound
comments that combine both levels (e.g., `"Greeting sequence / Says
hello"`). The syntax for this hasn't been decided yet.

## Range type for `for` loops

A compile-time range type could enable `for` loop syntax. The VM has no
range type — a range would be represented as two numeric registers
(start and end) at runtime, with the compiler tracking metadata like
whether the range is inclusive or half-open and generating the
appropriate `check_number` + body loop structure. Similar to how
strings are compile-time-only types baked into instructions, the
"range" concept would exist only in the compiler's type system.

## Parenthesized function calls

`notify("Hello")` as equivalent to `notify "Hello"`. The parser does
not currently handle `(` after a function name. See F13 in
`analysis-findings.md`.

## Extended comparison expressions

`>` and `<` work as expressions at behavior level, with `&&`/`||`
chaining. Natural extensions:

- **`>=`, `<=`, `==` as expressions**: Same 3-frame pattern with different
  branch mappings. `>=` maps larger+equal to true; `<=` maps
  smaller+equal to true; `==` maps equal to true (larger and smaller
  both go to false).
- **fn body comparison expressions**: Requires branching in the flat
  `fnBodyCall` list, which only supports linear sequences today.
  This also blocks fn body `&&`/`||` support.
- **Comparison in function arguments**: `notify (a > 5)` — needs
  parenthesized expressions to disambiguate from `notify a, ...`.
- **Number literal LHS**: `let x = 5 > b` — the `tokNumber` path in
  `compileVarInit` is consumed before checking for a comparison
  operator. Workaround: `let x = b < 5`.
- **Mixed `&&`/`||` precedence**: `a > 1 && b < 5 || c > 3` is
  currently a compile error. Supporting this requires either
  operator precedence (`&&` binds tighter than `||`) or parenthesized
  sub-expressions (`(a > 1 && b < 5) || c > 3`), or both.
- **Parenthesized sub-expressions**: `let x = (a > 1 || b < 2) && c > 3`
  would enable arbitrary nesting. Requires the parser to handle `(`
  in expression context.

## Known blocking issues (from audit)

These are known compiler bugs identified in `analysis-findings.md` that
will cause incorrect compilation or prevent reasonable future features.

### Nested control flow (F3, F4)

`compileBody` dispatches all statements to `compileDefaultStatement`,
which does not handle `if`, `while`, `loop`, or `break`. Control flow
cannot be nested (e.g., `while x <= 5 { if y >= 3 { ... } }` fails).
`break` only works inside `loop`, not `while`. Fix: refactor
`compileBody` to handle the same statement keywords as
`parseBehaviorBody`, or extract a shared dispatch function.

### Boolean literals (F2)

`true`/`false` are documented in `types.md` as planned syntax but are
not implemented. They parse as variable names. Fix: add them to the
`Keywords` map and handle in the parser, or defer and keep the
documentation accurate.
