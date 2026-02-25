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

## Fn body expression parity (Phase 1 complete, Phase 2-3 pending)

Behavior blocks support arithmetic, comparisons, boolean chains (`&&`/`||`),
`is`, truthy checks, and parenthesized grouping. None of these work in fn
bodies yet, but the architectural blocker has been removed: fn bodies now
use an AST IR (`[]Stmt` with `Expr` nodes in `ast.go`) instead of the old
flat `fnBodyCall` list. The AST already defines all the expression node
types needed (`ArithExpr`, `CompareExpr`, `BoolChainExpr`, `TypeCheckExpr`,
`TruthyExpr`, `IfStmt`, `WhileStmt`, `LoopStmt`, `BreakStmt`).

Remaining work:
- **Phase 2**: Parse behavior-level statements into the same AST types
  (refactor `codegen.go` to separate parsing from emission)
- **Phase 3**: Unify parsers — both fn bodies and behavior bodies call
  the same `parseStmtBlock`, removing artificial restrictions. Fn bodies
  gain expressions and control flow.
- **Phase 4**: AST-level optimizations (lock/unlock elimination on AST,
  constant folding, dead code elimination)

See the plan file for detailed steps.

## Extended comparison and type check expressions

Natural extensions beyond fn body parity:

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

Natural extensions beyond fn body parity:

- **Modulo operator**: `%` → `modulo` instruction. The instruction exists
  in the stdlib but has no operator syntax yet.

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
