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
- **`"parameters"`** (optional) — `[]bool`. Array declaring parameter slots. The array
  length determines how many parameters exist. `false` = input-only (the behavior
  only reads from this parameter), `true` = output (the behavior writes to this
  parameter). The game UI uses this to display I/O direction indicators. The game UI
  can only display 10 parameters.
- **`"pnames"`** (optional) — `[]any`. Array of parameter display names. Each entry is a
  `string` (custom name) or `false` (default name: `"Parameter N"` where N is the
  1-indexed position). Can be shorter than `"parameters"` — trailing entries are
  implicitly `false`.
- **`"pinits"`** (optional) — `[]any`. Array of parameter initial/default values. Each
  entry is `false` (no default) or a literal value (e.g., `{"id": "foundationplate",
  "num": 22}`). Can be shorter than `"parameters"` — trailing entries are implicitly
  `false`.
- **`"0"`, `"1"`, `"2"`, ...** — `map[string]any`. Numbered frames representing the
  behavior's instruction sequence. Keys are sequential 0-based integer strings
  (matching the reference JS codec convention).

## Frame keys

Each frame is a `map[string]any` with:

- **`"op"`** — `string`. The instruction opcode
  (e.g., `"notify"`, `"set_number"`, `"check_number"`).
- **`"next"`** (optional) — Controls execution flow after this frame:
  - Absent: fall through to the next sequential frame.
  - `false` (`bool`): do not proceed to the next frame. If there is no
    other path to continue, the behavior restarts from the beginning.
  - `int`: jump to the frame with that number.
- **`"txt"`** (optional) — `string`. Text parameter (e.g., the message for `"notify"`).
- **`"c"`** (optional) — Combo/mode selector. Integer for most instructions, table for
  `set_logistics_options`.
- **`"sub"`** (optional) — `int`. Subroutine behavior ID (used by `"call"`).
- **`"bp"`** (optional) — Blueprint library ID (used by `"build"`, `"produce"` and registered
  variants).
- **`"frame"`** (optional) — Frame type ID (used by `"build"`, `"produce"` when not using a
  library blueprint).
- **`"0"`, `"1"`, ...** — Numbered parameter slots (0-based). Values:
  - `string` — variable name (arbitrary)
  - `int` — positive: parameter reference or frame reference (1-based,
    matching Lua indexing); negative: unit register (`-4` Signal, `-3`
    Visual, `-2` Store, `-1` Goto)
  - `map[string]any` — literal value (e.g., `{"num": 5}`,
    `{"id": "foundationplate", "num": 22}`,
    `{"fr": "name"}` for faction registers)
  - `bool` — flag

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
