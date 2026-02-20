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

## Data Types

There are six data types: Item, Unit, Component, Technology, Informational
Value, and Coordinate. Every register value is a composite of a typed
value and a number — there is no dedicated number type. A zero numeric
value does not display.

Coordinates are composite: (X, Y, number). Math operations on
coordinates apply component-wise (X to X, Y to Y).

Several instructions exist for splitting register values into their
constituent parts (value and number) and combining parts back together.

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

Conditional instructions (e.g., `check_number`) have multiple output
paths. In the compiled JSON, these map to numbered slots (`"0"` for
if_larger, `"1"` for if_smaller) containing jump targets.

## Import/Export

The game's UI supports importing and exporting behaviors as Base62-encoded
strings (the format implemented by the `codec` package). This is the
primary way users share behaviors.
