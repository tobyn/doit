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
Functions with empty bodies (`fn foo() {}`) are stubs — they are parsed but skipped until their
`instruction` body is implemented.

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

`instructions.lua` is a commercially licensed file, so it cannot be committed
to the repository. It is the developer's responsibility to provide a copy of
this file, if necessary. If this file is needed and missing, notify the
developer and tell them it can be found inside
`Desynced/Content/mods/main.zip` in the `data` directory. If the game is
installed via Steam, the game's data will most likely be found in
`<Steam install directory>/steamapps/common`.
