# Desynced 1.0 Changes

Analysis of Desynced 1.0 game data compared to the previous version.
Source data extracted to `scratch/game_data_1_0/`.

## `make_asm` Pattern (Major — ~20 Instructions Affected)

Many instructions now extract their combo/mode value at assembly time via
a `make_asm` function, adding a `c` parameter to the runtime `func`
signature. Previously these read `GetSourceNode(state).c` at runtime.

The doit stdlib already handles the `"c"` field correctly — it's a JSON
slot on the instruction, and the game resolves it via `make_asm` at
load time. **No doit changes needed** unless the `make_asm` function
transforms the value (most just do `inst.c or <default>`).

Instructions affected: `domove`, `dodrop`, `dopickup`, `bitwise_op`,
`count_item`, `for_signal_match`, `get_unit_info`, `get_unit_power_info`,
`get_item_info`, `count_slots`, `request_item`, `request_wait`,
`moveaway_range`, `scout`, `domovexy`, `notify`, `set_signpost`,
`lock_slots`, and all build/produce variants.

## `var_args` Pattern (New)

`call`, `build`, `produce`, `build_registered`, `produce_registered`,
and new `load_behavior` accept variable arguments based on the
subroutine/blueprint's parameters. The `var_args` function dynamically
returns parameter info. This is a game UI concern — doit already handles
`call` parameters via the planned `call` keyword, and `build`/`produce`
are commented-out stubs.

## New Instructions (10)

| Instruction | Category | Purpose | doit action |
|---|---|---|---|
| `sequence` | Flow | Execute up to 5 exec branches in order | Wrap in stdlib |
| `for_producers_items` | Flow | Loop items a producer can make | Wrap in stdlib + iterator |
| `get_unlocked_components` | Flow | Loop produceable unlocked components | Wrap in stdlib + iterator |
| `has_like_component` | Flow | Check unit for component by base type | Wrap in stdlib |
| `get_offset` | Move | Get coordinate offset from unit | Wrap in stdlib |
| `move_offset` | Move | Move to offset from location/unit | Wrap in stdlib |
| `activate` | Global | Generic component activation | Wrap in stdlib |
| `load_behavior` | Unit | Remotely load behavior onto adjacent unit | Wrap in stdlib (uses `var_args`) |
| `event_radio` | Flow | Event on radio band signal change | New paradigm — see below |
| `event_parameter` | Flow | Event on parameter value change | New paradigm — see below |

## Removed Instructions

- **`unpackage_all`** — removed entirely
- **`package_all`** — removed entirely

These should be removed from the stdlib if present.

## Deprecated Instructions

- **`get_ingredients`** — use `for_recipe_ingredients` instead
- **`domove_range`** — deprecated
- **`move_east`**, **`move_west`**, **`move_north`**, **`move_south`** — deprecated

## Renamed / Rebranded

- `is_unlocked` → "Is Researched" (unlock → research terminology)
- `lock_slots` / `unlock_slots` / `is_fixed` — "Fix" → "Lock" rebrand
- `for_component` → "Loop Equipped Components"

These are display name changes only. The instruction IDs are unchanged,
so doit code is unaffected.

## Path Blocking — New Exec Branches

`dodrop`, `dopickup`, and `domove` now have a "Path Blocked" exec
branch. This is a new continuation path doit should expose. The exec
slot is the last arg in each instruction's args list.

## `bitwise_op` Expanded

Now has 14 operations (was 6). New operations: Compare Equal (7),
Compare Larger (8), Compare Larger or Equal (9), Add (10), Subtract (11),
Multiply (12), Divide (13), Modulo (14). The doit `BitwiseMode` enum
needs updating. Note: the arithmetic operations overlap with doit's
built-in arithmetic operators, which compile to dedicated instructions.

## Event System (New Paradigm)

`event_radio` and `event_parameter` are a new instruction category.
They have `event_setup` and `event_trigger` hooks that create persistent
listeners interrupting normal execution flow when a signal or parameter
changes. This is fundamentally different from normal branching — the
listener persists across ticks and fires asynchronously. Supporting
this in doit may require new language constructs or at minimum careful
stdlib design.

## Faction Registers / Radio Storage (New)

Register indices ≤ -100 address faction-wide shared registers. In
behavior JSON, these are `{fr: "name"}` table values. When a behavior
uses them, the runtime auto-creates the named faction register. This
enables global state shared across all behaviors in a faction.

The register addressing scheme is now:
- `-4` to `-1`: Unit registers (Signal, Visual, Store, Goto)
- `1` to `N`: Parameters
- `N+1` to `N+M`: Local variables (backed by `mem` array)
- `-100` and below: Faction registers (`-99 - index`)

doit has no syntax for faction registers yet. Supporting them would
require new language constructs (e.g., `$faction.my_counter` or a
`radio` keyword).

## `dependencies` Format Update

The behavior JSON format now prefers `dependencies` (flat array at root)
over the old `subs` format for subroutine packaging. The old format is
still supported as a fallback. The `dependencies` format is already
documented in `game.md` and `behavior_json.md`. The doit codec may need
to handle both formats for round-tripping.

## Behavior Properties

Three behavior-level fields confirmed in `library.lua`:

- **`pinits`** — Parameter initial/default values. Already documented in
  `behavior_json.md`. Not yet a language feature or codec concern.
- **`keepvars`** (boolean) — Don't zero-fill variables on restart.
- **`keeparrays`** (string `"store"`) — Memory arrays persist across
  restarts.

These are not new to 1.0 but are confirmed present. None are currently
supported by the doit compiler or codec.

## `wait` Behavior Change

`wait` with zero or negative ticks now does nothing (returns immediately)
instead of clamping to 1 tick. This may affect doit programs that rely
on `wait 0` as a one-tick pause — that no longer works.

## Entity Registry Summary

The 1.0 data files contain complete registries for reference:
- **114 informational values** (`v_*` IDs) — signals, colors, filters, shapes, letters, numbers
- **200+ components** (`c_*` IDs) — fabrication, mining, combat, utility, behavior controllers
- **100+ items** — resources, materials, hi-tech, research items
- **50+ frames** (`f_*` IDs) — buildings, bots, foundations

The doit compiler does not validate entity IDs, so these are for
documentation/autocompletion only.
