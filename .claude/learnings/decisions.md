# Design Decisions

Non-obvious choices and their rationale. Helps future sessions make
consistent decisions without re-deriving past conclusions.

For implementation details (frame layouts, internal function names,
emission patterns), read the source code directly. For continuation
system decisions, see `continuations.md`.

## Compiler-generated `@`-prefixed variable names

The compiler generates temporary variables with `@`-prefixed names
via `allocUniqueVar()`. These are **ordinary VM variable names** —
the `@` prefix has no special meaning to the game engine. It avoids
collisions with user-defined variables since `@` is not valid in
doit identifiers.

Separately, `@break`, `@return`, and `@exec_<name>` are
**control-flow placeholder opcodes** (not variable names). They
appear in intermediate frames and are patched to real jump targets
before finalization.

## Consistent invocation syntax

Language constructs that take arguments should follow function call
syntax by default: paren-optional, comma-separated positional args,
`key: value` keyword args. Diverge only with justification.

This applies to keywords like `label`, `jump`, `assert`, and even
zero-arg keywords like `exit`, `restart`, `last` (which accept
optional empty parens: `exit()`). The goal is learnability — once
you know function call syntax, you know how to use keywords too.

Zero-arg branching functions (`sequence`, `for_component`, etc.) and
zero-arg iterators (`each_component`, etc.) naturally work without
parens — the parser's peek-based disambiguation handles this correctly
with no code changes needed.

Constructs with legitimate divergence:
- `on` — event handler semantics are structurally different
  (parameter binding + block body, not a call)
- `for...in` — iterator consumption syntax, not invocation
- `wait` — single expression, not a call (though it could
  accept parens for consistency)

## `exit` keyword

`exit` is a language keyword (not a stdlib function). It emits
`{"op": "exit"}` — a terminal instruction. The old `exit()` stdlib
stub was removed. Accepts optional empty parens (`exit()`) for
consistency with function call syntax.

## `last` keyword

`last` is a language keyword (not a stdlib function). It emits
`{"op": "last"}` — a terminal instruction that stops the current
iterator. Mirrors the `exit` pattern exactly: dedicated AST node
(`LastStmt`), marked terminal (triggers unreachable code warnings),
no "next" field in output. No location restrictions — allowed
anywhere, same as `exit`. Accepts optional empty parens (`last()`).

## Unreachable code detection

Code after `exit`, `last`, `break`, or `return` is unreachable. The
compiler warns and skips remaining statements in the block.

**Label exemption**: `label` statements are exempt from unreachable-code
pruning. A `label` after a terminal statement (e.g., `exit`) resets the
terminal flag and resumes normal parsing. This is necessary because labels
are jump targets — they must be emitted even if not reachable by
fallthrough. The exemption is implemented in all three parser locations:
behavior bodies (`codegen.go`), inner blocks (`bhvast.go`), and function
bodies (`parse.go`).

**Break label syntax**: Labels use a `'` sigil (`'outer`), making
them syntactically distinct via `tokLabel`. No disambiguation
heuristic needed — `break 'label` is unambiguous.

## Behaviors as top-level functions

A behavior is conceptually a top-level function with extra syntax
sugar (parameters as external registers, behavior-level `default`
case dispatch). Behavior blocks and function bodies support almost
the exact same language constructs — this is a core design principle.

## Stdlib function signatures

- **Output parameters as return values**: Callers use
  `let x = get_self` instead of `get_self x`.
- **Optional inputs as keyword params**: Avoids forcing callers to
  pass placeholders for unused optional slots.
- **`"c"` mode fields**: Exposed via keyword parameters and stdlib
  mode enums.
- **`"txt"` fields as positional strings**: Natural call-site syntax
  (`notify "Hello"`).

## Boolean literals

`true` → `{"num": 1}`, `false` → `false` (Go bool, empty register).
Matches VM truthy convention. `false` and `null` compile identically.
The `'` sigil on labels means `true:` and `false:` can never be
confused with loop labels.

## Multiple return values

- **Contiguous sequence required**: `@N` values must start from `@1`
  with no gaps.
- **Prefix matching**: Callers can bind fewer returns than produced.
- **`_` as discard**: Maps to `false` in compiled output.
- **Mixed modifier binding lists**: `var a, b, _, let c = fn args`.
  Modifiers are sticky. Must start with `_`, `let`, or `var`.
- **Multi-value `return`**: `return x, y, z`. Literals desugared into
  synthetic body calls with `@retK` names.

## Type literal constructors

- **Capitalized names**: `Item("metalbar")` distinguishes constructors
  from function calls and reserves lowercase namespace.
- **Namespace prefixes hidden**: `Component`, `Technology`, `Value`
  auto-prepend `c_`, `t_`, `v_`.
- **Compile-time vs runtime**: Decided by AST node types, not resolved
  values after parameter substitution.
- **Constructor target optimization**: `let`/`var` declarations pass
  the declared variable directly as output target, avoiding a copy.

## Instruction metadata limitations

The compiler cannot assume all instructions are defined in the stdlib
(Desynced supports user mods). The `instruction` intrinsic exists
partly for modded instructions.

## Return/parameter name collision handling

When `return` references a parameter name, `expandCall` detects the
collision and emits a `set_reg` frame to copy the parameter value to
the caller's return target.

## Truthy semantics for `if x`

`if x` tests whether the register is **non-empty** (has been assigned any
value), not whether the numeric component is non-zero. This uses
`compare_register x, false` — the same instruction used for equality checks.

Consequence: `var x = 0; if x` is **truthy** because `{"num": 0}` is a
value-bearing register distinct from empty. Users who want numeric non-zero
checks use `if x != 0` or `if x > 0`.

Rationale: the VM's empty-vs-populated distinction is fundamental to
Desynced programming (checking whether a scan found something, whether a
parameter was passed, etc.). "Has a value" is the most useful default for
bare truthiness. Non-numeric types (Item, Unit, etc.) are also truthy under
this model, which would be surprising under `check_number` semantics.

## Boolean expressions

**Comparison operators**: Numeric comparisons (`>`, `<`, `>=`, `<=`)
use `check_number` (3-way branch). Equality comparisons (`==`, `!=`)
use `compare_register` (2-way) for full register composite equality.

**Constant-folded boolean truthiness**: When a boolean expression is
fully constant-folded (e.g., `const CF_CMP = 5 > 3`), it becomes
`{"num": 1}` (true) or `false`. Using it in `if CF_CMP` compiles to
`compare_register {"num": 1}, false` — this is correct. The VM treats
"different" (exec 0) as the truthy branch. With `stripFallThrough`,
the exec 0 slot is removed when the truthy branch is the next frame,
relying on VM fall-through. This pattern was verified in-game.

**Type check (`is` / `!is`)**: `is Number` is not supported because
`value_type` cannot distinguish numbers from null. `!is` is a shorthand
for negated type checks — `x !is Unit` compiles identically to
`!(x is Unit)`. Parsed via two-token lookahead (`!` + `is`) with
`save`/`restore` fallback in both `parseBoolPrimary` and
`maybeExprContinuation`.

**Precedence**: arithmetic > comparisons > function calls > negation
(`!`) > boolean operators (`&&`/`||`). `&&` binds tighter than `||`.

**Negation**: Swap-targets approach — no new opcodes, just swaps
`trueTarget`/`falseTarget`. De Morgan's law pushes negation to leaves.

## Structured locking

Static mode tracking — no `modeUnknown`. Mode is always statically
known. No-op elimination when already in target mode. Cross-function
tracking flows through inlined function bodies.

## Stdlib branching function conventions

All ~69 stdlib branching functions have `exec(...)` signatures.
Key conventions:

- Output params become `@N` slots, removed from the param list.
- `check_number`/`compare_register`/`value_type` have exec signatures
  AND hard-coded compiler paths — both coexist.
- `build`/`build_registered`/`produce_registered` omit `bp`/`frame`
  metadata fields (use `instruction` intrinsic for those).

## Call-site direction annotations

`out` and `inout` arguments must be annotated at the call site.
Mismatched annotations are compile errors. `in`, `out`, `inout` are
reserved keywords.

## Unary minus

Handled in `parseArithPrimary` as the single source of truth.
Number literals fold at compile time (`-5` → `LiteralExpr{-5}`).
Variables desugar to `0 - expr`.

## Control flow emission (unified)

Both behavior-level and fn body control flow share a single set of
emitters in `codegen.go`, parameterized by `*emitContext`. Two
constructors — `bhvEmitCtx` and `fnEmitCtx` — build the context
with closures.

`break` emits `{"op": "@break"}` placeholder. `return` emits values
to `@retK` targets then `{"op": "@return"}`.

## `break` back-edge for if/break as last statement

When `if ... { break }` is the last statement in a loop body, all
four loop/while emitters emit a noop back-edge frame after it.
Without this, the false branch would point past the loop, causing
exit after one iteration.

## Labeled loops and breaks

Labels use the `'` sigil (`'outer: loop { break 'outer }`), scanned
as `tokLabel` tokens. `@break` placeholder includes optional `"label"`
field. Patching matches unlabeled breaks to innermost loop, labeled
breaks to the named loop.

## `break` in exec blocks — decoupled from `last`

`break` in an exec block means "exit this block invocation" without
assuming iterator semantics. `loopDepth` and `loopLabels` are
saved/reset at exec block entry; `execBlockDepth` tracks nesting to
allow `break` without a surrounding loop. In detached blocks, `@break`
becomes a noop (`set_reg false false` with `"next": false`) that
re-dispatches to the iterator without stopping it. These noops are
eliminated by `eliminateNoopBridges` — numbered exec slot references
are replaced with `false`, which correctly triggers re-dispatch (the
VM treats `false` in any exec slot as "no connection" / re-dispatch).
In bridging blocks, `@break` jumps to the join point. Users who need
`last` use the `last` keyword (terminal — no `break` needed after
it). `for...in` loops still emit `last` automatically (the compiler
controls that path). Labeled `break` across an exec block boundary is
a parse error (labels are not visible).

## Range `for` loops — `for_number` emission

Three emission paths:
- **Literal Range** (`emitForStmtRange`): Evaluates start/stop/step
  from the constructor args, emits a single `for_number` frame.
- **Nested literal Range** (`emitForStmtRangeManual`): When
  `forNumberDepth > 0`, emits manual counter instructions. Uses
  1-based slot keys and slot constants (not 0-based like stdlib
  comments). The detach noop after the add frame is emitted WITHOUT
  `"next"` — `afterLoop` points TO the noop so noop elimination
  resolves it correctly based on context (re-dispatch when last in
  outer body, fall-through otherwise).
- **Variable Range** (`emitForStmtRuntime`): Decomposes with
  `separate_register`, then emits `for_number`. No step sign check
  needed — `for_number` handles direction natively.

## Enhanced `return` in fn bodies

Three paths based on post-parse analysis:
1. **Return-instruction**: Single `return instruction` at end.
   Supports `fnDef.frame` promotion.
2. **Zero-copy**: Single `return` at end with all `IdentExpr` values.
3. **Emit-and-jump**: Everything else. Max-arity rule: return count =
   max arity across all `ReturnStmt` nodes.

## Multi-arity expression lists

`let a, b, c = 1, 2, 3` — each expression contributes its arity.
Sum must equal binding count, with prefix matching allowed on last
function call.

## Block expressions (mode blocks and if-expressions)

Both support **continuation**: after parsing, arithmetic continuation
is tried, then comparison/boolean continuation. This enables
`unlocked { get_number v } + 1` and `if x { a } else { b } == c`.

If-expressions: `else` is optional — absent branches produce `null`.
Arity = max across all branches.

## Wait keyword

`wait` is a keyword, not a stdlib function. **Not a loop** — does
not support `break` or labels. Ticks expression is evaluated once
and stored in a temp (pure number literals skip the snapshot).

## Block scoping

The `usedVars` map is NOT saved/restored during scope push/pop —
it tracks all names ever used for inline variable rename collision
avoidance.

## Compiler warnings and shadowing

**Shadowing warning**: Fires when a variable is re-declared at the
same scope depth as an existing unused declaration. Child-scope
re-declarations don't warn.

## Prelude system

The compiler prepends `stdlib/prelude.doit` to every source file before
parsing. The prelude contains `import * from "std:instructions"`, which
brings all stdlib symbols into scope through the normal import path.

**Previous approach**: Stdlib symbols were pre-populated into the
parser's working maps (`fns`, `iters`, `enums`) by cloning them from
the cached stdlib parse results. This made stdlib implicitly available
without any import mechanism.

**New approach**: The parser starts with empty working maps. The
prelude's glob import resolves through `parseImportedFile`, which
parses `instructions.doit` and returns its declarations. This makes
stdlib availability explicit and uses the same import machinery as
user code.

**`skip prelude`**: A directive at the top of a file that prevents
prelude prepend. Used by `instructions.doit` to avoid circular
dependency (the prelude imports from it). `skip` is a reserved keyword.

**`fileDecls`**: `collectDecls` populates `parser.fileDecls` with
the names of symbols declared in the current file. `parseImportedFile`
uses this to return only file-declared symbols, preventing symbols
brought in via the prelude from leaking through imports. This also
fixes a pre-existing issue where stdlib enums leaked through
unfiltered.

**`sourceOffset`**: When the prelude is prepended, `sourceOffset`
is set to `len(preludeText)`. `posToLineCol` starts counting from
this offset so that error messages report positions relative to the
user's source, not the prepended prelude.

## Import system

**Transitive dependencies**: Imported functions carry a `scope`
containing all non-stdlib functions from their defining file.
During `expandCall`, scope entries are temporarily merged (gap-fill
only) so transitive callees are available.

**Collision rules**: Glob imports silently add and can be overridden.
Named import vs named import or namespace vs namespace collisions
are errors. Local variables can shadow imported names.

**Private symbols**: Excluded from glob imports; compile errors when
accessed via named import or namespace dot access.

**No re-exports**: Imported names are not visible to further
importers. Transitive dependencies resolve via `fnDef.scope`.

## Compile-time constants and folding

Functions, constants, and enums share a namespace — collisions are
compile errors. Constants are substituted as `LiteralExpr` nodes.

Folding covers arithmetic, comparison, boolean chains/negation, and
constructors. Division by zero is not folded. `isCompileTimeConstant`
guards comparison folding to prevent folding runtime references.

The compile-time evaluator (`tryEvalExpr`/`tryEvalCall`) uses a
try-and-bail approach with a step limit (10000) for loop safety.

## Enum declarations

`::` access syntax (`MyEnum::Member`). Using an enum name without
`::` produces a specific error. Members support explicit values,
commas as separators, and negative values.

## Instruction `"c"` mode field

**Metadata field unwrapping**: `resolveInstructionFrame` unwraps
`{"num": N}` to plain `int` for non-numeric keys after paramMap
substitution. This is safe because register-slot values (numbered
keys) use `{"num": N}` maps, while metadata fields like `"c"` never
want wrapped values.

**Keyword param omission**: When a mode keyword arg is omitted, the
`c` field is dropped from output and the game uses its default.

## Iterators (`iter` declarations)

Category 2 instructions (stateful iterators) are now declared with `iter`
instead of `fn...exec`. Key decisions:

- **`iter name() -> outputs { body }`**: Separates iteration from branching.
  `->` declares output variables (yielded per iteration).
- **Instruction-backed iters**: Simplified `instruction` block with
  `done: N` syntax. No `detach next:` or `exec N:` needed.
- **Yield-based wrappers**: `yield` inside an iter body produces values.
  Compiled via AST rewriting — `rewriteYieldToBody` replaces each
  `yield` with assignment + caller body inline. The caller body is
  wrapped in `YieldBodyStmt`, which patches `@continue` placeholders
  to jump to a bridge noop emitted after the body. Inside a loop, the
  loop emitter sets `next:false` on the bridge (re-dispatch); at top
  level, the bridge falls through sequentially.
- **Exact yield count**: `yield` must produce exactly as many values as
  the iter's `-> outputs` declares. Unlike `for` bindings (prefix match OK),
  yield is strict.
- **`skipFnDef` handles `->` syntax**: Pass 2 skips iter declarations by
  detecting `tokArrow` after the param list and consuming output names
  before the brace block.
- **Import system**: `symbolSet` includes `iters map[string]*iterDef`.
  Iters propagate through imports like fns.
- **Static sequence compilation**: When an iter body consists entirely
  of `yield` statements, `emitStateMachineIter` compiles to a
  `for_number(0, N-1, 1)` loop with a `check_number` dispatch chain.
  `for_number` is inclusive of the stop value, so N yields need indices
  0 through N-1. `for_number` was chosen because the VM tracks its
  state across ticks natively — one instruction handles all the per-tick
  counter management instead of manual counter + comparison + increment.
  For N=1, no dispatch check is emitted (single yield is the catch-all)
  and the body is emitted inline once. For N>1, single-body yield
  compilation is used: each yield's setup code sets a dispatch variable
  (`@jmp`) to its resume label, jumps to a shared body label, and
  resumes via `jump @jmp` after the body executes. The body is emitted
  once instead of N times. Labels use compiler-generated composite
  values (`{id: "v_letter_L", num: -1000001-n}`) allocated via
  `frameBuilder.allocLabels` to ensure uniqueness across multiple
  loops in the same behavior.

## `'` sigil for instruction-level local blocks

The `'` sigil on exec binding names (`exec 0: 'larger`) declares a
local continuation block — one handled inline by blocks attached to
the instruction itself, not forwarded to the enclosing function's exec.
This reuses the existing `tokLabel` scanner token (same `'` sigil as
loop labels). Rationale: `instruction` should be the clean boundary
between the VM and language constructs, able to express everything the
VM does without the function layer. Local blocks complete this by
adding branching capability directly to `instruction`.

## `jump` and `label` — named labels and keyword promotion

Both `label` and `jump` are **compiler keywords** (label was promoted
from stdlib). Both accept two forms, with optional parens per the
consistent invocation syntax principle:

- **Named**: `label 'foo` / `jump 'foo` (or `label('foo)` /
  `jump('foo)`) — the `'` sigil triggers compiler-managed label
  resolution. The compiler lazily allocates a `compilerLabel(n)` value
  the first time a name is seen (whether in `label` or `jump`) and
  reuses it for all subsequent references to the same name. Labels are
  behavior-scoped (flat, like the VM instruction list).
- **Expression**: `label expr` / `jump expr` (or `label(expr)` /
  `jump(expr)`) — evaluated at runtime, no compiler validation.
  Useful for computed-goto state machines.
- **No string literals**: String arguments are a compile error on both
  keywords. Strings caused a silent bug (VM reads them as variable
  names, matches on emptiness). Users needing raw string slots can use
  the `instruction` intrinsic.

### Emit-time validation (named labels only)

- **Duplicate label**: Two `label 'foo` instructions emitted → compile
  error. Checked when a label frame is actually emitted, not at parse
  time, since some parsed code may not reach the output.
- **Orphan jump**: `jump 'foo` emitted but no `label 'foo` emitted →
  compile error. Same emit-time check.
- **Orphan label**: `label 'foo` with no `jump 'foo` — allowed (dead
  code, not an error).

### VM semantics (verified in-game)

`jump` successfully escapes `for_number` loop bodies, including nested
loops. The VM follows the jump unconditionally and abandons any active
iterators. Variables set before the jump retain their values.

When no matching `label` exists, `jump` follows its `next` field
(falls through to the next instruction) rather than halting.

### Runtime error on fallthrough (deferred)

For jumps that should always match (named labels, iterator state
machines), the `next` field should chain to a `notify` (with error
message + expected label value) followed by `exit`. This is a
cross-cutting concern — deferred to the burndown list rather than
implemented per-feature.

## `iterator_instruction` keyword

`iterator_instruction` is a keyword (not a stdlib function or iter
declaration) for inline iteration in `for...in` loops. It uses
`parseInstruction()` to get the raw frame, requires a `done:` slot,
and validates that no exec bindings are present (use `instruction`
with `'` blocks for branching). Rationale: one-off iteration shouldn't
require declaring a named `iter` — `iterator_instruction` is to `iter`
what `instruction` is to `fn`.

## Block scope register isolation (`declareVarScoped`)

At the behavior level, variable names are register keys in compiled
output. Before this fix, `pushScope`/`popScope` only tracked name
visibility — an inner `var x = 99` inside an `if` block would
overwrite the outer `var x = 1` because both used register `"x"`.

Fix: `declareVarScoped` allocates a unique register name (e.g.,
`"x$1"`) when a variable shadows an outer-scope variable. Two
declaration methods exist: `declareVar` (no renaming, for exec
bindings where register names must match binding names) and
`declareVarScoped` (shadow renaming, for user `var`/`let`
declarations). The `$` character is safe because it's not valid in
doit identifiers, similar to the `@` prefix convention.

## `call` keyword for inter-behavior calls

`call` invokes another behavior as a subroutine via the VM's `call`
instruction. Behaviors share the function/const/enum namespace and
are importable like functions.

### Syntax

Keyword args mapped to callee's named `@param` parameters. Paren-optional
per the consistent invocation syntax principle.

- `in` params: `name: expr`
- `out` params: `name: out var` (explicit binding)
- `inout` params: `name: inout var`
- Unbound `in`/`inout` → null input / discarded output (not an error)

### Expression form (return values)

`let r = call foo(x: 5)` captures unbound `out` params as return
values. Scans callee's `out` params left-to-right in declaration
order, skips explicitly bound ones, maps remaining to lvalues.
More lvalues than unbound out params → compile error. Fewer → OK
(prefix matching).

### Compiled output

- `{"op": "call", "sub": N, "0": val, "1": val, ...}`
- `"sub"` is 1-based index into `dependencies` array, or `-1` for
  self-recursion
- Slot `"0"` = first `@param`, `"1"` = second, etc.
- `dependencies` is a flat array at root behavior level; each entry
  is a complete compiled behavior without its own dependencies

### Dependency compilation

On-demand recursive compilation. When emission hits `call B(...)`,
compile B immediately via `parseBehaviorBody(B)`. If B calls C,
C is compiled during B's compilation. A `depIndex` cache prevents
recompilation. Cycle detection: if B is currently being compiled
and we hit `call B(...)`, that's self-recursion (`"sub": -1`). If A
is compiling B which tries to compile A, that's a circular dependency
error.

### Parameter directions in callee output

`false` for `in`, `true` for `out`/`inout` in the callee's
`parameters` array. Standalone behaviors emit all `true` (backward
compatible). Only subroutine callees need the distinction.

## `behavior` parameter modifier

The `behavior` modifier on function parameters (analogous to `param`)
marks a parameter as requiring a behavior reference at the call site.
This enables stdlib functions like `load_behavior` to accept behavior
references and pass them through to `instruction` blocks.

### Design

- `fn load_behavior(behavior bhv, unit) { instruction "load_behavior" { sub: bhv; ... } }`
- At behavior-level call sites, the argument must be a known behavior
  name (from this file or imports). The compiler compiles it as a
  dependency and emits the dependency index.
- Propagates transitively through function inlining (like `param`):
  `fn wrapper(behavior b, u) { load_behavior b, u }` — the `behavior`
  flag flows through `paramMap` as a `behaviorRef` value.
- Cannot combine with `out` or `inout` (behaviors are compile-time
  constants, not runtime l-values).
- Cannot combine with `param` (a behavior reference is not a parameter
  register).
- The `behaviorRef` type wraps the dependency index (or -1 for self)
  and is unwrapped to a plain integer by `resolveInstructionFrame`.
- Behavior names pass through `resolveBhvOperand` as `IdentExpr` nodes
  (checked against `p.bhvs` before the unknown-identifier error).

## Dot access for register components

`expr.number` extracts the numeric component, `expr.value` extracts
the typed value (stripping the number). Inverse of the `&` operator.

- **AST**: `DotAccessExpr{Value Expr, Field string}`. Field is
  `"number"` or `"value"`.
- **Parsing**: `maybeParseDotAccess` runs after `parseArithPrimary`
  (in `parseArithTerm`) and after constructor/other expressions in
  `parseBhvArgExpr`. Binds tighter than any binary operator. Loops
  to support chaining (e.g., `(x & 5).value.number`).
- **Emission**: Emits `separate_register` directly (slot 1 for
  `.number`, slot 2 for `.value`). Does not use `expandCall` — just
  builds the frame directly with only the needed output slot populated.
- **Compile-time folding**: `tryResolveDotAccessLiteral` handles
  `LiteralExpr` and `ConstructorExpr` inputs. Extracts/strips the
  `"num"` key from the map. Pure number `.value` → null; typed value
  without number `.number` → null.
- **Namespace disambiguation**: Not an issue — `resolveFnName` handles
  namespace dot before `parseArithPrimary` returns; `maybeParseDotAccess`
  only runs on the returned expression. If an identifier matches both
  a namespace and a variable, the namespace takes priority (existing
  behavior).

## Event handler continuation

Event handlers are interrupts — their setup frames exist outside the
main control flow and all handlers can fire at any time. Where the
handler is placed in source code determines what happens when it
finishes.

- **`frameAtDeferral`**: Each deferred event records `b.pos()` at
  deferral time. This is the frame index that the next non-event
  statement will emit into — the natural continuation point.
- **Continuation vs restart**: In `emitDeferredEvents`, if
  `frameAtDeferral < mainEnd`, the handler's final frame gets
  `"next"` pointing to `frameAtDeferral`. Otherwise it gets
  `"next": false` (restart). No bridge frames needed.
- **Groups**: Multiple consecutive events (including events from
  inlined function calls that emit zero frames) naturally share
  the same `frameAtDeferral` value and thus the same continuation.
- **Nested blocks**: Events are currently restricted to top-level
  behavior/function bodies. Lifting this restriction is a separate
  burndown item.
