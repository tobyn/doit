# Design Decisions

Non-obvious choices and their rationale. Helps future sessions make
consistent decisions without re-deriving past conclusions.

For implementation details (frame layouts, internal function names,
emission patterns), read the source code directly.

## Behaviors as top-level functions

Behavior blocks and function bodies support almost the exact same
language constructs — this is a core design principle. A behavior is
conceptually a top-level function with extra syntax sugar (parameters
as external registers, behavior-level `default` case dispatch).

**Compiler architecture**: Parsing produces `[]Stmt` with `Expr` nodes
(in `ast.go`), then emitters walk the AST and emit frames via
`frameBuilder`. Expression parsers are shared between behavior bodies
and fn bodies via the `operandResolver` callback. Both paths share
low-level frame emitters in `codegen.go`. Fn bodies use inline
forward-jump patching for control flow (simpler than behavior-level
deferred bodies with `rebaseFrameRefs`).

## Stdlib function signatures

- **Output parameters as return values**: Functions with output slots
  use `return instruction` with `@1` markers. Callers use
  `let x = get_self` instead of `get_self x`.
- **Optional inputs as keyword params**: Avoids forcing callers to pass
  placeholders for unused optional slots.
- **`"c"` mode fields omitted**: No way to express enum-style mode
  selection yet.
- **`"txt"` fields as positional strings**: Natural call-site syntax
  (`notify "Hello"`).

## Boolean literals

`true` → `map[string]any{"num": 1}`, `false` → `false` (Go bool).
Matches comparison emitters (`emitComparison` sets true = `{"num": 1}`)
and VM convention (truthy checks use `compare_register value, false`).
Both are reserved keywords. `false` and `null` compile identically
(empty register). Handled in `parseArithPrimary`, `parseBoolPrimary`,
`parseBhvArgExpr`, `parseFnBodyExpr`, and expression tail sections.
Label lookahead exclusions prevent `true:` and `false:` from being
parsed as loop labels.

## Multiple return values

`@N` markers in `instruction` blocks are positional return values.
`return` passes them through as the wrapping function's returns.

- **Contiguous sequence required**: `@N` values must form a contiguous
  sequence starting from `@1`. Gaps are compile errors.
- **Prefix matching**: Callers can bind fewer returns than produced.
  Extra values receive `false`.
- **`_` as discard**: `let _, y = fn args` skips the first output.
  Maps to `false` in compiled output.
- **Mixed modifier binding lists** (behavior level only):
  `var a, b, _, let c, var d = fn args`. Modifiers are sticky. Bare
  idents inherit the active modifier. Must start with `_`, `let`, or
  `var`.
- **Multi-value `return`**: `return x, y, z` with comma-separated
  identifiers, number literals, or `null`. Literals are desugared into
  synthetic body calls with `@retK` names.

## Type literal constructors and `&`

- **Constructor syntax**: Capitalized names (`Item("metalbar")`) to
  distinguish from function calls and reserve lowercase namespace.
- **Namespace prefixes hidden**: `Component`, `Technology`, `Value`
  auto-prepend `c_`, `t_`, `v_`.
- **`&` operator**: Attaches numeric component to typed values. Visually
  distinct from arithmetic, mirrors the VM's register composite model.
- **Compile-time vs runtime**: All-literal operands produce inline JSON.
  Variable operands emit runtime instructions. Decision is based on AST
  node types, not resolved values after parameter substitution.
- **Constructor target optimization**: `let`/`var` declarations pass
  the declared variable directly as output target, avoiding extra
  `set_reg` copy.

## Instruction metadata limitations

The compiler cannot assume all instructions are defined in the stdlib
(Desynced supports user mods). The `instruction` intrinsic exists partly
for modded instructions. Direction enforcement in fn body `instruction`
blocks uses the `@N` convention instead of per-instruction metadata.

## Return/parameter name collision handling

When `return` references a parameter name, `expandCall` detects the
collision and preserves the parameter mapping. After body calls are
expanded, a `set_reg` frame copies the parameter value to the caller's
return target.

## Comparison expression operators

`>`, `<`, `>=`, `<=`, `==`, `!=` produce a boolean value (1 for true,
`false` for false). They compile to a 3-frame check + false-set +
true-set pattern.

- **Numeric comparisons** (`>`, `<`, `>=`, `<=`) use `check_number`
  (3-way branch: Larger/Smaller/Equal).
- **Equality comparisons** (`==`, `!=`) use `compare_register` (2-way:
  Different/Equal) for full register composite equality, not just
  numeric.
- **`null` as RHS**: `a == null` and `a != null` supported. `null`
  resolves to `false` (empty register).
- **Supported**: `let`/`var` init and assignment RHS. Number literal
  LHS. Arithmetic sub-expressions on both sides.
- **Deferred**: comparison in function call arguments; constructor RHS
  (`a == Item("metalbar")`).

## Type check operator (`is`)

`is` checks whether a value matches a game data type. Compiles to a
3-frame `value_type` + `set_reg` + `set_reg` pattern.

Syntax: `let a = b is Unit`. RHS is one of: `Item`, `Unit`,
`Component`, `Technology`, `Value`, `Coordinate`. `Number` is not
supported (`value_type` cannot distinguish numbers from null).

`tokIs` exists only as an internal marker in `comparisonTerm.op` — `is`
scans as `tokIdent` with val `"is"`.

**Deferred**: `is` in function arguments.

## Logical operators (`&&` and `||`)

Chain multiple boolean sub-expressions. Each sub-expression can be a
comparison, type check, bare variable (truthy check), or number literal.
`&&` binds tighter than `||` (standard precedence).
`a && b || c` parses as `(a && b) || c`. Parentheses can override.

- `&&`: each term must pass to continue; any failure short-circuits to
  false.
- `||`: any term passing short-circuits to true; all failures fall
  through to false.

Truthy checks use `compare_register value, false` — Different
(non-empty) = truthy, Equal (empty) = falsy. The internal sentinel
`tokTruthy` identifies these terms.

Single comparison without `&&`/`||` delegates to the existing 3-frame
emitters for backward-compatible output.

**Deferred**: logical expressions in function call arguments.

## Structured locking with `locked`/`unlocked` blocks

Lexically scoped mode blocks that set execution mode on entry and
restore on exit. Static mode tracking via `frameBuilder.mode` (initially
`modeLocked` for behaviors). No `modeUnknown` — mode is always
statically known. No-op elimination when already in target mode.
Cross-function tracking flows through inlined function bodies via the
caller's `frameBuilder`. The old `optimize.go` was deleted — mode
transitions are emitted on-the-fly.

## Control flow stubs

Control-flow instructions are left as empty-body stubs with a
`# control flow:` comment. They require compiler-level support that
`instruction` can't express.

## Test file conventions

Test `.json` files use graph isomorphism (`matchBehaviors`) for
comparison — frame order doesn't matter. Files use the reference JS
codec's 0-based key format; `refToNative` bridges to our 1-based
native format.

## Call-site direction annotations

`out` and `inout` arguments must be annotated at the call site to match
the parameter's direction. `in` is the default (no annotation needed,
but explicit `in` is accepted). Mismatched annotations are compile
errors. `in`, `out`, `inout` are fully reserved keywords. Enforcement
is uniform across behavior level and fn bodies via `checkCallAnnotation`.

## Arithmetic expression operators (+, -, *, /, %)

Each maps to one stdlib opcode (`add`, `sub`, `mul`, `div`, `modulo`).
Emitted directly as a single instruction frame. LHS preserves typed
value (the game's semantics). Number literal LHS supported.

**PEMDAS**: Chained arithmetic respects operator precedence via nested
`ArithExpr` AST nodes. `*`, `/`, `%` are high-priority; `+`, `-` are
low-priority. Intermediate results use `@arith`-prefixed temp variables.
The outermost operation writes directly to target.

## Compound assignment and increment/decrement

`+=`, `-=`, `*=`, `/=`, `%=` compile to a single instruction frame where
target appears in both input and output. RHS accepts both number
literals and variables.

`++`/`--` are sugar for `+= 1`/`-= 1`.

## Mutable variables (`var`) in fn bodies

`let` = immutable, `var` = mutable. `fnBodyContext.fnVars` map tracks
`name → true` (mutable) or `name → false` (immutable). Assignment
validation at parse time via `canAssign`/`canCompound`.

## Fn body control flow

`if`/`else if`/`else`, `while`, `loop`/`break` work in fn bodies with
the same syntax as behavior level. Emission uses inline forward-jump
patching. `break` emits `{"op": "@break"}` placeholder. `return` emits
values to `@retK` targets then `{"op": "@return"}` placeholder.
`expandCall` patches `@return` frames to jump past the function
expansion.

## `break` in `while` loops

Works in both `loop` and `while` at behavior level and fn bodies. At
behavior level, `emitBehaviorStmts` detects the if/break pattern
(single `BreakStmt` body, no else) and routes through `emitBhvIfBreak`
instead of `emitBhvIfStmt` to avoid deferred body issues inside loops.

## Labeled loops and breaks

Syntax: `label: loop { ... }`, `label: while cond { ... }`,
`label: for i in Range(5) { ... }`, `break label`.

Parser uses `parser.loopDepth` (int) and `parser.loopLabels` (map)
instead of threading `inLoop bool` through functions. Label detection
via three-token lookahead (`ident: loop/while/for`). Duplicate labels
are compile errors.

`@break` placeholder includes optional `"label"` field. Patching
condition: `fLabel == "" || fLabel == myLabel` — unlabeled breaks are
claimed by innermost loop, labeled breaks by the matching loop. All
10 loop/while/for emitters use this pattern.

## Counted loops (`loop N { ... }`)

Optional count expression: `loop 5 { ... }`, `loop n { ... }`,
`loop (a + b) { ... }`. When nil, infinite loop. Frame layout:
INIT (counter=0) → CHECK (counter vs limit) → BODY → INCR (counter+1)
→ back to CHECK. Counter uses `@loop` via `allocUniqueVar`. `break`
patches to EXIT.

## Range constructor and `for` loops

Range uses coordinate+number composite:
`{"coord": {"x": start, "y": stop}, "num": step}`. `Range(stop)`
defaults start=0, step=1. Only constructor with 1-3 args. Literal
step=0 is compile error. No `&` on Range (would overwrite step). No
`is Range` (uses Coordinate at VM level).

`for i in <range_expr> { body }`. Iteration variable is immutable.
Supports labels and `break`.

**Three emission paths** based on step sign knowledge:
- **A/B** (literal step, sign known): simple check + body + incr loop.
- **C** (variable range): extracts parts, checks step sign at runtime,
  direction-aware comparison.

## Enhanced `return` in fn bodies

Three paths based on post-parse analysis:
1. **Return-instruction**: Single `return instruction` at end. Supports
   `fnDef.frame` promotion.
2. **Zero-copy**: Single `return` at end with all `IdentExpr` values.
3. **Emit-and-jump**: Everything else. Uses `@retK` targets and
   `@return` placeholder. Max-arity rule: return count = max arity
   across all `ReturnStmt` nodes; shorter branches zero remaining slots.

## Parenthesized boolean expressions

Recursive tree model: `BoolChainExpr` has `Op` (`&&`/`||`) and
`Children`. Parentheses create nested nodes for mixed `&&`/`||`.
Two-phase: parse AST → resolve operands → emit. Single-leaf delegates
to existing 3-frame emitters for backward compatibility.

## Expression priority hierarchy

Highest to lowest: arithmetic > comparisons > function calls > boolean
operators. So `my_fn b + 1, c || d` parses as `(my_fn(b+1, c)) || d`.

Function calls work as first boolean term only (`my_fn x || d`).
Non-first position (`d || my_fn x`) is deferred.

## Multi-arity expression lists

`let a, b, c = 1, 2, 3` — comma-separated expression list. Each
expression contributes its arity. Sum must equal binding count, with
prefix matching allowed on last function call.

When RHS is a single item, uses `MultiReturnStmt`. Multiple items
use `ExprListExpr`. Variables registered after RHS parsing completes,
except when RHS contains a function call (registered before call args).

## Mode block expressions

`locked { ... }` / `unlocked { ... }` as expressions. Last item is
the value-producing tail. `ModeBlockExpr` has `Body []Stmt` and
`Tail Expr`. Arity from tail: `CallExpr` → `returnCount()`, else 1.
Mode transitions use `emitModeEntry`/`emitModeExit` helpers shared
with `ModeBlockStmt`.

**Deferred**: continuation after mode block, mode blocks in function
arguments, mode blocks in `return` items.

## If-expressions

`if`/`else if`/`else` as expressions. Each branch has optional
statements + tail expression. `else` is optional — when absent,
uncovered branches produce `null`. `IfExpr` has recursive structure
with `ElseIfExprClause` list; `ElsBody`/`ElsTail` are nil when no
else clause is present.

Arity = max across all branches (via `exprArity` helper); nil `ElsTail`
contributes arity 1 (the implicit null). Mixed-arity branches zero
remaining slots. Conditions use the full boolean expression parser.
At behavior level, uses child `frameBuilder` per branch (same as
`emitBhvIfStmt`). When `ElsTail` is nil, emitters emit
`set_reg false, target` (single) or `set_reg false, retVals[i]` for
each slot (multi) instead of emitting the else body/tail.

**Deferred**: if-expressions in function arguments, in `return` items,
continuation after if-expression.

## Wait keyword

`wait` is a language keyword (not a stdlib function). Replaced the old
`wait(time)` stdlib function. Syntax:

- **Simple**: `wait <ticks>` — pauses execution for the given number of
  ticks.
- **Conditional**: `wait <ticks> { condition }` — waits, then evaluates
  the condition. Repeats until the condition is truthy.
- **Body + condition**: `wait <ticks> { stmts...; condition }` — the
  last item in the block must be a value-producing expression (the
  condition). Preceding statements execute each iteration.

**Ticks snapshot**: The ticks expression is evaluated once and stored in
a `@wait` temp variable (via `allocUniqueVar`). Pure number literals
skip the snapshot (no temp needed). This prevents re-evaluation when
the source variable changes during iteration.

**Not a loop**: `wait` does not support `break` or labels. The condition
block is a specialized construct, not a general loop body.

**Frame layout** (conditional): WAIT → body → condition to `@wcond` →
`compare_register @wcond, false` (Different → after wait, `"next"` →
back to WAIT).

**AST**: `WaitStmt` with `Ticks Expr`, `Body []Stmt`, `Tail Expr`,
`Comment string`. Both Body and Tail are nil for simple wait.

**Expression tails in fn body wait blocks**: The fn body parser's
`exprTail` mode handles boolean expression continuations (comparisons,
`is`, `&&`/`||`) after arithmetic expressions, matching the
behavior-level `maybeBhvExprContinuation` pattern.
