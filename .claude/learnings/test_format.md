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

The `.json` test files were generated from the reference JavaScript codec and
should not be modified programmatically. When our implementation's output format
differs from the reference (e.g., 1-based vs 0-based integer keys), the test
code bridges the gap via the `refToNative` conversion routine in `main_test.go`
rather than modifying the test data.

## Writing test JSON: numbering conventions

The test JSON uses two numbering systems simultaneously, which is
confusing. Understanding the rules saves significant debugging time.

**`refToNative`** only transforms **map keys** (adds +1 to numeric
string keys). It does **not** touch integer values inside frames.

This means:

- **Frame keys** (top-level `"0"`, `"1"`, ...): 0-based in JSON.
  `refToNative` converts to 1-based (`"0"` → `"1"`).
- **Slot keys** within frames (`"0"`, `"1"`, ...): 0-based in JSON.
  `refToNative` converts to 1-based.
- **Frame reference values** (integers in slot values or `"next"`):
  **1-based (native) in JSON**. `refToNative` does NOT change them.
  The graph isomorphism matcher (`matchBehaviors`) compares integer
  values directly between got and want sides.

**Practical rule**: When writing a test `.json` file, use `compile -json`
to get the compiler's native (1-based) output. Convert frame keys and
slot keys to 0-based (subtract 1), but **leave integer frame reference
values exactly as the compiler produced them** (1-based). Non-frame
data like `"name"`, `"op"`, string slot values, `false`, and
`{"num": N}` objects are unchanged.

**Example**: If the compiler outputs native frame `"2"` with slot
`"1": 3` (a frame reference to native frame 3), the test JSON should
have frame key `"1"` (= 2-1) with slot key `"0"` (= 1-1) and value
`3` (unchanged).

**Why this works**: `matchBehaviors` uses BFS graph isomorphism. When
it sees unequal integer values at the same slot position, it treats
them as frame references and creates a mapping (e.g., got:3 → want:3).
Equal integers are treated as data values. Since both sides use the
same native numbering for frame refs, the mapping is identity and
everything matches.

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
