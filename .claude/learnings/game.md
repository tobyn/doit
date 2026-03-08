# Desynced Game Model

Background on how Desynced works, relevant to language and compiler design.

## Behavior Controllers

A behavior controller is a component that can be installed on mobile robots
or stationary buildings. It runs a behavior — a program expressed as a
directed graph of instruction nodes. The doit language compiles to the
serialized form of this graph (see `behavior_json.md`).

## Execution Model

The VM has two execution modes:

- **Lock** — Runs one instruction per tick.
- **Unlock** — Runs as many instructions as possible per tick; use
  `wait_ticks` to throttle.

The game runs at 5 ticks per second. When execution has nowhere to
proceed (no successor instruction), the behavior controller restarts
the behavior from the beginning. The `exit` instruction explicitly
terminates the behavior without restarting.

Each execution cycle starts fresh: all variables are reset and the
controller begins in locked mode. The `unlock` instruction switches to
unlocked mode for the current execution; `lock` switches back. Both
are noops if the controller is already in the requested mode. This
state does not persist — the next execution cycle starts locked again.
In unlocked mode, executing too many instructions in a single tick
causes the controller to panic and stop executing (no auto restart).

## Data Types

There are six data types: Item, Unit, Component, Technology, Informational
Value, and Coordinate. Every register value is a composite of a typed
value and a number — there is no dedicated number type. A zero numeric
value does not display.

Coordinates are composite: (X, Y, number). Math operations on
coordinates apply component-wise (X to X, Y to Y).

Several instructions exist for splitting register values into their
constituent parts (value and number) and combining parts back together.

There is no string type in the VM. Strings exist only as compile-time
constants baked into instruction fields (e.g., the text argument to
`notify`). There is no way to store a string in a register or
dynamically construct strings at runtime. This means any string
processing (such as localization) must be resolved at compile time.

More generally, language-level types do not necessarily map 1:1 to VM
registers. A type might map directly to a single register (like a
number), require multiple registers with compiler-managed metadata
(like a range represented as start + end registers with the compiler
tracking inclusive vs half-open), or have no runtime representation
at all (like strings, which are compile-time constants). The compiler
bridges the gap between the language's type system and the VM's flat
register model.

In the compiled JSON, typed values are represented as objects:

- **Item, Component, Technology, Informational Value**:
  `{"id": "<game_id>", "num": <n>}` — the type is implicit in the
  ID string's namespace (e.g., `"metalbar"` for an item, `"c_behavior"`
  for a component, `"t_signals2"` for a tech, `"v_pentagon"` for a value)
- **Coordinate**: `{"coord": {"x": <x>, "y": <y>}, "num": <n>}`
- **Number only**: `{"num": <n>}`
- **Empty/no value**: `null` or `false`

### Empty vs Numeric Zero

The VM distinguishes two "zero" states that look identical in the game
UI but behave differently under `compare_register`:

| State | JSON | Created by | `compare_register` vs `false` |
|-------|------|-----------|-------------------------------|
| **Empty** | `false` / `null` | unset register, `set_reg false`, `= null` | equal (falsy) |
| **Numeric zero** | `{"num": 0}` | `set_number x, 0`, arithmetic yielding 0 | **different** (truthy) |

Key behaviors verified by in-game probes:

- **`compare_register`** tests full register identity. It sees
  `{"num": 0}` as a value-bearing register, distinct from empty. This
  means truthy checks (`if x` → `compare_register x, false`) treat
  numeric zero as truthy.
- **`check_number`** extracts only the numeric component. Both empty
  and `{"num": 0}` read as numeric 0 — comparisons like `<=`, `>=`
  collapse the distinction.
- **Assignment propagates the distinction.** Copying `{"num": 0}` to
  another variable preserves its truthy state. Copying `false` over a
  `{"num": 0}` variable clears it to empty.
- **Arithmetic always produces `{"num": N}`**, even when the result is
  zero (e.g., `sub(5, 5)` → `{"num": 0}`, not `false`). The only way
  to produce an empty register is `set_reg` with `false`.
- **Unset parameters are empty** (falsy).
- **Typed values with num=0** (e.g., `Item("metalbar") & 0`) are
  equal to the same typed value without `& 0` under `compare_register`.
- **`{"num": 0}` survives parameter boundaries.** Writing `{"num": 0}`
  to an output/inout parameter preserves the truthy state — it does not
  collapse to empty. Reading it back via `compare_register` still
  distinguishes it from empty.
- **The game UI produces `{"num": 0}` when the player types 0** into a
  parameter field. Clearing the field produces empty (`false`). Both
  states are mechanically distinct in the VM, but the game UI displays
  them identically (visual ambiguity only — not an expressiveness gap).
- **`set_number(null, 0)` and `combine_register(0, null)` both produce
  truthy `{"num": 0}`** — instruction outputs are consistent.

This means the language can express a three-way distinction:
- `x == null` → register is empty
- `x == 0` → register holds numeric zero (not empty)
- `if x` → register has any value (including zero)

There is no expressiveness gap: every VM register state is reachable
from both doit code and the game UI. The distinction is fully usable
across parameter boundaries in both directions.

The `if x` semantics are a language design question — see `future.md`
burndown for the open decision on truthy checks.

The `value_type` instruction ("Data type switch" in the game UI) is a
6-way branch on data type. It takes one input and has 6 execution
branch slots (one per type) plus a fall-through for empty/no-match.
The branch order is: Item, Unit, Component, Tech, Value, Coord. In
the compiled JSON, the input is slot `"0"` and the branches are exec
slots `"1"` through `"6"`, with `"next"` as the no-match path.

## Data Storage

Three kinds of registers are available:

- **Parameters** — User-defined registers that appear at the top of the
  behavior UI. Each has a corresponding external register on the Behavior
  Controller component. Data flows bidirectionally: external writes are
  read as inputs, parameter writes are visible externally.
- **Variables** — Internal registers, allocated when an instruction
  produces output. The game suggests default names (A, B, C, ...)
  but they can be renamed to any string.
- **Unit Registers** — Four fixed registers with special purposes.
  They can be read and written normally, but should only be used for
  their intended purpose to avoid breaking player expectations:
  - **Signal** — readable by other units with a signal reader equipped,
    enabling inter-unit communication.
  - **Visual** — its value is overlaid on the unit in the game world
    (e.g., showing what a unit is constructing or mining).
  - **Store** — if set and the unit has inventory items, it will
    automatically deliver them to the target (e.g., a storage building).
  - **Goto** — the unit's default destination when it has no other
    orders.

- **Faction Registers** — Shared across all behaviors in a faction.
  Register indices ≤ -100 address faction-wide shared registers
  (`-99 - index`). In the behavior JSON, these appear as
  `{"fr": "name"}` map values. The runtime auto-creates named faction
  registers when a behavior uses them.

In the compiled JSON, variables and parameters are referenced by name
strings in instruction parameter slots. Unit registers are referenced
as negative integers: `-4` (Signal), `-3` (Visual), `-2` (Store),
`-1` (Goto). Faction registers are negative integers ≤ -100.

Variable names are arbitrary strings. The game suggests default
names (A, B, C, ...) but users can rename them freely.

Parameters are declared via a top-level `"parameters"` array in the
behavior JSON. Each entry is `false` (input) or `true` (output /
input-output). In standalone behaviors all parameters are `true`;
the distinction matters for subroutines (see Subroutines section).
The game UI can only display 10 parameters, so the compiler should
warn if a behavior declares more than 10.

## Instructions

Instructions are the nodes in the behavior graph. Each has:

- An opcode (the operation to perform)
- Input/output data pins (parameters/variables)
- Execution flow pins (next instruction, conditional branches)

Conditional instructions have multiple output paths. In the compiled
JSON, these map to numbered exec slots containing jump targets.

**Exec slot semantics — absent vs false**: Exec slots (both numbered
like `"0"` and `"next"`) have two distinct "empty" states:

- **Absent key** — fall-through to the next frame in sequence. Equivalent
  to the slot pointing to `currentFrame + 1`. The compiler's
  `stripFallThrough` optimization relies on this: it removes exec/next
  slots that explicitly point to the immediately following frame,
  producing smaller output with identical behavior.
- **`false`** — no connection. The VM treats the branch as having no
  successor, which triggers re-dispatch: control returns to the enclosing
  iterator (e.g., `for_number`, `sequence`), or the behavior restarts
  from the beginning at the top level. This applies uniformly to all
  exec slots — `"next": false` and `"0": false` have the same semantics.

Verified in-game: a `compare_register` with `"0": false` terminates
execution on the "different" branch (re-dispatch), identical to how
`"next": false` behaves on the "same" branch.

The `check_number` instruction ("Compare Number" in the game UI) is a
3-way branch that compares the numeric part of two registers. It takes
two inputs: a value (slot `"2"`) and a comparison target (slot `"3"`).
The three output paths are: slot `"0"` (if larger), slot `"1"` (if
smaller), and `"next"` (if equal, fall-through).

## Subroutines (Call Instruction)

The `call` instruction invokes another behavior as a subroutine. The
called behavior runs synchronously — execution resumes at the next
instruction after the call returns.

### Dependencies

Called behaviors are stored in a flat `dependencies` array on the
**root** behavior. All subroutines at any call depth share this same
array. Each dependency is a complete behavior definition (instructions,
parameters, pnames, name) but never has its own `dependencies` array.

The `call` instruction references a dependency via the `"sub"` field:
- **Positive integer** — 1-based index into the `dependencies` array
  (e.g., `"sub": 1` → `dependencies[0]`)
- **`-1`** — Self-call (the root behavior calls itself recursively)

### Parameter Mapping

The `call` instruction's numbered slots correspond 1:1 to the callee's
parameters. Each slot provides an **l-value** (storage location) to the
callee — the callee can both read from and write to it:

- **Register** (variable, parameter, unit register) — reads and writes
  go to that register in the caller's scope
- **Literal** (`{"num": 8}`) — callee can read the value; writes are
  silently discarded (no storage to modify)
- **Omitted** — callee reads the parameter as empty/null; writes go
  nowhere

This is effectively **pass-by-reference** for registers and
**pass-by-value** for literals.

The callee's `parameters` array indicates direction:
- `false` — pure input
- `true` — output or input/output

In standalone (non-subroutine) behaviors, all parameters are `true`
since they're bidirectional with external registers. The distinction
only matters in the subroutine context.

### Call Semantics

- **No exec slots** — `call` is a simple instruction with no branching.
- **Separate variables** — The callee has its own variable namespace,
  completely independent of the caller. It cannot see the caller's
  variables or parameters (only what's passed via call slots).
- **Shared unit registers** — The four unit registers (`$signal`,
  `$visual`, `$store`, `$goto`) are global to the unit and accessible
  across call depths.
- **Lock/unlock is global** — The callee inherits the caller's lock
  state. If the callee changes it (e.g., calls `lock`), the change
  persists after the call returns. Lock/unlock is controller-wide
  state, not scoped to call frames.
- **Unwritten outputs** — If the callee terminates without writing to
  an output parameter, the caller's destination register is untouched.
- **Call depth limit** — Recursion is allowed but the game enforces a
  maximum call depth. Exceeding it kills the behavior (same as
  exceeding the instruction budget in unlocked mode).

### Compiled JSON

The `pnames` array (parameter names) should be omitted when empty,
and included only when there are actual names.

Example call instruction:
```json
{"op": "call", "sub": 1, "1": "A", "2": {"num": 8}, "3": 2}
```
This calls dependency 1, passing variable A as param 1 (input),
literal 8 as param 2 (input), and routing param 3 (output) back
to the caller's parameter 2.

## Behavior Properties

Three optional top-level fields on the behavior JSON:

- **`pinits`** — Parameter initial/default values (array, parallel to
  `parameters`).
- **`keepvars`** (boolean) — Don't zero-fill variables on restart.
- **`keeparrays`** (string `"store"`) — Memory arrays persist across
  restarts.

## Event System

`event_radio` and `event_parameter` are a distinct instruction category.
They have `event_setup` and `event_trigger` hooks that create persistent
listeners interrupting normal execution flow when a signal or parameter
changes. The listener persists across ticks and fires asynchronously —
fundamentally different from normal branching.

- Event instructions are placed in the instruction list but disconnected
  from the main flow. They act as interrupt entry points.
- When the event fires, execution jumps to the instruction after the
  event node. The handler chain should end with `"next": false` to
  avoid falling through into unrelated instructions.
- `event_parameter` uses `"pnum": N` (1-based parameter index) to
  select which parameter to watch.
- `event_radio` uses `"band": {register_value}` to select the radio
  band. The band must be a valid entity ID (e.g., `v_octagon`).
- The `nx`/`ny` fields on event instructions are visual editor node
  positions (cosmetic, not semantic).

## Removed VM Opcodes

The `break` VM opcode was removed in Desynced 1.0. The `last`
instruction is the only block-control opcode. The doit compiler never
emits `break` — it uses `last` for iterator breaks and noop bridges /
`jump` for other break forms.

## Import/Export

The game's UI supports importing and exporting behaviors as Base62-encoded
strings (the format implemented by the `codec` package). This is the
primary way users share behaviors.
