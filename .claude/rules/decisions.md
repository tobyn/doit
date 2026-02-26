# Design Decisions

Non-obvious choices and their rationale. Helps future sessions make
consistent decisions without re-deriving past conclusions.

## Behaviors as top-level functions

**Uniform construct support**: Behavior blocks and function bodies
should support almost the exact same language constructs. A behavior
is conceptually a top-level function with some extra syntax sugar for
things that don't apply to functions (parameters as external registers,
behavior-level `default` case dispatch, etc.). Any expression or
statement that works at behavior level should also work in fn bodies,
and vice versa.

This is a core design principle, not a nice-to-have. Phase 3 of the
AST unification closed the parity gap: fn bodies now support
arithmetic, comparisons, boolean chains, `is`, `&&`/`||`, parenthesized
grouping, `var` declarations, assignment, compound assignment,
increment/decrement, and control flow (`if`/`else if`/`else`, `while`,
`loop`/`break`).

**Compiler architecture**: Parsing produces `[]Stmt` with `Expr`
nodes (defined in `ast.go`), then a separate emitter walks the AST
and emits frames via `frameBuilder`. For behavior bodies,
`parseBehaviorBody` uses a two-phase pipeline: parse statements,
then emit via `emitBehaviorStmts`. For fn bodies, `emitFnBody` runs
during `expandCall` inlining. Both paths use `frameBuilder.mode` for
on-the-fly execution mode tracking (no separate optimization pass).
Expression parsers are shared between both paths via the
`operandResolver` callback — behavior-level resolution goes through
`bhvResolver` (resolves `$register`/parameters via `symbolTable`),
while fn body resolution uses `fnBodyResolver` (resolves `$register`
to literals, checks out-only params). The shared parsers
(`parseArithExpr`, `parseBoolExpr`, etc.) accept an `operandResolver`
and return AST nodes. Behavior-level emitters (`emitBhv*` in
`bhvast.go`) resolve operands through `symbolTable`; fn body emitters
(`emitFnArithTo`, `emitFnBoolExprTo`, etc. in `parse.go`) resolve
through `paramMap`. Both paths share the low-level frame emitters in
`codegen.go` (`emitComparison`, `emitTypeCheck`, `emitTruthyCheck`,
`emitBoolCheckFrame`, `emitResolvedBoolFrames`).

**Fn body control flow emission**: Fn bodies use inline forward-jump
patching for control flow. `emitFnIfStmt` emits check frames with
placeholder false branches (`frameRef(0)`), emits the body, then
patches the placeholders to point after the body. `emitFnWhileStmt`
uses the same pattern plus a back-edge jump to the loop start.
`emitFnLoopStmt` scans for `@break` placeholder frames and patches
them to point after the loop. This is simpler than behavior-level
deferred bodies (`deferredBody` structs with `rebaseFrameRefs`).

## Stdlib function signatures

**Output parameters → return values**: Functions with output slots use
`return instruction` with `@1` instead of taking an output parameter.
This gives callers natural `let x = get_self` syntax instead of
`get_self x`. The `@1` marker in the instruction frame drives
`hasReturn()` — the `return` keyword is syntactic sugar for readability
in the stdlib file.

**Optional inputs as keyword params**: Optional inputs use keyword
parameters (`keyword varname`) rather than positional params. This avoids
forcing callers to pass placeholder values for unused optional slots.

**`"c"` mode fields omitted**: Mode/combo fields are not parameterized.
They use the game default. The language has no way to express enum-style
mode selection yet, so exposing them would be premature.

**`"txt"` fields as positional strings**: Text fields (like `notify`'s
message) are positional string parameters, matching the natural
call-site syntax (`notify "Hello"`).

## Multiple return values

The `instruction` intrinsic is semantically a function call — it takes
inputs and returns outputs. `@N` markers in the instruction block are
positional return values. `return` passes them through as the wrapping
function's returns.

**Callee (definition):**
```
fn separate_coordinate(coordinate) {
    return instruction "separate_coordinate" {
        0: coordinate
        1: @1
        2: @2
    }
}
```

**Caller (destructuring):**
```
let x, y = separate_coordinate coord
```

`@1` → first binding (`x`), `@2` → second (`y`). The positional order
in the destructuring matches `@N` numbering.

**Contiguous sequence required**: `@N` values in an instruction block
must form a contiguous sequence starting from `@1`. A gap (e.g., `@1`
and `@3` with no `@2`) is a compile error.

**Prefix matching**: Callers can bind fewer returns than a function
produces. `let x = separate_coordinate coord` captures only `@1`;
`@2` receives `false`. Binding count must be <= `returnCount()`.

**`_` as discard**: `let _, y = separate_coordinate coord` skips the
first output. `_` maps to `false` in the compiled instruction slot.

**Mixed modifier binding lists** (behavior level only):
`var a, b, _, let c, var d = fn args`. Modifiers are sticky — bare
idents inherit the active modifier. `_` does not change the active
modifier. Bare idents with no active modifier assign to existing
variables. Binding lists must start with `_`, `let`, or `var`
(bare-ident-first is deferred due to parsing ambiguity).

**Multi-value `return` in function bodies**: `return x, y, z` with
comma-separated identifiers, number literals, or `null`. Literals are
desugared into synthetic body calls with `@retK` names.

## Type literal constructors and `&`

**Constructor syntax over factory functions**: Type constructors use
capitalized names with parentheses (`Item("metalbar")`) rather than
lower-case factory functions (`item("metalbar")`). The capitalized
convention distinguishes type construction from function calls and
reserves the lowercase namespace for future use.

**Namespace prefixes hidden**: `Component`, `Technology`, and `Value`
automatically prepend `c_`, `t_`, `v_` to the user-supplied id. Users
write the short name without the game's namespace prefix. This is less
error-prone and matches how players think about these types.

**`&` as a binary operator**: The `&` operator was chosen for attaching
numeric components because it's visually distinct from arithmetic and
connotes "combining" two things. It mirrors the VM's register composite
model: every register holds (typed_value, number). `Item("metalbar") & 5`
reads naturally as "metalbar with count 5".

**Compile-time vs runtime paths**: Constructors and `&` are resolved at
compile time when all operands are literals, producing inline JSON values
with no runtime instructions. When any operand is a variable, the
compiler emits the appropriate stdlib call (`combine_coordinate` for
`Coordinate`, `set_number` for `&`). This works uniformly at both
behavior level and in function bodies. The compile-time vs runtime
decision is based on AST node types (`LiteralExpr` = compile-time,
`IdentExpr` = runtime), not on resolved values after parameter
substitution. This ensures that `Coordinate(x, y)` with variable
parameters always emits runtime instructions, even when the call site
passes literal values. At behavior level, runtime constructors emit
frames via `expandCall` with `frameBuilder`. In fn bodies, the AST
(`ConstructorExpr`, `AmpersandExpr`) is resolved during `emitFnBody`
using `@ctor`-prefixed temp variable names allocated via
`allocUniqueVar`.

**Constructor target optimization**: For `let`/`var` declarations, the
compiler avoids an extra `set_reg` copy by passing the declared variable
name directly as the output target for runtime constructor instructions.
The general-purpose argument parsing path (used in function call
arguments and assignments) allocates a temporary variable instead, which
is simpler but produces one extra frame for runtime cases.

## Instruction metadata limitations

The compiler cannot assume that all instructions are defined in the
standard library. Desynced supports user mods that add custom
instructions. The `instruction` intrinsic exists partly to support
these modded instructions — users can emit arbitrary instruction frames
without a stdlib wrapper. This means the compiler cannot rely on stdlib
metadata (like input/output slot direction) for enforcement that needs
to cover all instructions.

However, direction enforcement in fn body `instruction` blocks uses
the `@N` convention to distinguish inputs from outputs without needing
per-instruction metadata (see "Instruction direction checking via `@N`
convention" below). This works because the programmer explicitly marks
output slots with `@N` — the compiler doesn't need to know which slots
the instruction natively writes to.

## Instruction direction checking via `@N` convention

In fn body `instruction` blocks, the compiler distinguishes inputs from
outputs using the `@N` marker convention: `@N` slots are outputs (the
instruction writes to them), and all other slots are inputs (the
instruction reads from them). This allows direction enforcement without
knowing the instruction's slot metadata. An `out` parameter in a non-`@N`
slot is an error — it would be read as an input, violating the `out`
contract. `inout` parameters are fine in input positions since they
permit reading.

## Return/parameter name collision handling

When a function's `return` statement references a parameter name (e.g.,
`fn foo(x) { return x }`), `expandCall` detects the collision and
handles it without overwriting the parameter mapping. The parameter
mapping is preserved so body calls can read the original input value.
After all body calls are expanded, a `set_reg` frame is emitted to
copy the parameter value to the caller's return target. This means
`fn foo(x) { return x }` called as `let r = foo v` emits exactly one
`set_reg v r` — no extra instructions for non-colliding cases.

## Comparison expression operators

`>`, `<`, `>=`, `<=`, `==`, and `!=` work as boolean expression operators
that produce a value (1 for true, `false`/empty for false). They compile
to a 3-frame pattern:

```
Frame N:   check_number or compare_register (branch to true or false)
Frame N+1: set_reg { false → target, next: →N+3 }   (false case)
Frame N+2: set_reg { 1 → target }                    (true case)
```

**Numeric comparisons** (`>`, `<`, `>=`, `<=`) use `check_number`, a
3-way branch (Larger / Smaller / Equal):

For `>`: checkLarger → true (N+2), checkSmaller → false (N+1), equal
falls through to false (N+1). For `<`: checkSmaller → true (N+2),
checkLarger → false (N+1), equal falls through to false (N+1).

For `>=`: same as `>` plus `"next"` → true (N+2), routing equal to the
true frame. For `<=`: same as `<` plus `"next"` → true (N+2).

**Equality comparisons** (`==`, `!=`) use `compare_register`, a 2-way
branch (Different / Equal) that compares full register composites (typed
value + number), not just the numeric component:

For `==`: Different → false (N+1), Equal ("next") → true (N+2).
For `!=`: Different → true (N+2), Equal falls through to false (N+1).

**Why `compare_register` instead of `check_number`**: `check_number`
only compares numeric components. `==` and `!=` need full register
equality (e.g., `Item("metalbar") == Item("metalbar")` or
`a == null`). `compare_register` compares both typed value and number.

**`null` as RHS operand**: `a == null` and `a != null` are supported.
The parser recognizes `null` as a valid RHS for all comparison operators
and resolves it to `false` (the empty register value).

**Supported contexts**: `let`/`var` init and assignment RHS at behavior
level and in fn bodies. Number literals, variable identifiers, and
`null` are valid as operands. Number literal LHS (`5 > b`) is
supported. Both LHS and RHS can include arithmetic expressions
(`x + 1 > y - 2`).

**Deferred**: comparison in function call arguments
(parsing ambiguity); constructor RHS (`a == Item("metalbar")`).

## Type check operator (`is`)

`is` checks whether a value matches one of the six game data types. It
compiles to a 3-frame `value_type` + `set_reg` + `set_reg` pattern,
following the same boolean expression convention as comparison operators.

**Syntax**: `let a = b is Unit`. The LHS is a variable or parameter.
The RHS is one of the six
type constructor keywords: `Item`, `Unit`, `Component`, `Technology`,
`Value`, `Coordinate`.

**Compiled output** (3 frames):
```
Frame N:   value_type { input: LHS, matching_type → true, all_others → false, "next" → false }
Frame N+1: set_reg false → target, next → N+3
Frame N+2: set_reg 1 → target
```

The `value_type` instruction has 6 branch slots (one per type) plus
`"next"` (no-match/empty). For a given type check, the matching type's
slot points to the true frame; all 5 other type slots and "next" point
to the false frame.

**`tokIs` as internal token**: `is` is a keyword in the scanner but
does not produce a distinct token kind during scanning — it scans as
`tokIdent` with val `"is"`. The `tokIs` token kind exists only as an
internal marker in `comparisonTerm.op` to identify type check terms
in boolean expression chains.

**`Number` not supported**: `value_type` cannot distinguish numbers
from null (both fall through to "No Match"), so `is Number` is not
available.

**Supported contexts**: Same as comparison operators — `let`/`var` init
and assignment RHS at behavior level and in fn bodies. Works in
`&&`/`||` chains.

**Deferred**: `is` in function arguments.

## Logical operators (`&&` and `||`)

`&&` and `||` chain multiple boolean sub-expressions into a single
boolean value. Each sub-expression can be a comparison (with optional
arithmetic on either side), a type check (`ident is TypeName`), a bare
variable (truthy check), or a number literal (truthy check).
Same-operator chaining is supported
(`a > b && c < d && e > f`). Mixing `&&` and `||` at the same
parenthesization level is a compile error — use parentheses to
group: `(a > 1 || b < 2) && c > 3`. Different sub-expression types
(numeric comparisons, equality comparisons, type checks, and truthy
checks) can be freely mixed in the same chain — each term emits its
own independent check frame.

**`&&` frame pattern** (N terms → N+2 frames):

```
Frames 0..N-1: check_number, compare_register, or value_type per term

  For check_number (>, <, >=, <=):
  - true branch  → next check (or true frame for last)
  - false branch → shared false frame
  - equal (>/< ) → false frame (always explicit "next")
  - equal (>=/<= ) → next check (or true frame for last)

  For compare_register (==, !=):
  - == : Different → false, Equal → next check (or true for last)
  - != : Different → next check (or true for last), Equal → false

  For value_type (is):
  - matching type → next check (or true for last)
  - all other types + "next" → false

  For compare_register (truthy):
  - Different (non-empty) → next check (or true for last)
  - Equal (empty, "next") → false

Frame N:   set_reg false → target, next → N+2
Frame N+1: set_reg 1 → target (falls through)
```

**`||` frame pattern** (N terms → N+2 frames):

```
Frames 0..N-1: check_number, compare_register, or value_type per term

  For check_number (>, <, >=, <=):
  - true branch  → shared true frame
  - false branch → next check (or false frame for last)
  - equal (>/< ) → false target (always explicit "next")
  - equal (>=/<= ) → shared true frame

  For compare_register (==, !=):
  - == : Equal → true, Different → next check (or false for last)
  - != : Different → true, Equal → next check (or false for last)

  For value_type (is):
  - matching type → true
  - all other types + "next" → next check (or false for last)

  For compare_register (truthy):
  - Different (non-empty) → true
  - Equal (empty, "next") → next check (or false for last)

Frame N:   set_reg false → target, next → N+2
Frame N+1: set_reg 1 → target (falls through)
```

**Single comparison fallback**: When no `&&`/`||` follows the first
comparison, `emitBhvBoolExprTo` delegates to `emitComparison`
for the existing 3-frame pattern. This keeps single comparisons
unchanged.

**Supported contexts**: Same as single comparisons — `let`/`var` init
and assignment RHS at behavior level and in fn bodies.

**Parenthesized grouping**: Mixed `&&`/`||` is supported via
parenthesized sub-expressions (see "Parenthesized boolean expressions"
section below). Implicit precedence (without parens) is not supported.

**Deferred**: logical expressions in function call arguments.

## Structured locking with `locked`/`unlocked` blocks

**Lexically scoped mode blocks**: `locked { ... }` and `unlocked { ... }`
are block statements that set execution mode on entry and restore it on
exit. This replaces the old imperative `lock`/`unlock` keywords.

**Static mode tracking via `frameBuilder.mode`**: The `frameBuilder`
carries an `execMode` field (initially `modeLocked` for behaviors). Mode
transitions are emitted on-the-fly: `ModeBlockStmt` emission checks if
a transition is needed, emits the body, then restores mode. No post-parse
optimization pass is needed — `optimize.go` was deleted entirely.

**No `modeUnknown`**: Because mode blocks always restore, mode is
statically known at every program point. The `modeUnknown` constant was
removed. Mode after any statement = mode before it (since mode blocks
restore).

**No-op elimination**: When already in the target mode (e.g.,
`locked { ... }` when already locked), no transition frame is emitted.
Nested same-mode blocks (`unlocked { unlocked { ... } }`) emit no
transitions for the inner block.

**Cross-function tracking**: fn body mode blocks use the caller's
`frameBuilder`, so mode tracking flows naturally through inlined
function bodies. No cross-function analysis needed.

**Uniform handling**: `locked`/`unlocked` work in behavior bodies
(including if/else, while, and loop bodies) and fn bodies. They are
represented as `ModeBlockStmt` AST nodes with a `Body []Stmt` field.
Local `frameBuilder`s in `emitBhvIfStmt` and `emitBhvWhileStmt` are
initialized with `mode: b.mode` to inherit the enclosing mode.

## Control flow stubs

Control-flow instructions (branches, loops, terminals, jump/label) are
left as empty-body stubs with a `# control flow:` comment rather than
being implemented with `instruction`. They require compiler-level support
(emitting branch targets, loop structures) that the `instruction`
intrinsic can't express. The comment categorizes the type of control flow
for future implementation planning.

## Test file conventions

**Graph isomorphism for comparison**: Test `.json` files are compared
using graph isomorphism (`matchBehaviors`) rather than exact JSON
equality. This lets the compiler emit frames in any order without
breaking tests. Frame numbering is not semantically meaningful.

**Reference format in .json files**: Test `.json` files use the reference
JS codec's 0-based key format and are not modified programmatically. The
`refToNative` conversion in the test harness bridges to our 1-based
native format.

## Call-site direction annotations

**Mandatory annotation for out/inout**: Call sites must annotate `out`
and `inout` arguments explicitly to match the parameter's direction.
`fn my_fn(out foo, inout my_kw kw)` must be called as
`my_fn out x, inout my_kw: y`. This makes argument direction visible at
the call site without looking up the function definition.

**`in` is the default**: No annotation is needed for `in` parameters
(the common case), but explicit `in` is accepted for clarity:
`my_fn in x` is equivalent to `my_fn x` when the parameter is `in`.

**Annotation must match exactly**: An annotation that doesn't match the
parameter's effective direction is a compile error. `out` on an `in`
parameter, `in` on an `out` parameter, etc. are all rejected.

**Direction keywords are fully reserved**: `in`, `out`, and `inout`
cannot be used as variable names or parameter names. This avoids parsing
ambiguity — when the parser sees `out` before an argument value, it
always means a direction annotation.

**Uniform enforcement**: The same annotation rules apply at both behavior
level (`parseFnCallArgs`) and in fn bodies (`parseFnBodyCall`), for both
positional and keyword arguments. The shared `checkCallAnnotation` helper
validates the annotation against the parameter definition.

## Arithmetic expression operators (+, -, *, /)

`+`, `-`, `*`, `/` are expression operators that compile to the game's
`add`, `sub`, `mul`, `div` instructions. Each produces a single
instruction frame — no branching like comparison expressions.

**Syntax**: `let a = b + c`, `x = a - 3`, `let r = 5 + offset`.
The LHS is a variable, register, or number literal. The RHS is a number
literal or variable.

**Single-instruction mapping**: Each operator maps directly to one stdlib
opcode. `+` → `add`, `-` → `sub`, `*` → `mul`, `/` → `div`. The
compiler emits the frame directly (not via `expandCall`) since the
instruction format is uniform: slot 1 = LHS (to/from), slot 2 = RHS
(num), slot 3 = output target.

**LHS value semantics**: The game's arithmetic instructions preserve the
typed value from the first operand (slot 0/to/from). The number
component of the result is the arithmetic operation on both operands'
number components. This means `Item("metalbar") & 3` plus 2 yields
`Item("metalbar") & 5`.

**Number literal LHS supported**: Unlike comparison operators (which
defer number-literal LHS), arithmetic supports `let a = 5 + b` because
`add(5, b)` is meaningful — it adds b's number to 5.

**Supported contexts**: `let`/`var` init and assignment RHS at behavior
level and in fn bodies. Chained PEMDAS operations with `@arith` temp
variables. Arithmetic in function call arguments. Arithmetic within
comparison and boolean expression operands. Compound assignment RHS.

## Compound assignment operators (+=, -=, *=, /=)

`+=`, `-=`, `*=`, `/=` are compound assignment operators that read from
the target, apply the arithmetic operation, and write back. They compile
to a single instruction frame where the target appears in both the input
and output slots.

**Broadened RHS**: `+=` originally only accepted number literals. All
four compound operators now accept both number literals and variables
as the RHS operand (via `parseArithmeticRHS`).

**Unified handler**: `isCompoundAssignOp` and `compoundAssignOpName`
map all four compound tokens to their opcode.

## Increment/decrement (++, --)

`++` and `--` are sugar for `+= 1` and `-= 1`. `++` emits `add` with
`{"num": 1}`, `--` emits `sub` with `{"num": 1}`. Both use
`resolveAssignTarget` with `compound: true` (reads and writes the
target).

## Mutable variables (`var`) in fn bodies

`var` declares a mutable local variable in a function body. `let`
declarations are immutable. The `fnBodyContext.fnVars` map tracks
declared variables: `name → true` (mutable/var) or `name → false`
(immutable/let). Assignment validation happens at parse time via
`canAssign` (rejects `let` vars and `in` params) and `canCompound`
(additionally rejects `out` params since compound assignment reads).

The `fnBodyExprDir` function determines argument direction for fn body
call-site checking: `let` vars → `"in"`, `var` vars → `"inout"`,
params → their declared direction. This map replaces the old
`letVars map[string]bool` which only tracked let vars.

## Fn body control flow

`if`/`else if`/`else`, `while`, and `loop`/`break` work in fn bodies
with the same syntax as behavior level. Parsing uses `parseFnBodyIfStmt`,
`parseFnBodyWhileStmt`, `parseFnBodyLoopStmt` which delegate to
`parseFnBodyStmts` for nested blocks.

Conditions use the shared expression parsers (`parseBoolPrimary`,
`parseBoolChain`) via `fnBodyResolver`, supporting comparisons, type
checks, boolean chains, and parenthesized grouping.

Emission uses inline forward-jump patching:
- **IfStmt**: Emit check frame with placeholder false target → emit body
  → emit jump-to-continuation → patch false target → emit else/else-if.
- **WhileStmt**: Record loop start → emit check → emit body → last
  frame's `"next"` points back to start → patch false branches past
  body → scan for `@break` and patch to afterLoop.
- **LoopStmt**: If counted, delegate to `emitFnCountedLoop`. Otherwise,
  record loop start → emit body → scan for `@break` placeholder
  frames → last frame's `"next"` points to loop start → patch `@break`
  frames to point after loop.
- **BreakStmt**: Emits `{"op": "@break"}` placeholder (with optional
  `"label"` field for labeled breaks), patched by the enclosing loop
  or while emitter using label-aware matching.
- **ReturnStmt**: Emits values to `@retK` targets, zeros remaining
  slots, then emits `{"op": "@return"}` placeholder. `expandCall`
  patches `@return` frames to jump past the entire function expansion
  (same pattern as `@break` in loops).

## `break` in `while` loops

`break` works in both `loop` and `while` at behavior level and in fn
bodies.

At behavior level, `emitBehaviorStmts` handles `BreakStmt` directly
(emitting `{"op": "@break"}` with optional `"label"` field), and
detects the if/break pattern (`IfStmt` with single `BreakStmt` body,
no else) to route through `emitBhvIfBreak` instead of `emitBhvIfStmt`.
This avoids the issue where `emitBhvIfStmt` uses deferred bodies with
unset branch slots inside loop bodies (which would cause behavior
restart instead of continuation).

`emitBhvIfBreak` emits a check frame with the break-condition-true
path falling through to `@break`, and the break-condition-false path
explicitly jumping to continue. All 6 comparison operators are handled.
The break label is propagated from the `BreakStmt` to the `@break`
placeholder frame.

In fn bodies, `emitFnWhileStmt` scans body frames for `@break` after
emission and patches them to point after the loop, same pattern as
`emitFnLoopStmt`.

## Labeled loops and breaks

**Syntax**: `label: loop { ... }`, `label: while cond { ... }`,
`break label`. Labels follow identifier naming rules.

**Parser state**: Loop tracking uses `parser.loopDepth` (int, >0 when
inside a loop body) and `parser.loopLabels` (map of active labels).
This replaced the `inLoop bool` parameter that was previously threaded
through `parseBhvStmtBlockInner` and related functions. The parser
struct fields enable centralized validation without passing loop state
through every function signature. `enterLoop(label)` increments depth
and registers the label; `exitLoop(label)` decrements and unregisters.

**Label detection**: In all three parsing entry points
(`parseBehaviorBody` in codegen.go, `parseBhvStmtBlockInner` in
bhvast.go, `parseFnBodyStmtsInner` in parse.go), the default case
checks for the `ident: loop` / `ident: while` pattern via a
three-token lookahead (ident, colon, loop/while keyword). If detected,
the labeled loop/while parser is called; otherwise tokens are ungotten
and the default parsing logic continues.

**Duplicate label detection**: If a label is already in
`p.loopLabels`, it's a compile error (`"duplicate loop label %q"`).
This catches `x: loop { x: loop { } }`.

**Break with label parsing**: After scanning `break`, the parser peeks
at the next token. If it's an identifier that matches a label in
`p.loopLabels`, it's consumed as the break target. Otherwise the token
is ungotten and the break targets the innermost loop (empty label).

**@break placeholder with label**: `BreakStmt` emission produces
`{"op": "@break"}` for unlabeled breaks and
`{"op": "@break", "label": "name"}` for labeled breaks.

**Label-aware patching**: All 6 loop/while emitters (3 behavior-level:
`emitBhvWhileStmt`, `emitBhvLoopStmt`, `emitBhvCountedLoop`; 3 fn
body: `emitFnWhileStmt`, `emitFnLoopStmt`, `emitFnCountedLoop`) use
the same patching condition:
```
fLabel == "" || fLabel == myLabel
```
Unlabeled `@break` (fLabel == "") is always claimed by the innermost
loop. Labeled `@break` is claimed only by the matching loop. This
works with nesting because inner loops patch first during recursive
emission — by the time an outer loop scans for `@break`, all inner
breaks have already been resolved.

## Counted loops (`loop N { ... }`)

`loop` accepts an optional count expression: `loop 5 { ... }`,
`loop n { ... }`, `loop (a + b) { ... }`. When count is nil, the loop
is infinite (existing behavior). When count is non-nil, a counted loop
is emitted.

**AST**: `LoopStmt.Count Expr` — nil for infinite, non-nil for counted.

**Parsing**: Both `parseBhvLoopStmt` and `parseFnBodyLoopStmt` peek
after `loop`. If `{`, infinite. Otherwise parse count: number →
literal + arithmetic continuation, `(` → parenthesized arithmetic,
ident → resolve + arithmetic continuation.

**Counted loop frame layout**:
```
INIT:  set_number 0 → @loop       (counter = 0)
CHECK: check_number @loop vs limit (smaller falls through to body)
       checkLarger → EXIT, "next" → EXIT
BODY:  ... user body ...
INCR:  add @loop + 1 → @loop, next → CHECK
EXIT:  ... after loop ...
```

The counter variable `@loop` is allocated via `allocUniqueVar` to avoid
collisions. The check frame uses `checkSmaller` as the fall-through to
body (counter < limit means keep going), while `checkLarger` and `"next"`
(equal) both exit. `break` inside counted loops is patched to `EXIT`
the same way as infinite loops.

**Behavior level**: `emitBhvCountedLoop` uses `emitBehaviorStmts` with
a child builder for body emission, then rebases and copies. The
`mainFrameCount` return from `emitBehaviorStmts` is used to set the
main-line last frame's `"next"` to the increment frame (avoiding
deferred body interference).

**Fn body**: `emitFnCountedLoop` uses `emitFnBody` directly, sets the
last body frame's `"next"` to the increment frame.

## Enhanced `return` in fn bodies

`return` can appear anywhere in a function body, including inside control
flow blocks (`if`, `while`, `loop`). The compiler uses three paths based
on post-parse analysis of `ReturnStmt` nodes in the AST:

1. **Return-instruction path**: Single `return instruction` at end of
   top-level body. Converts to `InstructionStmt` + `rets` form with
   `@retK` names. Supports `fnDef.frame` promotion for pure instruction
   wrappers. Unchanged behavior from before.

2. **Zero-copy path**: Single `return` at end of top-level body with
   all `IdentExpr` values. Extracts ident names as `fn.rets`, removes
   `ReturnStmt` from body. Unchanged behavior from before.

3. **Emit-and-jump path**: Everything else — multiple returns, returns
   in blocks, returns with literals/calls. Sets `rets = ["@ret1", ...,
   "@retN"]` where N = max arity across all returns. `ReturnStmt` nodes
   stay in the body for `emitFnBody` to handle. Each `ReturnStmt` emits
   values to `@retK` targets, zeros remaining slots (for branches with
   fewer returns than max arity), then emits `{"op": "@return"}`.
   `expandCall` patches `@return` to jump past the function expansion.

**Max-arity rule**: The function's return count is the maximum arity
across all `ReturnStmt` nodes. Branches that return fewer values fill
remaining slots with `false` (null). This allows mixed arities like
`return separate_coordinate coord` (arity 2) in one branch and
`return coord, null` (arity 2) in another.

**Return item parsing**: `parseFnBodyReturnItem` parses each item in a
return list. If the item is a known function with returns, it's parsed
as a `CallExpr` via `parseFnBodyCallArgs`. Otherwise, it falls back to
`parseFnBodyExpr` (handles idents, numbers, null, constructors, `&`,
`$register`).

**Comma consumption fix**: `parseFnBodyCallArgs` only enters the keyword
argument parsing loop when the callee actually has keyword parameters
(`callee.positionalCount() < len(callee.params)`). This prevents the
trailing comma in `return my_fn arg, 5` from being consumed as a keyword
separator.

## Parenthesized boolean expressions

**Recursive tree model**: Boolean expressions are parsed into AST nodes
(`CompareExpr`, `TypeCheckExpr`, `TruthyExpr`, `BoolChainExpr`) that
form a recursive tree. `BoolChainExpr` has an `Op` (`&&` or `||`) and
`Children` list. Parentheses create nested `BoolChainExpr` nodes,
enabling mixed `&&`/`||` at different levels.

**Two-phase approach**: Parsing (`parseBhvBoolExpr` in `bhvast.go`)
produces AST nodes. Before emission, `resolveBhvBoolTree` resolves
operands into a `resolvedBoolExpr` tree (with `comparisonTerm` leaves).
Emission uses `emitBoolCheckFrame` (single term), which always sets
`"next"` on check frames.

**Single-leaf backward compatibility**: When `emitBhvBoolExprTo`
detects a single leaf (no `&&`/`||`), it delegates to the existing
`emitComparison`/`emitTypeCheck`/`emitTruthyCheck` functions. This
preserves the exact frame output for single comparisons (which omit
`"next"` as a fall-through optimization for `>`, `<`, and `!=`).

**Same-level operator enforcement**: Mixing `&&` and `||` at the same
parenthesization level is a compile error with a message suggesting
parentheses: `"cannot mix '&&' and '||' without parentheses; use '('
and ')' to group sub-expressions"`.

**Supported contexts**: `let`/`var` init and assignment RHS at behavior
level and in fn bodies. Works with all sub-expression types: numeric
comparisons, equality comparisons, and type checks, freely mixed.

**Deferred**: parenthesized expressions in function call arguments.

## Expression priority hierarchy

**Priority order** (highest first): arithmetic > comparisons > function
calls > boolean operators. This means `my_fn b + 1, c || d` parses as
`(my_fn(b+1, c)) || d`.

**PEMDAS arithmetic**: Chained arithmetic like `a + b * c` is parsed
into nested `ArithExpr` AST nodes respecting operator precedence
(`*`/`/` before `+`/`-`). During emission, `emitBhvArithTo` uses a
per-tree `arithCounter` to allocate `@arith`-prefixed temp variables
for intermediate results. `mul b, c → @arith1`, then
`add a, @arith1 → target`. The outermost operation writes directly to
the caller's target variable to avoid an extra copy.

**Truthy checks via `compare_register`**: Plain values in `&&`/`||`
chains (not comparisons) are tested for truthiness using
`compare_register value, false` — Different (non-empty) → truthy,
Equal (empty) → falsy. The internal sentinel `tokTruthy` in
`comparisonTerm.op` identifies these terms. The `emitTruthyCheck`
helper emits the standalone 3-frame pattern; `emitBoolCheckFrame`
handles `tokTruthy` within chained expressions.

**Function calls as first boolean term only**: `my_fn x || d` works
(function call is first, always executes, result goes to target, then
boolean check). `d || my_fn x` is deferred — it would require
interleaved frame emission for proper short-circuit semantics. After
a function call result, `maybeBhvExprContinuation` peeks for comparison,
`is`, or `&&`/`||` to compose the result into a larger expression.

**Arithmetic in function arguments**: `parseBhvArgExpr` checks for
arithmetic operators after parsing a number or variable argument. The
arithmetic parser produces `ArithExpr` AST nodes respecting PEMDAS
precedence. The parser naturally stops at non-arithmetic tokens (commas,
`&&`, `||`, `)`, etc.), so argument boundaries are respected.

**Supported contexts**: `let`/`var` init and assignment RHS at behavior
level and in fn bodies. Compound assignment RHS. Function call
arguments.

**Deferred**: function calls in non-first boolean position.

## Multi-arity expression lists

`let a, b, c = 1, 2, 3` declares multiple variables from a comma-separated
expression list. Each expression contributes its arity: 1 for simple
expressions (numbers, variables, constructors, arithmetic), and
`returnCount()` for function calls. The sum of arities must equal the
binding count, with one exception: the last item supports prefix matching
if it is a function call (same as standalone multi-return calls).

**Detection logic**: The expression list parser peeks at each item. If
the next token is an identifier that resolves to a known function with
returns (`p.fns[name] != nil && fn.hasReturn()`), it's parsed as a
`CallExpr` via `parseBhvCallArgs` / `parseFnBodyCallArgs`. Otherwise,
it's parsed as a simple expression via `parseBhvArgExpr` (behavior
level) or `parseFnBodyExpr` with arithmetic wrapping (fn bodies).

**AST representation**: When the RHS is a single item (function call
or simple expression), the existing `MultiReturnStmt{Value: CallExpr}`
representation is used directly. When there are multiple items, they
are wrapped in `ExprListExpr{Exprs: []Expr}`. This preserves backward
compatibility for the common single-function-call case.

**Variable registration timing**: Variables are registered in the
symbol table after all RHS parsing completes, except when the RHS
contains a function call — then variables are registered before
parsing call args (matching the existing behavior for single function
calls).

**Trailing comma fix**: `parseBhvCallArgs` now guards keyword arg
parsing with `fn.positionalCount() < len(fn.params)` (matching the
existing fn body version). This prevents expression list separator
commas from being consumed as keyword arg separators for functions
with no keyword params.

**Prefix matching on last item**: If the last expression list item is
a function call whose return count exceeds the remaining bindings,
the excess returns are silently discarded (same as standalone prefix
matching). Non-last items must have all their values consumed.

**Error diagnostics**: When a single function call doesn't fill all
bindings and no more items follow, the parser gives the specific
"too many bindings (N) for function X which returns M values" error
rather than a generic "expected comma" message.

**Emission**: `ExprListExpr` is emitted by iterating items. For each
`CallExpr`, `expandCall` is called with a slice of `retVals` covering
that call's arity. For simple expressions, `emitBhvExprTo` /
`emitExprTo` writes to the corresponding binding target. Discarded
bindings (`_`) skip emission for simple expressions.

**Supported contexts**: `let`/`var` multi-binding declarations at
behavior level and in fn bodies.

**Deferred**: expression lists in assignment (`x, y = 1, 2`) and
`return` statements.

## Mode block expressions (`locked`/`unlocked` as expressions)

`locked { ... }` and `unlocked { ... }` work as expressions where the
last item in the block is the value-producing tail. This extends the
existing statement-only mode blocks.

**AST representation**: `ModeBlockExpr` has `Unlock bool`, `Body []Stmt`
(leading statements), `Tail Expr` (the value-producing expression), and
`Comment string`. The internal `exprTailStmt` wrapper carries a bare
expression parsed at the end of a mode block body; it never appears in
the final AST.

**Parsing**: `parseBhvModeBlockExpr` and `parseFnBodyModeBlockExpr`
parse mode block expressions. They delegate to the existing statement
block parsers (`parseBhvStmtBlockInner` / `parseFnBodyStmtsInner`) with
an `exprTail` flag. When `exprTail` is true, the last item may be a
bare expression (function call, variable, number, constructor) wrapped
in `exprTailStmt`. The parser extracts the expression and discards the
wrapper.

**Arity**: Determined by `modeBlockExprArity(tail)`: if `CallExpr` →
`fn.returnCount()`, otherwise 1. Multi-return mode block expressions
work like multi-return function calls — `let x, y = unlocked {
separate_coordinate coord }`.

**Mode transition helpers**: `emitModeEntry(b, unlock, comment)` and
`emitModeExit(b, saved)` encapsulate the save/check/emit/restore
pattern. Both `ModeBlockStmt` and `ModeBlockExpr` emission use these
helpers.

**Emission**: `emitBhvModeBlockExpr` / `emitFnModeBlockExpr` handle
single-return cases (mode entry → body → tail to target → mode exit).
`emitBhvModeBlockExprMulti` / `emitFnModeBlockExprMulti` handle
multi-return cases where the tail is a `CallExpr` expanded with
`retVals`.

**Expression list support**: `ModeBlockExpr` is handled alongside
`CallExpr` in the `ExprListExpr` emission loops in both
`emitBhvStmtSimple` and `emitFnBody`, routing to single or multi
emitter based on arity.

**Supported contexts**: `let`/`var` init and assignment RHS at behavior
level and in fn bodies. Expression lists in multi-binding declarations.

**Deferred**: continuation after mode block (`locked { get_number v } + 1`),
mode blocks in function call arguments, mode blocks in `return` statement
items.

## If-expressions (`if`/`else if`/`else` as expressions)

`if`/`else if`/`else` can produce a value when used in expression context.
Each branch block contains optional statements followed by a
value-producing tail expression (the last item in the block). The `else`
clause is mandatory — without it, the expression would have no value on
the false path.

**AST representation**: `IfExpr` has `Cond Expr`, `Body []Stmt`,
`Tail Expr` (if-true branch value), `ElseIfs []ElseIfExprClause`,
`ElsBody []Stmt`, `ElsTail Expr` (else branch value), and `Comment`.
Each `ElseIfExprClause` has its own `Cond`, `Body`, and `Tail`. The
internal `exprTailStmt` wrapper carries a bare expression parsed at
the end of a branch body; it is extracted and never appears in the
final AST.

**Arity**: `ifExprArity` computes the maximum arity across all branches
recursively. This allows mixed-arity branches where some branches
return multi-return function calls. Branches with lower arity than
the maximum have remaining slots zeroed during emission. The generalized
`exprArity` helper replaces the old `modeBlockExprArity` — it handles
`CallExpr`, `IfExpr`, and `ModeBlockExpr` uniformly.

**Condition parsing**: If-expressions use the full boolean expression
parser (`parseBoolPrimary` + `parseBoolChain`) for conditions, not the
limited `parseBhvIfStmt` format. This is because if-expressions appear
in expression contexts where the full expression language is available.

**Emission**: At behavior level, `emitBhvIfExpr` uses the child
`frameBuilder` pattern per branch body (same as `emitBhvIfStmt`) to
isolate deferred body handling, followed by `rebaseFrameRefs`. Tail
expressions are emitted to the target after the branch body. Forward-jump
patching connects branches via `set_reg false false` placeholder frames.
At fn body level, `emitFnIfExpr` uses the simpler forward-jump pattern
(no child builders needed since fn bodies don't use deferred bodies).

**Multi-return**: `emitBhvIfExprMulti` and `emitFnIfExprMulti` handle
branches where the tail is a multi-return `CallExpr`. For single-return
tails in a multi-return context, the value goes to `retVals[0]` and
remaining slots are zeroed.

**Supported contexts**: `let`/`var` init and assignment RHS at behavior
level and in fn bodies. Expression lists in multi-binding declarations.
Mode block expression tails.

**Deferred**: if-expressions in function call arguments, if-expressions
in `return` statement items, continuation after if-expression
(`if cond { a } else { b } + 1`).
