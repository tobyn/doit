# Type System

Data types in the doit language. This is a reference for guiding
compiler and language design work.

## Types

### Register Types

These types can be stored in VM registers at runtime. Every register
holds a composite of a typed value and a number (integer). The typed
value is one of the six game types below, or empty (for a pure number).

- **Boolean** — True or false. In the VM, booleans are represented as
  the numbers `1` and `0`. Boolean is a logically distinct type from
  Number — future type checking will not allow using `1`/`0` in place
  of `true`/`false` or vice versa. Literal syntax: `true` → `{"num": 1}`,
  `false` → `false` (empty register, same as `null`). Both are reserved
  keywords.

- **Number** — An integer. No floating point. In the VM, this is a
  register with no typed value and a nonzero numeric component.
  Literal syntax: `42`

- **Item** — A game item (e.g., metal bar, circuit board).
  Constructor syntax: `Item("metalbar")` → `{"id": "metalbar"}`.

- **Unit** — A reference to a game entity (mobile robot or building).
  No constructor syntax. Values produced by instructions at runtime.

- **Component** — An equippable component (e.g., behavior controller).
  Constructor syntax: `Component("behavior")` → `{"id": "c_behavior"}`.
  The `c_` prefix is added automatically.

- **Technology** — A research technology.
  Constructor syntax: `Technology("signals2")` → `{"id": "t_signals2"}`.
  The `t_` prefix is added automatically.

- **Informational Value** — A special game value used for signaling and
  display. Constructor syntax: `Value("pentagon")` →
  `{"id": "v_pentagon"}`. The `v_` prefix is added automatically.

- **Coordinate** — A 2D position in the game world. Math on coordinates
  is component-wise (X to X, Y to Y). Constructor syntax:
  `Coordinate(3, 7)` → `{"coord": {"x": 3, "y": 7}}`. With literal
  arguments, resolved at compile time. With variable arguments, emits
  `combine_coordinate` at runtime.

All register types can be stored in variables, parameters, unit
registers, and faction registers (`%name`), and passed through register
slots in instructions. The VM is dynamically typed within registers —
any register can hold any register type.

Constructor names (`Item`, `Component`, `Technology`, `Value`,
`Coordinate`, `Range`) are reserved keywords and cannot be used as
variable or function names. `Unit` is also a reserved keyword — it has
no constructor (units are produced by instructions at runtime) but is
valid as a type operand in `is` expressions.

### Runtime type checking (`is`)

The `is` operator checks whether a register value matches one of the
six game data types: `x is Item`, `x is Unit`, `x is Component`,
`x is Technology`, `x is Value`, `x is Coordinate`. It produces 1
(true) or empty (false). `is Number` is not supported — `value_type`
cannot distinguish numbers from null.

Whether the compiler should track specific register subtypes (e.g.,
distinguish Item from Unit) or treat all register values as a single
type is a future decision. Most instructions accept any register type,
and the VM handles mismatches at runtime.

### String

Text values. Literal syntax: `"hello"`. Also produced by `localize`
blocks.

Strings have no runtime representation — they cannot be stored in VM
registers. The compiler resolves string values at compile time and bakes
them into instruction text fields. However, strings are full values in
the language: they can be assigned to variables, passed as function
arguments, and used anywhere the language allows.

### Range

A numeric sequence used for `for` loop iteration. Constructor syntax:
`Range(stop)`, `Range(start, stop)`, or `Range(start, stop, step)`.

Range has no dedicated VM type — it is stored as a coordinate+number
composite: `{"coord": {"x": start, "y": stop}, "num": step}`. With
all-literal arguments, the Range resolves at compile time. With variable
arguments, the compiler emits a `combine_register` instruction. `Range`
is not distinguishable from `Coordinate` at runtime (`is Range` is not
supported). The `&` operator is not supported on Range values (it would
overwrite the step stored in the num field).

### Null

The empty value. Literal syntax: `null`. Null is universal — it is a
valid value for any type, matching the VM behavior where any register
can be empty.

## Variables and Types

Variables are dynamically typed. A `var` variable can hold different
types over its lifetime:

```
var x = "hello"    // x holds a string
notify x           // compiler resolves x to "hello" for the txt field
x = 42             // x now holds a number
add x, 1           // x is a register value
```

The compiler determines what type each variable holds at each use point
and handles it accordingly — resolving strings at compile time, emitting
register references for runtime values, or reporting an error if the
type is ambiguous (e.g., a variable might be a string or number
depending on which control flow path was taken).

`let` variables are assigned once, so their type is always known.

## Register Composite Model

Every VM register holds a (typed_value, number) composite:

- A Number is a register with no typed value: `{"num": 42}`
- An Item with count is: `{"id": "metalbar", "num": 5}`
- A Coordinate with number is: `{"coord": {"x": 1, "y": 2}, "num": 3}`

The numeric component is accessible via instructions like `get_number`.
This composite nature doesn't change the type list — it's a property of
how the VM stores register values.

### The `&` Operator

The `&` binary operator attaches a numeric component to a register value:

```
Item("metalbar") & 5        // {"id": "metalbar", "num": 5}
Coordinate(1, 2) & 3        // {"coord": {"x": 1, "y": 2}, "num": 3}
Value("pentagon") & count    // runtime: emits set_number
```

When both operands are compile-time literals, the result is a compile-time
literal with the `"num"` field merged in. When either operand is a
runtime value (variable), the compiler emits a `set_number` instruction.

Runtime `&` is supported both at behavior level and in function bodies.
In function bodies, runtime `&` emits a synthetic `set_number` body call.

## Compile-Time vs Runtime

From the programmer's perspective, all types are uniform — you work
with values, not with "compile-time" or "runtime" categories. The
distinction is a compiler implementation concern:

- **Strings** are always resolved at compile time (no VM representation)
- **Register types** are always runtime values (stored in VM registers)
- **Null** is compatible with both contexts
- **Number literals** in function arguments can be baked directly into
  instruction slots as literal values without going through a register
- **Type constructors** with all-literal arguments produce compile-time
  values baked into instruction slots; with variable arguments, they
  emit runtime instructions

The compiler must track enough type information to know how to handle
each value at each use point, but the programmer doesn't need to think
about this boundary.
