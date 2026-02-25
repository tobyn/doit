---
paths:
  - "toolchain/compiler/**/*"
---

# doit Compiler

The `compiler` package compiles the doit language into the structured representation of a Desynced
behavior supported by the `codec` package. See `.claude/rules/behavior_json.md` for the compiled
output format.

## Architecture

- **`compiler/compiler.go`** — Public API (`Compile`, `CompileString`), shared types
  (`fnDef`, `fnBodyArg`, `fnBodyCall` with `frame` field, `symbolTable`,
  `unitRegisters`), `paramDef` (with `direction` field, `effectiveDirection()`)
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
  `execMode` type with `modeLocked`/`modeUnlocked`/`modeUnknown`
  constants for compile-time execution mode tracking
- **`compiler/scanner.go`** — `scanner` struct (embedded by `parser`, holds `locale`
  field), token types (including `tokAmpersand` for `&`,
  `tokDoubleAmpersand` for `&&`, `tokDoublePipe` for `||`,
  `tokNotEquals` for `!=`, `tokPlus`/`tokMinus`/`tokStar`/`tokSlash`
  for arithmetic operators, `tokMinusMinus`/`tokMinusEquals`/
  `tokStarEquals`/`tokSlashEquals` for compound assignment/decrement,
  `tokIs` for the internal-only `is` type check operator),
  `Keywords` map (includes `"is"`)
  (includes type constructor names, direction keywords, and `lock`/`unlock`), `isConstructor`
  helper, `isDirection` helper, `$`-prefix scanning, error formatting,
  `parseLocalePrefix` helper, `resolveLocalizedDocComment` for localized
  `#!` comments
- **`compiler/parse.go`** — Stdlib parsing (delegates to `parseUserFn`),
  file-level parsing, function definitions with `instruction` support,
  lock/unlock handling in fn bodies (emitted as `fnBodyCall` with inline frames),
  call expansion with `[]any`/`map[string]any` argument types, inline
  frame expansion for `fnBodyCall.frame`, fn body direction enforcement
  (`fnBodyArgDir`, `checkFnBodyCallDirections`,
  `checkFnBodyInstructionDirections`)
- **`compiler/codegen.go`** — Behavior body compilation: param/let/var declarations,
  symbol table tracking, rich argument parsing, assignment target resolution
  (with direction checks in `resolveAssignTarget`), direction enforcement
  (`argDirection`, `checkReadable`, `checkCallDirections`,
  `checkInstructionDirections`, `checkCallAnnotation`),
  `resolveInstructionFrame` helper for 0→1 key conversion and slot substitution,
  `frameHasReturnSlot`/`frameReturnCount` helpers, `instruction` as expression
  in let/var/assign/multi-return, arithmetic expression helpers
  (`isArithmeticOp`, `arithmeticOpName`, `parseArithmeticRHS`),
  compound assignment helpers (`isCompoundAssignOp`, `compoundAssignOpName`),
  comparison expression helpers
  (`emitComparison`, `resolveComparisonOperand`, `parseComparisonRHS`,
  `isComparisonOp`, `isEqualityOp`),
  type check helpers (`isTypeCheckOp`, `parseIsRHS`, `emitTypeCheck`),
  logical operator helpers (`parseAndEmitBooleanExpr`,
  `comparisonTerm`, `boolExpr`, `parseBoolTerm`, `parseBoolExprFull`,
  `parseBoolExprChain`, `emitBoolCheckFrame`, `emitBoolExprFrames`,
  `emitBoolExprTree`),
  lock/unlock keyword handling with compile-time mode tracking
  (in `parseBehaviorBody`, `compileBody`, `compileLoop`),
  loops, if/else, deferred body emission,
  `matchLocale` shared BCP 47 matching helper
- **`compiler/tests/`** — Test case pairs: `.doit` (source) + `.json` (expected compiled
  output)

The compiler is structured as a standalone `scanner` struct embedded in a
recursive-descent `parser`. The scanner tokenizes the source into identifiers
(including `$`-prefixed unit register names), string literals, numbers,
braces, parentheses, colons, commas, `@`, and comparison/assignment
operators, skipping whitespace and `#` line comments. The parser consumes
tokens via the promoted scanner methods and directly emits the
`*codec.Object` output (type `Behavior`) via `frameBuilder` without an
intermediate AST. Errors include line:column positions. Wire format details
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
errors. Both `parseFnCallArgs` (behavior level) and `parseFnBodyCall`
(fn body) peek for a direction keyword before each positional argument
and before each keyword name. The shared `checkCallAnnotation` helper
validates the annotation against the parameter's `effectiveDirection()`.
For keyword arguments, the direction annotation precedes the keyword name:
`my_fn 1, out kw: z`.

**Direction enforcement** has two layers. First, **annotation validation**
(`checkCallAnnotation`) checks that call sites provide the correct direction
keyword — `out`/`inout` arguments must be explicitly annotated, `in` is
implicit. Second, **compatibility checking** (`checkCallDirections` at
behavior level, `checkFnBodyCallDirections` in fn bodies) verifies that the
argument's inherent direction is compatible with the parameter's direction
using `canPass(paramDir, argDir)`. The `canPass` function enforces: `in`
params accept `in`/`inout` args, `out` params accept `out`/`inout` args,
`inout` params accept only `inout` args.

Argument directions are determined by `argDirection` (behavior level) and
`fnBodyArgDir` (fn bodies). At behavior level, `argDirection` looks up the
argument in the symbol table: `let` variables and `in` parameters are `in`,
`var` variables and `inout` parameters are `inout`, `out` parameters are
`out`. In fn bodies, `fnBodyArgDir` maps function parameter directions
through to the body-level calls.

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

The `return` statement in a function body declares which local names are
the function's return values. Single return:
`fn locate_self() { let me = get_self; let coord = get_location me; return coord }`.
Multi-value return uses comma-separated items:
`fn get_xy(coord) { let x, y = separate_coordinate coord; return x, y }`.
Return items can be identifiers, number literals, or `null`. Literals are
desugared into synthetic body calls (`set_number` for numbers, `set_reg`
for null) with `@retK` synthetic names that can't collide with user
identifiers. Return names are stored in `fnDef.rets` (a `[]string`;
nil/empty = no return). `returnCount()` returns `len(rets)` for body-based
functions. In `expandCall`, each `fn.rets[i]` is added to `paramMap` with
`retVals[i]` as its value, so body calls that reference the returned names
write directly into the caller's return targets with no copies. The
`return` statement is a compile-time binding — it does not emit a runtime
instruction.

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
`instruction` blocks are stored as `fnBodyCall` entries with the `frame` field
set (a `map[string]any`). During `expandCall`, these inline frames are resolved
through `resolveInstructionFrame` with `paramMap` substitution.

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

The `compileMultiReturn` helper in codegen.go handles behavior-level
binding list parsing. It builds a `retVals []any` slice (name strings for
new bindings, resolved targets for existing-var assignments, `false` for
`_`) and passes it to `expandCall`. The `retVals []any` parameter on
`expandCall` carries return targets through the call chain.

**fn body multi-return**: In function bodies, `let` supports multi-return
binding with `_` discards: `let x, y = separate_coordinate coord` and
`let _, y = separate_coordinate coord`. No modifier switching — all names
are `let` locals. Each binding becomes a `fnBodyArg` in `fnBodyCall.retArgs`
(name idents or `{literal: false}` for discards). During expansion,
`resolveBodyArg` resolves each retArg through `paramMap` and passes the
result slice as `retVals` to the recursive `expandCall`. No `var` in fn
bodies — mutability is a behavior-level concept.

The `parseFnCallArgs` helper (codegen.go) extracts positional + keyword arg
parsing into a reusable method shared by bare function calls, `let`/`var`
declarations, and assignment-from-function-call. Similarly, `parseFnBodyCall`
(parse.go) extracts fn body call argument parsing shared by regular calls
and `let` in fn bodies.

**Inline variable renaming**: When a user-defined function is inlined via
`expandCall`, its internal variables (those not mapped through `paramMap`
as parameters or return values) could collide with variables already in
use at the behavior level. The compiler automatically renames colliding
variables by appending a disambiguating suffix (`_2`, `_3`, etc.).

The mechanism works as a pre-scan in `expandCall`'s body-based expansion
path. Before iterating over `fn.body`, it scans all `retArgs` with
`isIdent == true` that are not already in `paramMap` (i.e., internal
variables, not parameters or return values). For each, it calls
`allocUniqueVar(name, usedVars)` which returns the original name if
unused or `name_2`, `name_3`, etc. if there's a collision. The result
is always added to `paramMap`, so `resolveBodyArg` resolves both output
slots (retArgs) and input references (args/kwArgs) to the renamed value
automatically. The `usedVars map[string]bool` on `symbolTable` tracks
all variable names in use across the behavior; it is threaded through
`expandCall` and all call sites in codegen.go.

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
(compile-time) or emits `set_number` (runtime). For `let`/`var`
declarations, `parseConstructorForTarget` avoids extra `set_reg` copies by
directly targeting the declared variable name. In `compileDefaultStatement`
assignments, the simpler `parseArgValue` path is used.

In function bodies (`fnBodyArg`), numbers, `null`, `$register`, and
compile-time type constructors are pre-resolved at parse time into
the `literal` field. Runtime constructors (e.g., `Coordinate(x, y)` with
variable args, `Item("metalbar") & count`) emit synthetic `fnBodyCall`
entries (like `combine_coordinate` or `set_number`) with `@ctorN`
synthetic variable names. The `parseFnBodyArgValue` method returns
`(fnBodyArg, []fnBodyCall, error)` — the middle value carries these
synthetic setup calls. `parseFnBodyCall` collects all synthetic calls
from its arguments and prepends them to the main call in its return
slice. For `let x = Constructor(args)` in fn bodies, the last synthetic
call's retArg is rewritten to target the declared variable directly,
avoiding an extra copy. Identifier arguments that refer to function
parameters are resolved at expansion time via `resolveBodyArg` and the
`paramMap`. The `expandCall` function uses `[]any`/`map[string]any` for
args and kwArgs, allowing non-string values to flow through to instruction
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
in `let`/`var` init and assignment RHS at behavior level. Each maps to a
single instruction: `+` → `add`, `-` → `sub`, `*` → `mul`, `/` → `div`.
The compiler emits the instruction frame directly. LHS can be a variable,
register, or number literal. RHS can be a number literal or variable.
Compound assignment (`+=`, `-=`, `*=`, `/=`) and decrement (`--`) are also
supported. `+=` was broadened to accept variable RHS (previously
number-only).

**Comparison expressions**: `>`, `<`, `>=`, `<=`, `==`, and `!=` work as
boolean expression operators in `let`/`var` init and assignment RHS at
behavior level. Syntax: `let result = a > b`, `x = a < 5`,
`let r = a >= 3`, `let eq = a == b`, `let ne = a != null`.
Numeric comparisons (`>`, `<`, `>=`, `<=`) emit a 3-frame
`check_number` + `set_reg` pattern. Equality comparisons (`==`, `!=`)
emit a 3-frame `compare_register` + `set_reg` pattern (see decisions.md
for frame layouts). `compare_register` is a 2-way branch
(Different / Equal) that compares full register composites, not just
numeric components. The `isEqualityOp` helper distinguishes equality ops
from numeric ops. Parsing is integrated into `compileVarInit` and
`compileDefaultStatement`: when the RHS ident is not a known function,
the parser peeks for a comparison operator (via `isComparisonOp`, which
covers all 6 operators) before reporting "unknown function". The helpers
`emitComparison`, `resolveComparisonOperand`, and `parseComparisonRHS`
handle the emission and operand validation. `parseComparisonRHS` accepts
number literals, identifiers, and `null`. Comparison expressions inside
`compileBody` (if/while bodies) use `frameRef` values, which are rebased
via `rebaseFrameRefs` when the body frames are transplanted into the
parent `frameBuilder`. The `&&` and `||` operators chain multiple
comparisons into a single boolean expression: `let r = a > 2 && b < 10`.
The `is` operator checks whether a value is a specific data type:
`let a = x is Unit`. It compiles to a 3-frame `value_type` + `set_reg`
pattern (same structure as comparisons). The `isTypeCheckOp` helper
identifies `tokIs` terms, `parseIsRHS` validates the type name via
`typeCheckSlot`, and `emitTypeCheck` emits the frames. The `is` keyword
scans as `tokIdent` with val `"is"` — `tokIs` is only used internally
in `comparisonTerm.op`. After parsing the first comparison or type
check, `parseAndEmitBooleanExpr` wraps it in a `boolExpr` leaf and
calls `parseBoolExprChain`; if no `&&`/`||` follows, it delegates to
`emitComparison` or `emitTypeCheck`; otherwise, `emitBoolExprTree`
emits the recursive frame pattern. Different expression types
(numeric comparisons, equality comparisons, and type checks) can be
freely mixed in the same `&&`/`||` chain — each term emits its own
independent check frame. Parenthesized sub-expressions allow mixing
`&&` and `||` at different nesting levels:
`(a > 1 || b < 2) && c > 3`. The recursive `boolExpr` tree model
supports arbitrary nesting depth. Mixing `&&` and `||` at the same
parenthesization level is a compile error. `compileVarInit` and
`compileDefaultStatement` handle `tokLParen` as a boolean expression
entry point via `parseBoolExprFull`.

**`rebaseFrameRefs`**: Returns a new slice of frame maps with all `frameRef`
values shifted by an offset. Non-destructive (creates copies) to handle the
shared-frames case in `compileElseClauses` where `== else` shares
`elseFrames` between two deferred body entries. Called at all four body
transplant sites: `compileWhile`, `compileIfStmt` `>=` inline,
`compileIfStmt` `==` inline, and `parseBehaviorBody` deferred body loop.

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
