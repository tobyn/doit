---
paths:
  - "toolchain/codec/**/*"
---

# Desynced Blueprint/Behavior Codec

See `manual/toolchain.md` for user-facing decode/encode documentation.

Desynced supports the importing and exporting of blueprints and behaviors. These are encoded as
Base62 strings. The developers of Desynced have provided an open source JavaScript library that
can bidirectionally convert between these strings and a structured representation of the blueprint
or behavior.

The `codec` package provides a Go implementation of the same functionality.

## Architecture

- **`codec/codec.go`** — Codec implementation
- **`codec/json.go`** — JSON unmarshaling into the codec's native Go value types
- **`codec/tests/`** — Test case pairs: `.encoded` (Base62 string) +
  `.decoded` (type indicator line + JSON)
- **`codec/tests/reference/`** — Vendored
  [Stage Games JavaScript codec](https://github.com/StageGames/DesyncedJavaScriptUtils)
  used as the reference implementation; `index.html` can be opened in a
  browser to create new test cases

## Test Case Format

Each test case is a pair of files sharing the same base name in `codec/tests/`:
- **`.encoded`** — A Base62-encoded string in the format 'DS' + type + encoded value
- **`.decoded`** — A file consisting of type + '\n' + JSON encoded value

Tests are in the root `main_test.go`. Each test case produces two subtests:
- The string in the `.encoded` file is decoded, and the type and value are
  compared with the data in the `.decoded` file
- The decoded information is re-encoded and decoded again, and the comparison
  repeated. This verifies encoding does not change the data.

The JSON in the `.decoded` file may differ from a JSON rendering of the
`.encoded` value in trivial ways (e.g., whitespace, object key ordering). Do
not rely on the JSON strings to be the same. Parse the JSON and validate deep
equality between the decoded value and the reference JSON in the `.decoded`
file.

There may be multiple valid ways to encode the same data. Do not rely on two
encodings of the same data to be equal.

The `.decoded` and `.encoded` files are trusted inputs derived from the
reference JavaScript implementation. They should never be changed
programmatically — changes require human verification against the reference
implementation. When our codec's output format differs from the reference
(e.g., 1-based vs 0-based integer keys), the test code bridges the gap via
a conversion routine (`refToNative` in `main_test.go`) rather than modifying
the test data.

`TestDecodeErrors` in `main_test.go` covers invalid-input cases for the
decoder (empty input, malformed prefix, bad checksums, corrupted data, etc.).
When adding new codec functionality, include error case tests for each
explicit error path, not just happy-path roundtrip cases.
