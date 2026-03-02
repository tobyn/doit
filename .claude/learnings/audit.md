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
   order (High, then Medium, then Low), committing each fix separately.
   **One item = one commit.** Never batch multiple items into a single
   commit, even if they are trivial, related, or at the same priority
   level. Each commit includes the code fix, any tests, docs updates,
   and the removal of that single item from this file. Skip any item
   that requires a design decision or developer input.
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

- **Positional arg comma handling inconsistent between behavior and fn
  body calls.** At behavior level, commas between positional args in
  unparenthesized calls are optional — `f 1, 2` and `f 1 2` both work
  (bhvast.go:882-894 consumes optional comma). In fn body unparenthesized
  calls, commas between positional args cause an error — `f 1, 2` fails
  with "expected argument value, got ','" because `parseFnBodyCallArgs`
  (parse.go:828) has no comma handling for non-paren mode. The manual
  says "multiple arguments are separated by commas" (functions.md:8)
  without noting this distinction. Either align the behavior (add
  optional comma consumption to fn body path) or document the
  difference. **Design decision needed.**

### Low

- **Dead `syms` parameter in `checkVarName`.** The `syms *symbolTable`
  parameter (codegen.go:989) is never used — the function only checks
  `isConstructor` and `Keywords`. Passed as `nil` from fn body context
  (parse.go:5142) and as a live pointer from behavior context
  (bhvast.go:2044).

### Deferred

### Rejected

- **`private fn` visibility within the same file.** Not a bug —
  `private` means file-scoped (not importable), and within a file
  everything is visible. Matches Go/Python/Rust module semantics.
- **`parseBhvVarInit` / `parseBhvDefaultStmt` RHS parsing
  duplication.** Investigated and rejected — extraction would require
  a complex return type and interleaving protocol (variable
  declaration timing, LetStmt vs AssignStmt wrapping, fn call
  continuation) that adds more indirection than it saves.
