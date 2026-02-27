# doit Standard Library

See `manual/functions.md` for user-facing standard library documentation.

doit has a standard library that the compiler makes available to all programs. It consists of all
the `*.doit` files in the `toolchain/stdlib/` directory.

## Architecture

- **`toolchain/stdlib/instructions.doit`** — The built-in game instructions
- **`toolchain/stdlib/instructions.lua`** — The Lua file from the game that
  defines all of Desynced's built-in instructions

## `instructions.doit`

`instructions.doit` wraps Desynced's built-in game instructions as doit functions using the
`instruction` intrinsic. Each function corresponds to an instruction defined in `instructions.lua`.
Functions fall into three categories:

- **Implemented** — contain an `instruction` or `return instruction` body. Functions
  with output slots use `return instruction` and mark the primary output with `@1`.
  Optional inputs and secondary outputs use keyword parameters. All non-control-flow
  instructions are implemented (~102 functions).
- **Control-flow stubs** — empty bodies with a `# control flow:` comment. These have
  `[exec]` branch slots, are loop iterators, terminal instructions, or jump/label
  pairs. They require compiler-level control flow support to implement (~70 functions).
- **Not-implementable stubs** — empty bodies with a `# not implementable:` comment.
  These have dynamic parameters (`call`), require UI selection (`produce`), or use
  non-standard field types (`set_logistics_options`).

Note: `lock` and `unlock` are **not** in the stdlib. They are language
keywords handled directly by the compiler with compile-time mode tracking.
The stdlib still contains `lock_slots` and `unlock_slots` (unrelated
inventory slot functions).

Note: `wait` is **not** in the stdlib. It was removed from the stdlib and
is now a language keyword with optional condition block syntax. See
`manual/language.md` for usage.

Each function body contains a `# frame:` comment showing the inferred JSON structure of the
compiled instruction. These were derived from `instructions.lua` by mapping:

- **`args`** array positions → numbered JSON slots (`"0"`, `"1"`, ...),
  tagged `[in]`/`[out]`/`[exec]`
- **`exec_arg`** → `"next"` field (fall-through execution path)
- **`node_ui`** patterns → special fields: `"txt"` (free text), `"c"` (combo/mode), `"sub"`
  (subroutine), `"bp"`/`"frame"` (blueprint selection)

Comments marked `(low confidence)` have JSON fields inferred from Lua variable names in `node_ui`
code rather than from structured `args` data. These should be verified against actual game output
before relying on them for code generation. The low-confidence instructions are:

- **`call`** — numbered params are dynamic, determined by the selected subroutine
- **`build`**, **`build_registered`**, **`produce`**, **`produce_registered`** — `"bp"`/`"frame"`
  field names inferred from `build_produce_ui` Lua code
- **`set_logistics_options`** — `"c"`/`"c2"` are flag tables (not simple integers like other
  `"c"` fields)

The `instruction` builtin's field keys and values should match the reference
JSON decoding (0-based parameter slot keys, as produced by the reference JS
codec). The `# frame:` comments reflect this same reference format. The
compiler is responsible for any conversion between the reference format and
the wire format — the details of this conversion are a future concern.

`instructions.lua` is a commercially licensed file, so it cannot be committed
to the repository. Assume the file is available and use it freely for
analysis, reference, and code generation. If a task requires the file and it
is missing, notify the developer with the following:

- The file can be found inside `Desynced/Content/mods/main.zip` in the
  `data` directory
- If the game is installed via Steam, the game's data will most likely be
  found in `<Steam install directory>/steamapps/common`

Then offer alternative approaches to proceed without the file if the
developer cannot provide it.
