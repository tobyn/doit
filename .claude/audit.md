# Language Ergonomics Audit

Periodic review of the doit language for unintuitive syntax, surprising
semantics, or potential footguns.

## Process

1. **Start from the open items below.** Fix all open items that don't
   require the developer's input first — work through them in priority
   order (HIGH, then MEDIUM), committing each fix separately (code,
   tests, docs, and the removal of the item from this file all go in
   the same commit). Skip any item that requires a design decision or
   developer input.
2. **Only after all actionable open items are done**, run a new audit
   round: read all manual pages (`manual/`), all test cases
   (`toolchain/compiler/tests/`), and the parser/emitter source
   (`toolchain/compiler/`) end-to-end. Identify anything that would
   surprise a developer coming from Go/Python/JS/Rust.
3. Categorize each finding as HIGH (likely bugs/confusion), MEDIUM
   (design decision needed), or LOW (minor gap). Do not re-report
   issues that have already been fixed — check git history if unsure.
4. Add new findings to the open items list in this file.
5. Repeat from step 1 until no new actionable items are found in a
   round.
6. **When only design-decision items remain**, stop and ask the
   developer for input on each one. Present the trade-offs and your
   recommendation, then wait for direction before proceeding. Do not
   silently skip these and end the audit — the developer needs to
   weigh in. Once the developer gives direction, implement and commit
   each item separately (same commit rules as step 1).
7. Keep working autonomously on non-design-decision items — do not
   stop to ask the developer for confirmation on those. The developer
   will interrupt if needed.
8. **Do not add resolved items to this file.** History is tracked in
   git commits. Only open items belong here.
9. **Keep this file in sync with the repo.** The commit that fixes an
   item must also remove that item from the open items list below.
   This file should reflect the current state of the codebase at all
   times — it is a real-time work log, not an append-only record.
10. **A fix is not done until it is committed.** The commit is part of
    the fix, not a follow-up step. Never leave uncommitted changes at
    the end of an audit loop or when reporting results to the developer.

## Open items

### Medium priority (design decisions needed)

- **`private fn` visibility is not enforced.** The compiler parses
  `private fn` but does not restrict its visibility — it is callable
  from any behavior in the same compilation unit. The current
  architecture (single source string, no file boundaries) makes
  file-level scoping structurally impossible. Deferred to the module
  system implementation.

### Low priority

- **Fn body expression list omits `%`.** `functions.md` line 295 lists
  `+`, `-`, `*`, `/` without `%` (modulo).
