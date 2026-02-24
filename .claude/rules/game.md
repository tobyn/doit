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

In the compiled JSON, variables and parameters are referenced by name
strings in instruction parameter slots. Unit registers are referenced
as negative integers: `-4` (Signal), `-3` (Visual), `-2` (Store),
`-1` (Goto).

Variable names are arbitrary strings. The game suggests default
names (A, B, C, ...) but users can rename them freely.

Parameters are declared via a top-level `"parameters"` array in the
behavior JSON. Each entry represents a parameter slot (`false` for
no default value). The game UI can only display 10 parameters, so
the compiler should warn if a behavior declares more than 10.

## Instructions

Instructions are the nodes in the behavior graph. Each has:

- An opcode (the operation to perform)
- Input/output data pins (parameters/variables)
- Execution flow pins (next instruction, conditional branches)

Conditional instructions have multiple output paths. In the compiled
JSON, these map to numbered exec slots containing jump targets.

The `check_number` instruction ("Compare Number" in the game UI) is a
3-way branch that compares the numeric part of two registers. It takes
two inputs: a value (slot `"2"`) and a comparison target (slot `"3"`).
The three output paths are: slot `"0"` (if larger), slot `"1"` (if
smaller), and `"next"` (if equal, fall-through).

## Import/Export

The game's UI supports importing and exporting behaviors as Base62-encoded
strings (the format implemented by the `codec` package). This is the
primary way users share behaviors.
