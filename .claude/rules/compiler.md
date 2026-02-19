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
- **`compiler/scanner.go`** — `scanner` struct (embedded by `parser`), token types,
  `Keywords` map, error formatting
- **`compiler/parse.go`** — Stdlib parsing, file-level parsing, function definitions,
  call expansion
- **`compiler/codegen.go`** — Behavior body compilation: loops, if/else, deferred body
  emission
- **`compiler/tests/`** — Test case pairs: `.doit` (source) + `.json` (expected compiled
  output)

The compiler is structured as a standalone `scanner` struct embedded in a
recursive-descent `parser`. The scanner tokenizes the source into identifiers,
string literals, numbers, braces, parentheses, colons, commas, `@`, and
comparison/assignment operators, skipping whitespace and `#` line comments. The parser consumes tokens via the promoted scanner methods and
directly emits the `*codec.Object` output (type `Behavior`) without an
intermediate AST. Errors include line:column positions. The exported `Keywords`
map lists all reserved keywords for use by editor tooling.

Brace-delimited blocks fall into two categories. **Statement blocks** (behavior
bodies, function bodies, if/else/while/loop bodies) all contain a sequence of
statements and can be parsed uniformly. **Structured data blocks** (the
`instruction` intrinsic and the `@name` localized block) each have their own
parsing rules and semantics.

**Statement termination** (not yet implemented): The language is line-oriented.
Statements terminate at end-of-line by default, with three exceptions:
(1) block-ending statements extend to `}` and peek for `else`/`else if`
continuation; (2) parenthesized function calls extend to the closing `)`
across lines; (3) unparenthesized function calls with a trailing comma
continue onto the next line. The scanner currently treats newlines as plain
whitespace; implementing these rules will require newline awareness.

**Function calls**: Parentheses are optional. `notify "Hello"` and
`notify("Hello")` are equivalent. The no-parens style is preferred for
statement-level calls. Parenthesized calls will become useful for argument
grouping in complex expressions.

**Doc comments** (`#!`): The scanner collects `#!` lines into a
`docComment` field, reset on each `skipWhitespaceAndComments` call and
preserved across `unget`. The parser captures `docComment` after scanning
the first token of each statement, then passes it through compilation. For
instruction-based stdlib calls, the comment is set as `"cmt"` on the
emitted frame. For user-defined function calls, each body call uses its
own `#!` comment if present, otherwise inherits the caller's comment,
recursively up the call stack.

Behavior IDs can be bareword identifiers or quoted strings. The `@name` attribute sets the display
name (at most once per behavior); if omitted, the behavior ID is used as the default name. `@name`
supports a localized block form (`@name { en_US "English" ja "日本語" }`) that selects the best
match for the compiler's locale setting using `golang.org/x/text/language`.

The `Compile` and `CompileString` functions accept an `fs.FS` containing the standard library, a
`behaviorID string` that selects which behavior to compile, and a `locale string` (BCP 47 tag) for
resolving localized `@name` blocks. When `behaviorID` is empty and the source contains a
single behavior, it is auto-selected. When the source contains multiple behaviors,
`behaviorID` must name one of them. When `locale` is empty, localized `@name` blocks use
their first entry. The compiler parses stdlib function definitions first, then compiles
user source. Stdlib functions that contain an `instruction` intrinsic are inlined at call
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

The JSON in the JSON file may differ from a JSON rendering of the compiled code in trivial
ways (e.g., whitespace, object key ordering). Do not rely on the JSON strings to be the
same. The compiler may also emit frames in a different order than the handwritten expected
output — the test comparison uses graph-isomorphism (`matchBehaviors` in `main_test.go`)
to verify structural equivalence regardless of frame numbering.
