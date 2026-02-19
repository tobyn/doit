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
statement terminates at the end of the line, with the following exceptions:

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

## Variables

Declare a variable with `var` and an initial numeric value:

```doit
var x = 1
```

Assign a new value with `=`:

```doit
x = 2
```

Compound assignment and increment are also supported:

```doit
x += 1
x++
```

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
