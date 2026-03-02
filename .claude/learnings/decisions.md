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

## `exit` keyword

`exit` is a language keyword (not a stdlib function). It emits
`{"op": "exit"}` — a terminal instruction. The old `exit()` stdlib
stub was removed.

## Unreachable code detection

Code after `exit`, `break`, or `return` is unreachable. The compiler
warns and skips remaining statements in the block.

**Break label disambiguation**: When `break` is followed by an
identifier, the parser checks if it's a known loop label. Keywords
and known functions are excluded from label consideration — they
cause the break to be treated as unlabeled.

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
Label lookahead exclusions prevent `true:` and `false:` from being
parsed as loop labels.

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

## Boolean expressions

**Comparison operators**: Numeric comparisons (`>`, `<`, `>=`, `<=`)
use `check_number` (3-way branch). Equality comparisons (`==`, `!=`)
use `compare_register` (2-way) for full register composite equality.

**Type check (`is`)**: `is Number` is not supported because
`value_type` cannot distinguish numbers from null.

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

`@break` placeholder includes optional `"label"` field. Patching
matches unlabeled breaks to innermost loop, labeled breaks to the
named loop.

## Range `for` loops — three emission paths

Based on step sign knowledge:
- **A/B** (literal step, sign known): simple comparison loop.
- **C** (variable range): runtime step sign check, direction-aware
  comparison.

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
