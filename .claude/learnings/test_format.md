# Compiler Test Format

Reference for writing and understanding compiler test cases.

## Test Case Pairs

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

The JSON in the JSON file may differ from a JSON rendering of the compiled code in trivial
ways (e.g., whitespace, object key ordering). Do not rely on the JSON strings to be the
same. The compiler may also emit frames in a different order than the handwritten expected
output — the test comparison uses graph-isomorphism (`matchBehaviors` in `main_test.go`)
to verify structural equivalence regardless of frame numbering.

The `.json` test files can be regenerated from their `.doit` sources using
`TestUpdateGolden` (run with `DOIT_UPDATE_GOLDEN=1`). This compiles each
`.doit` file through the full encode/decode round-trip and writes the result
as JSON. `TestUpdateGolden` is a permanent fixture in `main_test.go`.

## Numbering conventions

The codec and compiler use 0-based integer keys for table keys (both
frame keys and instruction slot keys), matching the reference JavaScript
implementation. Integer **values** (such as frame references in `"next"`
or branch slots) remain 1-based because they are data values stored
as-is in the binary format — the game's Lua engine uses 1-based table
lookup, so frame references must match.

Summary:

- **Frame keys** (top-level `"0"`, `"1"`, ...): 0-based.
- **Slot keys** within frames (`"0"`, `"1"`, ...): 0-based.
- **Frame reference values** (integers in slot values or `"next"`):
  1-based. These are plain integer data, not table keys.

The graph isomorphism matcher (`matchBehaviors`) handles frame reference
remapping via BFS, so frame numbering differences between got and want
are tolerated.

## Locale directive

Test `.doit` files can specify a compilation locale via a `# locale: <tag>`
comment on the second line (after `# AI-generated test`). The `TestCompile`
harness reads this directive and passes the locale to the compiler. If
absent, the locale defaults to `""` (first entry wins).

## AI-generated tests

AI-generated `.doit` test files are marked with a `# AI-generated test`
comment on their first line. When creating a new test case, add this comment.

All files belonging to an AI-generated test case (`.doit`, `.json`, and any
other associated files) may be edited freely to fix or improve them.
All files belonging to a test case without this marker are human-authored
and should not be modified programmatically.

## Error case coverage

New language features must include error case tests in `TestCompileErrors`
(or `TestDecodeErrors` for codec changes), not just happy-path `.doit`/`.json`
pairs. Cover at minimum: invalid syntax the user is likely to write by
mistake, and each explicit error path added by the implementation. For
example, keyword arguments added tests for unknown keywords, duplicate
keywords, positional-after-keyword in definitions, and extra positional
args at call sites.
