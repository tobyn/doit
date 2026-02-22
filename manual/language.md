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

For localized names, use `localize`:

```doit
@name localize {
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

#### Localized doc comments

Doc comments can carry locale-specific text using a `(locale)` prefix. The
compiler selects the best match for the active locale (set via `-l`), using
the same BCP 47 matching logic as `localize` blocks:

```doit
#! (en) Move to the target
#! (ja) ターゲットに移動
move_to target
```

The first `#!` line in a group determines the mode. If it starts with a
`(locale)` prefix, the entire group is localized. Otherwise, it's a plain
doc comment (preserving existing behavior).

Continuation lines without a `(locale)` prefix append to the previous
locale's text with a space:

```doit
#! (en) line one
#! continued on next line
#! (ja) 日本語テキスト
notify "Hello"
```

With locale `en`, this produces `"line one continued on next line"`.

The locale prefix pattern is `(` followed by identifier characters (letters,
digits, `_`, `-`) and `)`. The same locale codes accepted by `localize`
blocks work here (e.g., `en`, `en_US`, `ja`, `zh-Hans`). If no locale is
set, the first entry is used. If no match is found, the best BCP 47 match
is selected (falling back to the first entry).

## Parameters

Parameters are behavior-level registers declared with the `@param` attribute.
They appear as editable registers on the behavior controller component
in-game. Each parameter gets a 1-based index in declaration order.

```doit
behavior miner_hauler {
    @name "Miner Hauler"
    @param in gang_id "Gang ID"
    @param inout foreman "Foreman"
    @param inout storage "Storage"

    domove $foreman
    set_reg $storage, $store
}
```

The syntax is `@param <direction> <name> <display>`:

- **Direction**: `in` (read-only input), `out` (write-only output), or
  `inout` (read/write)
- **Name**: the identifier used to reference this parameter as `$name`
- **Display**: a string literal or `localize { ... }` (same as `@name`)

```doit
@param in target "Target"
@param inout gang_id localize {
    en "Gang ID"
    ja "ギャングID"
}
```

If the display name is omitted, it defaults to the identifier name.

Parameters must be declared before any instructions. The game UI can display
at most 10 parameters; the compiler errors if more are declared.

Parameter names are referenced with the `$` prefix (e.g., `$target`) and
can be used as function arguments and assignment targets. The parameter name
must not conflict with a built-in unit register or another parameter.

## Variables

Declare a mutable variable with `var` and an initial numeric value:

```doit
var x = 1
```

Declare an immutable variable with `let`:

```doit
let x = 5
```

The right-hand side of `var` and `let` can be a number literal, a function
call that has a return value, a type constructor expression, or an
[`instruction`](instruction.md) expression:

```doit
let me = get_self
var loc = get_location me
let item = Item("metalbar") & 5
let pos = Coordinate(3, 7)
let me = instruction "get_self" { 0: @1 }
```

When initialized with a number, a `set_number` instruction is emitted. When
initialized with a function call, the function's return value is assigned to
the variable. When initialized with a type constructor, a `set_reg` instruction
is emitted for compile-time values, or the appropriate runtime instructions
are emitted (e.g., `combine_coordinate` for `Coordinate` with variables).
When initialized with an `instruction` expression, the instruction is emitted
directly with the variable as the `@1` output target.

The difference between `var` and `let` is that `let` prevents reassignment —
the compiler errors on `=`, `+=`, or `++` targeting a `let` variable.

Both `var` and `let` allow shadowing — you can redeclare a variable with the
same name, and the new declaration replaces the previous one:

```doit
let x = 5
let x = 10
var x = 15
```

Assign a new value with `=`:

```doit
x = 2
x = get_self
x = Item("metalbar") & 5
```

The right-hand side of `=` can be a number literal, a function call with a
return value, a type constructor expression, or an
[`instruction`](instruction.md) expression.

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

## Localization

The `localize` construct provides locale-aware strings at compile time. It
can be used anywhere a string is expected — `@name`, `@param` display names,
and function arguments:

```doit
localize {
    en_US "English text"
    ja    "日本語テキスト"
}
```

Each entry is a locale identifier followed by a string literal. The compiler
selects the best match for the active locale (set via `-l`). If no match is
found, the first entry is used. If no locale is set, the first entry is
always used.

Examples:

```doit
@name localize {
    en_US "My Behavior"
    ja    "マイビヘイビア"
}

notify localize {
    en "Hello!"
    ja "こんにちは！"
}
```
