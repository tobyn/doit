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
Value, and Coordinate. There is no dedicated number type — numbers are
always encoded alongside one of the other data types. A zero numeric
value does not display.

Coordinates are composite: (X, Y, number). Math operations on
coordinates apply component-wise (X to X, Y to Y).

## Data Storage

Three kinds of registers are available:

- **Parameters** — User-defined registers that appear at the top of the
  behavior UI. Each has a corresponding external register on the Behavior
  Controller component. Data flows bidirectionally: external writes are
  read as inputs, parameter writes are visible externally.
- **Variables** — Automatically allocated (A, B, C, ...) when an
  instruction produces output. Internal to the behavior.
- **Unit Registers** — Four fixed registers with default data (e.g.,
  self-reference). Can be overwritten.

In the compiled JSON, all three register kinds are referenced by name
strings in instruction parameter slots.

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
