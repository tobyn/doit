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
- **`compiler/scanner.go`** — `scanner` struct (embedded by `parser`), token types, `Keywords` map, error formatting
- **`compiler/parse.go`** — Stdlib parsing, file-level parsing, function definitions, call expansion
- **`compiler/codegen.go`** — Behavior body compilation: loops, if/else, deferred body emission
- **`compiler/tests/`** — Test case pairs: `.doit` (doit language source code) + `.json` (JSON representation of the compiled code)

The compiler is structured as a standalone `scanner` struct embedded in a recursive-descent `parser`.
The scanner tokenizes the source into identifiers, string literals, numbers, braces, parentheses,
colons, commas, and comparison/assignment operators, skipping whitespace and line comments (`//` and
`#`). The parser consumes tokens via the promoted scanner methods and directly emits the
`*codec.Object` output (type `Behavior`) without an intermediate AST. Errors include line:column
positions. The exported `Keywords` map lists all reserved keywords for use by editor tooling.

The `Compile` and `CompileString` functions accept an `fs.FS` containing the standard library and a
`behaviorID string` that selects which behavior to compile. When `behaviorID` is empty and the source
contains a single behavior, it is auto-selected. When the source contains multiple behaviors,
`behaviorID` must name one of them. The compiler parses stdlib function definitions first, then
compiles user source. Stdlib functions that contain an `instruction` intrinsic are inlined at call
sites — the compiler substitutes arguments into the instruction template fields.

## Test Case Format

Each test case is a pair of files sharing the same base name in `compiler/tests/`:
- **`.doit`** — a doit language source file
- **`.json`** — The expected JSON representation of the compiler output

For multi-behavior test cases, the file name uses the `__` convention: the part after `__` is the
behavior ID passed to the compiler. For example, `multi_behavior__second.doit` compiles the
`second` behavior and compares against `multi_behavior__second.json`.

Tests are in the root `main_test.go`. `TestCompile` compiles each test case, encodes via `Compile`,
decodes, and compares against the JSON file. `TestCompileErrors` tests error cases (e.g., multiple
behaviors without `-b`, nonexistent behavior ID, no behaviors) using `compiler.CompileString`
directly.

The JSON in the JSON file may differ from a JSON rendering of the compiled code in trivial ways (e.g., whitespace,
object key ordering). Do not rely on the JSON strings to be the same. The compiler may also emit frames in a different
order than the handwritten expected output — the test comparison uses graph-isomorphism (`matchBehaviors` in
`main_test.go`) to verify structural equivalence regardless of frame numbering.
