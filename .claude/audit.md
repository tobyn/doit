# Language Ergonomics Audit

Periodic review of the doit language for unintuitive syntax, surprising
semantics, or potential footguns.

## Process

1. **Start from the open items below.** Fix all open items that don't
   require the developer's input first — work through them in priority
   order (HIGH, then MEDIUM), committing each fix separately. Skip any
   item that requires a design decision or developer input.
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
   weigh in.
7. Keep working autonomously on non-design-decision items — do not
   stop to ask the developer for confirmation on those. The developer
   will interrupt if needed.
8. **Do not add resolved items to this file.** History is tracked in
   git commits. Only open items belong here.

## Open items

### Medium priority (design decisions needed)

- **`-Werror` style flag for promoting warnings to errors.** The
  compiler now supports warnings (returned alongside compiled output).
  A flag to treat warnings as errors would be useful for CI/strict
  mode. Needs a CLI flag (`-Werror` or similar) and plumbing through
  the `Compile`/`CompileString` API.

- **Undeclared variable names silently succeed as function arguments.**
  `set_reg completely_undeclared_var` compiles without error or warning.
  The compiler treats the name as a runtime register reference. A typo
  in a variable name passed as a function argument has no compile-time
  feedback. Adding a warning for names not in the symbol table would
  catch the common typo case. The challenge is backward compatibility
  and distinguishing intentional "dynamic" register names from typos.

- **`private fn` visibility is not enforced.** The compiler parses
  `private fn` but does not restrict its visibility — it is callable
  from any behavior in the same compilation unit. The current
  architecture (single source string, no file boundaries) makes
  file-level scoping structurally impossible. Either document the
  limitation, remove the feature, or repurpose it.

### Low priority

(none)
