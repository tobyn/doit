# Signposts

Pointers to detailed reference material. Read the linked file when the
topic is relevant to your current task.

## Processes

- **In-game testing** — `.claude/learnings/in_game_testing.md`
  Guidelines for building behaviors the developer tests in-game
  (parameters for I/O, exit at end, output to scratch/).
- **Language ergonomics audit** — `.claude/learnings/audit.md`
  Process and open items for auditing language ergonomics.
- **Sanity check** — `.claude/learnings/sanity_check.md`
  Process and test artifacts for running a sanity check.
- **Therapy** — `.claude/learnings/therapy.md`
  Reorganize project memory to keep context lean and useful.

## Editor integration

- **TextMate grammar** — `editors/doit.tmLanguage.json`
  Regex-based syntax highlighting grammar for doit. Used by VS Code,
  JetBrains (via TextMate bundles), and web highlighters (Shiki).
- **Semantic tokenizer** — `toolchain/syntax/highlight.go`
  Context-aware token classifier producing LSP semantic types.
- **Lexical grammar** — `toolchain/syntax/scanner.go`
  Authoritative token definitions, scanner, keywords, and type constructors.
- **Language server** — `toolchain/lsp/server.go`
  LSP server over stdio. Invoked as `doit language-server`.

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

## Design

- **Design decisions** — `.claude/learnings/decisions.md`
  Non-obvious choices and their rationale. Consult when making changes
  to avoid contradicting past decisions.
- **Continuation system** — `.claude/learnings/continuations.md`
  Design rules for the branching/continuation system (exec blocks,
  bridging vs looping, expression form, pure-logic dispatch).
- **Future ideas** — `.claude/learnings/future.md`
  Iterators, subroutines, and other planned features.
