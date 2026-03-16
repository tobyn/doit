---
paths:
  - "toolchain/**/*"
---

# doit Language Toolchain

The `toolchain` directory contains the [Go](https://go.dev) implementation of the doit language's
toolchain. The module compiles into a single toolchain binary.

## Build and Test Commands

All commands run from the `toolchain/` directory:

```sh
go build -o doit                  # Build (use doit.exe on Windows)
go test ./...                     # Run all tests
go test ./compiler                # Run compiler tests only
go test ./compiler -run TestCompileErrors  # Run compiler error case tests only
go test ./codec                   # Run codec tests only

# Validate against reference JS codec (requires Node.js):
node codec/refcodec.js decode file.b62   # decode with reference
node codec/refcodec.js encode file.json  # encode with reference
```

## Architecture

- **`main.go`** — Toolchain CLI's entry point; also exposes `Compile` for
  use by integration tests
- **`main_test.go`** — Integration smoke test (compile-encode-decode
  pipeline) and CLI flag tests
- **`stdlib/`** — The standard library, embedded into the binary at build
  time
- **`codec/`** — Encodes and decodes the Base62 strings used by the game to
  represent blueprints and behaviors. Tests live in `codec/codec_test.go`.
- **`syntax/`** — Authoritative lexical grammar for doit. Contains
  the scanner (`scanner.go`), token types, keywords, type constructors,
  the semantic tokenizer (`highlight.go`), and grammar sync tests
  (`grammar_sync_test.go`). Used by both the compiler and LSP.
- **`compiler/`** — Compiles the doit language into the structured
  representation supported by the `codec` package. Tests live in
  `compiler/compiler_test.go`, using test cases from `compiler/tests/`.
  The compiler's `scanner` wraps `syntax.Scanner`, keeping a local
  `token` type with lowercase fields to minimize refactoring churn.
- **`formatter/`** — Canonical code formatter for doit source. Normalizes
  indentation (4 spaces), operator spacing, trailing semicolons, and
  blank lines. Used by both `doit fmt` and the LSP formatting handler.
- **`lsp/`** — Language Server Protocol server for doit. Communicates
  over stdio JSON-RPC 2.0. Provides semantic token highlighting via
  the `syntax.Tokenize` function, document formatting via the
  `formatter` package, real-time diagnostics (errors and warnings)
  via `compiler.Check`, document symbols (outline), hover info, and
  signature help. Invoked as `doit language-server`.
- **`sanity_check/`** — Drift checker program and golden files. Compiles
  sanity check source and compares against last-known-good output.
  See `.claude/learnings/sanity_check.md` for details.

## Pre-commit: Sanity Check Drift

Before each commit that touches `toolchain/**/*.go` or
`toolchain/stdlib/**/*.doit`, run the sanity check drift checker:

```sh
cd toolchain
go run ./sanity_check
```

This is informational — drift does not block the commit. The program
manages its own state in `.claude/learnings/sanity_check.md` (the
`> Drift status:` line). If it updates the status to drifted, include
the changed `sanity_check.md` in the commit.

## Toolchain functionality

See `manual/toolchain.md` for user-facing CLI documentation.

The toolchain itself is a self-contained executable canonically named `doit`.
Its functionality is accessed via its various subcommands. See `usage/` for
subcommand documentation. Each file contains a one-line summary followed by
detailed help text. These files are embedded into the binary and used as the
output of `doit help`.
