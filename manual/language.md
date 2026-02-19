# Language

[Back to index](index.md)

## Program Structure

A doit program consists of one or more `behavior` declarations:

```doit
behavior my_behavior {
    name "My Behavior"
    // ... body ...
}
```

The identifier after `behavior` is the behavior id. The `name` statement sets
its display name in-game.

When a file contains multiple behaviors, the `-b` flag selects which to compile:

```doit
behavior patrol {
    name "Patrol"
    // ...
}

behavior harvest {
    name "Harvest"
    // ...
}
```

```sh
doit compile -b harvest source.doit
```

When a file contains only one behavior, `-b` is optional.

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
