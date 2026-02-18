---
paths:
  - "toolchain/compiler/**/*"
---

# doit Compiler

The `compiler` package compiles the doit language into the structured representation of a Desynced
behavior supported by the `codec` package. See `.claude/rules/behavior_json.md` for the compiled
output format.

## Architecture

- **`compiler/compiler.go`** — Public API (`Compile`, `CompileString`) and shared types
- **`compiler/scanner.go`** — Token types, scanner, error formatting
- **`compiler/parse.go`** — Stdlib parsing, file-level parsing, function definitions, call expansion
- **`compiler/codegen.go`** — Behavior body compilation: loops, if/else, deferred body emission
- **`compiler/tests/`** — Test case pairs: `.doit` (doit language source code) + `.json` (JSON representation of the compiled code)

The compiler is structured as a scanner and recursive descent parser. The scanner tokenizes the
source into identifiers, string literals, numbers, braces, parentheses, colons, commas, and
comparison/assignment operators, skipping whitespace and line comments (`//` and `#`). The parser
consumes tokens and directly emits the `*codec.Object` output (type `Behavior`) without an
intermediate AST. Errors include line:column positions.

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
object key ordering). Do not rely on the JSON strings to be the same. The compiler may also emit frames in a different
order than the handwritten expected output — the test comparison uses graph-isomorphism (`matchBehaviors` in
`main_test.go`) to verify structural equivalence regardless of frame numbering.
