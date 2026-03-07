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

## Sequence Instruction (Verified In-Game)

`sequence` uses the `BeginBlock`/`next`/`last` iterator mechanism to
execute up to 5 exec branches in order. `exec_arg = false` — no
`"next"` field; all branching is through explicit exec slots 0–4.
Each branch must end with `"next": false` to re-dispatch back to the
sequence handler.

- Branches execute in order: First, Second, Third, Fourth, then Last.
- Optional branches (Second–Fourth) are skipped if not connected.
- The `last` VM instruction works within sequence branches — it skips
  remaining branches and jumps directly to the Last handler.
- The `break` VM opcode has been **removed in 1.0**. The game shows
  "invalid instruction [break]" with a tooltip saying it has been
  removed. The `last` instruction is the only block-control opcode.
  The doit compiler never emits `break` — it uses `last` for iterator
  breaks and noop bridges / `jump` for other break forms — so this
  removal does not affect doit output.

## New Instructions (10)

| Instruction | Category | Purpose | Status |
|---|---|---|---|
| `sequence` | Flow | Execute up to 5 exec branches in order | **TODO** — new paradigm |
| `for_producers_items` | Flow | Loop items a producer can make | **Done** — stdlib + iterator |
| `get_unlocked_components` | Flow | Loop produceable unlocked components | **Done** — stdlib + iterator |
| `has_like_component` | Flow | Check unit for component by base type | **Done** — stdlib |
| `get_offset` | Move | Get coordinate offset from unit | **Done** — stdlib |
| `move_offset` | Move | Move to offset from location/unit | **Done** — stdlib |
| `activate` | Global | Generic component activation | **Done** — stdlib |
| `load_behavior` | Unit | Remotely load behavior onto adjacent unit | **TODO** — uses `var_args`/`sub` |
| `event_radio` | Flow | Event on radio band signal change | **TODO** — new paradigm |
| `event_parameter` | Flow | Event on parameter value change | **TODO** — new paradigm |

## Removed Instructions — **Done**

- **`unpackage_all`** — removed from stdlib
- **`package_all`** — removed from stdlib

## Deprecated Instructions

- **`get_ingredients`** — use `for_recipe_ingredients` instead
- **`domove_range`** — deprecated (still in stdlib for compatibility)
- **`move_east`**, **`move_west`**, **`move_north`**, **`move_south`** — deprecated (still in stdlib)

## Renamed / Rebranded

Display name changes only. The instruction IDs are unchanged, so doit
code is unaffected. No changes needed.

## Path Blocking — New Exec Branches — **Done**

`dodrop`, `dopickup`, and `domove` now have a "Path Blocked" exec
branch. These are exposed as optional `exec(path_blocked)` continuations
in the stdlib. Callers can omit the branch if they don't need it — the
compiler strips unresolved exec bindings. `domove` also gained an
optional `unit` keyword parameter for specifying a target unit other
than self.

## `bitwise_op` Expanded — **Done**

Now has 14 operations (was 6). The `BitwiseMode` enum has been updated
with: `CompareEqual` (7), `CompareLarger` (8), `CompareLargerOrEqual` (9),
`Add` (10), `Subtract` (11), `Multiply` (12), `Divide` (13),
`Modulo` (14). Note: the arithmetic operations overlap with doit's
built-in arithmetic operators, which compile to dedicated instructions.

## Event System (New Paradigm — Verified In-Game)

`event_radio` and `event_parameter` are a new instruction category.
They have `event_setup` and `event_trigger` hooks that create persistent
listeners interrupting normal execution flow when a signal or parameter
changes. This is fundamentally different from normal branching — the
listener persists across ticks and fires asynchronously. Supporting
this in doit may require new language constructs or at minimum careful
stdlib design.

Verified behavior:
- Event instructions are placed in the instruction list but disconnected
  from the main flow. They act as interrupt entry points.
- When the event fires, execution jumps to the instruction after the
  event node. The handler chain should end with `"next": false` to
  avoid falling through into unrelated instructions.
- `event_parameter` uses `"pnum": N` (1-based parameter index) to
  select which parameter to watch.
- `event_radio` uses `"band": {register_value}` to select the radio
  band. The band must be a valid entity ID (e.g., `v_octagon`).
  Note: `v_circle` is NOT a valid ID.
- The `nx`/`ny` fields on event instructions are visual editor node
  positions (cosmetic, not semantic).

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
