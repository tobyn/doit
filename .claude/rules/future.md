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

## AST unification plan (Phases 1-3 complete, Phase 4 pending)

Both fn bodies and behavior bodies use a two-phase AST approach with
shared expression parsers (parameterized via `operandResolver`). Phase
3 enabled fn body parity: arithmetic, comparisons, boolean chains,
type checks, `var` declarations, assignment, compound assignment,
increment/decrement, and control flow (`if`/`else if`/`else`, `while`,
`loop`/`break`). It also extended behavior-level inner block parsing
(`parseBhvStmtBlock`) to handle the full statement set.

### Phase 4: AST-level optimizations

**Goal**: Optimization passes between parsing and emission.

**Steps**:

1. **Lock/unlock elimination on AST**: Walk `[]Stmt`, track `execMode`,
   remove redundant `LockStmt` nodes before emission. Replaces the current
   post-emission frame-scanning approach. Can do cross-branch analysis
   (both branches end in `lock` → mode is `locked` after `if`).

2. **Future**: Constant folding, dead code elimination after
   `return`/`break`, etc.

**Files**: New `optimize.go` (or in `ast.go`)

**Tests**: Existing `lock_unlock_optimize` tests pass. New optimization
tests.

**Risk**: Low. Optimizations are additive.

### Verification

After each phase:
```sh
cd toolchain && go build ./... && go test ./...
```

All existing test cases must pass. Graph isomorphism comparison
(`matchBehaviors`) tolerates frame reordering, so emission order
changes don't break tests.

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

### Boolean literals (F2)

`true`/`false` are documented in `types.md` as planned syntax but are
not implemented. They parse as variable names. Fix: add them to the
`Keywords` map and handle in the parser, or defer and keep the
documentation accurate.
