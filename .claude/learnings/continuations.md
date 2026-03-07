# Continuations

Design decisions and rules for the continuation/branching system.
Consult when modifying continuation-related code or designing new
features that interact with exec branches.

## Two connection types (exhaustive)

Bridging blocks run once and merge back to the join point (compiler
adds a jump frame). Detached blocks run subordinate to an iterator —
their last frame gets `"next": false` and the VM re-dispatches
internally. Every exec slot in every known instruction (including
modded ones examined) is cleanly one or the other. No third category
was found.

## `return` as reserved continuation name

In instruction blocks, `return` represents the function's exit point.
`exec 0: return(@1)` routes a branch to the caller with data. Bare
`return` or absent binding defaults to all `@N` in order (backward
compatible with non-branching instructions). `next: return` is the
implicit default when `"next"` isn't explicitly bound. Unbound
numbered exec slots also default to `return` (bridge to join point).

## `return` inside blocks is a compile error

Continuation blocks are not functions. `return` of any kind inside a
block (bridging or detached) is rejected at parse time. Wrapping a
branching function with forwarded continuations requires the raw
`instruction` intrinsic.

## Parser disambiguation for `fn() {`

After a call's `{`, if the next token is an identifier followed by
`{`, it's the multi-block form. Otherwise it's the collapsed unnamed
form. `{ var -> body }` (Kotlin binding) is always collapsed since
`->` cannot follow a continuation name.

## `break` and `last` in exec blocks

Exec blocks reset `loopDepth` and `loopLabels` at entry via
`enterExecBlock()` (which also merges current labels into
`outerLoopLabels` for cross-boundary break support).

Unlabeled `break` inside an exec block means "exit this block":

- **Detached blocks**: `@break` → `set_reg false false` with
  `"next": false` (re-dispatches to the iterator, does NOT stop it).
  These re-dispatch noops are excluded from `eliminateNoopBridges`
  (`isNoopBridge` returns false when `"next" == false`) because
  numbered exec slots can't express re-dispatch — `false` in a
  numbered slot means "unconnected/fall-through", not "re-dispatch".
  To stop the iterator, use the `last` keyword (terminal — no `break`
  needed after it).
- **Bridging blocks**: `@break` → `set_reg false false` patched to
  the join point (same as normal block completion).

Labeled `break` from inside an exec block targeting an outer loop
compiles to `jump`/`label` pairs (cross-boundary break). The parser
checks `outerLoopLabels` when `loopLabels` doesn't have the label
and sets `BreakStmt.CrossBoundary = true`. The emitter produces
`@jumpbreak` placeholders, which `patchJumpBreakPlaceholders`
replaces with `jump <compiler_label>` instructions. A matching
`label <compiler_label>` is emitted after the target loop.

## Value arguments in exec binding arg lists

Besides `@N` references, exec binding args support literal values
(numbers, null, enum values, constructors). Enables patterns like
internalizing failure (`exec 0: return(null)`), discriminated merging
(`exec 0: handler(@1, MyEnum::PathA)`, `exec 1: handler(@1,
MyEnum::PathB)`), and default fill (`exec 0: result(@1, 0)`). Full
expressions deferred until a real use case demands them.

## Hard-coded boolean/comparison paths coexist

`check_number`, `compare_register`, and `value_type` have both exec
signatures (for explicit continuation block usage) AND hard-coded
compiler paths (for `if`/`while`/`is` expressions). The two calling
conventions coexist — the continuation system adds a new option
alongside the existing boolean expression compilation.

## `for` keyword overload

`for` means counted loop and for-in loop — two forms distinguished
by the presence of `in`. The old looping block prefix (`for body`)
was renamed to `detach` in instruction blocks and removed from
call sites (the compiler derives detached status from the function
definition).

## Pure-logic branching (`return <cont_name>`)

`ReturnStmt.Continuation` holds the continuation name. Emits a
`set_reg false false` frame with `"next": "@exec_<name>"`. The
`@exec_` prefix is a string placeholder (like `@break` and
`@return`) patched by `expandContinuationBlocks` to the block start
or join point.

`fnBodyContext.execNames` distinguishes `return yes` (continuation
dispatch) from returning a variable named `yes`. Exec names take
priority over variables.

## Pure-logic data dispatch (`return <cont_name>(args...)`)

`ReturnStmt.ContinuationArgs` holds optional data arguments. All
continuations share a single positional slot space — different
continuations may have different arg counts, only the max determines
register allocation.

`buildExecBindingMap` generates synthetic `execBinding` values from
`fnDef.execContArgs`, letting `allocExecOutputRegs` and
`expandContinuationBlocks` reuse existing infrastructure. `@cargN`
synthetic names in `paramMap` connect emission to allocated output
registers.

All returns for the same continuation must pass the same number of
args (validated during post-parse in `parseUserFn`).

## Expression form

`ContinuationBlock.Tail` holds a tail expression that produces the
block's value. `emitTail` callback threads through `expandCallOpts`
→ `expandCall` → `expandContinuationBlocks` to write tail values
to the caller's target register.

Expression form is rejected at parse time if any block has
`Detached=true` — detached blocks iterate, they don't produce values.

Expression arity = max across all continuation paths. Each path
contributes: tail expression for provided blocks, `@N` values for
the `return` path, `null` for unprovided blocks.

## Block param count validation

`expandContinuationBlocks` validates that each block's Kotlin-style
params don't exceed the data args the continuation provides (from
`buildExecBindingMap`). Applies uniformly to instruction-based and
pure-logic dispatch.

## `iter` declarations (Phase 1)

Iterator instructions (Category 2) are now declared with `iter` instead
of `fn...exec`. The `for ... in` call syntax replaces continuation blocks
for iterators. The continuation system remains for non-iterator branching
(Categories 1, 3, 4, 5).

Instruction-backed iters emit the same frames as the old `fn...exec`
form — the `for_component` instruction with `"next": false` on the body's
last frame and a done slot. `break` in `for...in` loops emits `last`
(the compiler controls this, not the user). Labeled `break` uses direct
jump via `patchBreakPlaceholders`.

## Instruction-level local blocks (`'` sigil)

The `'` sigil on exec binding names (`exec 0: 'larger`) declares a
local continuation block — handled inline by blocks attached to the
instruction itself, not forwarded to the enclosing function's exec.
This reuses the existing `tokLabel` scanner token (same `'` sigil as
loop labels).

Key differences from function-level continuations:

- **Scope**: Local blocks are scoped to a single instruction, not a
  function. `expandInstructionBlocks` patches only the instruction frame
  at `instrIdx`, leaving non-local bindings untouched for function-level
  `expandContinuationBlocks`.
- **Mixing**: Local (`'name`) and non-local (`name`) bindings can coexist
  in one instruction. Non-local bindings refer to the enclosing
  function's exec and are resolved by function-level expansion.
- **Expression form**: When all local blocks are bridging, the instruction
  can be used as an expression — each block's tail expression is its
  value. Detached local blocks in expression form are a compile error.
- **`'return` reserved**: Cannot be used as a local block name.
- **Data args**: Local blocks receive data from `@N` args on the exec
  binding, same as function-level continuations.
- **`break`/`return` rules**: Same as function-level blocks — `return`
  inside a local block is a parse error; `break` exits the block.
