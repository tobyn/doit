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

The toolchain itself is a self-contained executable canonically named `doit`. Its functionality is accessed via its various subcommands.

### Converting exported strings to JSON

```sh
doit decode [-b|-c] [-o output_path] [input_path]
```

Desynced allows the player to export blueprints and behaviors as Base62-encoded strings. The `decode` subcommand reads an
exported string, parses it, and writes a JSON object containing the parsed data.

The structure of the written object is:

```json
{ "type": "<type>", "value": <object> }
```

The value of `type` will be `B` if the string contains a blueprint, or `C` if the string contains a behavior. The structure
of `value` depends on the type, and will match the output of the reference JavaScript decoder.

If the `-b` option is provided, the command fails if the decoded object is not a blueprint. If the `-c` option is
provided, the command fails if the decoded object is not a behavior. The `-b` and `-c` options are mutually exclusive.
When either option is provided, only the `value` object is written instead of the type/value envelope.

If no arguments are provided, `decode` reads from standard input and writes to standard output. If an `input_path`
argument is provided, it reads from the given path. If the `-o <output_path>` option is provided, it writes to the given
output path.

The command exits with a zero status if decoding and printing is successful, non-zero otherwise.

### Converting JSON to exported strings

```sh
doit encode [-b|-c] [-o output_path] [input_path]
```

The `encode` subcommand reads a JSON object, encodes it as a Base62 string in Desynced's export format, and writes the
result.

If neither `-b` nor `-c` is provided, the input must be a JSON object with `type` and `value` fields:

```json
{ "type": "<type>", "value": <object> }
```

The value of `type` must be `B` for a blueprint or `C` for a behavior. The structure of `value` depends on the type.

If the `-b` option is provided, the input is treated as a blueprint `value` object directly. If the `-c` option is
provided, the input is treated as a behavior `value` object directly. The `-b` and `-c` options are mutually exclusive.

If no arguments are provided, `encode` reads from standard input and writes to standard output. If an `input_path`
argument is provided, it reads from the given path. If the `-o <output_path>` option is provided, it writes to the given
output path.

The command exits with a zero status if encoding and printing is successful, non-zero otherwise.
