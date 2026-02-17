# doit Standard Library

doit has a standard library that the compiler makes available to all programs. It consists of all
the `*.doit` files in the `toolchain/stdlib/` directory.

## Architecture

- **`toolchain/stdlib/instructions.doit`** — The built-in game instructions
- **`toolchain/stdlib/instructions.lua`** — The Lua file from the game that defines all of Desynced's built-in instructions

## `instructions.doit`

`instructions.doit` wraps Desynced's built-in game instructions as doit functions using the
`instruction` intrinsic. Each function corresponds to an instruction defined in `instructions.lua`.
Functions with empty bodies (`fn foo() {}`) are stubs — they are parsed but skipped until their
`instruction` body is implemented.

`instructions.lua` is a commercially licensed file, so it cannot be committed to the repository. It is the developer's
responsibility to provide a copy of this file, if necessary. If this file is needed and missing, notify the developer and
tell them it can be found inside `Desynced/Content/mods/main.zip` in the `data` directory. If the game is installed via
Steam, the game's data will most likely be found in `<Steam install directory>/steamapps/common`.
