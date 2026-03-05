# Signposts

Pointers to detailed reference material. Read the linked file when the
topic is relevant to your current task.

## Processes

- **Language ergonomics audit** — `.claude/learnings/audit.md`
  Process and open items for auditing language ergonomics.
- **Sanity check** — `.claude/learnings/sanity_check.md`
  Process and test artifacts for running a sanity check.
- **Therapy** — `.claude/learnings/therapy.md`
  Reorganize project memory to keep context lean and useful.

## Language reference

- **Language syntax and usage** — `manual/` (start at `manual/index.md`)
  User-facing documentation for the doit language.
- **Type system** — `.claude/learnings/types.md`
  All data types, the register composite model, `&` operator, and
  compile-time vs runtime semantics.
- **Desynced game model** — `.claude/learnings/game.md`
  VM execution model, data types, registers, and instruction structure.
- **Standard library** — `.claude/learnings/stdlib.md`
  Stdlib architecture, function categories, mode enums, and
  `instructions.lua` licensing.
- **Test case format** — `.claude/learnings/test_format.md`
  Test pair conventions, JSON numbering, AI-generated test markers.

## Game updates

- **Desynced 1.0 changes** — `.claude/learnings/desynced_1_0.md`
  New/removed/changed instructions, event system, faction registers,
  path blocking, `bitwise_op` expansion, and entity registries.

## Design

- **Design decisions** — `.claude/learnings/decisions.md`
  Non-obvious choices and their rationale. Consult when making changes
  to avoid contradicting past decisions.
- **Continuation system** — `.claude/learnings/continuations.md`
  Design rules for the branching/continuation system (exec blocks,
  bridging vs looping, expression form, pure-logic dispatch).
- **Future ideas** — `.claude/learnings/future.md`
  Iterators, subroutines, and other planned features.
