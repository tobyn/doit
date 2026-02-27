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
var x = 1; notify "Hello"; x++
```

Exceptions to end-of-line termination:

1. If a statement ends in a brace-delimited block, it extends to the closing
   `}`. For `if` statements, `else if` and `else` clauses continue the
   statement regardless of whether they appear on the same line as the `}`.
2. If a function call's line ends in a comma, the statement continues onto
   the next line.

## Function Calls

Functions can be called with or without parentheses:

```doit
notify "Hello"
notify("Hello")
```

Both forms are equivalent. The unparenthesized form is preferred for
statement-level calls. In parenthesized form, arguments are separated by
commas:

```doit
add(a, b)
notify("Hello", value: x)
let me = get_self()
```

For unparenthesized calls with multiple arguments, a trailing comma
continues the argument list onto the next line:

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

The direction keywords `in`, `out`, and `inout` are reserved and cannot be
used as variable or parameter names. When calling a function with `out` or
`inout` parameters, the call site must annotate the argument with the matching
direction keyword — see [Direction annotations](functions.md#direction-annotations).

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
call that has a return value, a type constructor expression, an
[arithmetic expression](#arithmetic-expressions), a
[comparison expression](#comparison-expressions), a
[type check expression](#type-check-expressions), or an
[`instruction`](instruction.md) expression:

```doit
let me = get_self
var loc = get_location me
let item = Item("metalbar") & 5
let pos = Coordinate(3, 7)
let sum = a + b
let is_big = count > 10
let in_range = count > 0 && count < 100
let same = a == b
let is_unit = me is Unit
let me = instruction "get_self" { 0: @1 }
```

When initialized with a number, a `set_number` instruction is emitted. When
initialized with a function call, the function's return value is assigned to
the variable. When initialized with a type constructor, a `set_reg` instruction
is emitted for compile-time values, or the appropriate runtime instructions
are emitted (e.g., `combine_coordinate` for `Coordinate` with variables).
When initialized with an arithmetic expression, the corresponding math
instruction (`add`, `sub`, `mul`, or `div`) is emitted.
When initialized with a comparison or type check expression (optionally chained
with `&&` or `||`), a `check_number`, `compare_register`, or `value_type` +
`set_reg` pattern is emitted that assigns 1 (true) or empty (false) to the
variable. When initialized with an
`instruction` expression, the instruction is emitted directly with the
variable as the `@1` output target.

The difference between `var` and `let` is that `let` prevents reassignment —
the compiler errors on `=`, `+=`, `-=`, `*=`, `/=`, `++`, or `--` targeting
a `let` variable.

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
x = a + b
x = a > b
x = a == b
x = me is Unit
```

The right-hand side of `=` can be a number literal, a function call with a
return value, a type constructor expression, an
[arithmetic expression](#arithmetic-expressions), a
[comparison expression](#comparison-expressions), a
[type check expression](#type-check-expressions), or an
[`instruction`](instruction.md) expression.

Compound assignment, increment, and decrement are also supported:

```doit
x += 1
x -= 1
x *= 2
x /= 3
x += y
x++
x--
```

The right-hand side of `+=`, `-=`, `*=`, `/=` can be an arithmetic
expression (number literals, variables, and `+`, `-`, `*`, `/` with PEMDAS
precedence). `++` adds 1, `--` subtracts 1:

```doit
x += y + 1
x -= a * 2
```

Assignment targets can also be unit registers (`$store = 5`) or parameters.

### Multi-binding expression lists

Multiple variables can be declared from a comma-separated list of
expressions:

```doit
let a, b, c = 1, 2, 3
var x, y = Item("metalbar"), Item("circuit")
let sum, diff = a + 1, a - 1
```

Each expression on the right contributes one value, and the total must
match the number of bindings on the left.

Function calls can appear as expression list items. A function call
contributes as many values as its return count:

```doit
let a, b, c = get_self, 1, 2        # get_self returns 1 value
let a, b, c, d, e = 1, my_fn, 5     # my_fn returns 3 values (1+3+1=5)
```

The last item in an expression list supports **prefix matching**: if it is
a function call, the caller can bind fewer values than the function returns.
Extra return values are silently discarded:

```doit
let a, b = 1, separate_coordinate coord   # separate_coordinate returns 2,
                                           # but only first is bound to b
```

At behavior level, binding lists support mixed `let`/`var` modifiers and
`_` discards (same syntax as multi-return function calls):

```doit
var a, let b, _, var c = 1, 2, 3, 4
```

In function bodies, all bindings inherit the leading `let` or `var`
modifier.

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

## Boolean Literals

`true` and `false` represent boolean values:

```doit
let t = true
let f = false
set_number true, 5
```

In the VM, `true` compiles to `{"num": 1}` (a register with numeric value
1) and `false` compiles to `false` (an empty register, same as `null`).
Boolean literals can be used anywhere a value is expected: variable
initialization, function arguments, comparisons, and return values.

`true` and `false` are reserved keywords and cannot be used as variable
names.

## Control Flow

All control flow constructs work in both behavior bodies and function bodies.
In behavior bodies, control flow blocks support the full statement set
including `let`/`var` declarations, nested control flow, and `break` (inside
`loop` and `while`).

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

### If Expressions

`if`/`else if`/`else` can be used as an expression to produce a value.
Each branch block contains optional statements followed by a
value-producing tail expression (the last item). The `else` clause is
optional — when absent, uncovered branches produce `null`.

```doit
let x = if a > 1 { 5 } else { 3 }

let y = if a > 10 {
    5
} else if a == 0 {
    0
} else {
    3
}
```

When `else` is omitted, the expression evaluates to `null` if no branch
matches:

```doit
let x = if a > 10 { 20 }               # x is null when a <= 10
let y = if a > 10 { 20 } else if a < 5 { 5 }  # y is null when 5 <= a <= 10
```

Branches can contain statements before the tail expression:

```doit
let x = if a > 1 {
    notify "big"
    5
} else {
    notify "small"
    3
}
```

If-expression tails can be multi-return function calls, enabling
multi-return conditional bindings:

```doit
let x, y = if a > 0 {
    separate_coordinate coord
} else {
    separate_coordinate fallback
}
```

If-expressions work in both behavior bodies and function bodies.

### Expression Priority

When multiple expression types appear in the same statement, they are
evaluated in this priority order (highest first):

1. **Arithmetic** (`*`, `/`, `%`, `+`, `-`) — PEMDAS rules: multiplication,
   division, and modulo bind tighter than addition and subtraction
2. **Comparisons** (`>`, `<`, `>=`, `<=`, `==`, `!=`, `is`) — compare
   arithmetic results
3. **Function calls** — consume arguments (which can contain arithmetic)
4. **Boolean** (`&&`, `||`) — chain comparisons, function results, or
   truthy values

This means you can write combined expressions naturally:

```doit
let a = x + 1 > y - 2           # arithmetic, then comparison
let b = my_fn a + 1, c || d     # fn call with arithmetic args, then boolean
let c = count * 2 > threshold && active
```

### Arithmetic Expressions

Arithmetic operators produce a value by applying the corresponding game
instruction:

| Operator | Instruction | Description |
|----------|-------------|-------------|
| `+`      | `add`       | Addition    |
| `-`      | `sub`       | Subtraction |
| `*`      | `mul`       | Multiplication |
| `/`      | `div`       | Division    |
| `%`      | `modulo`    | Modulo      |

Operands can be variables, registers, number literals, or parenthesized
sub-expressions:

```doit
let sum = a + b
let diff = a - 3
let product = x * y
let quotient = x / 2
let scaled = 5 + offset
var result = a + b
result = a - b
```

#### PEMDAS precedence

Chained arithmetic follows standard PEMDAS rules — `*`, `/`, and `%` are
evaluated before `+` and `-`. The compiler emits intermediate results
using temporary variables:

```doit
let a = b + c * d       # c*d first, then +b
let a = w * x + y * z   # w*x and y*z first, then add
```

Use parentheses to override the default precedence:

```doit
let a = (b + c) * d     # b+c first, then *d
```

#### Arithmetic in function arguments

Arithmetic expressions can appear directly in function call arguments:

```doit
notify_number b + 1
my_fn a * 2, b + c
```

#### Value semantics

Each arithmetic operation compiles to a single instruction frame. The
result's typed value comes from the left operand, and the number component
is the arithmetic result of both operands' number components. This means
`Item("metalbar") & 3` followed by `item + 2` would produce
`Item("metalbar") & 5` — the item type is preserved.


### Comparison and Type Check Operators

- `==` — equal
- `!=` — not equal
- `<` — less than
- `<=` — less than or equal
- `>` — greater than
- `>=` — greater than or equal
- `is` — type check (e.g., `x is Unit`)

### Comparison Expressions

Comparison operators can be used as expressions that produce a boolean
value — 1 for true, or empty (0) for false:

```doit
let is_big = count > 10
let is_small = count < 3
let at_least = count >= 5
let at_most = count <= 20
let same = a == b
let different = a != b
var result = a > b
result = a < b
```

The left operand can be a variable, register, or number literal. The right
operand can be a number literal, a variable, `null`, or a type constructor.
Both sides can include arithmetic:

```doit
let gt_num = x > 5         # compare with number
let gt_var = x > y          # compare with variable
let lt_param = x < $input   # compare with parameter
let is_empty = x == null    # compare with null
let has_value = x != null   # not-null check
let num_lhs = 5 > b         # number literal on left
let arith = x + 1 > y - 2   # arithmetic on both sides
let is_metal = x == Item("metalbar")    # compare with constructor
let is_bot = x != Component("behavior") # constructor with !=
```

**Numeric vs equality comparisons:** `>`, `<`, `>=`, and `<=` compare only
the numeric component of register values (using the `check_number`
instruction). `==` and `!=` compare full register composites — both the
typed value and numeric component (using the `compare_register`
instruction). This means `==` and `!=` can distinguish between different
item types, coordinates, etc., not just numbers.

#### Type Check Expressions

The `is` operator checks whether a value is a specific data type,
producing 1 for true or empty (0) for false:

```doit
let me = get_self
let is_unit = me is Unit
let is_item = $input is Item
```

The left operand must be a variable or register. The right operand is
one of the six game data types:

| Type name     | Checks for    |
|---------------|---------------|
| `Item`        | Game items    |
| `Unit`        | Game entities |
| `Component`   | Components    |
| `Technology`  | Technologies  |
| `Value`       | Info values   |
| `Coordinate`  | Coordinates   |

`Number` is not supported — the underlying `value_type` instruction
cannot distinguish numbers from empty/null values.

Type checks work in `let`/`var` initialization and assignment:

```doit
let a = x is Unit       # let init
var b = x is Item        # var init
b = x is Coordinate      # assignment
```

#### Logical operators (`&&`, `||`)

Multiple comparisons and type checks can be chained with `&&` (and) or
`||` (or):

```doit
let in_range = x > 0 && x < 100
let extreme = x > 90 || x < 10
let both_set = a != null && b != null
```

`&&` produces 1 only if **all** comparisons are true. `||` produces 1 if
**any** comparison is true. Chains of three or more are supported:

```doit
let ok = a > 1 && b < 10 && c > 0
let any_match = a == b || c == d || e == f
```

Each sub-expression can be a comparison (with optional arithmetic),
a type check, a bare variable (truthy check), or a number literal.
Different expression types can be freely mixed in the same chain:

```doit
let ok = x is Unit && y > 5
let match = a == b || x is Item
let any = x && y               # truthy: non-empty is true
let either = x || y            # truthy: first non-empty wins
```

#### Truthy values in boolean chains

Bare variables and number literals in `&&`/`||` chains are tested for
"truthiness" — a non-empty register value is true, an empty value is
false:

```doit
let a = x && y               # true if both x and y are non-empty
let b = x || y                # true if either is non-empty
let c = x > 5 && active       # comparison AND truthy check
```

#### Function call results in boolean chains

A function call can be the first term in a boolean chain. The function
always executes, and its result is tested for truthiness or used as
the LHS of a comparison:

```doit
let a = get_number x || d           # fn result OR'd with d
let b = get_number x > 5            # fn result compared to 5
let c = my_fn b + 1, c || d         # fn with arithmetic args, then ||
```

`&&` binds tighter than `||` (standard precedence), so you can mix
them freely:

```doit
let r = a > 1 && b < 5 || c > 3    # same as (a > 1 && b < 5) || c > 3
let r = a || b && c                  # same as a || (b && c)
```

Parentheses can override the default precedence or make grouping
explicit:

```doit
let r = (a > 1 || b < 2) && c > 3
let r = ((a > 1 || b < 2) && c > 3) || d > 4
let r = ($x is Item || $x is Unit) && count > 0
let r = (a == b || c != null) && a > 1
```

Redundant parentheses are allowed: `let r = (a > 3)` is equivalent to
`let r = a > 3`.

Parenthesized expressions work in `let`/`var` initialization and
assignment:

```doit
let r = (a > 1 || b < 2) && c > 3
var r = (a > 1 || b < 2) && c > 3
r = (a > 1 || b < 2) && c > 3
```

> **Not yet supported:** Function calls in non-first boolean position
> (`d || my_fn x`); `is Number` (cannot distinguish from null).

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

`break` can exit a `while` loop early:

```doit
var i = 1
while i <= 10 {
    if i >= 5 {
        break
    }
    i += 1
}
```

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

`loop` also accepts a count expression to execute a fixed number of times:

```doit
loop 5 {
    notify "Iteration"
}
```

The count can be a variable or arithmetic expression:

```doit
loop n {
    x += 1
}
```

`break` works in counted loops too, exiting before all iterations complete.

### Labeled loops and `break`

Both `loop` and `while` can be labeled with a `label:` prefix. `break`
can target a specific label to exit an outer loop from within a nested
loop:

```doit
var i = 0
outer: loop {
    loop {
        if i >= 5 {
            break outer
        }
        i += 1
    }
}
```

Without a label, `break` exits the innermost enclosing loop:

```doit
outer: loop {
    var j = 0
    inner: while j < 10 {
        if j >= 5 {
            break inner   # exits inner while loop
        }
        j += 1
    }
    # execution continues here after break inner
}
```

Labels work with `loop`, counted `loop`, and `while`:

```doit
scan: while has_targets {
    loop 10 {
        if done {
            break scan   # exits the while loop
        }
        process_next
    }
}
```

Label names follow the same rules as variable names (letters, digits,
underscores; must start with a letter or underscore). Duplicate labels
on nested loops are a compile error. `break` with an unknown label is
not an error — the label is treated as the next statement (which will
likely produce its own error). `break` outside of any loop is a compile
error.

### `for` loops

`for` iterates over a `Range`:

```doit
for i in Range(5) {
    notify "hello"      # runs 5 times, i = 0, 1, 2, 3, 4
}
```

The iteration variable (`i`) is immutable — assigning to it inside the
loop body is a compile error.

`Range` accepts 1–3 arguments:

| Form | Meaning |
|------|---------|
| `Range(stop)` | 0, 1, 2, …, stop−1 |
| `Range(start, stop)` | start, start+1, …, stop−1 |
| `Range(start, stop, step)` | start, start+step, …, up to (but not including) stop |

Negative steps count downward:

```doit
for i in Range(10, 0, -2) {
    notify "countdown"   # i = 10, 8, 6, 4, 2
}
```

A literal step of `0` is a compile error.

`Range` can also be stored in a variable and iterated later:

```doit
let r = Range(5)
for i in r {
    notify "hello"
}
```

`for` loops support `break`, labeled `for`, and `break label`:

```doit
outer: for i in Range(5) {
    for j in Range(3) {
        if j == 1 {
            break outer
        }
    }
}
```

`for` loops work in both behavior bodies and function bodies.

### `wait`

Pauses execution for a number of ticks:

```doit
wait 5
notify "done"
```

With a condition block, `wait` repeats until the condition is truthy.
The ticks expression is evaluated once (snapshotted):

```doit
wait 5 { $a > 3 }
notify "a is now greater than 3"
```

The condition block can contain statements before the condition
expression. The condition is always the last item in the block:

```doit
wait 10 {
    notify "checking"
    $a > 5
}
```

`wait` is not a loop — `break` and labels are not supported. The
condition block is evaluated after each wait period, and the wait
repeats only if the condition is falsy.

`wait` works in both behavior bodies and function bodies.

## Execution Mode

Behavior controllers start each execution cycle in **locked** mode, running
one instruction per tick. Use `unlocked { ... }` to run a block in unlocked
mode (runs as many instructions as possible per tick) and `locked { ... }`
to run a block in locked mode:

```doit
unlocked {
    notify "Fast"
}
notify "Slow"
```

Mode blocks are lexically scoped — the mode is set on entry and restored
on exit. They can be used in behavior bodies, control flow blocks
(`if`/`while`/`loop`), and function bodies:

```doit
fn go_fast(txt) {
    unlocked {
        notify txt
    }
}

behavior runner {
    go_fast "Speedy"
}
```

Mode blocks can be nested:

```doit
unlocked {
    notify "Fast"
    locked {
        notify "Slow"
    }
    notify "Fast again"
}
```

### Redundant mode change elimination

The compiler tracks the current execution mode via `frameBuilder.mode` and
only emits transition frames when the mode actually changes:

- `locked { ... }` at the start of a behavior emits no transition (already
  locked)
- Nested `unlocked { ... }` inside an `unlocked` block emits no transition
  (already unlocked)
- Mode is always statically known at every program point — no conservative
  fallback needed

When a function containing mode blocks is inlined at a call site, the
compiler tracks mode through the inlined body. If the caller is already in
the target mode, no transition frame is emitted.

### Mode block expressions

`locked { ... }` and `unlocked { ... }` can also be used as expressions.
The last item in the block becomes the value of the expression:

```doit
let me = unlocked { get_self }
```

The block can contain statements before the tail expression:

```doit
let me = unlocked { notify "getting self"; get_self }
```

Multi-return tail expressions work like multi-return function calls:

```doit
let x, y = unlocked { separate_coordinate coord }
```

Mode block expressions can appear in expression lists:

```doit
let a, b, c = unlocked { get_self }, 1, 2
```

Mode block expressions are supported in `let`/`var` declarations and
assignments, at both behavior level and in function bodies.

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
