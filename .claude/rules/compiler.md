---
paths:
  - "toolchain/compiler/**/*"
---

# doit Compiler

The `compiler` package compiles the doit language into the structured representation of a Desynced
behavior supported by the `codec` package. See `.claude/rules/behavior_json.md` for the compiled
output format.

## Architecture

- **`compiler/compiler.go`** — Public API (`Compile`, `CompileString`), shared types
  (`fnDef`, `fnBodyArg`, `symbolTable`, `unitRegisters`),
  and `frameBuilder`/`frameRef` abstraction for frame management
- **`compiler/scanner.go`** — `scanner` struct (embedded by `parser`, holds `locale`
  field), token types, `Keywords` map, `$`-prefix scanning, error formatting,
  `parseLocalePrefix` helper, `resolveLocalizedDocComment` for localized `#!` comments
- **`compiler/parse.go`** — Stdlib parsing, file-level parsing, function definitions,
  call expansion with `[]any`/`map[string]any` argument types
- **`compiler/codegen.go`** — Behavior body compilation: param/let/var declarations,
  symbol table tracking, rich argument parsing, assignment target resolution,
  loops, if/else, deferred body emission, `matchLocale` shared BCP 47 matching helper
- **`compiler/tests/`** — Test case pairs: `.doit` (source) + `.json` (expected compiled
  output)

The compiler is structured as a standalone `scanner` struct embedded in a
recursive-descent `parser`. The scanner tokenizes the source into identifiers
(including `$`-prefixed unit register names), string literals, numbers,
braces, parentheses, colons, commas, `@`, and comparison/assignment
operators, skipping whitespace and `#` line comments. The parser consumes
tokens via the promoted scanner methods and directly emits the
`*codec.Object` output (type `Behavior`) via `frameBuilder` without an
intermediate AST. Errors include line:column positions. Wire format details
(like Lua's 1-based indexing) are encapsulated at the `frameBuilder`
boundary — compilation logic uses 0-based indices internally, and `frameRef`
values are converted to 1-based wire format integers by `finalize`. The
exported `Keywords` map lists all reserved keywords for use by editor tooling.

Brace-delimited blocks fall into two categories. **Statement blocks** (behavior
bodies, function bodies, if/else/while/loop bodies) all contain a sequence of
statements and can be parsed uniformly. **Structured data blocks** (the
`instruction` intrinsic and `localize { ... }` blocks) each have their own
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
recursively up the call stack. Doc comments support localized text via a
`(locale)` prefix on each `#!` line (e.g., `#! (en) English text`). The
first line's presence of a prefix determines the mode: if present, all
lines are parsed as localized entries; otherwise, they are joined as plain
text. Continuation lines without a prefix append to the previous locale's
text. The `resolveLocalizedDocComment` method on `scanner` handles this,
using the shared `matchLocale` helper for BCP 47 matching. The
`parseLocalePrefix` package-level function extracts the locale code from
a `(locale) text` pattern.

Behavior IDs can be bareword identifiers or quoted strings. The `@name` attribute sets the display
name (at most once per behavior); if omitted, the behavior ID is used as the default name. The
`@param` attribute declares behavior parameters (see "Behavior parameters" below). Both `@name`
and `@param` accept the `localize` intrinsic for localized strings
(`@name localize { en_US "English" ja "日本語" }`), which selects the best match for the
compiler's locale setting using `golang.org/x/text/language`. The `localize` intrinsic is a
compile-time string construct usable anywhere a string argument is expected (e.g., function
call arguments).

The `Compile` and `CompileString` functions accept an `fs.FS` containing the standard library, a
`behaviorID string` that selects which behavior to compile, and a `locale string` (BCP 47 tag) for
resolving `localize` blocks. When `behaviorID` is empty and the source contains a
single behavior, it is auto-selected. When the source contains multiple behaviors,
`behaviorID` must name one of them. When `locale` is empty, `localize` blocks use
their first entry. The compiler parses stdlib function definitions first, then compiles
user source. Stdlib functions that contain an `instruction` intrinsic are inlined at call
sites — the compiler substitutes arguments into the instruction template fields.
Numeric keys in instruction templates are converted from 0-based (reference
format) to 1-based (native wire format) during expansion.

**Function parameters** support keyword arguments with the `keyword varname`
syntax in parameter lists (e.g., `fn notify(txt, value v, timeout t)`).
All positional parameters must precede keyword parameters. At call sites,
keyword args follow positional args after a comma: `notify "Hello!", timeout: "10"`.
Keyword args are optional — omitting one omits the corresponding field from
the compiled instruction. The `paramDef` type tracks each parameter's name
and keyword (empty for positional). Helper methods on `fnDef` support
keyword lookup and positional counting.

**Return values**: Functions can produce return values. The `return`
statement in a function body declares which local name is the function's
return value:
`fn locate_self() { let me = get_self; let coord = get_location me; return coord }`.
The return identifier is stored in `fnDef.ret` (a `string`; empty = no
return). In `expandCall`, `fn.ret` is added to `paramMap` with `retVal`
as its value, so body calls that reference the returned name write
directly into the caller's return target with no copies. The `return`
statement is a compile-time binding — it does not emit a runtime
instruction.

The `@1` syntax inside an `instruction` block marks an output slot as
the first return value:
`fn get_self() { instruction "get_self" { 0: @1 } }`. The `@1` is stored
in the instruction frame as a `returnSlot(1)` value. During `expandCall`,
`returnSlot` values are replaced with `retVal` (or `false` if discarded).
Only `@1` is supported (single return); `@2`+ will be used for multiple
returns in the future. The `returnSlot` type is defined in compiler.go.

The `fnDef.hasReturn()` method checks both mechanisms: `ret != ""` OR
the frame contains a `returnSlot`. All call-site error checks ("has no
return value") use `hasReturn()`.

At call sites, functions with returns can be called via assignment syntax
(`let x = get_self`, `var x = get_self`, `x = get_self`). When called as
a bare statement (no assignment), `expandCall` receives `nil` for `retVal`
and substitutes `false` (null/empty slot). The `retVal any` parameter on
`expandCall` carries the return target through the call chain.

In function bodies, `let` introduces a local name that captures a return
value: `let me = get_self`. This appends a `fnBodyCall` with `retArg` set
to `&fnBodyArg{isIdent: true, val: varName}`. During expansion,
`resolveBodyArg` resolves the `retArg` through `paramMap` and passes it as
`retVal` to the recursive `expandCall`. No `var` in fn bodies — mutability
is a behavior-level concept.

The `parseFnCallArgs` helper (codegen.go) extracts positional + keyword arg
parsing into a reusable method shared by bare function calls, `let`/`var`
declarations, and assignment-from-function-call. Similarly, `parseFnBodyCall`
(parse.go) extracts fn body call argument parsing shared by regular calls
and `let` in fn bodies.

**Symbol table**: During behavior compilation, a `symbolTable` tracks
`@param` declarations (with `$name` keys mapping to 1-based indices,
direction, and display names), `var` declarations (mutable), and `let`
declarations (immutable). Variables can be initialized with a number literal
(`let x = 5`) or a function call with a return value (`let me = get_self`).
Assignment (`x = ...`) also supports both number literals and function calls.
Unit registers (`$signal`, `$visual`, `$store`, `$goto`) are a package-level
`unitRegisters` map. The symbol table is threaded through all compilation
functions via a `syms *symbolTable` parameter.

**Rich argument types**: At behavior level, function arguments accept six
value types: string literals (`"hello"` → string), number literals
(`42` → `map[string]any{"num": 42}`), `null` (`false`), `$`-prefixed
references (unit register negative ints or parameter 1-based indices),
bare identifiers (variable name strings), and `localize { ... }` blocks
(resolved to a string at compile time). `$name` resolution order:
unit register → parameter. The same resolution applies to assignment
targets (`=`, `+=`, `++`), with an immutability check for `let` variables.

In function bodies (`fnBodyArg`), numbers, `null`, and `$register` are
pre-resolved at parse time into the `literal` field. Identifier arguments
that refer to function parameters are resolved at expansion time via
`resolveBodyArg` and the `paramMap`. The `expandCall` function uses
`[]any`/`map[string]any` for args and kwArgs, allowing non-string values
to flow through to instruction template substitution.

**Behavior parameters**: Declared with the `@param` attribute before any
instructions: `@param <direction> <name> <display>` where direction is
`in`, `out`, or `inout`. The display name can be a string literal or a
`localize { ... }` block (same as `@name`). Each parameter gets a 1-based index in
declaration order. References use the `$name` syntax (same prefix as unit
registers). Resolution order: unit registers first, then parameters.
Duplicate parameter names and conflicts with built-in unit register names
are compiler errors. The compiler emits `"parameters"` (array of default
values, currently all `false`) and `"pnames"` (array of display name
strings) in the behavior JSON. Maximum 10 parameters.

**Positional arg separators**: At behavior level, commas between positional
arguments are optional. This preserves backward compatibility with
string-only args (which are unambiguous without commas) while supporting
the natural `set_reg x, $store` style with mixed types.

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

The `.json` test files were generated from the reference JavaScript codec and
should not be modified programmatically. When our implementation's output format
differs from the reference (e.g., 1-based vs 0-based integer keys), the test
code bridges the gap via the `refToNative` conversion routine in `main_test.go`
rather than modifying the test data.

### Locale directive

Test `.doit` files can specify a compilation locale via a `# locale: <tag>`
comment on the second line (after `# AI-generated test`). The `TestCompile`
harness reads this directive and passes the locale to the compiler. If
absent, the locale defaults to `""` (first entry wins).

### AI-generated tests

AI-generated `.doit` test files are marked with a `# AI-generated test`
comment on their first line. When creating a new test case, add this comment.

All files belonging to an AI-generated test case (`.doit`, `.json`, and any
other associated files) may be edited freely to fix or improve them.
All files belonging to a test case without this marker are human-authored
and should not be modified programmatically.

### Error case coverage

New language features must include error case tests in `TestCompileErrors`
(or `TestDecodeErrors` for codec changes), not just happy-path `.doit`/`.json`
pairs. Cover at minimum: invalid syntax the user is likely to write by
mistake, and each explicit error path added by the implementation. For
example, keyword arguments added tests for unknown keywords, duplicate
keywords, positional-after-keyword in definitions, and extra positional
args at call sites.
