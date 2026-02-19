---
paths:
  - "toolchain/compiler/**/*"
  - "toolchain/codec/**/*"
---

# Behavior JSON Representation

The compiler produces, and the codec encodes/decodes, a behavior as a `map[string]any` with this
structure:

## Top-level keys

- **`"name"`** — `string`. The behavior's display name.
- **`"0"`, `"1"`, `"2"`, ...** — `map[string]any`. Numbered frames representing the behavior's
  instruction sequence. Keys are sequential integer strings starting from `"0"`.

## Frame keys

Each frame is a `map[string]any` with:

- **`"op"`** — `string`. The instruction opcode
  (e.g., `"notify"`, `"set_number"`, `"check_number"`).
- **`"next"`** (optional) — Controls execution flow after this frame:
  - Absent: fall through to the next sequential frame.
  - `false` (`bool`): terminal, execution stops.
  - `int`: jump to the frame with that number.
- **`"txt"`** (optional) — `string`. Text parameter (e.g., the message for `"notify"`).
- **`"c"`** (optional) — Combo/mode selector. Integer for most instructions, table for
  `set_logistics_options`.
- **`"sub"`** (optional) — `int`. Subroutine behavior ID (used by `"call"`).
- **`"bp"`** (optional) — Blueprint library ID (used by `"build"`, `"produce"` and registered
  variants).
- **`"frame"`** (optional) — Frame type ID (used by `"build"`, `"produce"` when not using a
  library blueprint).
- **`"0"`, `"1"`, ...** — Numbered parameter slots. Values: `string` (register name), `int`
  (data value or frame reference), `map[string]any` (literal, e.g., `{"num": 5}`), `bool` (flag).

## Example

```json
{
  "name": "Example",
  "0": { "op": "set_number", "1": {"num": 1}, "2": "i" },
  "1": { "op": "notify", "txt": "Loop iteration" },
  "2": { "op": "notify", "txt": "Done", "next": false }
}
```

This behavior sets register `"i"` to 1, sends a "Loop iteration" notification, then sends "Done"
and stops.
