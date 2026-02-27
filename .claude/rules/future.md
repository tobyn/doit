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

### 3. Negative number literals in variable init/assignment

`let x = -5` and `x = -5` fail because the RHS parsers don't handle
a leading `tokMinus`. Negative literals work in function call arguments
and `Range()` constructor args (where `parseBhvArgExpr`/
`parseFnBodyExpr` handle them). Unary minus on variables (`-x`) is
also unsupported everywhere.

### 4. Nested function calls in arguments

`set_reg get_self` and `add get_number x, 5` fail — function calls
can't appear as arguments to other function calls. Developers must
use intermediate `let` bindings. Function calls DO work in boolean
expression position (`let a = get_number x > 5`), just not as direct
call arguments.

### 5. `wait` condition function-call continuation

`wait 5 { get_count > 0 }` fails because after parsing a function
call as the tail expression, the parser expects `}` without trying
comparison/boolean continuation. `wait 5 { x > 0 }` (variable) and
`wait 5 { get_count }` (bare call truthy check) both work.

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
