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

## Expression composability work items

Prioritized list of language limitations that will surprise developers.
Work these top-to-bottom.

### ~~1. Behavior-level `if`/`while` full expression support~~ (Done)

Behavior-level `if`, `else if`, `while`, and `if/break` now accept the
full boolean expression language: variable RHS, `&&`/`||` chains, `is`
type checks, truthy checks, arithmetic sub-expressions, and function
calls in conditions. The old deferred body pattern was replaced with
forward-jump patching (same pattern as fn bodies).

### ~~2. Negation operator (`!`)~~ (Done)

`!expr` prefix operator negates any boolean expression. Uses the
swap-targets approach in emission — `emitBoolCheckFrame` swaps
`trueTarget`/`falseTarget` when `comparisonTerm.negated` is set.
For chains, `negateResolved` applies De Morgan's law to push negation
to leaves. Works in `let`/`var` init, assignment, `if`/`while`
conditions, fn bodies, and parenthesized call arguments.

### ~~3. Negative number literals and unary minus~~ (Done)

Unary minus (`-expr`) works everywhere: `let`/`var` init, assignment,
function call arguments, comparison operands, compound assignment RHS,
and fn bodies. For number literals, compile-time fold (`-5` →
`LiteralExpr{-5}`). For variables/expressions, desugar to `0 - expr`
(`ArithExpr{sub, 0, x}`). Implemented in `parseArithPrimary` as the
single source of truth. Call sites simplified to delegate through it.

### ~~4. Nested function calls in arguments~~ (Done)

Function calls with return values work as arguments to other function
calls: `set_reg get_self`, `add x, get_resource_num y`. Implemented
by adding function call detection to `parseArithPrimary` (the
lowest-level shared expression parser) and `parseFnBodyArgExpr`.
Argument boundaries are always unambiguous because function parameter
counts are fixed. Deep nesting (`set_reg get_type get_self`) and
arithmetic continuation within inner arguments work naturally.

### ~~5. `wait` condition function-call continuation~~ (Done)

Function call results in `wait` condition blocks now support
comparison/boolean continuation: `wait 5 { get_count > 0 }`. Fixed by
merging the function-call and variable branches in `exprTail` mode
(both `parseBhvStmtBlockInner` and `parseFnBodyStmtsInner`) so both
go through `parseArithExprFromFull` + `maybeExprContinuation`. The fn
body variable path also replaced manual continuation with
`maybeExprContinuation` for consistency.

### 6. Compound assignment RHS limitations

`x += get_number y` and `x += (a > 5)` fail. Compound assignment
RHS only accepts arithmetic expressions. Function calls, comparisons,
and boolean expressions are not supported.

## Documentation improvements

Items that can't be fixed by the compiler but should be documented
more prominently to avoid confusion:

- **`false == null` at runtime**: Both compile to empty register.
  No way to distinguish "explicitly false" from "no value."
- **Comparison semantics split**: `>`, `<`, `>=`, `<=` compare only
  numeric component; `==`, `!=` compare full register composite.
- **Functions are always inlined**: No recursion. Every call site
  duplicates the function body. Not explicitly stated in docs.
- **Mixed modifier binding lists**: `var a, let b, _ = ...` works
  at behavior level only, not in fn bodies.
- **~70 control-flow instructions inaccessible**: `instruction`
  intrinsic can only emit single frames, cannot express branching.
