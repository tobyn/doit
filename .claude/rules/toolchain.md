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
- **`compiler/`** — Compiles the doit language into the structured
  representation supported by the `codec` package. Tests live in
  `compiler/compiler_test.go`, using test cases from `compiler/tests/`.
  Also contains `highlight.go` — a semantic tokenizer (`Tokenize`)
  that classifies source tokens into LSP semantic types (keyword,
  function, variable, parameter, type, label, register, etc.) using
  the scanner's `rawNext()` method with context-aware classification.
  `grammar_sync_test.go` verifies that keywords, type constructors,
  and escape sequences stay in sync between `scanner.go` and the
  TextMate grammar (`editors/doit.tmLanguage.json`).
- **`lsp/`** — Language Server Protocol server for doit. Communicates
  over stdio JSON-RPC 2.0. Provides semantic token highlighting via
  the compiler's `Tokenize` function. Invoked as `doit language-server`.

## Toolchain functionality

See `manual/toolchain.md` for user-facing CLI documentation.

The toolchain itself is a self-contained executable canonically named `doit`.
Its functionality is accessed via its various subcommands. See `usage/` for
subcommand documentation. Each file contains a one-line summary followed by
detailed help text. These files are embedded into the binary and used as the
output of `doit help`.
