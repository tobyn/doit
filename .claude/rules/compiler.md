---
paths:
  - "toolchain/compiler/**/*"
---

# doit Compiler

The `compiler` package compiles the doit language into the structured representation of a Desynced
behavior supported by the `codec` package. See `.claude/rules/behavior_json.md` for the compiled
output format.

## Architecture

- **`compiler/ast.go`** — AST node type definitions: `Stmt` interface
  (13 concrete types: `CallStmt`, `LetStmt`, `AssignStmt`,
  `CompoundAssignStmt`, `IncrDecrStmt`, `MultiReturnStmt`,
  `InstructionStmt`, `ModeBlockStmt`, `ReturnStmt`, `IfStmt`, `WhileStmt`,
  `LoopStmt`, `BreakStmt`) and `Expr` interface (12 concrete types:
  `LiteralExpr`, `IdentExpr`, `CallExpr`, `InstructionExpr`,
  `ArithExpr`, `CompareExpr`, `TypeCheckExpr`, `TruthyExpr`,
  `BoolChainExpr`, `ConstructorExpr`, `AmpersandExpr`, `ExprListExpr`)
- **`compiler/compiler.go`** — Public API (`Compile`, `CompileString`), shared types
  (`fnDef`, `symbolTable`, `unitRegisters`),
  `paramDef` (with `direction` field, `effectiveDirection()`)
  and `paramInfo` (with `direction` field) types, direction compatibility
  via `canPass(paramDir, argDir)`, `frameBuilder`/`frameRef` abstraction
  for frame management, `rebaseFrameRefs` for shifting `frameRef` values
  when transplanting body frames into a parent builder,
  `check_number` slot constants (`checkLarger`,
  `checkSmaller`, `checkValue`, `checkTarget`),
  `compare_register` slot constants (`compareRegDifferent`,
  `compareRegValue1`, `compareRegValue2`),
  `value_type` slot constants (`valueTypeInput`, `valueTypeItem`,
  `valueTypeUnit`, `valueTypeComp`, `valueTypeTech`, `valueTypeValue`,
  `valueTypeCoord`), `allTypeSlots` (the 6 branch slot keys),
  `typeCheckSlot` helper (maps constructor names to slot keys),
  the `setComment` helper for setting `"cmt"` on frames,
  `allocUniqueVar` helper for inline variable renaming,
  `execMode` type with `modeLocked`/`modeUnlocked`
  constants for compile-time execution mode tracking
- **`compiler/scanner.go`** — `scanner` struct (embedded by `parser`, holds `locale`
  field), token types (including `tokAmpersand` for `&`,
  `tokDoubleAmpersand` for `&&`, `tokDoublePipe` for `||`,
  `tokNotEquals` for `!=`, `tokPlus`/`tokMinus`/`tokStar`/`tokSlash`
  for arithmetic operators, `tokMinusMinus`/`tokMinusEquals`/
  `tokStarEquals`/`tokSlashEquals` for compound assignment/decrement,
  `tokIs` for the internal-only `is` type check operator,
  `tokTruthy` for the internal-only truthy check in boolean chains),
  `Keywords` map (includes `"is"`)
  (includes type constructor names, direction keywords, and `locked`/`unlocked`), `isConstructor`
  helper, `isDirection` helper, `$`-prefix scanning, error formatting,
  `parseLocalePrefix` helper, `resolveLocalizedDocComment` for localized
  `#!` comments
- **`compiler/parse.go`** — Stdlib parsing (delegates to `parseUserFn`),
  file-level parsing, function definitions with `instruction` support,
  `fnBodyResolver` (operandResolver for fn bodies),
  `fnBodyContext` (tracks paramDirs, fnVars, resolve for fn body parsing),
  fn body AST parsing (`parseFnBodyStmts`, `parseFnBodyReturnItem`,
  `parseFnBodyLetVar`, `parseFnBodyRHSExpr`, `parseFnBodyIfStmt`,
  `parseFnBodyElseIfChain`, `parseFnBodyWhileStmt`,
  `parseFnBodyLoopStmt`, `parseFnBodyExpr`,
  `parseFnBodyConstructorExpr`, `parseFnBodyCallArgs`,
  `fnBodyExprDir`, `checkFnBodyCallDirectionsExpr`),
  post-parse analysis (`collectReturnStmts`, `returnStmtArity`,
  `tryPromoteInstruction`),
  fn body AST emission (`emitFnBody`, `emitExprGetValue`, `emitExprTo`,
  `emitFnArithTo`, `emitFnArithNode`, `emitFnBoolExprTo`,
  `resolveFnBoolTree`, `emitFnIfStmt`, `emitFnWhileStmt`, `emitFnLoopStmt`,
  `emitConstructorTo`, `emitAmpersandTo`, `emitCallExprArgs`,
  `collectASTOutputVars`, `resolveVarName`, `tryResolveConstructorLiteral`,
  `tryResolveAmpersandLiteral`),
  call expansion with `[]any`/`map[string]any` argument types,
  fn body instruction direction enforcement
  (`checkFnBodyInstructionDirections`)
- **`compiler/bhvast.go`** — Behavior-level AST parsers and emitters,
  plus shared expression parsers. `operandResolver` type and
  `bhvResolver` factory for behavior-level operand resolution.
  Shared expression parsers (parameterized by `operandResolver`,
  used by both behavior and fn body paths): `parseArithExpr`/
  `parseArithExprFrom`/`parseArithExprFromFull`/`parseArithTerm`/
  `parseArithTermFrom`/`parseArithPrimary` (PEMDAS arithmetic →
  `ArithExpr`), `parseBoolExpr`/`parseBoolPrimary`/`parseBoolChain`
  (boolean expressions → `BoolChainExpr`/`CompareExpr`/
  `TypeCheckExpr`/`TruthyExpr`). Behavior-level argument parser:
  `parseBhvArgExpr`. Constructor parsers: `parseBhvConstructorExpr`/
  `parseBhvAmpersandExpr`. Statement parsers:
  `parseBhvVarInit` → `LetStmt`/`AssignStmt`,
  `parseBhvDefaultStmt` → `CallStmt`/`AssignStmt`/`CompoundAssignStmt`/
  `IncrDecrStmt`, `parseBhvIfStmt` → `IfStmt`, `parseBhvWhileStmt` →
  `WhileStmt`, `parseBhvLoopStmt` → `LoopStmt`, `parseBhvMultiReturn`
  → `MultiReturnStmt`, `parseBhvStmtBlock`/`parseBhvStmtBlockInner`
  (full statement set in inner blocks including var/let/instruction/
  control flow/break). Emitter functions: `emitBehaviorStmts` (top-level
  behavior emitter with deferred body management, break target patching,
  and mode tracking), `emitBhvStmtSimple` (non-control-flow statements),
  `emitBhvExprTo`/`emitBhvExprGetValue` (expression emission),
  `emitBhvArithTo`/`emitBhvArithNode` (arithmetic with per-tree
  `arithCounter`), `emitBhvBoolExprTo` (boolean expression emission with
  single-leaf delegation to `emitComparison`/`emitTypeCheck`/
  `emitTruthyCheck`), `emitBhvCallStmt` (function call emission),
  `emitBhvModeBlock` (mode block emission with on-the-fly transitions),
  `emitBhvIfStmt`/`emitBhvWhileStmt`/`emitBhvLoopStmt` (control flow
  emission). Internal types: `resolvedBoolExpr` (pre-resolved boolean
  tree for emission)
- **`compiler/codegen.go`** — Behavior body dispatch and shared helpers:
  `parseBehaviorBody` (two-phase: parse `@name`/`@param` attributes +
  statements into `[]Stmt` via `parseBhv*` functions, then emit via
  `emitBehaviorStmts` with `frameBuilder.mode` tracking),
  `resolveInstructionFrame` (0→1 key conversion
  and slot substitution), `frameHasReturnSlot`/`frameReturnCount`,
  direction enforcement (`argDirection`, `checkReadable`,
  `checkCallDirections`, `checkInstructionDirections`,
  `checkCallAnnotation`), `resolveAssignTarget`, single-expression
  emitters (`emitComparison`, `emitTypeCheck`, `emitTruthyCheck`,
  `emitBoolCheckFrame`, `comparisonTerm` type), arithmetic/comparison
  op helpers (`isArithmeticOp`, `arithmeticOpName`, `isComparisonOp`,
  `isEqualityOp`, `isCompoundAssignOp`, `compoundAssignOpName`,
  `isHighPriorityArithOp`, `isLowPriorityArithOp`), type check helpers
  (`isTypeCheckOp`, `parseIsRHS`), `parseParamAttr`, `checkVarName`,
  `parseName`, `parseLocalize`, `matchLocale` shared BCP 47 helper
- **`compiler/tests/`** — Test case pairs: `.doit` (source) + `.json` (expected compiled
  output)

The compiler is structured as a standalone `scanner` struct embedded in a
recursive-descent `parser`. The scanner tokenizes the source into identifiers
(including `$`-prefixed unit register names), string literals, numbers,
braces, parentheses, colons, commas, `@`, and comparison/assignment
operators, skipping whitespace and `#` line comments. Both function bodies
and behavior bodies use an AST approach: parse into `[]Stmt` with
`Expr` nodes (defined in `ast.go`), then emit frames. Both paths use
`frameBuilder.mode` for on-the-fly execution mode tracking. For fn bodies,
`emitFnBody` runs during `expandCall` inlining. For behavior bodies,
`parseBehaviorBody` collects statements into `[]Stmt` using `parseBhv*`
parsers (in `bhvast.go`), then `emitBehaviorStmts` walks the AST to emit
frames. Errors include line:column positions. Wire format details
(like Lua's 1-based indexing) are encapsulated at the `frameBuilder`
boundary — compilation logic uses 0-based indices internally, and `frameRef`
values are converted to 1-based wire format integers by `finalize`. The
exported `Keywords` map lists all reserved keywords for use by editor tooling.

Brace-delimited blocks fall into two categories. **Statement blocks** (behavior
bodies, function bodies, if/else/while/loop bodies) all contain a sequence of
statements and can be parsed uniformly. **Structured data blocks** (the
`instruction` intrinsic and `localize { ... }` blocks) each have their own
parsing rules and semantics.

**Statement termination** (not yet implemented): The language is line-oriented.
Statements terminate at end-of-line by default, with three exceptions:
(1) block-ending statements extend to `}` and peek for `else`/`else if`
continuation; (2) parenthesized function calls extend to the closing `)`
across lines; (3) unparenthesized function calls with a trailing comma
continue onto the next line. The scanner currently treats newlines as plain
whitespace; implementing these rules will require newline awareness.

**Function calls**: Parentheses are optional. `notify "Hello"` and
`notify("Hello")` are equivalent. The no-parens style is preferred for
statement-level calls. Parenthesized calls will become useful for argument
grouping in complex expressions.

**Doc comments** (`#!`): The scanner collects `#!` lines into a
`docComment` field, reset on each `skipWhitespaceAndComments` call and
preserved across `unget`. The parser captures `docComment` after scanning
the first token of each statement, then passes it through compilation. For
instruction-based stdlib calls, the comment is set as `"cmt"` on the
emitted frame. For user-defined function calls, each body call uses its
own `#!` comment if present, otherwise inherits the caller's comment,
recursively up the call stack. Doc comments support localized text via a
`(locale)` prefix on each `#!` line (e.g., `#! (en) English text`). The
first line's presence of a prefix determines the mode: if present, all
lines are parsed as localized entries; otherwise, they are joined as plain
text. Continuation lines without a prefix append to the previous locale's
text. The `resolveLocalizedDocComment` method on `scanner` handles this,
using the shared `matchLocale` helper for BCP 47 matching. The
`parseLocalePrefix` package-level function extracts the locale code from
a `(locale) text` pattern.

Behavior IDs can be bareword identifiers or quoted strings. The `@name` attribute sets the display
name (at most once per behavior); if omitted, the behavior ID is used as the default name. The
`@param` attribute declares behavior parameters (see "Behavior parameters" below). Both `@name`
and `@param` accept the `localize` intrinsic for localized strings
(`@name localize { en_US "English" ja "日本語" }`), which selects the best match for the
compiler's locale setting using `golang.org/x/text/language`. The `localize` intrinsic is a
compile-time string construct usable anywhere a string argument is expected (e.g., function
call arguments).

The `Compile` and `CompileString` functions accept an `fs.FS` containing the standard library, a
`behaviorID string` that selects which behavior to compile, and a `locale string` (BCP 47 tag) for
resolving `localize` blocks. When `behaviorID` is empty and the source contains a
single behavior, it is auto-selected. When the source contains multiple behaviors,
`behaviorID` must name one of them. When `locale` is empty, `localize` blocks use
their first entry. The compiler parses stdlib function definitions first, then compiles
user source. Stdlib functions that contain an `instruction` intrinsic are inlined at call
sites — the compiler substitutes arguments into the instruction template fields.
Numeric keys in instruction templates are converted from 0-based (reference
format) to 1-based (native wire format) during expansion by `resolveInstructionFrame`.
Stdlib parsing is unified: `parseStdlibFile` delegates to `parseUserFn`, which handles
`instruction`, `return instruction`, empty bodies, and call-based bodies uniformly.
Pure instruction wrappers (functions whose body is a single `instruction` block with
only param/ret references) are automatically promoted to `fnDef.frame` for the fast
direct-frame expansion path.

**Function parameters** support keyword arguments with the `keyword varname`
syntax in parameter lists (e.g., `fn notify(txt, value v, timeout t)`).
All positional parameters must precede keyword parameters. At call sites,
keyword args follow positional args after a comma: `notify "Hello!", timeout: "10"`.
Keyword args are optional — omitting one omits the corresponding field from
the compiled instruction. The `paramDef` type tracks each parameter's name
and keyword (empty for positional). Helper methods on `fnDef` support
keyword lookup and positional counting. The `positionalParam(i)` method
returns the i-th positional parameter (0-based), skipping keyword params.

**Parameter direction** annotations (`in`, `out`, `inout`) are declared in
parameter lists: `fn writer(out target)`. Direction defaults to `in` when
omitted. Direction keywords (`in`, `out`, `inout`) are fully reserved —
they cannot be used as variable or parameter names (`checkVarName` rejects
them).

**Call-site direction annotations**: Call sites must annotate `out` and
`inout` arguments to match the callee's parameter direction. For example,
`fn writer(out target)` must be called as `writer out z`. Explicit `in`
is accepted but not required. Mismatched or missing annotations are compile
errors. Both `parseBhvCallArgs` (behavior level) and `parseFnBodyCallArgs`
(fn body) peek for a direction keyword before each positional argument
and before each keyword name. The shared `checkCallAnnotation` helper
validates the annotation against the parameter's `effectiveDirection()`.
For keyword arguments, the direction annotation precedes the keyword name:
`my_fn 1, out kw: z`.

**Direction enforcement** has two layers. First, **annotation validation**
(`checkCallAnnotation`) checks that call sites provide the correct direction
keyword — `out`/`inout` arguments must be explicitly annotated, `in` is
implicit. Second, **compatibility checking** (`checkCallDirections` at
behavior level, `checkFnBodyCallDirectionsExpr` in fn bodies) verifies that
the argument's inherent direction is compatible with the parameter's direction
using `canPass(paramDir, argDir)`. The `canPass` function enforces: `in`
params accept `in`/`inout` args, `out` params accept `out`/`inout` args,
`inout` params accept only `inout` args.

Argument directions are determined by `argDirection` (behavior level) and
`fnBodyExprDir` (fn bodies). At behavior level, `argDirection` looks up the
argument in the symbol table: `let` variables and `in` parameters are `in`,
`var` variables and `inout` parameters are `inout`, `out` parameters are
`out`. In fn bodies, `fnBodyExprDir` examines `Expr` AST nodes: `IdentExpr`
names are checked against parameter directions and let-variable sets.

In fn body `instruction` blocks, direction enforcement uses the `@N`
convention: non-`@N` slots are inputs (reads), `@N` slots are outputs
(writes). `checkFnBodyInstructionDirections` verifies that `out` parameters
don't appear in non-`@N` positions. This check runs for all four instruction
paths in fn bodies: bare `instruction`, `return instruction`, `let =
instruction` (single and multi-return). Functions that are pure instruction
wrappers get promoted to `fnDef.frame` for fast expansion, which bypasses
fn body direction checks. Direction enforcement for these functions happens
at the call site when the wrapper is called as a regular function.

**Return values**: Functions can produce one or more return values.

`return` is always parsed into `ReturnStmt` AST nodes by
`parseFnBodyStmts`. Post-parse analysis in `parseUserFn` determines one
of three paths:

1. **Return-instruction path**: Single `return instruction` at end of
   top-level body. Converted to `InstructionStmt` with `@retK` slot
   replacement. Supports `fnDef.frame` promotion via
   `tryPromoteInstruction`.

2. **Zero-copy path**: Single `return` at end of top-level body with
   all `IdentExpr` values. Ident names extracted as `fn.rets`,
   `ReturnStmt` removed from body. In `expandCall`, each `fn.rets[i]`
   is added to `paramMap` with `retVals[i]` as its value, so body calls
   write directly into caller's return targets with no copies.

3. **Emit-and-jump path**: Multiple returns, returns in blocks, or
   returns with literals/calls. Sets `fn.rets = ["@ret1", ..., "@retN"]`
   where N = max arity across all returns. `ReturnStmt` nodes stay in
   the body. During `emitFnBody`, each `ReturnStmt` emits values to
   `@retK` targets (via `emitExprTo` for simple values, `expandCall`
   for `CallExpr`, `resolveInstructionFrame` for `InstructionExpr`),
   zeros remaining slots, then emits `{"op": "@return"}` placeholder.
   `expandCall` patches `@return` frames to jump past the function
   expansion (same pattern as `@break` in loops).

Return items are parsed by `parseFnBodyReturnItem`: function calls with
returns become `CallExpr`, everything else goes through `parseFnBodyExpr`.
Multi-value return uses comma-separated items:
`fn get_xy(coord) { let x, y = separate_coordinate coord; return x, y }`.
Return names are stored in `fnDef.rets` (a `[]string`; nil/empty = no
return). `returnCount()` returns `len(rets)` for body-based functions.

The `@N` syntax inside an `instruction` block marks output slots as
return values:
`fn get_self() { return instruction "get_self" { 0: @1 } }`. Each `@N` is
stored in the instruction frame as a `returnSlot(N)` value. The `@N`
values must form a contiguous sequence starting from `@1` — gaps (e.g.,
`@1` and `@3` with no `@2`) are compile errors. `returnCount()` for
instruction-based functions counts `returnSlot` values in the frame.
During `expandCall`, `returnSlot(N)` values are replaced with `retVals[N-1]`
(or `false` if the caller provides fewer bindings or discards that
position). The `returnSlot` type is defined in compiler.go.

**`instruction` as general expression**: The `instruction` intrinsic works
everywhere — not just in stdlib function definitions. It can be used as:
- Bare statement: `instruction "op" { ... }` (behavior level and fn bodies)
- Single-return: `let x = instruction "op" { 0: @1 }` (behavior level and fn bodies)
- Multi-return: `let x, y = instruction "op" { 0: @1, 1: @2 }` (behavior level and fn bodies)
- Assignment: `x = instruction "op" { 0: @1 }` (behavior level)
- `return instruction`: `return instruction "op" { 0: @1 }` (fn bodies)

At behavior level, `instruction` expressions go through `resolveInstructionFrame`
which handles 0→1 key conversion and `@N` slot substitution. In fn bodies,
`instruction` blocks are stored as `InstructionStmt` or `InstructionExpr` AST
nodes containing `map[string]any` frames. During `emitFnBody`, these frames
are resolved through `resolveInstructionFrame` with `paramMap` substitution.

In stdlib files, `return instruction` is the preferred form for functions
with output slots. The `@1` in the frame is what drives `hasReturn()`.
Plain `instruction` (without `return`) remains valid for functions with no
output slots. Stdlib parsing now uses `parseUserFn`, so all fn body syntax
(including function calls, constructors, and `instruction`) is available
in stdlib files.

The `fnDef.hasReturn()` method delegates to `returnCount() > 0`, which
checks both mechanisms (rets for body-based, returnSlot count for
instruction-based). All call-site error checks ("has no return value")
use `hasReturn()`.

**Single-return call sites**: Functions with returns can be called via
assignment syntax (`let x = get_self`, `var x = get_self`, `x = get_self`).
When called as a bare statement (no assignment), `expandCall` receives
`nil` for `retVals` and substitutes `false` for all return slots.

**Multi-return call sites (binding lists)**: Destructuring syntax captures
multiple return values: `let x, y = separate_coordinate coord`. Binding
lists support mixed modifiers:
`var a, b, _, let c, var d = my_fn args`. Rules:
- `let`/`var` set the active modifier (sticky for subsequent bare idents)
- `_` discards that return position (does not change active modifier)
- Bare idents with an active modifier declare new variables
- Bare idents with no active modifier assign to existing variables
- Binding lists must start with `_`, `let`, or `var` (bare-ident-first
  like `a, b = fn` is not supported due to parsing ambiguity)
- **Prefix matching**: binding count must be <= `returnCount()`; extra
  returns are silently discarded. `let x = fn_with_3_returns` captures
  the first return only.
- `_` as a standalone variable name (`var _ = 5`, `let _ = 5`) is a
  compile error.

The `parseBhvMultiReturn` helper in bhvast.go handles behavior-level
binding list parsing, producing `MultiReturnStmt` AST nodes. The RHS
can be a single function call, an `instruction` block, or a
comma-separated expression list (`ExprListExpr`). The expression list
parser detects function calls by checking `p.fns[name]` and falls back
to `parseBhvArgExpr` for simple expressions. During emission,
`emitBhvStmtSimple` builds a `retVals []any` slice (name strings for
new bindings, resolved targets for existing-var assignments, `false`
for `_`) and passes it to `expandCall` (for single calls) or iterates
`ExprListExpr.Exprs` (for expression lists). The `retVals []any`
parameter on `expandCall` carries return targets through the call chain.

**fn body multi-return**: In function bodies, `let` supports multi-return
binding with `_` discards: `let x, y = separate_coordinate coord` and
`let _, y = separate_coordinate coord`. No modifier switching — all names
are `let` locals. Each binding becomes a `MultiBinding` in
`MultiReturnStmt.Bindings` (with `Name` for idents, `Discard: true` for
`_`). The RHS supports expression lists (same as behavior level).
During emission, `emitFnBody` resolves bindings through `paramMap`
and passes the result slice as `retVals` to the recursive `expandCall`.
`var` is supported in fn bodies for mutable local variables. `let` is
immutable. Multi-return binding lists in fn bodies use the modifier of
the leading keyword (`let` or `var`) for all bindings.

The `parseBhvCallArgs` helper (bhvast.go) extracts behavior-level
positional + keyword arg parsing into a reusable method shared by bare
function calls, `let`/`var` declarations, and assignment-from-function-call.
Similarly, `parseFnBodyCallArgs` (parse.go) extracts fn body call argument
parsing into AST `Expr` nodes, shared by regular calls and `let` in fn
bodies.

**Inline variable renaming**: When a user-defined function is inlined via
`expandCall`, its internal variables (those not mapped through `paramMap`
as parameters or return values) could collide with variables already in
use at the behavior level. The compiler automatically renames colliding
variables by appending a disambiguating suffix (`_2`, `_3`, etc.).

The mechanism works as a pre-scan in `emitFnBody`. Before iterating over
`fn.astBody`, `collectASTOutputVars` scans all `LetStmt`, `AssignStmt`,
and `MultiReturnStmt` nodes for output variable names not already in
`paramMap`. For each, it calls `allocUniqueVar(name, usedVars)` which
returns the original name if unused or `name_2`, `name_3`, etc. if
there's a collision. The result is added to `paramMap`, so
`resolveVarName` resolves both output targets and input references to
the renamed value automatically. The `usedVars map[string]bool` on
`symbolTable` tracks all variable names in use across the behavior; it
is threaded through `expandCall` and all call sites in codegen.go.

**Symbol table**: During behavior compilation, a `symbolTable` tracks
`@param` declarations (with `$name` keys mapping to 1-based indices,
direction, and display names), `var` declarations (mutable), `let`
declarations (immutable), and `usedVars` (all variable names in use, for
inline rename collision detection). Variables can be initialized with a
number literal (`let x = 5`), a function call with a return value
(`let me = get_self`), or an inline instruction
(`let me = instruction "get_self" { 0: @1 }`). Assignment (`x = ...`)
also supports number literals, function calls, and inline instructions. Both `var` and `let` allow shadowing —
redeclaring a variable with the same name overwrites the previous symbol
table entry. The new declaration's mutability applies going forward.
Every `let`/`var` declaration also registers the name in `usedVars`.
Unit registers (`$signal`, `$visual`, `$store`, `$goto`) are a package-level
`unitRegisters` map. The symbol table is threaded through all compilation
functions via a `syms *symbolTable` parameter.

**Rich argument types**: At behavior level, function arguments accept
string literals (`"hello"` → string), number literals
(`42` → `map[string]any{"num": 42}`), `null` (`false`), `$`-prefixed
references (unit register negative ints or parameter 1-based indices),
bare identifiers (variable name strings), `localize { ... }` blocks
(resolved to a string at compile time), type constructors
(`Item("metalbar")`, `Component("behavior")`, `Technology("signals2")`,
`Value("pentagon")`, `Coordinate(x, y)`), and the `&` operator for
attaching numeric components. `$name` resolution order:
unit register → parameter. The same resolution applies to assignment
targets (`=`, `+=`, `++`), with an immutability check for `let` variables.

**Type constructors**: `Item`, `Component`, `Technology`, `Value` take a
single string argument. `Component` prefixes `c_`, `Technology` prefixes
`t_`, `Value` prefixes `v_`. `Coordinate` takes two arguments that can be
literals (compile-time) or variables (emits `combine_coordinate` at
runtime). Constructor names are reserved keywords via `isConstructor()`.
The `&` operator follows a constructor or value and merges a `"num"` field
(compile-time) or emits `set_number` (runtime). At behavior level,
constructors and `&` are parsed into `ConstructorExpr`/`AmpersandExpr` AST
nodes by `parseBhvConstructorExpr`/`parseBhvAmpersandExpr`. During emission,
`emitBhvExprTo` resolves compile-time literals directly and emits runtime
instructions targeting the declared variable name.

In function bodies, arguments are parsed into `Expr` AST nodes.
`LiteralExpr` holds compile-time values (numbers, `null`, `$register`
refs, and compile-time type constructors). `IdentExpr` holds variable
references. `ConstructorExpr` and `AmpersandExpr` represent runtime
constructors. During emission (`emitFnBody`), `emitExprGetValue` and
`emitExprTo` resolve expressions: compile-time literals flow through
as values, while runtime constructors emit frames (e.g.,
`combine_coordinate` or `set_number`) with `@ctor`-prefixed temp
variable names allocated via `allocUniqueVar`. For `let x =
Constructor(args)` in fn bodies, `emitConstructorTo` writes directly
to the declared variable target, avoiding an extra copy. The
`expandCall` function uses `[]any`/`map[string]any` for args and
kwArgs, allowing non-string values to flow through to instruction
template substitution.

**Behavior parameters**: Declared with the `@param` attribute before any
instructions: `@param <direction> <name> <display>` where direction is
`in`, `out`, or `inout`. The display name can be a string literal or a
`localize { ... }` block (same as `@name`). Each parameter gets a 1-based index in
declaration order. References use the `$name` syntax (same prefix as unit
registers). Resolution order: unit registers first, then parameters.
Duplicate parameter names and conflicts with built-in unit register names
are compiler errors. The compiler emits `"parameters"` (array of default
values, currently all `false`) and `"pnames"` (array of display name
strings) in the behavior JSON. Maximum 10 parameters.

**Positional arg separators**: At behavior level, commas between positional
arguments are optional. This preserves backward compatibility with
string-only args (which are unambiguous without commas) while supporting
the natural `set_reg x, $store` style with mixed types.

**Arithmetic expressions**: `+`, `-`, `*`, `/` work as expression operators
in `let`/`var` init, assignment RHS, compound assignment RHS, function call
arguments, and comparison operands at behavior level. Each maps to a
single instruction: `+` → `add`, `-` → `sub`, `*` → `mul`, `/` → `div`.
Chained arithmetic follows PEMDAS precedence (`*`/`/` before `+`/`-`).
At behavior level, `parseBhvArithExpr` (in `bhvast.go`) produces nested
`ArithExpr` AST nodes. During emission, `emitBhvArithTo` uses a per-tree
`arithCounter` to allocate `@arith`-prefixed temp variables for
intermediate results. The outermost operation writes directly to the
caller's target. Parenthesized arithmetic `(b + c) * d` is supported.
Compound assignment (`+=`, `-=`, `*=`, `/=`) and decrement (`--`) are
also supported.

**Comparison expressions**: `>`, `<`, `>=`, `<=`, `==`, and `!=` work as
boolean expression operators in `let`/`var` init and assignment RHS at
behavior level. Syntax: `let result = a > b`, `x = a < 5`,
`let r = a >= 3`, `let eq = a == b`, `let ne = a != null`,
`let x = 5 > b`, `let x = a + 1 > b - 2`.
Numeric comparisons (`>`, `<`, `>=`, `<=`) emit a 3-frame
`check_number` + `set_reg` pattern. Equality comparisons (`==`, `!=`)
emit a 3-frame `compare_register` + `set_reg` pattern (see decisions.md
for frame layouts). `compare_register` is a 2-way branch
(Different / Equal) that compares full register composites, not just
numeric components. The `isEqualityOp` helper distinguishes equality ops
from numeric ops. Number literal LHS is supported (`5 > b`). Both LHS
and RHS can include arithmetic expressions. At behavior level,
`parseBhvBoolPrimary` parses comparison operands and produces
`CompareExpr` AST nodes. During emission, `emitBhvBoolExprTo` resolves
operands and delegates to `emitComparison` (for single comparisons) or
the recursive `emitResolvedBoolFrames` (for chains).
The `&&` and `||` operators chain multiple comparisons, type checks, and
truthy values into a single boolean expression: `let r = a > 2 && b < 10`,
`let r = x && y`, `let r = get_number x || d`.
The `is` operator checks whether a value is a specific data type:
`let a = x is Unit`. It compiles to a 3-frame `value_type` + `set_reg`
pattern (same structure as comparisons). The `isTypeCheckOp` helper
identifies `tokIs` terms, `parseIsRHS` validates the type name via
`typeCheckSlot`, and `emitTypeCheck` emits the frames. The `is` keyword
scans as `tokIdent` with val `"is"` — `tokIs` is only used internally
in `comparisonTerm.op`. `tokTruthy` is an internal-only token kind
used in `comparisonTerm.op` to identify truthy check terms (bare
variables or numbers tested for non-emptiness via `compare_register`).
Different expression types (numeric comparisons, equality comparisons,
type checks, and truthy checks) can be freely mixed in the same
`&&`/`||` chain — each term emits its own independent check frame.
Parenthesized sub-expressions allow mixing `&&` and `||` at different
nesting levels: `(a > 1 || b < 2) && c > 3`. The recursive
`BoolChainExpr` AST node supports arbitrary nesting depth. Mixing `&&`
and `||` at the same parenthesization level is a compile error.
Function call results can compose with boolean operators:
`let a = my_fn x || d` (function call as first boolean term);
`maybeBhvExprContinuation` handles peeking for comparison/is/&&/||
after a computed value.

**`rebaseFrameRefs`**: Returns a new slice of frame maps with all `frameRef`
values shifted by an offset. Non-destructive (creates copies). Called at
body transplant sites in `emitBhvIfStmt`, `emitBhvWhileStmt`, and the
deferred body loop in `emitBehaviorStmts`.

## Test Case Format

Each test case is a pair of files sharing the same base name in `compiler/tests/`:
- **`.doit`** — a doit language source file
- **`.json`** — The expected JSON representation of the compiler output

For multi-behavior test cases, the file name uses the `__` convention: the part after `__` is the
behavior ID passed to the compiler. For example, `multi_behavior__second.doit` compiles the
`second` behavior and compares against `multi_behavior__second.json`.

Tests are in the root `main_test.go`. `TestCompile` compiles each test case, encodes via `Compile`,
decodes, and compares against the JSON file. `TestCompileErrors` tests error cases (e.g., multiple
behaviors without `-b`, nonexistent behavior ID, no behaviors) using `compiler.CompileString`
directly.

The JSON in the JSON file may differ from a JSON rendering of the compiled code in trivial
ways (e.g., whitespace, object key ordering). Do not rely on the JSON strings to be the
same. The compiler may also emit frames in a different order than the handwritten expected
output — the test comparison uses graph-isomorphism (`matchBehaviors` in `main_test.go`)
to verify structural equivalence regardless of frame numbering.

The `.json` test files were generated from the reference JavaScript codec and
should not be modified programmatically. When our implementation's output format
differs from the reference (e.g., 1-based vs 0-based integer keys), the test
code bridges the gap via the `refToNative` conversion routine in `main_test.go`
rather than modifying the test data.

### Locale directive

Test `.doit` files can specify a compilation locale via a `# locale: <tag>`
comment on the second line (after `# AI-generated test`). The `TestCompile`
harness reads this directive and passes the locale to the compiler. If
absent, the locale defaults to `""` (first entry wins).

### AI-generated tests

AI-generated `.doit` test files are marked with a `# AI-generated test`
comment on their first line. When creating a new test case, add this comment.

All files belonging to an AI-generated test case (`.doit`, `.json`, and any
other associated files) may be edited freely to fix or improve them.
All files belonging to a test case without this marker are human-authored
and should not be modified programmatically.

### Error case coverage

New language features must include error case tests in `TestCompileErrors`
(or `TestDecodeErrors` for codec changes), not just happy-path `.doit`/`.json`
pairs. Cover at minimum: invalid syntax the user is likely to write by
mistake, and each explicit error path added by the implementation. For
example, keyword arguments added tests for unknown keywords, duplicate
keywords, positional-after-keyword in definitions, and extra positional
args at call sites.
