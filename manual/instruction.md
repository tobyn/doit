# The `instruction` Intrinsic

[Back to index](index.md) | See also: [Functions](functions.md)

doit has an `instruction` intrinsic that can be used to emit a single, arbitrary behavior
instruction, even if the language doesn't support it. This can be used to generate instructions
in the base game that aren't supported by the stdlib (yet), or to generate
instructions added by mods.

The `instruction` intrinsic works as a general expression — it can appear as a bare
statement, on the right-hand side of `let`/`var` declarations and assignments, in
multi-return destructuring, and inside function bodies.

## Bare Statement

At its simplest, `instruction` emits a single instruction with no return value:

`.doit` source:

```doit
instruction "notify" {
    txt: "Hello, World!"
}
```

compiled behavior JSON:

```json
{
    "op": "notify",
    "txt": "Hello, World!"
}
```

When an instruction has no fields, the block can be omitted entirely:

```doit
instruction "nop"
```

compiles to:

```json
{
    "op": "nop"
}
```

## Return Values with `@N`

Use `@1`, `@2`, etc. inside the instruction block to mark output slots.
These map to return values that can be captured with `let`, `var`, or
assignment:

```doit
let me = instruction "get_self" {
    0: @1
}
notify "Got self", value: me
```

The `@N` markers must form a contiguous sequence starting from `@1`.

## Multi-Return Destructuring

Instructions with multiple output slots can be destructured into multiple
bindings:

```doit
let x, y = instruction "separate_coordinate" {
    0: coord
    1: @1
    2: @2
}
```

`@1` maps to the first binding (`x`), `@2` to the second (`y`). The `_`
discard syntax works here too: `let _, y = instruction ...` skips the first
output.

## Assignment

An existing variable can be reassigned from an instruction:

```doit
var me = get_self
me = instruction "get_self" {
    0: @1
}
```

## In Function Bodies

The `instruction` intrinsic works inside function bodies, enabling user-defined
functions that wrap raw instructions:

### Bare instruction

```doit
fn my_notify(txt) {
    instruction "notify" {
        txt: txt
    }
}
```

### `return instruction`

The `return instruction` form marks output slots as the function's return
values. This is the form used by the standard library:

```doit
fn my_get_self() {
    return instruction "get_self" {
        0: @1
    }
}

let me = my_get_self
```

### `let` in function bodies

```doit
fn locate_self() {
    let me = instruction "get_self" {
        0: @1
    }
    let coord = get_location me
    return coord
}
```

## Direction Enforcement

Inside function bodies, the compiler enforces parameter direction constraints
in `instruction` blocks using the `@N` convention:

- **`@N` slots** are outputs — the instruction writes to them
- **All other slots** are inputs — the instruction reads from them

An `out` parameter cannot appear in a non-`@N` slot, because that would
read a write-only value:

```doit
fn bad(out x) {
    instruction "notify" { txt: x }    # error: reads out parameter
}

fn good(out target) {
    let target = get_self              # ok: writes via return binding
}
```

`inout` parameters can appear in either position since they allow both
reading and writing.

## Field Reference Format

Field keys in the instruction block use the reference codec's 0-based format
for numbered parameter slots (`0`, `1`, `2`, ...). The compiler converts
these to 1-based native format automatically. Named fields like `txt` are
passed through as-is.
