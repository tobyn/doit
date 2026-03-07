---
paths:
  - "toolchain/compiler/**/*"
---

# doit Compiler

The `compiler` package compiles the doit language into the structured
representation of a Desynced behavior supported by the `codec` package.
See `.claude/rules/behavior_json.md` for the compiled output format.

For design rationale behind specific features, see
`.claude/learnings/decisions.md`. For test case format and conventions,
see `.claude/learnings/test_format.md`.

## Architecture

- **`ast.go`** — `Stmt` interface (24 types) and `Expr` interface
  (15 types). `isTerminalStmt`/`terminalKeyword` for unreachable code
  detection.
- **`scanner.go`** — `scanner` struct (embedded by `parser`), token
  types, `Keywords` map, `skipToCloseBrace`. The `scanner` has a
  `sourceOffset` field (byte offset of user source after prepended
  prelude; `posToLineCol` starts counting from this offset). The
  `parser` struct extends `scanner` with `fns`, `iters`, `consts`,
  `enums`, import state, `prelude` string (propagated to sub-parsers),
  `fileDecls` (names declared in this file), loop/exec-block tracking,
  `callExprParser` callback, and `warnings []string`.
- **`compiler.go`** — Public API (`Compile`/`CompileString`), shared
  types (`symbolSet`, `fnDef`, `iterDef`, `paramDef`, `symbolTable`, `constDef`,
  `enumDef`), `frameBuilder`/`frameRef` abstraction, `emitContext`
  struct, `execMode` tracking, slot constants for `check_number`,
  `compare_register`, and `value_type`.
- **`parse.go`** — Stdlib parsing, file-level parsing, function and iterator
  definitions (`parseUserFn`, `parseIterDecl`), fn body AST parsing and emission
  (`emitFnBody`), instruction parsing (`parseInstruction`), call
  expansion (`expandCall`), `resolveInstructionFrame`, enum/const
  parsing, compile-time evaluator (`tryEvalExpr`/`tryEvalCall`).
- **`bhvast.go`** — Behavior-level AST parsers and emitters. Shared
  expression parsers used by both behavior and fn body paths:
  `parseArithExpr` chain, `parseBoolExpr`/`parseBoolChain`,
  `maybeExprContinuation`. Behavior-level emitters:
  `emitBehaviorStmts`, `emitBhvStmtSimple`, `emitBhvCallStmt`.
- **`codegen.go`** — `parseBehaviorBody` (two-phase parse+emit),
  unified control flow emitters via `emitContext` (`emitIfStmt`,
  `emitWhileStmt`, `emitLoopStmt`, `emitForStmt`, `emitWaitStmt`,
  `emitIfExpr`, `emitModeBlockExpr`), single-expression emitters
  (`emitComparison`, `emitTypeCheck`, `emitTruthyCheck`), direction
  enforcement, `resolveInstructionFrame`.
- **`import.go`** — Import system: parsing, path resolution,
  `processImports`, collision checking, `resolveFnName` for
  namespace-qualified lookup.
- **`tests/`** — Test case pairs (`.doit` + `.json`).

## Compiler Pipeline

The compiler is a recursive-descent parser (`parser` struct embeds
`scanner`). Both function bodies and behavior bodies use an AST
approach: parse into `[]Stmt` with `Expr` nodes, then emit frames.

1. **Stdlib parsing** — `parseStdlib` parses `*.doit` files from the
   stdlib FS (skipping `prelude.doit`). `parseStdlibFile` handles
   `fn`, `iter`, `enum`, and `skip` declarations. Results are cached
   in `stdlibFns`/`stdlibIters`/`stdlibEnums`.
2. **Prelude prepend** — `CompileString` reads `prelude.doit` and
   prepends it to the source unless `hasSkipPrelude(src)` is true.
   The parser is created with **empty** working maps (not cloned from
   stdlib). The prelude's `import * from "std:instructions"` brings
   stdlib symbols in through the normal import path.
3. **Import processing** — `processImports` resolves paths, reads
   imported files, merges symbols into the namespace. Imported files
   also get the prelude prepended (unless they have `skip prelude`).
4. **User source compilation** — Two passes: first collects
   declarations (`collectDecls`), then compiles the selected behavior
   via `parseBehaviorBody`.
5. **Noop bridge elimination** — `eliminateNoopBridges` removes
   `set_reg false, false` frames that serve only as control-flow
   redirects. Runs after all emission/patching and
   `validateNamedLabels`, before `finalize`. Five phases: identify
   noops, resolve targets transitively, redirect references, fix
   fall-through predecessors, remove and reindex. Phase 4 restores
   branch slots on check/compare/value_type frames that were stripped
   by `stripFallThrough` (which runs during emission) when the noop's
   target differs from the natural fall-through after removal. See
   `isNoopBridge` and `resolveNoopTarget` in `compiler.go`.

`Compile` and `CompileString` accept `fs.FS` + path for import
resolution, a `behaviorID` string (auto-selected when only one
behavior), a `locale` string (BCP 47) for `localize` blocks, and
return `(*codec.Object, []string, error)` where the middle value is
compiler warnings.

## Key Patterns

**Two contexts for parsing and emission**: Both parsing and emission
use context structs with closures to unify behavior-level and fn body
paths. `parseContext` (compiler.go) drives statement parsers
(`parseIfStmt`, `parseWhileStmt`, `parseLoopStmt`, `parseForStmt`,
`parseWaitStmt`, `parseModeBlockExpr`, `parseIfExpr`, etc. in
codegen.go). `emitContext` (compiler.go) drives control flow emitters.
Factory functions — `bhvParseCtx`/`bhvEmitCtx` (bhvast.go) and
`fnParseCtx`/`fnEmitCtx` (parse.go) — build each context with
closures capturing resolution state.

**Frame building**: `frameBuilder` is an append-based builder.
`frameRef(int)` values are 0-based internally, converted to 1-based
wire format by `finalize`. All control flow uses `frameRef` values in
`"next"` and exec branch slots.

**Instruction wrapper promotion**: Pure instruction wrappers (fn body
is a single `instruction` block with only param/ret references) are
promoted to `fnDef.frame` for fast single-frame expansion in
`expandCall`, bypassing AST emission.

**Call expansion**: `expandCall` inlines function calls. For promoted
wrappers, it calls `resolveInstructionFrame`. For AST-based functions,
it calls `emitFnBody`. Patches `@return` placeholders to jump past
the expansion afterward.

**Brace-delimited blocks**: Two categories. **Statement blocks**
(behavior/function/if/while/loop bodies) contain statement sequences.
**Structured data blocks** (`instruction` intrinsic, `localize`) have
their own parsing rules.

**Function calls**: Parentheses are optional. `notify "Hello"` and
`notify("Hello")` are equivalent. Parenthesized mode requires commas
between positional args and a closing `)`.

**Doc comments** (`#!`): Collected by scanner, captured after the
first token of each statement. For stdlib calls, set as `"cmt"` on
the emitted frame. For user function calls, each body call uses its
own `#!` if present, otherwise inherits the caller's comment
recursively. Supports localized text via `(locale)` prefix.

**Statement termination** (not yet implemented): The language is
line-oriented but the scanner currently treats newlines as whitespace.
Future work will add newline awareness with three exceptions:
block-ending statements extending to `}`, parenthesized calls
extending to `)`, and trailing commas continuing to the next line.

**Positional arg separators**: Commas between positional args are
required in both parenthesized and unparenthesized call forms.

**Control flow placeholders**: `@break`, `@continue`, `@return`, and
`@jumpbreak` are placeholder opcodes in intermediate frames. In loops,
`@break` is patched to jump past the enclosing loop. `@continue` is
patched to jump to the loop's back-edge target: `frameRef(loopStart)`
for infinite loops and while, `frameRef(incrFrame)` for counted loops
and manual counter loops, and `false` (re-dispatch) for instruction-backed
for loops. In iterator-backed loops, unlabeled `@break` is patched to `"last"`
(stops the iterator) by `patchUnlabeledBreakToLast`, while labeled
breaks still jump past the loop. In exec blocks, `@break` is patched
to exit the block: detached blocks get `set_reg false false` with
`"next": false` (re-dispatch); bridging blocks get `set_reg false
false` patched to the join point. `@jumpbreak` is emitted for
cross-exec-block-boundary labeled breaks (`BreakStmt.CrossBoundary`);
`patchJumpBreakPlaceholders` replaces them with `jump` instructions
targeting a compiler-generated label, emits a matching `label` after
the target loop, and emits a fallthrough error handler (see below).
`patchJumpBreakPlaceholders` must be called BEFORE computing
`afterLoop` since it may emit frames. `@return` is patched to jump past the function
expansion. `@break`, `@continue`, and `@return` are patched to
`set_reg false false` with appropriate `"next"` targets; these noop
bridge frames are eliminated by `eliminateNoopBridges` in a post-
emission pass. `@continue` in yield-based iterators is handled by
`YieldBodyStmt`: a bridge noop is emitted after the inlined caller
body, and `@continue` is patched to jump to it.

**Jump fallthrough protection**: Compiler-emitted jumps that should
always match (named labels, state machine dispatch, cross-boundary
breaks) chain `"next"` to a `notify` + `exit` error handler via
`emitJumpFallthroughError`. If the jump fails to match a label at
runtime, the player sees "jump: no matching label" with the expected
label value. Expression-form user jumps (`jump someVar`) are NOT
protected — those are user-controlled. The error handler frames are
dead code in normal execution, only reachable via the jump's `"next"`
fallthrough. For `patchJumpBreakPlaceholders`, the label's `"next"`
is set to skip over the error handler.

**Nested `for_number` loops**: The VM's `for_number` instruction uses
re-dispatch (`next: false`), but nested `for_number` loops cause the
inner body's re-dispatch to hit the outer `for_number` first (lower
frame number), advancing both counters every inner iteration. Fix:
`forNumberDepth` on the parser tracks nesting. When
`emitForStmtRange` detects `forNumberDepth > 0`, it dispatches to
`emitForStmtRangeManual`, which compiles the inner loop as manual
counter instructions (`set_number` init + `check_number` condition +
body + `add` increment with explicit back-edge to `check_number`).
A detach noop (`set_reg false false`, no `"next"` key) is emitted
after the add frame. It serves two purposes: (1) absorbs the outer
for_number's compile-time `"next": false` assignment on the last
body frame, preserving the add frame's back-edge; (2) provides the
inner loop's `afterLoop` target — `afterLoop` points TO the noop
(not past it). When the inner loop is the outer body's last
statement, the outer's `"next": false` assignment sets the noop to
re-dispatch; otherwise, the noop falls through to the next statement.
`eliminateNoopBridges` resolves correctly in both cases. Requires
compile-time constant step for direction detection (positive →
`larger` branch = done; negative → `smaller` branch = done).
`emitForStmtRuntime` with `forNumberDepth > 0` errors since runtime
step direction can't be determined at compile time.

**Iterator helpers**: Shared helpers reduce duplication across the
five iterator emitters (`emitStateMachineIter`, `emitInstructionIter`,
`emitForStmtRange`, `emitForStmtRuntime`, `emitInlineIterInstruction`):
`buildIterParamMap` (on `emitContext`) resolves positional/keyword
args and maps output names to iter var registers.
`patchIterDoneSlot` patches the done-slot on the iterator instruction
frame. `patchUnlabeledBreakToLast` converts unlabeled `@break` to
`"last"` instructions.

**Block scoping**: Variables declared inside blocks are scoped to that
block. Behavior level uses `symbolTable.pushScope`/`popScope`. Fn
bodies use `fnBodyContext.pushFnScope`/`popFnScope`. The `usedVars`
map is NOT saved/restored — it tracks all names ever used for inline
variable rename collision avoidance. At the behavior level, variable
names are register keys in compiled output. When an inner-scope
variable shadows an outer-scope variable, `declareVarScoped` allocates
a unique register name (`name$1`, `name$2`, etc.) via
`allocUniqueShadow` so the inner assignment doesn't overwrite the
outer register. `resolveReg(name)` returns the register name for a
user-visible variable. `declareVar` (no renaming) is used for exec
bindings and other paths where register names must match binding names;
`declareVarScoped` (with shadow renaming) is used for user `var`/`let`
declarations.

**Inline variable renaming**: When inlining via `expandCall`, internal
variables are pre-scanned by `collectASTOutputVars` and renamed with
`allocUniqueVar` to avoid collisions with the caller's namespace.
`emitFnBody` calls `collectASTOutputVars` then delegates to
`emitFnBodyCore` (no scan). `YieldBodyStmt` (inlined caller loop body
at yield sites) uses `emitFnBodyCore` directly — caller-scope variables
must not be renamed.

**Continuations and branching**: Functions declare exec branches via
`exec(name1, name2)` after the param list. Instruction blocks bind
exec slots with `exec N: name`, `next: name`, or `detach next: name` syntax. `execBinding`
structs in instruction frames are patched to `frameRef` targets by
`expandContinuationBlocks`. **Optional exec branches**: When a
branching function is called without continuation blocks, `expandCall`
strips unresolved `execBinding` values and absent keyword params from
emitted frames after `emitFnBody` returns. This enables functions like
`domove` to declare optional exec branches (e.g., `path_blocked`) that
callers can omit. `tryPromoteInstruction` rejects frames with exec
bindings, so these functions always go through the AST body path. Exec bindings can carry data via
`@N` args (e.g., `exec 0: found(@1, @2)`) that map instruction
output slots to continuation block params. Call sites use Kotlin-style
bindings (`{ name -> body }`) to receive this data. Scanner supports
`->` (tokArrow) and save/restore for multi-token lookahead.
`allocExecOutputRegs` allocates registers for exec data output slots,
reusing caller-provided registers from `fn.rets`+`retVals` when
available. Functions without explicit `return` that have instruction
frames with `returnSlot` values get synthetic `@retN` names via
`findMaxReturnSlot`. Pure-logic functions use `return <cont_name>`
to dispatch to continuations; `emitFnBody` emits `@exec_<name>`
string placeholders patched by `expandContinuationBlocks`.
Pure-logic data dispatch (`return cont(args...)`) uses
`fnDef.execContArgs` to track arg counts per continuation;
`buildExecBindingMap` generates synthetic `execBinding` values from
these, and `@cargN` synthetic names in `paramMap` connect emission
to allocated output registers.
Expression-form blocks have a `Tail` field on `ContinuationBlock`;
`emitTail` callback in `expandCallOpts` writes tail values to the
caller's target register. Both behavior level
(`maybeParseBhvContinuationBlocksExpr`) and fn body level
(`maybeParseFnBodyContinuationBlocksExpr`) parse expression-form
blocks using `exprTail=true`.

**Instruction-level local blocks**: `exec 0: 'name` (tick sigil) declares
a local continuation block on the instruction itself. Parsed via
`tokLabel` in `parseInstruction`. `hasLocalExecBindings` and
`extractLocalExecInfo` inspect the frame. `allocLocalExecOutputRegs`
allocates registers for `@N` data args on local bindings.
`expandInstructionBlocks` patches only the instruction frame, leaving
non-local bindings for function-level expansion. Parse helpers
`maybeParseBhvLocalBlocks`/`maybeParseBhvLocalBlocksExpr` (bhvast.go) and
`maybeParseFnBodyLocalBlocks`/`maybeParseFnBodyLocalBlocksExpr` (parse.go)
detect and parse local blocks at each instruction site. Emission helpers
`emitBhvInstructionWithBlocks`/`emitBhvInstructionExprWithBlocks`
(bhvast.go) and `emitFnBodyInstructionWithBlocks`/
`emitFnBodyInstructionExprWithBlocks` (parse.go) emit the instruction
frame and expand local blocks.

**`iterator_instruction`**: Keyword for inline iteration in `for...in`
loops without declaring an `iter`. Parsed by `parseIteratorInstruction`
(codegen.go), which calls `parseInstruction()`, extracts the `done:` key,
validates no exec bindings, and validates `@N` count. Emitted by
`emitInlineIterInstruction` (codegen.go), which uses `resolveInstrFrame`
callback on `emitContext` for frame resolution, then reuses the shared
iterator helpers. `ForStmt` carries `IterInstrFrame` and `IterInstrDone`
fields for this form.
