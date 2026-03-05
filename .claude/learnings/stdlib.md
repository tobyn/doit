# doit Standard Library

See `manual/functions.md` for user-facing standard library documentation.

doit has a standard library that the compiler makes available to all programs. It consists of all
the `*.doit` files in the `toolchain/stdlib/` directory.

## Architecture

- **`toolchain/stdlib/prelude.doit`** — The prelude, automatically prepended
  to every source file. Contains `import * from "std:instructions"` and
  `import * from "std:iterators"` to bring all stdlib symbols into scope.
  Files opt out with `skip prelude` at the top.
- **`toolchain/stdlib/instructions.doit`** — The built-in game instructions.
  Has `skip prelude` to avoid circular dependency with the prelude.
  Iterators are defined here as `fn` exec-block versions (e.g.,
  `for_component`) for discoverability and exec-block call syntax.
- **`toolchain/stdlib/iterators.doit`** — Iterator versions of the game's
  loop instructions, using `each_*` names (e.g., `each_component`).
  Has `skip prelude`. These are the primary names for `for...in` usage.
- **`toolchain/stdlib/instructions.lua`** — The Lua file from the game that
  defines all of Desynced's built-in instructions

## `instructions.doit`

`instructions.doit` wraps Desynced's built-in game instructions as doit functions using the
`instruction` intrinsic. Each function corresponds to an instruction defined in `instructions.lua`.
Functions fall into two categories:

- **Implemented** — contain an `instruction` or `return instruction` body. This
  includes all non-control-flow instructions (~102 functions) and all branching
  instructions (~69 functions with `exec` signatures and `instruction` bodies).
  Functions with output slots use `return instruction` and mark the primary output
  with `@1`. Optional inputs and secondary outputs use keyword parameters.
- **Commented-out stubs** — `produce` (requires blueprint/frame UI selection)
  and `set_logistics_options` (flag-table parameters) are commented out with
  rationale. May be revisited if the language adds support for their parameter types.

### Branching instruction categories

The ~69 branching instructions fall into five semantic categories.
All use the same call-site syntax (continuation blocks).

1. **Pure conditionals** (`check_number`, `compare_register`,
   `value_type`, `compare_entity`, `compare_item`, `is_a`,
   `unit_type`, `is_unit_a`, `is_empty`, `is_daynight`,
   `get_season`, `check_altitude`, `check_blightness`,
   `check_health`, `check_battery`, `check_grid_effeciency`,
   `is_logistics`, `is_same_grid`, `is_moving`, `is_passable`,
   `is_fixed`, `is_unlocked`, `have_item`, `checkfreespace`,
   `can_produce`, `gettrust`, `match`, `switch`, `check_bit`) —
   Route execution to one of N paths. No data output. All
   continuations are bridging.

2. **Iterators** — Stateful instructions with a looping body
   continuation and a bridging "done" continuation. Produce output
   data each iteration. Declared in two forms:
   - **`each_*` iterators** (in `iterators.doit`): `each_component`,
     `each_nearby_resource`, `each_entity_in_range`,
     `each_inventory_item`, `each_number`, `each_producer`,
     `each_recipe_ingredient`, `each_repair_ingredient`, `each_tech`,
     `each_research_ingredient`, `each_research_unlock`,
     `each_signal`, `each_signal_match`, `each_memory_entry` —
     `iter` declarations for `for ... in` call syntax.
   - **`for_*` / `memory_loop` fns** (in `instructions.doit`):
     `for_component`, `for_count_resources`, `for_entities_in_range`,
     `for_inventory_item`, `for_number`, `for_producers`,
     `for_recipe_ingredients`, `for_repair_ingredients`,
     `for_research`, `for_research_ingredients`,
     `for_research_unlocks`, `for_signal`, `for_signal_match`,
     `memory_loop` — `fn...exec(body, done)` declarations for
     exec-block call syntax.

3. **Failable getters** (`get_inventory_item`,
   `get_inventory_item_index`, `get_resource_item`,
   `get_reg_remotely`, `faction_item_amount`, `scan`, `solve`,
   `is_docked`, `is_equipped`, `is_working`) — Output on success
   (fall-through), bridging continuation on failure.

4. **Action outcomes** (`build`, `build_registered`,
   `produce_registered`, `mine`, `equip_component`,
   `unequip_component`, `equip_component_remotely`,
   `unequip_component_remotely`, `set_reg_remotely`, `make_carrier`,
   `make_miner`, `make_producer`, `make_turret_bots`,
   `serve_construction`, `wait_component`) — No data output.
   Bridging continuations for status (success/failure,
   working/blocked).

5. **Conditional with output** (`select_nearest`) — Bridging
   continuations AND data output. Output available regardless of
   which path is taken. Rare (one known case).

Categories 1, 3, 4, 5 use bare (bridging) blocks at call sites.
Category 2 uses `for`-prefixed (looping) blocks.

Note: `lock` and `unlock` are **not** in the stdlib. They are language
keywords handled directly by the compiler with compile-time mode tracking.
The stdlib still contains `lock_slots` and `unlock_slots` (unrelated
inventory slot functions).

Note: `wait` is **not** in the stdlib. It was removed from the stdlib and
is now a language keyword with optional condition block syntax. See
`manual/language.md` for usage.

Note: `exit` is **not** in the stdlib. It is a language keyword that emits
`{"op": "exit"}` with no successor. The compiler knows it is terminal and
detects unreachable code after it.

Note: `restart` is **not** in the stdlib. It is a language keyword that
emits `{"op": "restart"}` with no successor. Terminal like `exit`.

Note: `jump` is **not** in the stdlib. It is a language keyword:
`jump <expr>` emits `{"op": "jump", "1": <value>}` with no successor.
Terminal — the compiler detects unreachable code after it.

Note: `label` is **not** in the stdlib. It is a language keyword:
`label <expr>` emits `{"op": "label", "1": <value>}`. Not terminal —
execution continues past it. Both `label` and `jump` accept named labels
(`'name` sigil) with compiler-managed value allocation, or runtime
expressions. String literals are disallowed on both keywords (use
`instruction` intrinsic for raw string slots).

Note: `call` is **not** in the stdlib. It requires the dependency index
of the callee, which can't be expressed as a parameter. A `call` keyword
for inter-behavior calls is planned post-1.0 (see `future.md`).

`instructions.doit` also defines **mode enums** (e.g., `MoveMode`,
`BitwiseMode`, `AmountMode`) for instructions with `"c"` combo fields.
These enums use `= 1` on the first member to match the game's 1-based
combo indexing. Affected functions expose mode via keyword parameters
(e.g., `mode mode`, `stat stat`, `type type`) with `c: param` in the
instruction block. When the keyword arg is omitted, the `c` field is
dropped from the compiled output and the game uses its default.

`parseStdlibFile` handles `fn`, `iter`, `enum`, and `skip` declarations.
`parseStdlib` skips `prelude.doit` (it contains `import`, which
`parseStdlibFile` can't handle). Stdlib symbols are cached in
`stdlibFns`/`stdlibIters`/`stdlibEnums` and made available to user
code through the prelude's glob import rather than by pre-populating
working maps. Iterator instructions have two stdlib representations:
`each_*` iters in `iterators.doit` (for `for...in`) and `for_*` fns
in `instructions.doit` (for exec-block calls).

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

- **`build`**, **`build_registered`**, **`produce`**, **`produce_registered`** — `"bp"`/`"frame"`
  field names inferred from `build_produce_ui` Lua code (`produce` is
  commented out in the stdlib)
- **`set_logistics_options`** — `"c"`/`"c2"` are flag tables (not simple integers like other
  `"c"` fields) (commented out in the stdlib)

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
