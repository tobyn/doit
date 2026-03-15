# Toolchain

[Back to index](index.md)

The doit toolchain is a single executable called `doit`. It provides
subcommands for compiling doit source code and working with Desynced's Base62
export format.

## `doit compile`

Compile doit source into a Base62-encoded behavior string.

```
doit compile [--stdlib stdlib_path] [-e|--error] [-j|--json] [-b behavior_id] [-l locale] [-o output_path] [input_path]
```

Reads doit source from `input_path`, or stdin if no path is given. If the source
contains a single behavior it is compiled automatically. If the source contains
multiple behaviors, the `-b` option selects which behavior to compile by its
behavior ID.

If `-e` or `--error` is provided, any compiler warnings are treated as errors
and compilation will fail. This is useful for CI pipelines or strict mode.

Compiler errors and warnings include the source line and a caret pointing to the
error location:

```
error: 3:9: unknown function or variable "foo"
  3 |     x = foo(42)
    |         ^
```

The compiled behavior is encoded as a Base62 string and written to `output_path`
if `-o` is provided, or stdout otherwise. If `-j` or `--json` is provided, the
JSON representation of the behavior is emitted instead of the Base62 string.

The `-l` or `--locale` option sets the locale for resolving localized `@name`
blocks (e.g., `-l ja` or `-l en_US`). If not specified, the locale is
auto-detected from the operating system. When no match is found, the first entry
in the `@name` block is used.

The `--stdlib` option overrides the embedded standard library with one at the
given path. This is useful for standard library development or for using a
custom standard library.

## `doit fmt`

Format doit source files.

```
doit fmt [-w] [-l] [file ...]
```

Reads doit source from the given files (or stdin if none are given) and prints
the canonically formatted version to stdout.

The formatter normalizes:

- **Indentation** — 4 spaces per nesting level, based on brace depth
- **Operator spacing** — consistent spaces around `+`, `-`, `=`, `==`, `&&`,
  etc.
- **Trailing semicolons** — removed (mid-line semicolons like `$failed = 1;
  exit` are preserved)
- **Consecutive blank lines** — collapsed to at most one
- **Trailing whitespace** — stripped

The formatter preserves the user's line-break decisions: it does not join or
split lines.

Use `-w` to format files in place (requires at least one file argument). Use
`-l` to list files whose formatting differs from the canonical style without
modifying them.

The same formatting logic is available in editors via the language server's
`textDocument/formatting` capability. In JetBrains, use **Code → Reformat
Code**. In VS Code, use **Format Document**.

## `doit decode`

Decode a Base62 exported string to JSON.

```
doit decode [-b|-c] [-o output_path] [input_path]
```

Desynced allows the player to export blueprints and behaviors as Base62-encoded
strings. This subcommand reads an exported string, parses it, and writes a JSON
object containing the parsed data.

The output structure is:

```json
{ "type": "<type>", "value": <object> }
```

The `"type"` is `"B"` for a blueprint or `"C"` for a behavior. Use `-b` to
require a blueprint or `-c` to require a behavior; when either option is
provided, only the value object is written.

Reads from stdin and writes to stdout by default. Use `input_path` to read from
a file and `-o` to write to a file.

## `doit encode`

Encode a JSON object to a Base62 exported string.

```
doit encode [-b|-c] [-o output_path] [input_path]
```

Reads a JSON object, encodes it as a Base62 string in Desynced's export format,
and writes the result.

Without `-b` or `-c`, the input must include `"type"` and `"value"` fields.
With `-b`, the input is treated as a blueprint directly. With `-c`, the input is
treated as a behavior directly.

Reads from stdin and writes to stdout by default. Use `input_path` to read from
a file and `-o` to write to a file.

## `doit language-server`

Start the doit language server.

```
doit language-server
```

Starts a Language Server Protocol (LSP) server that communicates over
stdin/stdout using JSON-RPC 2.0. Editors and IDEs use this to provide
syntax highlighting, diagnostics, and other language features for doit
source files.

The language server is typically launched by an editor extension rather than
invoked directly. Editor extensions handle this automatically:
- **JetBrains** — the doit plugin (in `editors/jetbrains/`) uses LSP4IJ
- **VS Code** — the doit extension (in `editors/vscode/`)

### Supported capabilities

- **Semantic token highlighting** — classifies tokens as keywords, functions,
  variables, parameters, types, labels, registers, etc. for rich syntax
  coloring.
- **Document formatting** — canonical code formatting (same as `doit fmt`).
  Triggered by the editor's "Reformat Code" or "Format Document" command.
- **Diagnostics** — reports syntax errors and warnings as you type. Runs the
  compiler's declaration-level checks on every edit and publishes results as
  inline editor diagnostics (red/yellow squiggles).
- **Document symbols** — lists all top-level declarations (behaviors,
  functions, iterators, constants, enums) for outline views and quick
  navigation (Ctrl+Shift+O in VS Code).
- **Full document sync** — tracks open documents and responds to changes in
  real time.

## `doit help`

Print usage information.

```
doit help [command]
```

With no argument, prints a summary of all available commands. With a command
name, prints detailed usage for that command.

## Reference codec CLI

A Node.js CLI wrapper around the vendored reference JavaScript codec is
available at `toolchain/codec/refcodec.js`. This uses the first-party
codec from Desynced's developers (`toolchain/codec/tests/reference/`)
without modification.

```
node toolchain/codec/refcodec.js decode [file]
node toolchain/codec/refcodec.js encode [file]
```

- **decode** reads a Base62 string and writes the type character on the
  first line followed by the JSON representation (matching the
  `.decoded` test file format).
- **encode** reads a type-prefixed JSON file (or plain JSON, defaulting
  to behavior type `C`) and writes the Base62 string.

Both commands read from the given file, or from stdin if no file is
provided. Requires Node.js.

Use this tool to validate our Go codec against the authoritative
reference implementation. The reference JS codec is the ground truth —
our codec must always match its output.
