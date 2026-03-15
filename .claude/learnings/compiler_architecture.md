# Compiler Architecture

Per-file breakdown of the `compiler` package. Reference material for
navigating the codebase when working on compiler changes.

For the compilation pipeline and key design patterns, see
`.claude/rules/compiler.md` (loaded automatically when touching
compiler files). For design rationale, see `decisions.md`.

## File Guide

- **`ast.go`** — `Stmt` interface (27 types, including `OnEventStmt`,
  `AssertStmt`, and `CallBehaviorStmt`) and `Expr` interface (17 types,
  including `CallBehaviorExpr` and `DotAccessExpr`). `BreakStmt` has
  `Values []Expr` for break-with-value. `AssertStmt` has `Condition`,
  `Body`, `Message`, `Value`, `ConditionText`, `File`, `Line`.
  `isTerminalStmt`/`terminalKeyword` for unreachable code detection
  (`AssertStmt` is NOT terminal). After a terminal statement, the
  parser scans the rest of the block (via
  `blockContainsReachabilityPath`) for `on` or `label` at any depth.
  If found, the terminal flag is reset and parsing continues — event
  handler continuations and jump targets make subsequent code
  reachable. If not found, the code is warned about and skipped via
  `skipToCloseBrace`.
- **`scanner.go`** — Thin wrapper around `syntax.Scanner`. The
  `scanner` struct stores a `syntax.Scanner` as field `syn` and keeps
  a local `token` type with lowercase fields (`kind`, `val`, `pos`)
  to avoid mass renames across the compiler. `rawNext()` delegates to
  `syn.RawNext()` and converts; `next()` wraps `rawNext()`, filtering
  comments and accumulating `#!` doc comments. Token kind constants
  are aliases (`tokEOF = syntax.TokEOF`, etc.) plus compiler-internal
  kinds `tokIs` (200) and `tokTruthy` (201). `Keywords`,
  `isConstructor`, `isIdentStart`, `isIdentCont` are re-exported from
  syntax. Parser-specific methods remain: `posToLineCol`,
  `sourceAnnotation`, `errorf`, `expect`, `blockContainsReachabilityPath`,
  `skipToCloseBrace`, `save`/`restore`, `unget`, `next`,
  `parseLocalePrefix`, `resolveLocalizedDocComment`. The `scanner` has
  a `sourceOffset` field (byte offset of user source after prepended
  prelude; `posToLineCol` starts counting from this offset). The
  `parser` struct extends `scanner` with `fns`, `iters`, `consts`,
  `enums`, import state, `prelude` string (propagated to sub-parsers),
  `fileDecls` (names declared in this file), loop/exec-block/mode-block
  tracking (`loopDepth`, `execBlockDepth`, `modeBlockDepth`),
  `callExprParser` callback, `breakRetVals []any` (target registers
  for break-with-value in expression-form blocks; nil outside),
  `warnings []string`, `releaseMode bool` (omits assert statements),
  and behavior call fields: `bhvs` (behavior definitions from this
  file + imports), `dependencies` (accumulated compiled sub-behaviors),
  `depIndex` (dedup cache), `depCompiling` (cycle detection),
  `selfBehaviorID` (for self-recursion detection).
- **`compiler.go`** — Public API (`Compile`/`CompileString`), shared
  types (`symbolSet` (includes `bhvs` map for behavior definitions),
  `fnDef`, `iterDef`, `bhvDef`, `paramDef` (with `isParam` and
  `isBehavior` fields), `behaviorRef` (dependency index wrapper),
  `symbolTable`, `constDef`, `deferredEvent`, `enumDef`),
  `frameBuilder`/`frameRef` abstraction, `parseContext` struct
  (with `parseMode`: `modeBehavior`/`modeFunction`/`modeIterator`),
  `emitContext` struct, `execMode` tracking (initial `modeUnknown`),
  slot constants for `check_number`, `compare_register`, and
  `value_type`.
- **`consteval.go`** — Compile-time constants and evaluator.
  Const declarations (`parseConstDecl`), enum declarations
  (`parseEnumDecl`), compile-time evaluator
  (`tryEvalExpr`/`tryEvalCall`/`tryEvalStmts`/`tryEvalCallArgs`),
  const call argument parsing (`parseConstCallArgs`/`parseConstArgExpr`),
  helper types and functions (`constEvalStatus`, `extractNum`,
  `isTruthy`, `evalCompare`, `evalTypeCheck`).
- **`continuations.go`** — Continuation block parsing and expansion.
  Parsing: `parseContinuationBlocks` (entry point, disambiguates
  multi-block vs collapsed form), `parseContinuationBlocksMulti`
  (multi-block form), `tryParseContBlockBindings` (Kotlin-style
  bindings), `parseExecBindingArgs` (exec binding argument list).
  Expansion: `expandContinuationBlocks` (emit block bodies, patch
  exec binding slots), `expandInstructionBlocks` (local instruction
  block expansion). Helpers: `buildExecBindingMap` (exec binding map
  from data args), `allocExecOutputRegs` (register allocation for
  exec data output), `findMaxExecOutputSlot`, `findMaxReturnSlot`,
  `isDirection`.
- **`parse.go`** — File-level parsing, function and iterator
  definitions (`parseUserFn`, `parseIterDecl`), fn body AST parsing
  (`parseFnBodyStmts`, `parseFnBodyLetVar`, `parseFnDefaultStmtUnified`),
  instruction parsing
  (`parseInstruction`), call expansion (`expandCall`),
  `resolveInstructionFrame`.
- **`fnbody_emit.go`** — AST-based fn body emission. Pre-scan for
  output variables (`collectASTOutputVars`, `collectExprOutputVars`),
  literal resolution (`resolveVarName`, `tryResolveConstructorLiteral`,
  `tryResolveAmpersandLiteral`, `tryResolveDotAccessLiteral`),
  expression emission (`emitExprGetValue`, `emitExprTo`,
  `emitConstructorTo`, `emitAmpersandTo`, `emitDotAccessTo`,
  `emitDotAccessFrame`), arithmetic and boolean emission
  (`emitFnArithTo`, `emitFnBoolExprTo`, `resolveFnBoolTree`),
  fn body context factories (`fnParseCtx`, `fnEmitCtx`), instruction
  emission with
  local blocks (`emitFnBodyInstructionWithBlocks`,
  `emitFnBodyInstructionExprWithBlocks`), top-level fn body emission
  (`emitFnBody`, `emitFnBodyCore`), and helpers (`inheritComment`,
  `emitCallExprArgs`).
- **`expr.go`** — Shared expression parsers parameterized by
  `operandResolver`. Arithmetic parsing (`parseArithExpr` chain,
  `parseArithPrimary`, constructors, dot access, compile-time
  folding), boolean expression parsing (`parseBoolExpr`,
  `parseBoolChain`, `parseBoolPrimary`, `collectAndChain`), and
  `maybeExprContinuation`. Used by both behavior and fn body paths.
- **`bhvast.go`** — Behavior-level AST parsers and emitters.
  Context setup (`bhvResolver`, `bhvParseCtx`, `bhvEmitCtx`),
  operand resolution (`resolveBhvOperand`), behavior-level emitters:
  `emitBehaviorStmts`, `emitBhvStmtSimple`, `emitBhvCallStmt`.
- **`call_behavior.go`** — Behavior subroutine call system (`call`
  keyword): name resolution (`resolveCallBehaviorName` — direct +
  namespaced), argument parsing (`parseCallBehaviorArgs` — keyword
  args with direction validation), dependency compilation
  (`resolveCallSub`, `compileDependency` — on-demand compilation
  with cycle detection), and call emission at both behavior level
  (`resolveBhvCallArgValue`, `emitBhvCallBehavior`,
  `emitBhvCallBehaviorExpr`) and fn body level
  (`emitFnBodyCallBehavior`, `emitFnBodyCallBehaviorExpr`).
- **`assert.go`** — Assert statement system: `parseAssertStmt`
  (main parser), `parseAssertBlock` (block form), `parseAssertKwArgs`
  (keyword args after message), `parseAssertTrailingArgs` (trailing
  args after condition), `parseAssertParens` (parenthesized form),
  `emitAssertStmt` (check frames + notify/exit error handler).
- **`codegen.go`** — `parseStmtBlock` (unified statement-block
  parser for all three modes), `parseBehaviorBody` (two-phase
  parse+emit, attaches `dependencies` array),
  unified control flow emitters via `emitContext` (`emitIfStmt`,
  `emitWhileStmt`, `emitLoopStmt`, `emitWaitStmt`,
  `emitIfExpr`, `emitModeBlockExpr`), single-expression emitters
  (`emitComparison`, `emitTypeCheck`, `emitTruthyCheck`), direction
  enforcement, `resolveInstructionFrame`.
- **`iterators.go`** — All iterator and for-loop code: `emitForStmt`
  (dispatcher), `emitForIterStmt`, `emitStateMachineIter`,
  `emitInstructionIter`, `emitInlineIterInstruction`, `emitYieldIter`,
  `rewriteYieldToBody`, `emitForStmtRange`, `emitForStmtRangeManual`,
  `emitForStmtRuntime`, `parseForStmt`, `parseIteratorInstruction`,
  `parseIterCallArgs`, `parseBhvOrFnExpr`, `iterKeywordByName`.
  Iterator helpers: `buildIterParamMap` (on `emitContext`),
  `isStaticSequence`, `patchIterDoneSlot`, `patchUnlabeledBreakToLast`.
- **`import.go`** — Import system: parsing, path resolution,
  `processImports`, collision checking, `resolveFnName` for
  namespace-qualified lookup. Builds `scope` (fn map) and `iterScope`
  (iter map) for transitive dependency resolution and assigns both to
  exported functions and iterators with AST bodies.
- **`tests/`** — Test case pairs (`.doit` + `.json`). Library files
  for import tests live in `tests/libs/` (not picked up by the test
  glob).
