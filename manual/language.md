# Language

[Back to index](index.md)

## Program Structure

A doit program consists of a single `behavior` declaration:

```doit
behavior my_behavior {
    name "My Behavior"
    // ... body ...
}
```

The identifier after `behavior` is the behavior's internal name. The `name`
statement sets its display name in-game.

## Comments

Line comments start with `//` or `#`:

```doit
// This is a comment
# This is also a comment
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

Compound assignment is also supported:

```doit
x += 1
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
- `>` — greater than
- `>=` — greater than or equal

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
