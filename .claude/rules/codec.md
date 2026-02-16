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
- **`.encoded`** — The Base62-encoded string as the game imports/exports it
- **`.decoded`** — First line is a type indicator (`B` for blueprint, `C` for behavior); remaining lines are the JSON representation

Tests verify round-trip correctness: decoding `.encoded` must produce the `.decoded` content, and encoding `.decoded` must produce the `.encoded` content.
