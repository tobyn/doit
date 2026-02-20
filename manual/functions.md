# Functions

[Back to index](index.md)

## Calling Functions

Call a function by writing its name followed by its arguments. String arguments
are quoted; multiple arguments are separated by commas:

```doit
notify "Hello, World!"
```

### Argument types

Function arguments accept several value types:

| Syntax | Example | Description |
|--------|---------|-------------|
| String literal | `"Hello"` | Quoted string |
| Number literal | `42` | Numeric value |
| Identifier | `target` | Variable, parameter, or function parameter |
| Unit register | `$store` | Unit register reference |
| `null` | `null` | Empty value |

```doit
var x = 5
set_number x, 10, x
domove $goto
set_number null, 5, x
add x, 10, x
```

Unit register and parameter references use the `$` prefix (e.g., `$store`,
`$my_param`). Bare identifiers resolve as variable names.

## The Standard Library

doit includes a standard library that wraps Desynced's built-in game
instructions as functions. The standard library is automatically available to
all programs — no import is needed.

For example, `notify` is a standard library function that maps to Desynced's
`notify` instruction.

## Keyword Arguments

Some functions accept keyword arguments — named, optional parameters that
follow the positional arguments. Keyword arguments are separated from
positional arguments (and from each other) by commas, and use `keyword: value`
syntax:

```doit
notify "Hello!", timeout: "10"
notify "Hello!", value: x, timeout: y
notify "Hello!"
```

Keyword arguments can be provided in any order and are optional — omitting one
omits the corresponding field from the compiled instruction.

### Defining keyword parameters

In a function definition, keyword parameters are written as `keyword varname`
after all positional parameters:

```doit
fn timed_notify(txt, timeout t) {
    notify txt, timeout: t
}
```

The keyword and variable name can be the same: `timeout timeout`.

All positional parameters must come before keyword parameters.

## Defining Functions

Define a function with `fn`:

```doit
fn my_notify(txt) {
    notify txt
}
```

Parameters are passed by name. In the body, parameters can be used as arguments
to other function calls.

### Private Functions

A function defined with `private fn` is only visible within the file that
defines it:

```doit
private fn my_notify(txt) {
    notify txt
}
```

## The `instruction` Intrinsic

Functions can use the [`instruction` intrinsic](instruction.md) to emit
arbitrary game instructions directly.
