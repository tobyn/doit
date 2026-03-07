# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Project Overview

This project defines and implements a programming language called doit ("do it") that targets
[Desynced](https://www.desyncedgame.com/)'s behavior controllers

## Generating Behaviors

When asked to generate a behavior, prefer writing doit source code that
compiles into the behavior over generating the JSON directly. Dog-fooding
the language helps guide its evolution. If something isn't supported by the
language or doesn't seem to work correctly, ask the developer for help or
permission to fall back to raw JSON.

## Completing Changes

Every implementation task has three parts — treat them as a single unit
of work, not separate steps the developer must request:

1. **Code** — the implementation itself
2. **Manual** — update `manual/` docs so users can discover and use the
   feature (new syntax, new operators, changed behavior, etc.)
3. **Project memory** — update `.claude/` files so future sessions
   have accurate context (rules/, learnings/, etc.)

All three must be done before considering the task complete. Do not wait
for the developer to ask for docs or memory updates.

## Keeping Tests Green

All tests must pass after every change. Run `go test ./...` from
`toolchain/` before considering any change set done. If tests fail —
whether from the current change or pre-existing — investigate and fix
them as part of the work. If the fix is unrelated to the current task
and significant enough to warrant its own commit, ask the developer
whether to include it or split it out. Never dismiss failing tests as
"pre-existing" without flagging them.

## Validating Against the Reference Codec

The reference JavaScript codec (`toolchain/codec/tests/reference/`) is
the authoritative specification for the binary format. A CLI wrapper at
`toolchain/codec/refcodec.js` makes it easy to use from the command
line (requires Node.js).

Any behavior test JSON we create or update must roundtrip cleanly
through the reference codec. After writing a `.json` test file, verify:

```sh
(echo C; cat compiler/tests/foo.json) \
  | node codec/refcodec.js encode \
  | node codec/refcodec.js decode \
  | tail -n +2 \
  | python3 -c "import sys,json; a=json.load(sys.stdin); b=json.load(open(sys.argv[1])); exit(0 if a==b else 1)" compiler/tests/foo.json \
  && echo OK
```

If it prints `OK`, the roundtrip succeeded. If not, our JSON contains
values the reference codec handles differently — fix the discrepancy
before considering the work done.

## Project Memory

Project memory lives in two places under `.claude/`:

- **`rules/`** — Always-loaded context. Path-scoped rules (via
  frontmatter) load only when touching relevant files. `signposts.md`
  provides pointers to on-demand reference material.
- **`learnings/`** — On-demand reference material. Detailed design
  decisions, type system docs, game model, future plans, etc. Read
  via signpost pointers when the topic is relevant.

After implementing a feature or making a design change, update the
relevant files to reflect the new state. This keeps future sessions
accurate — they rely on these files rather than re-exploring the codebase.

During long sessions, write important design decisions and intermediate
conclusions to project memory promptly rather than waiting until the end.
Context from earlier in the conversation may be compressed, so anything
worth remembering should be persisted to files before it's needed later.

## Scratch Directory

The `scratch/` directory in the project root is a shared collaboration area
for passing large or unwieldy data between the developer and Claude Code.
It is excluded from Git and may not always exist — recreate it if needed.
Do not overwrite existing files in it.

## Architecture

- **`toolchain/`** — The doit language's Go-based toolchain implementation
