# Design Decisions

Non-obvious choices and their rationale. Helps future sessions make
consistent decisions without re-deriving past conclusions.

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
behavior level and in function bodies. At behavior level, runtime
constructors emit frames via `expandCall` with `frameBuilder`. In fn
bodies, they emit synthetic `fnBodyCall` entries with `@ctorN` temp
variable names (using the same `@`-prefix convention as `@retK` return
desugaring to avoid collisions with user identifiers).

**`parseConstructorForTarget` optimization**: For `let`/`var`
declarations, the compiler avoids an extra `set_reg` copy by passing the
declared variable name directly as the output target for runtime
constructor instructions. The general-purpose `parseArgValue` path
(used in function call arguments and assignments) allocates a temporary
variable instead, which is simpler but produces one extra frame for
runtime cases.

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
level. Number literals, variable identifiers, and `null` are valid as
the RHS operand.

**Deferred**: fn body comparison expressions (requires branching in
flat `fnBodyCall` list); comparison in function call arguments (parsing
ambiguity); number literal LHS (`5 > b` — use `b < 5` instead);
constructor RHS (`a == Item("metalbar")`).

## Type check operator (`is`)

`is` checks whether a value matches one of the six game data types. It
compiles to a 3-frame `value_type` + `set_reg` + `set_reg` pattern,
following the same boolean expression convention as comparison operators.

**Syntax**: `let a = b is Unit`. The LHS is a variable or parameter
(resolved via `resolveComparisonOperand`). The RHS is one of the six
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
and assignment RHS at behavior level. Works in `&&`/`||` chains.

**Deferred**: fn body `is` expressions; `is` in function arguments.

## Logical operators (`&&` and `||`)

`&&` and `||` chain multiple boolean sub-expressions into a single
boolean value. Each sub-expression can be a comparison
(`ident >|<|>=|<=|==|!= number|ident|null`) or a type check
(`ident is TypeName`). Same-operator chaining is supported
(`a > b && c < d && e > f`). Mixing `&&` and `||` at the same
parenthesization level is a compile error — use parentheses to
group: `(a > 1 || b < 2) && c > 3`. Different sub-expression types
(numeric comparisons, equality comparisons, and type checks) can
be freely mixed in the same chain — each term emits its own
independent check frame.

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

Frame N:   set_reg false → target, next → N+2
Frame N+1: set_reg 1 → target (falls through)
```

**Single comparison fallback**: When no `&&`/`||` follows the first
comparison, `parseAndEmitBooleanExpr` delegates to `emitComparison`
for the existing 3-frame pattern. This keeps single comparisons
unchanged.

**Supported contexts**: Same as single comparisons — `let`/`var` init
and assignment RHS at behavior level.

**Parenthesized grouping**: Mixed `&&`/`||` is supported via
parenthesized sub-expressions (see "Parenthesized boolean expressions"
section below). Implicit precedence (without parens) is not supported.

**Deferred**: fn body logical expressions; logical expressions in
function call arguments; fn body `is` expressions.

## Lock/unlock as keywords with compile-time mode tracking

**Keywords, not stdlib functions**: `lock` and `unlock` are language
keywords, not stdlib function wrappers. They were removed from
`instructions.doit` and are now handled directly by the compiler. This
enables compile-time optimization that stdlib functions cannot provide.

**Frame-scanning for mode tracking**: Rather than precomputing mode
effects on user-defined functions, the behavior-level mode tracker
scans newly emitted frames after each `default` case statement. Since
all function calls are inlined via `expandCall`, any lock/unlock inside
a called function appears as a frame in the main builder. Scanning
after each statement catches these automatically — no per-function
metadata needed.

**Redundant elimination**: The compiler tracks an `execMode` (locked,
unlocked, or unknown) starting at `modeLocked`. A `lock` when already
locked or `unlock` when already unlocked is simply not emitted. After
control flow (`if`/`while`/`loop`), mode resets to `modeUnknown`
(conservative), so subsequent lock/unlock is always emitted.

**Uniform handling**: `lock`/`unlock` work in behavior bodies,
`compileBody` (if/else bodies), `compileLoop` bodies, and fn bodies
(as `fnBodyCall` entries with inline frames). In fn bodies, they flow
through the existing `call.frame != nil` path in `expandCall`.

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
literal or variable (resolved via `resolveComparisonOperand` for
readability checking and `$` resolution).

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
level. Single operation per expression (no chaining).

**Deferred**: fn body arithmetic expressions; chained operations
(`a + b + c`); arithmetic in function call arguments.

## Compound assignment operators (+=, -=, *=, /=)

`+=`, `-=`, `*=`, `/=` are compound assignment operators that read from
the target, apply the arithmetic operation, and write back. They compile
to a single instruction frame where the target appears in both the input
and output slots.

**Broadened RHS**: `+=` originally only accepted number literals. All
four compound operators now accept both number literals and variables
as the RHS operand (via `parseArithmeticRHS`).

**Unified handler**: `isCompoundAssignOp` and `compoundAssignOpName`
map all four compound tokens to their opcode. The handler in
`compileDefaultStatement` uses `parseArithmeticRHS` for the RHS.

## Increment/decrement (++, --)

`++` and `--` are sugar for `+= 1` and `-= 1`. `++` emits `add` with
`{"num": 1}`, `--` emits `sub` with `{"num": 1}`. Both use
`resolveAssignTarget` with `compound: true` (reads and writes the
target).

## Parenthesized boolean expressions

**Recursive tree model over flat chain**: The flat `[]comparisonTerm`
chain model was replaced with a recursive `boolExpr` tree. Each node
is either a leaf (single comparison/type-check) or a group (children
connected by `&&` or `||`). Parentheses create nested groups, enabling
mixed `&&`/`||` at different levels.

**`boolExpr` struct**: `term *comparisonTerm` (leaf) or
`chainOp`+`children []*boolExpr` (group). `isLeaf()` and
`frameCount()` methods support both emission paths.

**Recursive parser**: Three functions handle parsing:
- `parseBoolTerm(syms)`: Parses `(expr)` (recursive) or `ident op rhs`.
- `parseBoolExprFull(syms)`: Entry point — calls `parseBoolTerm` then
  `parseBoolExprChain`.
- `parseBoolExprChain(first, syms)`: Collects same-operator terms;
  errors on mixed `&&`/`||` at the same level.

**Recursive emitter**: Three functions handle emission:
- `emitBoolCheckFrame(term, true, false, b, comment)`: Emits one
  check frame with explicit true/false targets. Always sets `"next"`.
- `emitBoolExprFrames(expr, true, false, b, comment)`: Recursive.
  For `&&`: child true → next child (or parent true for last), child
  false → parent false. For `||`: child true → parent true, child
  false → next child (or parent false for last).
- `emitBoolExprTree(expr, target, b, comment)`: Top-level wrapper that
  allocates false/true `set_reg` frames.

**Always-set `"next"` on check frames**: The new emitter always sets
`"next"` on every check frame (via `emitBoolCheckFrame`). The old
emitter omitted `"next"` as a fall-through optimization for `>` and
`<` in certain positions. This simplification makes the emitter uniform
and the frame structure more predictable.

**Single-leaf backward compatibility**: When `parseAndEmitBooleanExpr`
detects a single leaf (no `&&`/`||`), it delegates to the existing
`emitComparison`/`emitTypeCheck` functions. This preserves the exact
frame output for single comparisons. `tokLParen` paths in
`compileVarInit` and `compileDefaultStatement` also check `isLeaf()`.

**Same-level operator enforcement**: Mixing `&&` and `||` at the same
parenthesization level is a compile error with a message suggesting
parentheses: `"cannot mix '&&' and '||' without parentheses; use '('
and ')' to group sub-expressions"`.

**Supported contexts**: `let`/`var` init and assignment RHS at behavior
level. Works with all sub-expression types: numeric comparisons,
equality comparisons, and type checks, freely mixed.

**Deferred**: fn body parenthesized boolean expressions; parenthesized
expressions in function call arguments.
