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

## Field Values

Field values can be strings, identifiers (variable/parameter names), `@N`
return slot markers, or number literals:

```doit
instruction "notify" {
    txt: "Hello"        # string literal
    0: target           # identifier (variable or parameter)
}

instruction "domove" {
    0: target
    c: 2                # number literal (mode selector)
}
```

Number literals in fields are useful for metadata fields like `c` (mode
selector). For register slot values, identifiers are the normal choice.

When a function parameter resolves to an enum value (e.g.,
`BitwiseMode::Xor` → `3`), the compiler automatically unwraps it to a
plain integer for metadata fields.

## Exec Bindings

Inside function bodies, instruction blocks can declare exec (branch)
slots using the `exec` keyword. This is used to implement branching
functions — see [Functions](functions.md#branching-functions-continuations).

```doit
fn my_check(value, target) exec(larger, smaller, equal) {
    instruction "check_number" {
        exec 0: larger
        exec 1: smaller
        2: value
        3: target
        next: equal
    }
}
```

- `exec N:` marks a numbered slot as an exec branch
- `next:` implies exec (the fall-through path)
- `detach` prefix marks a detached continuation: `detach next: body`
- `@N` args pass output data to continuations: `exec 0: found(@1, @2)`

## Local Continuation Blocks with `'`

Instructions can declare local continuation blocks using the `'` (tick) sigil
on exec binding names. This lets you branch within a single instruction without
wrapping it in a function:

```doit
instruction "check_number" {
    exec 0: 'larger
    exec 1: 'smaller
    2: $signal
    3: 5
    next: 'equal
} {
    larger { notify "large" }
    smaller { notify "small" }
    equal { notify "equal" }
}
```

The `'name` syntax declares a *local* continuation — one handled by blocks
attached directly to the instruction. Without `'`, the name refers to the
enclosing function's exec continuation (existing behavior, unchanged).

### Data arguments

Local blocks can receive data from the instruction via `@N` args, just like
function-level continuations:

```doit
instruction "check_number" {
    exec 0: 'larger(@1)
    exec 1: 'smaller
    2: value
    3: threshold
    next: 'equal
} {
    larger { v -> notify "large", value: v }
    smaller { notify "small" }
    equal { notify "equal" }
}
```

### Expression form

When all local blocks are bridging (not detached), the instruction can be
used as an expression. Each block's last expression is its value:

```doit
let result = instruction "check_number" {
    exec 0: 'larger(@1)
    exec 1: 'smaller
    2: $signal
    3: 5
    next: 'equal
} {
    larger { v -> v }
    smaller { 0 }
    equal { 5 }
}
```

Detached local blocks in expression form are a compile error.

### Collapsed form

When there is only one local block, the collapsed form works:

```doit
instruction "check_number" {
    exec 0: 'larger
    2: $signal
    3: 5
} {
    notify "was larger"
}
```

### Mixing local and non-local bindings

In a function body, local (`'name`) and non-local (`name`) bindings can
coexist. Non-local bindings refer to the function's exec continuation and
are resolved by the function-level expansion:

```doit
fn wrapper(v, t) exec(pass, fail) {
    instruction "check_number" {
        exec 0: 'big       // local block
        exec 1: fail        // forward to fn's exec
        2: v
        3: t
        next: pass          // forward to fn's exec
    } {
        big { notify "big" }
    }
}
```

### `'return` is reserved

`'return` cannot be used as a local block name — `return` is reserved for
the function-level continuation system.

## `iterator_instruction`

The `iterator_instruction` keyword creates an inline iterator from a raw
VM instruction, usable in `for...in` loops without declaring an `iter`:

```doit
for comp, idx in iterator_instruction "for_component" {
    0: @1
    1: @2
    done: 2
} {
    notify "found", value: comp
}
```

- `@N` maps instruction output slots to iteration variables
- `done:` specifies the exec slot for exhaustion (required)
- Exec bindings are not allowed (use `instruction` with `'` blocks for
  branching iterators)
- `break`, `continue`, and labeled `break` work normally

See [Language](language.md#iterator_instruction) for more details.

## Field Reference Format

Field keys in the instruction block use 0-based format for numbered
parameter slots (`0`, `1`, `2`, ...), matching the reference codec
convention. Named fields like `txt` and `c` are passed through as-is.
