---
paths:
  - "toolchain/codec/**/*"
---

# Desynced Blueprint/Behavior Codec

Desynced supports the importing and exporting of blueprints and behaviors. These are encoded as
Base62 strings. The developers of Desynced have provided an open source JavaScript library that
can bidirectionally convert between these strings and a structured representation of the blueprint
or behavior.

The `codec` package provides a Go implementation of the same functionality.

## Architecture

- **`codec/codec.go`** — Codec implementation
- **`codec/codec_test.go`** — Tests driven by file-based test cases
- **`codec/tests/`** — Test case pairs: `.encoded` (Base62 string) + `.decoded` (type indicator line + JSON)
- **`codec/tests/reference/`** — Vendored [Stage Games JavaScript codec](https://github.com/StageGames/DesyncedJavaScriptUtils) used as the reference implementation; `index.html` can be opened in a browser to create new test cases

## Test Case Format

Each test case is a pair of files sharing the same base name in `codec/tests/`:
- **`.encoded`** — A Base62-encoded string in the format 'DS' + type + encoded value
- **`.decoded`** — A file consisting of type + '\n' + JSON encoded value

Each test case produces two tests:
- The string in the `.encoded` file is decoded, and the type and value are compared with the data in the `.decoded` file
- The decoded information is re-encoded and decoded again, and the comparison repeated. This verifies encoding does not change the data.

The JSON in the `.decoded` file may differ from a JSON rendering of the `.encoded` value in trivial ways (e.g., whitespace,
object key ordering). Do not rely on the JSON strings to be the same. Parse the JSON and validate deep equality between
the decoded value and the reference JSON in the `.decoded` file.

There may be multiple valid ways to encode the same data. Do not rely on two encodings of the same data to be equal.
