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

- **`ast.go`** — `Stmt` interface (17 types) and `Expr` interface
  (15 types). `isTerminalStmt`/`terminalKeyword` for unreachable code
  detection.
- **`scanner.go`** — `scanner` struct (embedded by `parser`), token
  types, `Keywords` map, `skipToCloseBrace`. The `parser` struct
  extends `scanner` with `fns`, `consts`, `enums`, import state,
  loop tracking, `callExprParser` callback, and `warnings []string`.
- **`compiler.go`** — Public API (`Compile`/`CompileString`), shared
  types (`symbolSet`, `fnDef`, `paramDef`, `symbolTable`, `constDef`,
  `enumDef`), `frameBuilder`/`frameRef` abstraction, `emitContext`
  struct, `execMode` tracking, slot constants for `check_number`,
  `compare_register`, and `value_type`.
- **`parse.go`** — Stdlib parsing, file-level parsing, function
  definitions (`parseUserFn`), fn body AST parsing and emission
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
   `parseUserFn`) and `enum` (via `parseEnumDecl`) declarations.
   Stdlib enums are propagated to user/imported file parsers via
   `parser.stdlibEnums`.
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

**Two emission contexts**: Behavior-level and fn body emission share
unified control flow emitters via `emitContext`. Two factory functions
— `bhvEmitCtx` (bhvast.go) and `fnEmitCtx` (parse.go) — build the
context with closures capturing resolution state.

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

**Positional arg separators**: At behavior level, commas between
positional args are optional.

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
