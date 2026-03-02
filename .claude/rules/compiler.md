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

- **`ast.go`** — `Stmt` interface (18 types) and `Expr` interface
  (15 types). `isTerminalStmt`/`terminalKeyword` for unreachable code
  detection.
- **`scanner.go`** — `scanner` struct (embedded by `parser`), token
  types, `Keywords` map, `skipToCloseBrace`. The `parser` struct
  extends `scanner` with `fns`, `iters`, `consts`, `enums`, import state,
  loop tracking, `callExprParser` callback, and `warnings []string`.
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

1. **Stdlib parsing** — `parseStdlibFile` handles `fn` (via
   `parseUserFn`), `iter` (via `parseIterDecl`), and `enum` (via
   `parseEnumDecl`) declarations. Stdlib iters are propagated via
   `parser.stdlibIters`.
2. **Import processing** — `processImports` resolves paths, reads
   imported files, merges symbols into the namespace.
3. **User source compilation** — Two passes: first collects
   declarations (`collectDecls`), then compiles the selected behavior
   via `parseBehaviorBody`.

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

**Control flow placeholders**: `@break` and `@return` are placeholder
opcodes in intermediate frames. `@break` is patched to jump past the
enclosing loop. `@return` is patched to jump past the function
expansion. Both are patched to `set_reg false false` with appropriate
`"next"` targets.

**Block scoping**: Variables declared inside blocks are scoped to that
block. Behavior level uses `symbolTable.pushScope`/`popScope`. Fn
bodies use `fnBodyContext.pushFnScope`/`popFnScope`. The `usedVars`
map is NOT saved/restored — it tracks all names ever used for inline
variable rename collision avoidance.

**Inline variable renaming**: When inlining via `expandCall`, internal
variables are pre-scanned by `collectASTOutputVars` and renamed with
`allocUniqueVar` to avoid collisions with the caller's namespace.

**Continuations and branching**: Functions declare exec branches via
`exec(name1, name2)` after the param list. Instruction blocks bind
exec slots with `exec N: name` or `next: name` syntax. `execBinding`
structs in instruction frames are patched to `frameRef` targets by
`expandContinuationBlocks`. Exec bindings can carry data via
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
