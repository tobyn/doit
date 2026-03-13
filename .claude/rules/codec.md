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
**Our codec must always match the reference JS codec's output.** The JS
codec is a first-party product of Desynced's developers and is the
authoritative specification. When in doubt, decode the same input with
both codecs and compare.

## Architecture

- **`codec/codec.go`** — Codec implementation
- **`codec/json.go`** — JSON unmarshaling into the codec's native Go value types
- **`codec/refcodec.js`** — Node.js CLI wrapper for the reference JS codec
  (`node codec/refcodec.js decode|encode [file]`). Use this to validate
  our Go codec against the reference. See `manual/toolchain.md` for usage.
- **`codec/tests/`** — Test case pairs: `.encoded` (Base62 string) +
  `.decoded` (type indicator line + JSON)
- **`codec/tests/reference/`** — Vendored
  [Stage Games JavaScript codec](https://github.com/StageGames/DesyncedJavaScriptUtils)
  used as the reference implementation; `index.html` can be opened in a
  browser to create new test cases. **Do not modify** files in this
  directory.

## Test Case Format

Each test case is a pair of files sharing the same base name in `codec/tests/`:
- **`.encoded`** — A Base62-encoded string in the format 'DS' + type + encoded value
- **`.decoded`** — A file consisting of type + '\n' + JSON encoded value

Tests are in `codec/codec_test.go` (package `codec_test`). Each test case produces two subtests:
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
implementation. Our codec matches the reference JS codec's 0-based key
convention: array-part keys start at `"0"`, and hash-part numeric keys
are decremented by 1 on decode (incremented by 1 on encode) to convert
between Lua's 1-based indexing and 0-based JSON keys. Integer data
**values** (e.g., frame references) are stored as-is without adjustment.

`TestDecodeErrors` in `codec/codec_test.go` covers invalid-input cases for
the decoder (empty input, malformed prefix, bad checksums, corrupted data, etc.).
When adding new codec functionality, include error case tests for each
explicit error path, not just happy-path roundtrip cases.
