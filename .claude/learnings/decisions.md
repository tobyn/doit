# Design Decisions

Non-obvious choices and their rationale. Helps future sessions make
consistent decisions without re-deriving past conclusions.

For implementation details (frame layouts, internal function names,
emission patterns), read the source code directly.

## Compiler-generated `@`-prefixed variable names

The compiler generates temporary variables with `@`-prefixed names
(`@arith1`, `@call`, `@loop`, `@wait`, `@wcond`, `@bool`, `@ctor`,
`@amp`, `@mode`, `@if`, `@step`, `@start`, `@stop`, `@retK`) via
`allocUniqueVar()`. These are **ordinary VM variable names** — the `@`
prefix has no special meaning to the game engine. It avoids collisions
with user-defined variables since `@` is not valid in doit identifiers.

Separately, `@break` and `@return` are **control-flow placeholder
opcodes** (not variable names). They appear in intermediate frames and
are patched to real jump targets before finalization.

## `exit` keyword

`exit` is a language keyword (not a stdlib function). It emits
`{"op": "exit"}` with no `"next"` field — a terminal instruction.
Works in both behavior bodies and function bodies. The `ExitStmt`
AST node mirrors `BreakStmt` in structure. The old `exit()` stdlib
stub was removed.

## Unreachable code warnings

Code after `exit`, `break`, or `return` is unreachable. The compiler
warns and skips remaining statements in the block (brace-depth-aware
skip via `skipToCloseBrace`). Detected in three places:
`parseBehaviorBody`, `parseBhvStmtBlockInner`, and
`parseFnBodyStmtsInner`. The `isTerminalStmt` helper identifies
`*ExitStmt`, `*BreakStmt`, and `*ReturnStmt`.

**Break label disambiguation**: When `break` is followed by an
identifier, the parser checks if it's a known loop label. Previously,
any non-label identifier (like a function name) would error as
"unknown loop label". Now, keywords and known functions (`p.fns`)
are excluded from label consideration — they cause the break to be
treated as unlabeled, allowing the unreachable code check to fire on
the next iteration.

## Behaviors as top-level functions

A behavior is conceptually a top-level function with extra syntax sugar
(parameters as external registers, behavior-level `default` case
dispatch). Behavior blocks and function bodies support almost the exact
same language constructs — this is a core design principle. Expression
parsers are shared via the `operandResolver` callback.

## Stdlib function signatures

- **Output parameters as return values**: Functions with output slots
  use `return instruction` with `@1` markers. Callers use
  `let x = get_self` instead of `get_self x`.
- **Optional inputs as keyword params**: Avoids forcing callers to pass
  placeholders for unused optional slots.
- **`"c"` mode fields**: Exposed via keyword parameters and stdlib
  mode enums (see "Instruction `"c"` mode field" section below).
- **`"txt"` fields as positional strings**: Natural call-site syntax
  (`notify "Hello"`).

## Boolean literals

`true` → `map[string]any{"num": 1}`, `false` → `false` (Go bool).
Matches VM convention (truthy checks use `compare_register value,
false`). Both are reserved keywords. `false` and `null` compile
identically (empty register). Label lookahead exclusions prevent
`true:` and `false:` from being parsed as loop labels.

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

The `{ }` block after the opcode string is optional. When no fields are
needed, `instruction "op"` is equivalent to `instruction "op" { }`.
This also works with `return instruction "op"` in fn bodies.

## Return/parameter name collision handling

When `return` references a parameter name, `expandCall` detects the
collision and preserves the parameter mapping. After body calls are
expanded, a `set_reg` frame copies the parameter value to the caller's
return target.

## Boolean expressions

### Comparison operators

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
- **Contexts**: `let`/`var` init, assignment RHS, number literal LHS,
  arithmetic sub-expressions on both sides, constructor RHS, and
  parenthesized comparisons in call arguments.

### Type check (`is`)

`is` checks whether a value matches a game data type. Compiles to a
3-frame `value_type` + `set_reg` + `set_reg` pattern. RHS is one of:
`Item`, `Unit`, `Component`, `Technology`, `Value`, `Coordinate`.
`Number` is not supported (`value_type` cannot distinguish numbers
from null). `tokIs` is an internal marker only — `is` scans as
`tokIdent`.

### Logical operators (`&&` and `||`)

Chain multiple boolean sub-expressions (comparisons, type checks, bare
variables as truthy checks, number literals). `&&` binds tighter than
`||` (standard precedence). Parentheses override. Short-circuit
evaluation: `&&` fails fast, `||` succeeds fast.

Truthy checks use `compare_register value, false` — Different =
truthy, Equal = falsy. Single comparison without `&&`/`||` delegates
to existing 3-frame emitters for backward-compatible output.

### Negation (`!`)

`!expr` negates any boolean sub-expression via the swap-targets
approach: the emission system routes through `trueTarget`/`falseTarget`,
so negation just swaps them. No new opcodes. `negateResolved` pushes
negation to leaves via De Morgan's law, toggling `comparisonTerm.negated`
at leaves and swapping `chainOp` (`&&`↔`||`) at groups.

### Expression priority hierarchy

Highest to lowest: arithmetic > comparisons > function calls > negation
(`!`) > boolean operators (`&&`/`||`). So `my_fn b + 1, c || d` parses
as `(my_fn(b+1, c)) || d`. `!a > 5` parses as `!(a > 5)`.

Function calls work in any boolean term position via the
`callExprParser` callback, set contextually by `parseBehaviorBody` and
`parseUserFn`.

### Parenthesized boolean expressions

Recursive tree model: `BoolChainExpr` has `Op` (`&&`/`||`) and
`Children`. Parentheses create nested nodes for mixed `&&`/`||`.
Two-phase: parse AST → resolve operands → emit. Single-leaf delegates
to existing 3-frame emitters for backward compatibility.

## Structured locking with `locked`/`unlocked` blocks

Lexically scoped mode blocks that set execution mode on entry and
restore on exit. Static mode tracking via `frameBuilder.mode` (initially
`modeLocked` for behaviors). No `modeUnknown` — mode is always
statically known. No-op elimination when already in target mode.
Cross-function tracking flows through inlined function bodies via the
caller's `frameBuilder`.

## Stdlib branching function conventions

All ~69 stdlib functions that wrap branching game instructions have
`exec(...)` signatures and `instruction` blocks with exec bindings.
The transformation patterns:

- **Output params** that become `@N` slots are removed from the param
  list; data flows via `@N` → continuation bindings or return values.
- **Optional inputs** not in the original signature are added as
  keyword params.
- **Exec continuation names** are short snake_case identifiers derived
  from the game's descriptive labels.
- **`check_number`/`compare_register`/`value_type`** have exec
  signatures AND are hard-coded in the compiler for `if`/`while`/`is`
  expressions. Both calling conventions coexist.
- **`build`/`build_registered`/`produce_registered`** implement the
  exec part but omit `bp`/`frame` metadata fields (users needing
  those use the `instruction` intrinsic directly).

## Test file conventions

Test `.json` files use graph isomorphism (`matchBehaviors`) for
comparison — frame order doesn't matter. Files use the reference JS
codec's 0-based key format; `refToNative` bridges to our 1-based
native format.

## Call-site direction annotations

`out` and `inout` arguments must be annotated at the call site to match
the parameter's direction. `in` is the default (no annotation needed,
but explicit `in` is accepted). Mismatched annotations are compile
errors. `in`, `out`, `inout` are fully reserved keywords.

## Arithmetic operators (+, -, *, /, %)

Each maps to one stdlib opcode (`add`, `sub`, `mul`, `div`, `modulo`).
LHS preserves typed value (the game's semantics). Number literal LHS
supported. PEMDAS precedence via nested `ArithExpr` AST nodes (`*`/`/`/`%`
high-priority, `+`/`-` low-priority). Intermediate results use
`@arith`-prefixed temp variables.

## Unary minus (`-expr`)

Handled in `parseArithPrimary` as the single source of truth — all
expression contexts benefit automatically.

- **Number literals**: compile-time fold. `-5` → `LiteralExpr{-5}`.
- **Variables/expressions**: desugar to `0 - expr`. `-x` →
  `ArithExpr{tokMinus, LiteralExpr{0}, IdentExpr{x}}`. Emits `sub`.
- **Double negation**: `--x` chains correctly via recursive calls.

## Compound assignment and increment/decrement

`+=`, `-=`, `*=`, `/=`, `%=` compile to a single instruction frame where
target appears in both input and output. RHS accepts the full expression
language. `++`/`--` are sugar for `+= 1`/`-= 1`.

## Mutable variables (`var`) in fn bodies

`let` = immutable, `var` = mutable. `fnBodyContext.fnVarInfo` tracks
mutability, scope depth, and used status. Assignment validation at
parse time via `canAssign`/`canCompound`.

## Control flow emission (unified)

Both behavior-level and fn body `if`/`else if`/`else`, `while`,
`loop`/`break`, `for`, `wait` share a single set of emitters in
`codegen.go`, parameterized by `*emitContext`. Two constructors —
`bhvEmitCtx` and `fnEmitCtx` — build the context with closures that
capture the relevant resolution state.

Emission is direct into the parent `frameBuilder`. Condition check
frames use `frameRef(0)` as a false-branch placeholder, patched after
body emission. `stripFallThrough` removes redundant branch slots.
`break` emits `{"op": "@break"}` placeholder. `return` emits values
to `@retK` targets then `{"op": "@return"}`; `expandCall` patches
these to jump past the function expansion.

Loop back-edges check `!hasNext` on the last body frame before setting
`"next"`, preserving inner control flow. If-expression and
wait-statement bodies rely on natural frame fall-through.

## `break` in `while` loops

Works in both `loop` and `while` at behavior level and fn bodies.
At behavior level, `emitBehaviorStmts` detects the if/break pattern
(single `BreakStmt` body, no else) and routes through `emitBhvIfBreak`
for optimized emission.

**Back-edge for if/break as last statement**: When `if ... { break }`
is the last statement in a loop body, all four loop/while emitters
detect the last body frame is `@break` and emit a noop back-edge frame
after it. Without this, the false branch (condition not met → continue)
would point past the loop, causing exit after one iteration. Counted
loops and for-loops are unaffected because their false branch reaches
the INCR frame naturally.

## Labeled loops and breaks

Syntax: `label: loop { ... }`, `label: while cond { ... }`,
`label: for i in Range(5) { ... }`, `break label`. Label detection via
three-token lookahead. Duplicate labels and unknown labels are compile
errors. `@break` placeholder includes optional `"label"` field;
patching matches unlabeled breaks to innermost loop, labeled breaks to
the named loop.

## Counted loops (`loop N { ... }`)

Optional count expression: `loop 5 { ... }`, `loop n { ... }`,
`loop (a + b) { ... }`. When nil, infinite loop. Frame layout:
INIT (counter=0) → CHECK (counter vs limit) → BODY → INCR (counter+1)
→ back to CHECK. Counter uses `@loop` via `allocUniqueVar`.

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

Return items support the full expression language via `parseBoolExpr`
as the fallback parser (arithmetic, comparisons, negation, boolean
chains, type checks, constructors with `&`, parenthesized expressions).
Mode blocks and if-expressions are detected as special cases first.

## Multi-arity expression lists

`let a, b, c = 1, 2, 3` — comma-separated expression list. Each
expression contributes its arity. Sum must equal binding count, with
prefix matching allowed on last function call. Single-item RHS uses
`MultiReturnStmt`; multiple items use `ExprListExpr`.

## Block expressions (mode blocks and if-expressions)

### Mode block expressions

`locked { ... }` / `unlocked { ... }` as expressions. Last item is
the value-producing tail. `ModeBlockExpr` has `Body []Stmt` and
`Tail Expr`. Arity from tail: `CallExpr` → `returnCount()`, else 1.

### If-expressions

`if`/`else if`/`else` as expressions. Each branch has optional
statements + tail expression. `else` is optional — when absent,
uncovered branches produce `null`. Arity = max across all branches;
nil else contributes arity 1. Mixed-arity branches zero remaining
slots.

### Shared patterns

Both block expression types support **continuation**: after parsing,
`parseArithExprFromFull` tries arithmetic continuation, then
`maybeExprContinuation` tries comparison/boolean continuation. This
enables `unlocked { get_number v } + 1` and `if x { a } else { b } == c`.

Both are accepted in call arguments (behavior and fn body level) and
return items.

## Wait keyword

`wait` is a language keyword (not a stdlib function). Syntax:

- **Simple**: `wait <ticks>` — pauses for the given number of ticks.
- **Conditional**: `wait <ticks> { condition }` — waits, evaluates
  condition, repeats until truthy.
- **Body + condition**: `wait <ticks> { stmts...; condition }` — last
  item is the condition; preceding statements execute each iteration.

**Ticks snapshot**: Evaluated once and stored in a `@wait` temp. Pure
number literals skip the snapshot. Prevents re-evaluation when the
source variable changes during iteration.

**Not a loop**: `wait` does not support `break` or labels.

**Frame layout** (conditional): WAIT → body → condition to `@wcond` →
`compare_register @wcond, false` (Different → after wait, `"next"` →
back to WAIT).

## Nested function calls in arguments

Function calls with return values work as arguments to other calls:
`set_reg get_self`, `set_reg get_type get_self` (2-deep). Argument
boundaries are always unambiguous because function parameter counts
are fixed.

Detection lives in `parseArithPrimary` (shared expression parser) so
it works everywhere: call arguments, arithmetic operands, comparison
operands. Fn body arguments have separate detection in
`parseFnBodyArgExpr`.

## Block scoping

Variables declared inside blocks (`if`/`else`, `while`, `loop`, `for`,
`locked`, `unlocked`, `wait`) are scoped to that block. Both parse-time
and emit-time need scoping (emitters access `syms.vars` for direction
checking). The `usedVars` map is NOT saved/restored — it tracks all
names ever used for inline variable rename collision avoidance.

Fn body scoping uses separate `pushFnScope()`/`popFnScope()` (saves
`fnVarInfo` + `fnScopeDepth`). For-loop iterator variables get their
own scope that encompasses the body but doesn't leak to the parent.

## Compiler warnings and shadowing

The compiler returns non-fatal warnings alongside compiled output.
`Compile`/`CompileString` return `(*codec.Object, []string, error)`.

**Shadowing warning**: When a variable is re-declared at the same scope
depth as an existing declaration that was never used. Child-scope
re-declarations don't warn (different depth).

**`-e`/`--error` CLI flag**: Treats warnings as errors.

## Undeclared variable errors

All variables must be declared (`let`/`var`) or be `@param` parameters
before use. Using an undeclared name is a compile error. This replaces
the previous behavior where bare identifiers silently compiled as
runtime register references. Checked at both behavior level
(`resolveBhvOperand`) and fn bodies (`fnBodyResolver` +
`checkFnBodyExprDeclared`).

## Import system

**Syntax**: `import <names> from "<path>"`, `import "<path>" as <ns>`,
or `import <names> from "<path>" as <ns>`. Glob imports use `*`.
`import`, `from`, `as` are reserved keywords. Imports must appear
before all `fn` and `behavior` declarations.

**Path resolution**: `./` and `../` are relative to the importing file.
`std:` resolves against the stdlib `fs.FS`. Paths always use `/`.
Compiling from stdin disables imports. Self-imports are rejected.

**Namespace resolution**: `resolveFnName(tok)` peeks for `tokDot`
after an identifier. Namespace members are looked up in `symbolSet`,
checked for private access, and cached into flat maps under
`"ns.member"` qualified names.

**Transitive dependencies**: Imported functions carry a `scope`
containing all non-stdlib functions from their defining file. During
`expandCall`, scope entries are temporarily merged into `p.fns` (gap-fill
only) so transitive callees are available. Removed after expansion.

**Collision rules**: Glob imports silently add symbols and can be
overridden. Named import vs named import or namespace vs namespace
collisions are errors. Same-file declarations are checked against
named imports and namespace names. Behavior IDs never collide with
imports. Local variables can shadow imported names.

**Private symbols**: `private` modifier for `fn`, `const`, `enum`.
Excluded from glob imports; compile errors when accessed via named
import or namespace dot access.

**Circular imports**: Detected via `importStack`. Reported with full
chain.

**Only functions are importable**: Behaviors are silently skipped.

**No re-exports**: Imported names are not visible to further importers.
Transitive dependencies resolve automatically via `fnDef.scope`.

**Glob + rename**: `import *, hello as hw from "./lib"` — `hello` is
only accessible as `hw` (glob original removed via `deleteAll`).

**File caching**: `processImports` caches to avoid re-parsing.

## Compile-time constants

`const` declarations define named compile-time values, substituted as
`LiteralExpr` nodes wherever the constant name appears. Functions,
constants, and enums share a namespace — collisions are compile errors
via `checkDeclName`.

`parseConstDecl` uses `parseBoolExpr` with a const-only resolver.
String and `localize` RHS are handled specially (since `parseBoolExpr`
rejects strings). `tryEvalExpr` evaluates the resulting AST to a Go
value, including tracing through function calls via `tryEvalCall`.

Constants are resolved in `parseArithPrimary` (covers most paths),
both resolvers (`resolveBhvOperand`, `fnBodyResolver`),
`parseFnBodyExpr`, and `checkFnBodyExprDeclared`.

## Constant folding

Expressions with all-literal operands are folded at compile time:

- **Arithmetic**: `tryFoldArith` — `+`, `-`, `*`, `/`, `%`. Division
  by zero not folded. Cascades: `2 + 3 + 4` → `9`.
- **Comparison**: `tryFoldCompare` — guarded by `isCompileTimeConstant`
  to prevent folding runtime references wrapped in `LiteralExpr`.
- **Boolean chain/not**: `tryFoldBoolChain` and `tryFoldNot` — fold
  when all children/inner value are `LiteralExpr`.
- **Constructors**: `tryResolveConstructorLiteral` checks for
  `LiteralExpr` args, so `Coordinate(1 + 2, 3 + 4)` folds
  automatically.

`LiteralExpr` in boolean contexts (if/while/wait conditions) is
handled by `resolveBoolTree` as a truthy check.

## Compile-time evaluator

`tryEvalExpr`, `tryEvalCall`, and `tryEvalStmts` form a compile-time
evaluator used by `parseConstDecl` for `const` expressions that include
function calls.

**Design**: Try-and-bail approach — returns `(value, true)` on success,
`(nil, false)` on bail. Bails on `instruction` blocks, `wait`, and
anything requiring runtime state. `p.evalStepLimit` (10000) prevents
infinite loops. `tryEvalCall` builds an environment from params + args,
merges transitive scope, and evaluates `fn.astBody`.

## Enum declarations

`enum` is a top-level compile-time construct defining named groups of
integer values. Shares the top-level namespace with `fn` and `const`.

**Member values**: Auto-increment from 0. Explicit values set the
counter. Negative values supported. Duplicate names and duplicate
values are compile errors.

**`::` access**: `MyEnum::Member` resolves to `LiteralExpr{{"num": N}}`.
Using an enum name without `::` produces "enum requires '::' member
access". Resolution in `parseArithPrimary` (most contexts),
`parseBhvVarInit` (namespace-qualified), and `parseFnBodyExpr`.

**Comma separators**: Enum members can be separated by newlines or
commas. `enum Color { Red, Green, Blue }` is valid. The parser consumes
an optional comma after each member's value (including `= N`).

**Import system**: Enums are part of the unified `symbolSet`.
`resolveFnName` handles namespace detection and caches resolved enums.

## Instruction `"c"` mode field

Many game instructions have a `"c"` field — a 1-based integer selecting
from a combo dropdown in the game UI (e.g., bitwise operation type,
sync/async movement). Mode enums are defined in the stdlib
(`instructions.doit`) with explicit `= 1` on the first member. Affected
stdlib functions expose mode via keyword parameters with `c: param` in
the instruction block.

**Number literals in instruction fields**: `parseInstruction` accepts
`tokNumber` values (in addition to `tokString`, `tokIdent`, `@N`),
producing plain `int` values in the frame map. Enables direct
`instruction "domove" { 0: target, c: 2 }`.

**Metadata field unwrapping**: `resolveInstructionFrame` unwraps
`map[string]any{"num": N}` to plain `int` for non-numeric keys after
paramMap substitution. When an enum value like `BitwiseMode::Xor`
flows through `paramMap`, it arrives as `{"num": 3}`. The game expects
`"c": 3` (plain integer). The unwrap applies to all non-numeric keys
— safe because register-slot values (numbered keys) correctly use
`{"num": N}` maps, while metadata fields like `"c"` and `"txt"` never
want wrapped values.

**Stdlib enums**: Defined in `instructions.doit` alongside functions.
`parseStdlibFile` accepts `enum` declarations. Stdlib enums are stored
in `parser.stdlibEnums` and cloned into user/imported file parsers,
making them available everywhere like stdlib functions.

**Keyword param omission**: When a mode keyword arg is omitted at the
call site, the `c` field's string value hits the `kwVars` check in
`resolveInstructionFrame` and is omitted from the output. The game then
uses its built-in default.

## Continuations — pure-logic branching

**`return <cont_name>`**: `ReturnStmt.Continuation` holds the
continuation name. In `emitFnBody`, this emits a `set_reg false false`
frame with `"next": "@exec_<name>"`. The `@exec_` prefix is a string
placeholder (like `@break` and `@return`) that `expandContinuationBlocks`
patches to `frameRef(blockStart)` for provided blocks or
`frameRef(joinPoint)` for unprovided ones.

**`fnBodyContext.execNames`**: The fn body parser needs to distinguish
`return yes` (continuation dispatch) from `return yes` (return variable
named `yes`). `execNames` on the context enables `isExecName()` lookup.
Exec names take priority — if an exec name shadows a variable, `return`
dispatches to the continuation.

## Continuations — pure-logic data dispatch

**`return <cont_name>(args...)`**: `ReturnStmt.ContinuationArgs` holds
optional data arguments for continuation dispatch. This closes the gap
between instruction-based (data via `@N`) and pure-logic (control only)
branching.

**Positional slot sharing**: All continuations share a single positional
slot space (slots 1, 2, 3...). Different continuations may have
different arg counts — only the max determines register allocation.
This mirrors the instruction-based model where `@1` can appear in
multiple exec bindings.

**Synthetic exec bindings**: `buildExecBindingMap` generates synthetic
`execBinding` values from `fnDef.execContArgs`, mapping `return yes(x)`
to `execBinding{name: "yes", args: [returnSlot(1)]}`. This lets
`allocExecOutputRegs` and `expandContinuationBlocks` reuse their
existing infrastructure unchanged.

**`@carg` synthetic names**: Like `@retN` for return values, `@cargN`
names are added to `paramMap` during call expansion, mapping to the
allocated output registers. `emitFnBody` resolves these to emit
continuation arg values before the `@exec_` jump.

**Consistency validation**: All returns for the same continuation must
pass the same number of args. The validation runs during post-parse
analysis in `parseUserFn`, building `execContArgs` map from collected
`ReturnStmt` nodes.

**Expression parsing**: Continuation args use `parseFnBodyReturnItem`
(full expression language via `parseBoolExpr`) rather than the simpler
`parseFnBodyArgExpr`, enabling arithmetic, comparisons, constructors,
and function calls as arguments.

## Continuations — expression form

**`ContinuationBlock.Tail`**: Expression-form blocks have a tail
expression (the last item in the body) that produces the block's
value. This mirrors `IfExpr` and `ModeBlockExpr` patterns. The
`exprTailStmt` wrapper is used during parsing and extracted during
post-processing.

**`emitTail` callback**: Threaded through `expandCallOpts` →
`expandCall` → `expandContinuationBlocks`. Each continuation block
emits its body statements, then calls `emitTail(blk.Tail)` to write
the tail value to the caller's target. This reuses the existing
`emitBhvExprTo`/`emitExprTo` infrastructure.

**Expression form restricted to bridging**: Looping blocks don't
produce values (they iterate). Expression form is rejected at parse
time if any block has `Looping=true`. This matches the design
principle that expression form follows if-expression rules.

**Two parsing paths**: Behavior level uses
`maybeParseBhvContinuationBlocksExpr` (calls `parseBhvStmtBlockInner`
with `exprTail=true`). Fn body level uses
`maybeParseFnBodyContinuationBlocksExpr` (calls
`parseFnBodyStmtsInner` with `exprTail=true`). Both extract tails
from `exprTailStmt` wrappers.

**Emission path for `let x = fn() { blocks }`**: At behavior level,
`LetStmt` emission goes through `emitBhvExprTo` → `expandCall`
(not `emitBhvExprGetValue`). Both paths have blocks support. At fn
body level, `emitExprTo` handles `CallExpr` with blocks.
