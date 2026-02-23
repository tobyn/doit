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

### Missing `frameHasReturnSlot` check on assignment (F5)

`x = instruction "op" { ... }` does not check whether the instruction
has any `@N` return slots. If it doesn't, the assignment silently has
no effect. The `let x = instruction` and multi-return paths both have
this check. Fix: add the check to the assignment path in
`compileDefaultStatement`.

### Return/parameter name collision in `expandCall` (F10)

If a `return` statement references a parameter name (e.g.,
`fn example(x) { ... return x }`), the return mapping in `expandCall`
overwrites the parameter mapping. Body calls that read the parameter
would instead read the caller's return target. Fix: detect the
collision and either error or handle it deliberately (the "return the
input" semantics are useful but need intentional implementation).

### Boolean literals (F2)

`true`/`false` are documented in `types.md` as planned syntax but are
not implemented. They parse as variable names. Fix: add them to the
`Keywords` map and handle in the parser, or defer and keep the
documentation accurate.
