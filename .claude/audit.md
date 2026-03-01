# Codebase Audit

Periodic review of the doit language and its implementation. Covers two
dimensions in a single pass:

- **Language ergonomics** — unintuitive syntax, surprising semantics,
  or potential footguns that would trip up a developer coming from
  Go/Python/JS/Rust.
- **Implementation quality** — duplicated code, redundant data
  structures, extractable helpers, latent bugs from structural issues,
  and simplification opportunities that don't change the language or
  break tests.

## Process

1. **Start from the open items below.** Fix all open items that don't
   require the developer's input first — work through them in priority
   order (High, then Medium, then Low), committing each fix separately
   (code, tests, docs, and the removal of the item from this file all
   go in the same commit). Skip any item that requires a design
   decision or developer input.
2. **Only after all actionable open items are done**, run a new audit
   round. Read end-to-end:
   - All manual pages (`manual/`)
   - All test cases (`toolchain/compiler/tests/`)
   - The parser/emitter source (`toolchain/compiler/`)

   Look for issues in both dimensions:
   - **Ergonomics**: syntax that would surprise users, semantics that
     differ from expectations, missing error messages, misleading
     behavior. Focus on compiler-level issues — VM limitations that
     the language faithfully reflects (null/false/0 equivalence,
     `is Number` unsupported, etc.) are not audit findings.
   - **Implementation**: duplicated logic between bhv/fn paths,
     redundant data structures, repeated code patterns that could be
     extracted into helpers, structural issues that risk bugs.
3. Categorize each finding as High (likely bugs/confusion), Medium
   (cleanup or design decision needed), or Low (minor gap). Do not
   re-report issues that have already been fixed — check git history
   if unsure.
4. **Verify each finding against the actual code before adding it.**
   Agent-assisted analysis can produce false positives — confirm that
   the issue exists and is not already handled by a different code
   path before recording it.
5. Add new findings to the open items list in this file.
6. Repeat from step 1 until no new actionable items are found in a
   round.
7. **When only design-decision items remain**, stop and ask the
   developer for input on each one. Present the trade-offs and your
   recommendation, then wait for direction before proceeding. Do not
   silently skip these and end the audit — the developer needs to
   weigh in. Once the developer gives direction, implement and commit
   each item separately (same commit rules as step 1).
8. Keep working autonomously on non-design-decision items — do not
   stop to ask the developer for confirmation on those. The developer
   will interrupt if needed.
9. **Do not add resolved items to this file.** History is tracked in
   git commits. Only open items belong here.
10. **Keep this file in sync with the repo.** The commit that fixes an
    item must also remove that item from the open items list below.
    This file should reflect the current state of the codebase at all
    times — it is a real-time work log, not an append-only record.
11. **A fix is not done until it is committed.** The commit is part of
    the fix, not a follow-up step. Never leave uncommitted changes at
    the end of an audit loop or when reporting results to the developer.
12. **If investigation reveals an item is not worth fixing**, remove it
    with a commit explaining why. Move the item to the Rejected section
    below (one-line summary with rationale) so future rounds don't
    re-discover and re-investigate the same thing.

## Open items

### High

### Medium

- **`parseBehaviorBody` duplicates `parseBhvStmtBlockInner` statement
  parsing (~260 lines).** The statement-parsing loop in
  `parseBehaviorBody` (codegen.go:250-510) is a near-copy of
  `parseBhvStmtBlockInner` (bhvast.go:2396-2761). Both handle the same
  statement types with the same switch structure. `parseBehaviorBody`
  additionally handles `@name`/`@param` attributes, but the rest is
  duplicated. The `resolveFnName` bug above is a direct consequence of
  this duplication. `parseBehaviorBody` could call
  `parseBhvStmtBlockInner` for statement parsing with `@name`/`@param`
  as a pre-processing step.

- **`var`/`let` case arms identical within both `parseBehaviorBody` and
  `parseBhvStmtBlockInner` (4 copies).** The `var` and `let` cases in
  both functions are structurally identical — only the `mutable` boolean
  differs. ~40 lines each, 4 copies. A helper like
  `parseBhvLetVarStmt(mutable, syms)` would replace all four.

- **Stdlib re-parsed for every imported file.** `parseImportedFile`
  (import.go:345) calls `parseStdlib(p.stdlibFS)` for each imported
  file. The stdlib is immutable during compilation and only needs to be
  parsed once. With N imported files, the stdlib is parsed N+1 times.
  Fix: pass the already-parsed stdlib map as a parameter and clone it.

- **`collectImportedFns` and `collectUserFns` share most of their loop
  body (~55 lines).** Both handle the same top-level keywords
  (`behavior`, `private`, `fn`, `const`, `import`) with nearly identical
  dispatch. The differences (behavior ID collection, collision tracking)
  could be handled by a callback or options struct.

- **`emitBhvIfExpr`/`emitBhvIfExprMulti` duplication (~85 lines), and
  same for fn body counterparts.** The single-target and multi-target
  if-expression emitters share identical branch-collection, condition
  resolution, check-frame emission, body emission, and jump-patching
  structure. Only the tail-emission step differs (single target vs
  slice). Could parameterize with a tail-emission callback, or handle
  single targets as a `[]any{target}` slice.

- **Ticks/count expression parsing duplicated 4 times.** The three-way
  switch on `tokNumber`/`tokLParen`/`tokIdent` for parsing a simple
  expression appears in `parseBhvLoopStmt`, `parseBhvWaitStmt`,
  `parseFnBodyLoopStmt`, and `parseFnBodyWaitStmt` (~25 lines each).
  A shared `parseSingleExpr(resolve, errContext)` helper would
  eliminate all four copies.

- **`tryEvalExpr`/`tryEvalStmts` duplicate call-argument evaluation 3
  times.** The "evaluate positional args, evaluate keyword args, call
  `tryEvalCall`" pattern is copy-pasted in the `*CallExpr` case of
  `tryEvalExpr`, the `*MultiReturnStmt`/`*CallExpr` case, and the
  `*CallStmt` case of `tryEvalStmts`. A `tryEvalCallArgs` helper would
  centralize this (~45 lines).

### Low

- **`arithmeticOpName` and `compoundAssignOpName` are identical
  functions.** Both return `arithOpNames[kind]` (codegen.go:664-688).
  Could merge into a single `opName` function.

- **`exprArity`/`ifExprArity` vs `exprArityStatic`/`ifExprArityStatic`
  duplication.** The method versions (bhvast.go:2177-2205) use `p.fns`,
  the free-function versions (parse.go:3498-3526) take a `fns` map
  parameter. If `returnStmtArity` were a method on `*parser`, the
  free-function versions could be eliminated (~30 lines).

- **Comment inheritance boilerplate in `emitFnBody` (14 occurrences).**
  Every statement case repeats the 4-line `callComment := s.Comment;
  if callComment == "" { callComment = comment }` pattern. A one-line
  helper would save ~42 lines.

- **`allAliases` map stores unused positions.** (import.go:31,54-66)
  Declared as `map[string]int` but the stored position is immediately
  discarded with `_ = prevPos`. Should be `map[string]bool`.

- **Redundant nil guards in `resolveFnName`.** (import.go:607-614)
  The `p.namespaces != nil` and `p.namespaceConsts != nil` checks
  are unnecessary — Go map lookups on nil maps are safe (return zero
  value). Removing them reduces nesting depth.

- **Spurious `_ = name` in `collectImportedFns`.** (import.go:456)
  After `name, err := p.parseConstDecl(true)`, should be `_, err :=`.

- **Redundant `isConstructor` check at bhvast.go:2715.** The
  `!isConstructor(tok.val)` guard in the `isExprTail` condition is
  always true at that point — constructors are handled earlier with
  an early return at line 2673.

### Deferred

- **`private fn` visibility is not enforced.** The compiler parses
  `private fn` but does not restrict its visibility — it is callable
  from any behavior in the same compilation unit. The current
  architecture (single source string, no file boundaries) makes
  file-level scoping structurally impossible. Deferred to the module
  system implementation.

- **Full emitter unification via `emitContext` interface.** The bhv
  and fn emitter pairs differ only in operand resolution, scope
  management, and body emission dispatch. Detailed analysis of the
  8 emitter pairs shows:
  - Control flow emitters (`emitIfStmt`, `emitWhileStmt`,
    `emitLoopStmt`, `emitCountedLoop`, `emitForStmt*`,
    `emitWaitStmt`) are **84–96% structurally identical** — only
    3–4 callback points differ (resolveBool, emitBody,
    exprGetValue, scope push/pop). ~250–300 lines saveable.
  - Statement dispatch (`emitBhvStmtSimple` vs `emitFnBody` switch)
    is ~70% identical, differing in variable declaration and comment
    merging.
  - Expression emission (`emitBhvExprTo` vs `emitExprTo`) is ~50%
    identical — a smaller win.

  An `emitContext` interface with `resolveBool()`, `emitBody()`,
  `exprGetValue()`, and scope callbacks would allow merging the
  control flow emitters into single implementations. Large
  architectural change — needs developer input.

### Rejected

- **`parseBhvVarInit` / `parseBhvDefaultStmt` RHS parsing
  duplication.** Investigated and rejected — extraction would require
  a complex return type and interleaving protocol (variable
  declaration timing, LetStmt vs AssignStmt wrapping, fn call
  continuation) that adds more indirection than it saves.
