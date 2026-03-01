# Design Decisions

Non-obvious choices and their rationale. Helps future sessions make
consistent decisions without re-deriving past conclusions.

For implementation details (frame layouts, internal function names,
emission patterns), read the source code directly.

## Compiler-generated `@`-prefixed variable names

The compiler generates temporary variables with `@`-prefixed names
(`@arith1`, `@call`, `@loop`, `@wait`, `@wcond`, `@bool`, `@ctor`,
`@amp`, `@mode`, `@if`, `@step`, `@start`, `@stop`, `@retK`) via the
`allocUniqueVar()` function. These are **ordinary VM variable names** —
the `@` prefix has no special meaning to the game engine. It is purely
a namespace convention to avoid collisions with user-defined variables,
since `@` is not valid in doit source identifiers.

These names survive into the final compiled JSON and are visible in the
game's behavior editor as internal registers. The game has been verified
to accept `@` in variable names without issues.

Separately, `@break` and `@return` are **control-flow placeholder
opcodes** (not variable names). They appear as `{"op": "@break"}` and
`{"op": "@return"}` in intermediate frames and are patched to real jump
targets before finalization — they never reach the final output.

## Behaviors as top-level functions

Behavior blocks and function bodies support almost the exact same
language constructs — this is a core design principle. A behavior is
conceptually a top-level function with extra syntax sugar (parameters
as external registers, behavior-level `default` case dispatch).

**Compiler architecture**: Parsing produces `[]Stmt` with `Expr` nodes
(in `ast.go`), then emitters walk the AST and emit frames via
`frameBuilder`. Expression parsers are shared between behavior bodies
and fn bodies via the `operandResolver` callback. Both paths share
low-level frame emitters in `codegen.go`. Both behavior-level and fn
body control flow use inline forward-jump patching.

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
- **Mixed modifier binding lists**: `var a, b, _, let c, var d = fn args`.
  Modifiers are sticky. Bare idents inherit the active modifier. Must
  start with `_`, `let`, or `var`. Works at both behavior level and in
  fn bodies.
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
  LHS. Arithmetic sub-expressions on both sides. Constructor RHS
  (`a == Item("metalbar")`) via `parseArithConstructor` in
  `parseArithPrimary`. Parenthesized comparisons in function call
  arguments (`set_reg (a > 5)`) via `tokLParen` handling in
  `parseBhvArgExpr` and `parseFnBodyArgExpr`.

## Type check operator (`is`)

`is` checks whether a value matches a game data type. Compiles to a
3-frame `value_type` + `set_reg` + `set_reg` pattern.

Syntax: `let a = b is Unit`. RHS is one of: `Item`, `Unit`,
`Component`, `Technology`, `Value`, `Coordinate`. `Number` is not
supported (`value_type` cannot distinguish numbers from null).

`tokIs` exists only as an internal marker in `comparisonTerm.op` — `is`
scans as `tokIdent` with val `"is"`.

Type checks work in function arguments via parenthesized expressions:
`set_reg (x is Unit)`.

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

Logical expressions work in function arguments via parenthesized
expressions: `set_reg (a > 5 && b < 10)`. Parenthesized args are
handled by `tokLParen` cases in `parseBhvArgExpr` and
`parseFnBodyArgExpr`, plus `maybeExprContinuation` in paren-mode
`parseBhvCallArgs` and `parseFnBodyCallArgs`.

## Negation operator (`!`)

`!expr` negates any boolean sub-expression. Implementation uses the
swap-targets approach: the boolean emission system already routes
through `trueTarget`/`falseTarget` parameters, so negation just swaps
them. No new opcodes or frame types.

**Parsing**: `tokBang` token in scanner. `parseBoolPrimary` checks for
`tokBang` at the top and recursively calls itself, wrapping the result
in `NotExpr`. This naturally handles `!!x`, `!a > 5`, `!(a && b)`,
`!x is Unit`, `!x` (negated truthy).

**Resolution**: `negateResolved` pushes negation to leaves via
De Morgan's law. For leaves, toggles `comparisonTerm.negated`. For
groups, swaps `chainOp` (`&&`↔`||`) and recurses. Both
`resolveBhvBoolTree` and `resolveFnBoolTree` handle `*NotExpr` by
resolving the inner expression then calling `negateResolved`.

**Emission**: `emitBoolCheckFrame` swaps `trueTarget`/`falseTarget`
when `term.negated` is true. The single-leaf backward-compat path in
`emitBhvBoolExprTo`/`emitFnBoolExprTo` is guarded by
`!resolved.term.negated` so negated single leaves fall through to the
chain path which handles negation.

**RHS parsing**: `parseBhvVarInit` and `parseBhvDefaultStmt` (assignment
path) check for `tokBang` and route through `parseBoolExpr`. Fn body
RHS (`parseFnBodyRHSExpr`) already falls through to `parseBoolExpr`
for non-identifier tokens.

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

## Unary minus (`-expr`)

Unary minus negates a number or variable. Handled in
`parseArithPrimary` as the single source of truth — all expression
contexts (let/var init, assignment, call args, comparison operands,
compound assignment RHS, fn bodies) benefit automatically.

- **Number literals**: compile-time fold. `-5` → `LiteralExpr{-5}`.
  No instruction emitted.
- **Variables/expressions**: desugar to `0 - expr`. `-x` →
  `ArithExpr{tokMinus, LiteralExpr{0}, IdentExpr{x}}`. Emits a single
  `sub` instruction at runtime.
- **Double negation**: `--x` folds or chains correctly via recursive
  `parseArithPrimary` calls.
- **`parseBoolPrimary`**: `tokNumber` and `tokMinus` both unget and
  delegate to `parseArithExpr`, eliminating duplicated number handling.
- **`parseBhvVarInit`/`parseBhvDefaultStmt`**: `tokMinus` shares the
  `tokBang` path through `parseBoolExpr`, with `TruthyExpr` unwrapping
  for plain arithmetic results.
- **`parseBhvArgExpr`**: `tokMinus` and `tokNumber` merged into one
  case that ungets and delegates to `parseArithExpr`.
- **`parseFnBodyExpr`**: `tokMinus` extended to accept `-variable`
  (produces `ArithExpr{0-x}`), not just `-number`.

## Compound assignment and increment/decrement

`+=`, `-=`, `*=`, `/=`, `%=` compile to a single instruction frame where
target appears in both input and output. RHS accepts the full expression
language: arithmetic, function calls, comparisons, type checks, boolean
chains, and negation. The RHS is parsed via `parseBoolExpr` with
`TruthyExpr` unwrapping (same pattern as `parseFnBodyRHSExpr`).

`++`/`--` are sugar for `+= 1`/`-= 1`.

## Mutable variables (`var`) in fn bodies

`let` = immutable, `var` = mutable. `fnBodyContext.fnVarInfo` map tracks
each variable's mutability, scope depth, and used status. Assignment
validation at parse time via `canAssign`/`canCompound`.

## Control flow emission (unified)

Both behavior-level and fn body `if`/`else if`/`else`, `while`,
`loop`/`break`, `for`, `wait` share a single set of emitters in
`codegen.go`, parameterized by `*emitContext`. The `emitContext` struct
has function fields for `resolveBool`, `emitBody`, `exprGetValue`,
`exprTo`, `expandCallExpr`, `pushScope`/`popScope`, and
`declareIterVar`. Two constructors — `bhvEmitCtx` (in `bhvast.go`) and
`fnEmitCtx` (in `parse.go`) — build the context with closures that
capture the relevant resolution state. All 10 control flow emitters
(`emitIfStmt`, `emitIfExpr`, `emitWhileStmt`, `emitLoopStmt`,
`emitCountedLoop`, `emitForStmt`/`emitForStmtRange`/
`emitForStmtRuntime`, `emitWaitStmt`, `emitModeBlockExpr`) are methods
on `*parser` taking `*emitContext`.

Emission is direct into the parent `frameBuilder` — no child builders
or `rebaseFrameRefs`. Condition check frames use `frameRef(0)` as a
false-branch placeholder, patched after body emission.
`stripFallThrough` removes redundant branch slots that point to the
natural next frame. `break` emits `{"op": "@break"}` placeholder. In
fn bodies, `return` emits values to `@retK` targets then
`{"op": "@return"}` placeholder; `expandCall` patches these to jump
past the function expansion.

Loop back-edges (counted loop INCR, infinite loop jump-back) check
`!hasNext` on the last body frame before setting `"next"`, preserving
inner control flow back-edges. If-expression and wait-statement bodies
rely on natural frame fall-through to the tail expression — no
explicit `"next"` is set on the last body frame.

Behavior-level `if`/`while` conditions use `parseBoolPrimary` +
`parseBoolChain` (same parsers as fn bodies and `let`/`var`
expressions), accepting the full boolean expression language:
comparisons with variable RHS, `&&`/`||` chains, `is` type checks,
truthy checks, arithmetic sub-expressions, and function calls.

## `break` in `while` loops

Works in both `loop` and `while` at behavior level and fn bodies. At
behavior level, `emitBehaviorStmts` detects the if/break pattern
(single `BreakStmt` body, no else) and routes through `emitBhvIfBreak`
instead of `emitBhvIfStmt` for optimized break emission.

**Back-edge for if/break as last statement**: When `if ... { break }` is
the last statement in a loop body, all four affected loop emitters
(`emitBhvLoopStmt`, `emitBhvWhileStmt`, `emitFnLoopStmt`,
`emitFnWhileStmt`) detect that the last body frame is `@break` and emit
a noop back-edge frame after it. This is necessary because `emitBhvIfBreak`
sets the false branch (condition not met → continue loop) to
`checkStart + checkCount + 1` — the frame after the @break placeholder.
Without the back-edge noop, that position would equal `afterLoop`, causing
the loop to exit after one iteration regardless of the condition. Counted
loops and for-loops are not affected because the false branch reaches the
INCR frame, which naturally loops back to CHECK.

## Labeled loops and breaks

Syntax: `label: loop { ... }`, `label: while cond { ... }`,
`label: for i in Range(5) { ... }`, `break label`.

Parser uses `parser.loopDepth` (int) and `parser.loopLabels` (map)
instead of threading `inLoop bool` through functions. Label detection
via three-token lookahead (`ident: loop/while/for`). Duplicate labels
are compile errors. `break` followed by an identifier not in
`loopLabels` is also a compile error ("unknown loop label") — it does
not silently fall through to unlabeled break.

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

**Return item expressions**: `parseFnBodyReturnItem` supports the full
expression language via `parseBoolExpr` as the fallback parser. This
enables `return a + b`, `return a > 5`, `return !flag`,
`return a > 0 && b < 100`, `return x is Unit`, constructors with `&`,
and parenthesized expressions. Mode block expressions and if-expressions
are handled as special cases before the `parseBoolExpr` fallback (since
`locked`/`unlocked` and `if` are not recognized by the shared expression
parser). Function calls are handled by `callExprParser` within
`parseArithPrimary`.

## Parenthesized boolean expressions

Recursive tree model: `BoolChainExpr` has `Op` (`&&`/`||`) and
`Children`. Parentheses create nested nodes for mixed `&&`/`||`.
Two-phase: parse AST → resolve operands → emit. Single-leaf delegates
to existing 3-frame emitters for backward compatibility.

## Expression priority hierarchy

Highest to lowest: arithmetic > comparisons > function calls > negation
(`!`) > boolean operators (`&&`/`||`). So `my_fn b + 1, c || d` parses
as `(my_fn(b+1, c)) || d`. `!a > 5` parses as `!(a > 5)`.

Function calls work in any boolean term position. First position uses
the existing call-as-expression path. Non-first position uses the
`callExprParser` callback on the `parser` struct, set contextually by
`parseBehaviorBody` and `parseUserFn`. In `parseBoolPrimary`, when
an identifier matches a function with a return value and
`callExprParser` is set, the function is called and its result becomes
the boolean primary (with optional arithmetic/comparison continuation).
Example: `d || my_fn x`, `d || get_count > 5`.

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

**Continuation**: After parsing a mode block, `parseArithExprFromFull`
tries arithmetic continuation, then `maybeExprContinuation` (or
`maybeBhvExprContinuation`) tries comparison/boolean continuation.
This enables `let x = unlocked { get_number v } + 1` and
`let y = unlocked { get_self } == me`.

**Call arguments**: Mode blocks are accepted in `parseBhvArgExpr`
(behavior level) and `parseFnBodyArgExpr` (fn bodies). The
`parseFnBodyArgExpr` helper extends `parseFnBodyExpr` with mode
block and if-expression detection; used by `parseFnBodyCallArgs`.

**Return items**: `parseFnBodyReturnItem` detects `locked`/`unlocked`
before the function-call check.

**Shared refactoring**: `maybeExprContinuation(valueExpr, resolve)` is
the resolver-parameterized core; `maybeBhvExprContinuation` wraps it
with `bhvResolver(syms)`. `parseFnBodyCallArgs` takes `*fnBodyContext`
instead of separate `paramDirs`/`letVars` parameters.

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

**Continuation**: After parsing an if-expression, `parseArithExprFromFull`
tries arithmetic continuation, then `maybeExprContinuation` (or
`maybeBhvExprContinuation`) tries comparison/boolean continuation.
Same pattern as mode block expressions.

**Call arguments**: If-expressions are accepted in `parseBhvArgExpr`
(behavior level) and `parseFnBodyArgExpr` (fn bodies).

**Return items**: `parseFnBodyReturnItem` detects `if` before the
function-call check.

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

**Expression tails**: In `exprTail` mode (used by wait condition blocks
and mode block expressions), both function-call and variable branches
go through `parseArithExprFromFull` + `maybeExprContinuation`, so
`wait 5 { get_count > 0 }` works the same as `wait 5 { x > 0 }`.
The fn body path uses `maybeExprContinuation` (replacing earlier manual
continuation code) for consistency with the behavior-level path.

## Nested function calls in arguments

Function calls with return values work as arguments to other function
calls: `set_reg get_self`, `add x, get_resource_num y`,
`set_reg get_type get_self` (2-deep).

**Approach**: Function call detection in `parseArithPrimary` — the
lowest-level shared expression parser. In the `tokIdent` case, before
`resolve(tok)`, check `callExprParser != nil && fns[name].hasReturn()`.
This makes function calls work everywhere arithmetic expressions are
accepted: call arguments, arithmetic operands, comparison operands.

**Fn body path**: `parseFnBodyArgExpr` has its own function call
detection (before the `tokLParen` check) because fn body expressions
don't go through `parseArithPrimary`. Uses `parseArithExprFromFull`
for arithmetic continuation after the call result.

**Argument boundaries**: Always unambiguous because function parameter
counts are fixed. `add get_resource_num x, 5` → `get_resource_num`
takes 1 arg (consumes `x`), `5` is `add`'s second arg.

**Simplifications**: `parseBoolPrimary`'s `tokIdent` case merged with
`tokNumber`/`tokMinus` to delegate through `parseArithExpr` (which
now handles function calls, null/false/true, constructors).
`parseBhvArgExpr`'s explicit `null`/`false`/`true` cases removed
(handled by `parseArithPrimary`); bare-ident else branch delegates
through `parseArithExpr`.

**No emitter changes**: `emitBhvExprGetValue` already handles
`*CallExpr` (allocates `@call` temp, calls `expandCall`).
`emitExprGetValue`/`emitExprTo` in fn bodies also handle `CallExpr`.

## Block scoping

Variables declared inside blocks (`if`/`else`, `while`, `loop`, `for`,
`locked`, `unlocked`, `wait`) are scoped to that block — they vanish when
the block ends. This is a breaking change from the earlier flat-scope
model where all variables leaked to the parent scope.

**Implementation**: `symbolTable.pushScope()` copies the `vars` map and
increments `scopeDepth`; `popScope(saved)` restores it and decrements.
The `usedVars` map is NOT saved/restored — it tracks all names ever used
(for inline variable rename collision avoidance). Both parse-time and
emit-time need scoping: `parseBhvStmtBlockInner` and
`parseFnBodyStmtsInner` push/pop during parsing, and emitter functions
(`emitBhvIfStmt`, `emitBhvWhileStmt`, etc.) push/pop during emission
(needed because the emitter accesses `syms.vars` for direction checking
and assignment target resolution).

**Fn body scoping**: Uses `fnBodyContext.pushFnScope()`/`popFnScope()`
which saves/restores `fnVarInfo` map plus `fnScopeDepth`. No emit-time
scoping needed for fn bodies — fn body emitters resolve variables
through `paramMap`, not `ctx.fnVarInfo`.

**For-loop special case**: The for-loop iterator variable needs its own
scope that encompasses the body but doesn't leak to the parent. The
for-loop parser wraps pushScope/declareVar/body/popScope explicitly
(not using parseBhvStmtBlockInner's automatic scoping).

## Compiler warnings and shadowing

The compiler supports non-fatal warnings returned alongside the compiled
object. `Compile`/`CompileString` return `(*codec.Object, []string, error)`
where the middle value is warnings (nil if none).

**Shadowing warning**: When a variable is re-declared at the same scope
depth as an existing declaration that was never used, a warning is emitted.
`declareVarWarn` (behavior level) and `declareFnVarWarn` (fn bodies) check
`existing.depth == scopeDepth && !existing.used`. Child-scope
re-declarations don't warn because they're at a different depth.

**Used tracking**: `varInfo.used` and `fnVarInfo.used` track whether a
variable has been read since declaration. At behavior level,
`resolveBhvOperand` and `resolveAssignTarget` (compound) call
`syms.markUsed()`. In fn bodies, `fnBodyResolver`, `canCompound`,
`parseFnBodyArgExpr`, `parseFnBodyReturnItem`, and the expression list
parser call `ctx.markFnVarUsed()` or `ctx.markExprUsed()`.

**Warning infrastructure**: `parser.warnings []string` field,
`parser.warnf(pos, format, args...)` method. Format matches error
messages: `line:col: message`.

**`-e`/`--error` CLI flag**: Treats warnings as errors. When enabled
and warnings are present, `cmdCompile` returns the first warning as an
error instead of printing warnings to stderr and continuing.

## Undeclared variable errors

All variables must be declared (`let`/`var`) or be `@param` parameters
before use. Using an undeclared name is a compile error ("undeclared
variable"). This replaces the previous behavior where bare identifiers
silently compiled as runtime register references.

**Behavior level**: `resolveBhvOperand` checks `syms.lookupVar()` for
non-`$` identifiers. `resolveAssignTarget` errors on names not in
`syms.vars` or `syms.paramMap`. The old "unknown function" error for
bare identifiers in `let x = nonexistent` is subsumed — `resolve()`
now catches it first with "undeclared variable".

**Fn bodies**: `fnBodyResolver` checks `ctx.paramDirs` and `ctx.fnVarInfo`.
`canAssign` errors on names not in either map. For call arguments and
return items parsed via `parseFnBodyExpr()` (which creates `IdentExpr`
without going through the resolver), `checkFnBodyExprDeclared` validates
all `IdentExpr` nodes in the result. This covers `IdentExpr`,
`ArithExpr`, `AmpersandExpr`, and `ConstructorExpr` recursively.

## Import system

**Syntax**: `import <names> from "<path>"`, `import "<path>" as <ns>`,
or `import <names> from "<path>" as <ns>`. Glob imports use `*` in the
name list. `import`, `from`, `as` are reserved keywords. Import
statements must appear before all `fn` and `behavior` declarations.

**Path resolution**: `./` and `../` are relative to the importing file
(via `path.Join(sourceDir, importPath+".doit")`). `std:` resolves
against the stdlib `fs.FS`. Paths always use `/` as a separator
regardless of platform. The compiler API takes `sourceFS fs.FS`
and `sourcePath string` for filesystem context. When compiling from
stdin, `sourceFS` is nil and imports produce a compile error.
Self-imports (importing the current file) are detected and rejected.

**Namespace resolution**: `resolveFnName(tok)` peeks for `tokDot`
after an identifier. If the identifier is a known namespace name and
a dot follows, it reads the function name, looks it up in
`p.namespaces[ns]`, checks for private access, and stores the result
in `p.fns["ns.fn"]` for `expandCall` to find. The qualified name
`"ns.fn"` is stored in `CallStmt.Name`/`CallExpr.Name`.

**Transitive dependencies**: Imported functions carry a `scope`
(`fnDef.scope`) containing all non-stdlib functions from their defining
file. During `expandCall`, scope entries are temporarily merged into
`p.fns` (only filling gaps, not overriding existing entries) so that
functions called by the imported function are available during body
expansion. This is removed after expansion completes.

**Collision rules**: Named imports and namespace names are tracked in
`p.namedImports` and `p.namespaceNames`. Same-file function names are
checked against both after `collectUserFns`. Glob imports silently add
functions to `p.fns` and can be overridden by named imports or same-file
functions (no error). Named import vs named import or namespace vs
namespace collisions are detected across all import statements via
`allAliases` in `parseImports`. Behavior IDs never collide with imports
(behaviors are invisible to the import system). Local variables can
shadow imported names within their scope.

**Private functions**: `fnDef.private` is set by `private fn` syntax.
Private functions are included in `parseImportedFile`'s return map (for
error reporting) but excluded from glob imports and produce compile
errors when accessed via named import or namespace dot access.

**Circular imports**: Detected via `parser.importStack []string`. Each
`parseImportedFile` call checks if the target path is already in the
stack. The cycle is reported with the full chain.

**Only functions are importable**: Behaviors in imported files are
silently skipped — they are entry points and have no mechanism to be
called or referenced from other files.

**No re-exports**: Imported functions are not re-exported. If file A
imports `greet` from file B, file C cannot import `greet` through A.
Transitive *dependencies* (functions called by imported functions) are
resolved automatically via `fnDef.scope`, but the imported names
themselves are not visible to further importers.

**Glob + rename**: `import *, hello as hw from "./lib"` imports all
public functions via `*`, but `hello` is only accessible as `hw` — the
named rename removes the glob-imported original from `p.fns` so only
the alias is accessible. Other non-renamed functions remain under their
original names.

**Imported file errors**: Parse or compile errors in imported files
propagate as errors in the importing compilation. Error messages include
the imported file's path for correct source location reporting.

**File caching**: `processImports` uses a `fileCache` to avoid
re-parsing files imported multiple times within the same file's import
statements.

## Compile-time constants

`const` declarations define named compile-time values. Values are
evaluated during pass 1 (`collectUserFns`) and substituted as
`LiteralExpr` nodes wherever the constant name appears.

**Data model**: `constDef` struct with `value any` and `private bool`.
Stored in `parser.consts` (flat map) and `parser.namespaceConsts`
(for namespace-qualified access). Constants are pure values with no
transitive dependency concerns (unlike functions with `scope`).
Constants and functions share a namespace — a constant and a function
with the same name is a compile error regardless of declaration order.
Both `parseConstDecl` (checks `p.fns`) and `parseUserFn` (checks
`p.consts`) enforce this.

**Evaluation**: `parseConstDecl` uses `parseBoolExpr` with a
const-only `operandResolver` that resolves known constants to
`LiteralExpr` and errors on everything else. String and `localize`
RHS are handled specially before `parseBoolExpr` (since `parseBoolExpr`
rejects strings). `TruthyExpr` is unwrapped after `parseBoolExpr` to
preserve arithmetic values. `evalConstExpr` recursively evaluates the
resulting AST to a Go value.

**Import system**: `parseImportedFile` returns `*importedFile` (fns +
consts). `processImports` handles glob, named, and namespace imports
for both functions and constants. `resolveFnName` resolves namespace
constants via `p.namespaceConsts`. The `checkImportCollisions` function
checks both function and constant names.

**Expression integration**: Constants are resolved in five places:
(1) `parseArithPrimary` (shared arithmetic parser — covers most paths),
(2) `resolveBhvOperand` (behavior-level resolver fallback),
(3) `fnBodyResolver` (fn body resolver fallback),
(4) `parseFnBodyExpr` (fn body expression parser),
(5) `checkFnBodyExprDeclared` (declaration validator).
Additionally, `parseBhvVarInit` checks `p.consts` after `resolveFnName`
to handle namespace-qualified constants like `lib.COUNT`.

**Pass 2 skipping**: `skipConstDecl` skips const declarations during
pass 2 by scanning until the next top-level keyword (same approach as
`skipImportStmt`).

## Constant folding

Arithmetic and comparison expressions with all-literal operands are
folded at compile time, producing no runtime instructions.

**Arithmetic folding**: `tryFoldArith` in `parseArithTermFrom` and
`parseArithExprFrom` — after constructing an `ArithExpr`, checks if
both operands are numeric `LiteralExpr`. Supports `+`, `-`, `*`, `/`,
`%`. Division by zero is not folded (left to runtime). Cascades
naturally: `2 + 3 + 4` folds bottom-up to `9`.

**Comparison folding**: `tryFoldCompare` in `parseBoolPrimary` — after
constructing a `CompareExpr`, checks if both operands are `LiteralExpr`
with compile-time constant values. The `isCompileTimeConstant` guard
prevents folding runtime references (parameter indices as bare `int`,
variable names as `string`) that happen to be wrapped in `LiteralExpr`
by the behavior-level resolver.

**Boolean chain/not folding**: `tryFoldBoolChain` and `tryFoldNot` —
fold `BoolChainExpr` when all children are `LiteralExpr`, and `NotExpr`
when the inner value is `LiteralExpr`.

**`LiteralExpr` in boolean contexts**: `resolveBoolTree` handles
`*LiteralExpr` by treating it as a truthy check, emitting
`compare_register <value>, false`. This handles folded comparisons
that appear as if/while/wait conditions.

**Constructor integration**: `tryResolveConstructorLiteral` already
checks for `LiteralExpr` args, so `Coordinate(1 + 2, 3 + 4)` folds
automatically after arithmetic folding.

## Compile-time evaluator

`tryEvalExpr`, `tryEvalCall`, and `tryEvalStmts` form a compile-time
evaluator that can trace through function calls. Used by `parseConstDecl`
to evaluate `const` expressions that include function calls.

**Design**: Try-and-bail approach. Returns `(value, true)` on success,
`(nil, false)` on bail. Bails on `instruction` blocks, `wait` statements,
and any construct that can't be evaluated without runtime state.
`p.evalStepLimit` (initialized to 10000) prevents infinite loops.

**Expression evaluation** (`tryEvalExpr`): Handles all `Expr` types.
`LiteralExpr` returns the value. `IdentExpr` looks up in the local
environment. `CallExpr` delegates to `tryEvalCall`. `IfExpr` evaluates
the condition and the matching branch. `ModeBlockExpr` ignores mode
and evaluates body + tail.

**Function call evaluation** (`tryEvalCall`): Builds an environment
from params + args, merges transitive function scope (same pattern as
`expandCall`), evaluates `fn.astBody`, extracts return values. Bails
if `fn.frame != nil` (instruction-based function).

**Statement evaluation** (`tryEvalStmts`): Returns `(*constEvalStatus, bool)`.
Normal completion returns `(nil, true)`. `ReturnStmt` returns
`(&constEvalStatus{returned: true, retVals: ...}, true)`. `BreakStmt`
returns `(&constEvalStatus{broke: true, breakLabel: ...}, true)`.
Bail returns `(nil, false)`.

**Call argument parsing**: `parseConstCallArgs` and `parseConstArgExpr`
are const-specific helpers for parsing function call arguments within
`const` declarations. They use the `constResolver` for identifier
resolution and handle strings, constructors, `&`, and arithmetic.

**Parsing integration**: `parseConstDecl` sets `callExprParser` to a
closure that uses `parseConstCallArgs`, enabling function call detection
in `parseArithPrimary`. After parsing, `tryEvalExpr` replaces the
earlier `evalConstExpr`.
