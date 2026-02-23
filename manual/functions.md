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
| `localize { ... }` | `localize { en "Hi" ja "こんにちは" }` | Locale-aware string |
| Type constructor | `Item("metalbar")` | Typed game value (see below) |
| `&` operator | `Item("metalbar") & 5` | Value with numeric component |

```doit
var x = 5
set_number x, 10, x
domove $goto
set_number null, 5, x
add x, 10, x
notify "Items", value: Item("metalbar") & 5
```

Unit register and parameter references use the `$` prefix (e.g., `$store`,
`$my_param`). Bare identifiers resolve as variable names.

### Type constructors

Type constructors create typed game values. Five constructors are available:

| Constructor | Example | Compiled value |
|-------------|---------|----------------|
| `Item("id")` | `Item("metalbar")` | `{"id": "metalbar"}` |
| `Component("id")` | `Component("behavior")` | `{"id": "c_behavior"}` |
| `Technology("id")` | `Technology("signals2")` | `{"id": "t_signals2"}` |
| `Value("id")` | `Value("pentagon")` | `{"id": "v_pentagon"}` |
| `Coordinate(x, y)` | `Coordinate(3, 7)` | `{"coord": {"x": 3, "y": 7}}` |

`Component`, `Technology`, and `Value` automatically add their namespace prefix
(`c_`, `t_`, `v_`) — use the short id without the prefix.

`Coordinate` accepts number literals or variables. With literals, the coordinate
is resolved at compile time. With variables, the compiler emits a
`combine_coordinate` instruction at runtime.

Constructor names are reserved — they cannot be used as variable or function
names.

### The `&` operator

The `&` operator attaches a numeric component to a typed value, creating the
composite (typed_value, number) that every VM register holds:

```doit
Item("metalbar") & 5          # item with count 5
Coordinate(1, 2) & 3          # coordinate with number 3
Value("pentagon") & count      # runtime: emits set_number
```

When both sides are compile-time literals, the result is a compile-time literal.
When either side is a variable, the compiler emits a `set_number` instruction.
This works in both behavior bodies and function bodies.

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

### Direction annotations

When a function parameter is declared `out` or `inout`, the call site must
annotate the argument with the matching direction keyword. This makes the
direction visible at the call site without looking up the function definition:

```doit
fn writer(out target) {
    let target = get_self
}

var z = 5
writer out z        # 'out' annotation required
```

For keyword arguments, the direction annotation precedes the keyword name:

```doit
fn my_fn(x, out kw result) {
    let result = get_location x
}

var z = 5
my_fn 1, out kw: z  # 'out' before the keyword name
```

`in` is the default — no annotation is needed for `in` parameters, but
explicit `in` is accepted for clarity:

```doit
fn reader(x) { notify x }

reader in 5          # explicit 'in' accepted
reader 5             # same — 'in' is implicit
```

A missing or mismatched annotation is a compile error.

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

## Return Values

Some functions produce an output value. Call them using assignment syntax:

```doit
let me = get_self
var me = get_self
me = get_self
```

The return value is assigned to the variable on the left-hand side. If a
function with a return value is called as a bare statement (no assignment),
the return slot is discarded:

```doit
get_self    # return value discarded
```

In function bodies, `let` introduces a local name that captures a return value
for use by subsequent calls. The `return` statement declares which local name
is the function's return value:

```doit
fn locate_self() {
    let me = get_self
    let coord = get_location me
    return coord
}
```

## Defining Functions

Define a function with `fn`:

```doit
fn my_notify(txt) {
    notify txt
}
```

Parameters are passed by name. In the body, parameters can be used as arguments
to other function calls.

### Parameter directions

Function parameters can be annotated with a direction: `in` (default), `out`,
or `inout`. The direction precedes the parameter name:

```doit
fn writer(out target) {
    let target = get_self
}

fn updater(inout value) {
    instruction "add" { 0: value  1: value  2: value }
}

fn reader(x) {           # defaults to 'in'
    notify x
}
```

Direction annotations serve the same purpose as `@param` directions on
behavior parameters — they constrain how the callee uses the argument and
how the caller must annotate it. `in`, `out`, and `inout` are reserved
keywords and cannot be used as variable or parameter names.

In `instruction` blocks, non-`@N` slots are treated as inputs and `@N` slots
as outputs. An `out` parameter cannot appear in a non-`@N` slot (that would
read it). `inout` parameters can appear in either position.

### The `return` statement

The `return` statement declares which local name is the function's return
value:

```doit
fn locate_self() {
    let me = get_self
    let coord = get_location me
    return coord
}
```

The `return` statement is a compile-time binding, not a runtime instruction.
It tells the compiler that the named local maps to the caller's return target.
This means instructions that write to the returned name write directly into
the caller's destination with no copies. At call sites, the caller provides
the return target via assignment syntax (`let loc = locate_self`).

### Private Functions

A function defined with `private fn` is only visible within the file that
defines it:

```doit
private fn my_notify(txt) {
    notify txt
}
```

## The `instruction` Intrinsic

The [`instruction` intrinsic](instruction.md) emits arbitrary game
instructions directly. It works as a general expression — in behavior bodies,
in `let`/`var` declarations and assignments, and inside function bodies. The
standard library uses `return instruction` to wrap game instructions as
functions.
