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
go build -o doit                # Build the toolchain binary (use `doit.exe` instead of `doit` on Windows)
go test ./...                   # Run all tests
go test ./codec                 # Run codec tests only
go test ./compiler              # Run compiler tests only
go test ./codec -run TestCodec  # Run a specific test
```

## Architecture

- **`main.go`** — Toolchain CLI's entry point
- **`codec/`** — A package that encodes and decodes the Base62 strings used by the game to represent blueprints and behaviors
- **`compiler/`** — A package that compiles the doit language into the JSON intermediate representation supported by the `codec` module

## Toolchain functionality

The toolchain itself is a self-contained executable canonically named `doit`. Its functionality is accessed via its
various subcommands. See `usage/` for subcommand documentation. Each file contains a one-line summary followed by detailed
help text. These files are embedded into the binary and used as the output of `doit help`.
