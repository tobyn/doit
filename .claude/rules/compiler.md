---
paths:
  - "toolchain/compiler/**/*"
---

# doit Compiler

The `compiler` package compiles the doit language into the structured representation of a Desynced
behavior supported by the `codec` package.

## Architecture

- **`compiler/compiler.go`** — Scanner, parser, and code generation
- **`compiler/tests/`** — Test case pairs: `.doit` (doit language source code) + `.json` (JSON representation of the compiled code)

The compiler is structured as a single-pass scanner and recursive descent parser. The scanner
tokenizes the source into identifiers, string literals, braces, parentheses, colons, and commas,
skipping whitespace and line comments (`//` and `#`). The parser consumes tokens and directly emits
the `*codec.Object` output (type `Behavior`) without an intermediate AST. Errors include
line:column positions.

The `Compile` and `CompileString` functions accept an `fs.FS` containing the standard library. The
compiler parses stdlib function definitions first, then compiles user source. Stdlib functions that
contain an `instruction` intrinsic are inlined at call sites — the compiler substitutes arguments
into the instruction template fields.
 
## Test Case Format

Each test case is a pair of files sharing the same base name in `compiler/tests/`:
- **`.doit`** — a doit language source file
- **`.json`** — The expected JSON representation of the compiler output

Tests are in the root `main_test.go`. For each test case, the source is compiled and encoded via `Compile`, then decoded
and compared against the JSON file.

The JSON in the JSON file may differ from a JSON rendering of the compiled code in trivial ways (e.g., whitespace,
object key ordering). Do not rely on the JSON strings to be the same. Parse the JSON and validate deep equality between
the expected JSON and the compiler results.
