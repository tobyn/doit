# Language

[Back to index](index.md)

## Program Structure

A doit program consists of one or more `behavior` declarations:

```doit
behavior my_behavior {
    @name "My Behavior"
    // ... body ...
}
```

The identifier after `behavior` is the behavior id. It can be a bareword
identifier or a quoted string (for IDs containing spaces):

```doit
behavior "My Behavior" {
    // ...
}
```

The `@name` attribute sets the display name shown in-game. It is optional and
can appear at most once per behavior. If omitted, the display name defaults to
the behavior id:

```doit
behavior patrol {
    // display name will be "patrol"
    notify "Patrolling"
}
```

For localized names, use the block form with locale codes:

```doit
@name {
    en_US "US English name"
    ja    "日本語の名前"
}
```

The compiler selects the best match for the active locale (set via `-l` or
auto-detected). If no match is found, the first entry is used.

When a file contains multiple behaviors, the `-b` flag selects which to compile:

```doit
behavior patrol {
    @name "Patrol"
    // ...
}

behavior harvest {
    @name "Harvest"
    // ...
}
```

```sh
doit compile -b harvest source.doit
```

When a file contains only one behavior, `-b` is optional.

## Statements

Behavior bodies and control flow blocks contain sequences of statements. A
statement terminates at the end of the line. Semicolons can be used to
separate multiple statements on a single line:

```doit
lock; notify "Hello"; unlock
```

Exceptions to end-of-line termination:

1. If a statement ends in a brace-delimited block, it extends to the closing
   `}`. For `if` statements, `else if` and `else` clauses continue the
   statement regardless of whether they appear on the same line as the `}`.
2. If a statement is a parenthesized function call, it extends to the closing
   `)`, even across multiple lines.
3. If a statement is an unparenthesized function call and the line ends in a
   comma, the statement continues onto the next line.

## Function Calls

Functions can be called with or without parentheses:

```doit
notify "Hello"
notify("Hello")
```

Both forms are equivalent. The preferred style for statement-level calls is
without parentheses. Parenthesized calls are useful for argument grouping in
more complex expressions.

For unparenthesized calls, a trailing comma continues the argument list onto
the next line:

```doit
my_function "arg1",
    "arg2",
    "arg3"
```

## Comments

Line comments start with `#`:

```doit
# This is a comment
```

### Doc comments (`#!`)

Lines starting with `#!` are doc comments. When placed before a statement,
they set the `"cmt"` field on the compiled instruction, which appears as a
comment on the instruction node in-game.

```doit
#! Move to the target
move_to target
```

Multiple `#!` lines are joined with spaces:

```doit
#! This is a longer comment
#! that spans two lines
notify "Hello"
```

Doc comments propagate through function calls: if a function call has a
`#!` comment, all instructions expanded from that call inherit it, unless
they have their own `#!` comment.

```doit
fn greet() {
    #! Says hello
    notify "Hello"
    notify "World"
}

#! Greeting sequence
greet
```

In this example, the first `notify` gets `"Says hello"` (its own), and the
second gets `"Greeting sequence"` (inherited from the call site).

## Parameters

Parameters are behavior-level inputs declared with `param`. They appear as
editable registers on the behavior controller component in-game. Each
parameter gets a 1-based index in declaration order.

```doit
behavior miner_hauler {
    @name "Miner Hauler"
    param gang_id "Gang ID"
    param foreman "Foreman"
    param store "Store"

    domove foreman
    set_reg store, $store
}
```

- `param name` — declares a parameter; the display name defaults to the
  identifier name
- `param name "Display Name"` — declares a parameter with a custom display
  name

Parameters must be declared before any instructions. The game UI can display
at most 10 parameters; the compiler warns if more are declared.

Parameter names can be used as function arguments and assignment targets,
where they compile to the parameter's 1-based index.

## Variables

Declare a mutable variable with `var` and an initial numeric value:

```doit
var x = 1
```

Declare an immutable variable with `let`:

```doit
let x = 5
```

Both `var` and `let` emit a `set_number` instruction and map to a behavior
variable register. The difference is that `let` prevents reassignment — the
compiler errors on `=`, `+=`, or `++` targeting a `let` variable.

Assign a new value with `=`:

```doit
x = 2
```

Compound assignment and increment are also supported:

```doit
x += 1
x++
```

Assignment targets can also be unit registers (`$store = 5`) or parameters.

## Unit Registers

Four special registers are available via the `$` prefix:

- `$signal` — readable by other units with a signal reader
- `$visual` — overlaid on the unit in the game world
- `$store` — automatic item delivery target
- `$goto` — the unit's default destination

Unit registers can be used anywhere a value is expected:

```doit
domove $goto
set_reg x, $store
$signal = 0
```

Unknown `$` names are a compile error.

## Null

The `null` keyword represents an empty value. Use it in argument positions
for instruction slots that should be explicitly empty:

```doit
set_number null, 5, x
```

`null` compiles to `false` in the behavior JSON.

## Control Flow

### `if` / `else if` / `else`

```doit
if a < 9 {
    notify "a < 9!"
}

if a >= 3 {
    notify "a >= 3"
} else {
    notify "a < 3"
}

if a == 1 {
    notify "one"
} else if a > 1 {
    notify "more than one"
} else {
    notify "less than one"
}
```

### Comparison Operators

- `==` — equal
- `<` — less than
- `<=` — less than or equal
- `>` — greater than
- `>=` — greater than or equal

### `while`

`while` loops while a condition holds:

```doit
var i = 1
while i <= 5 {
    notify "While iteration"
    i++
}
```

The body executes as long as `i <= 5`, then execution continues past the loop.

### `loop` and `break`

`loop` creates an infinite loop. Use `break` to exit:

```doit
var i = 1
loop {
    notify "Loop iteration"

    if i >= 5 {
        break
    }

    i += 1
}
```
