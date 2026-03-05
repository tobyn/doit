# Language

[Back to index](index.md)

## Program Structure

A doit program consists of one or more `behavior` declarations:

```doit
behavior my_behavior {
    @name "My Behavior"
    # ... body ...
}
```

The identifier after `behavior` is the behavior id. It can be a bareword
identifier or a quoted string (for IDs containing spaces):

```doit
behavior "My Behavior" {
    # ...
}
```

The `@name` attribute sets the display name shown in-game. It is optional and
can appear at most once per behavior. If omitted, the display name defaults to
the behavior id:

```doit
behavior patrol {
    # display name will be "patrol"
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
    # ...
}

behavior harvest {
    @name "Harvest"
    # ...
}
```

```sh
doit compile -b harvest source.doit
```

When a file contains only one behavior, `-b` is optional.

## The Prelude

Every source file is automatically prepended with the **prelude**
(`stdlib/prelude.doit`), which imports the standard library:

```doit
import * from "std:instructions"
import * from "std:iterators"
```

This makes all built-in functions, iterators, and enums available without
any explicit imports. The prelude is prepended before parsing, so its imports
behave exactly like imports you write yourself.

To opt out of the prelude, add `skip prelude` at the very top of the file
(before any other declarations or imports):

```doit
skip prelude

# This file has no stdlib functions available unless explicitly imported.
import notify from "std:instructions"

behavior minimal {
    notify "Only notify is available"
}
```

`skip prelude` is mainly used internally by stdlib files to avoid circular
dependencies. Most programs should not need it.

## Imports

Functions can be shared across files using `import` statements. All imports
must appear at the top of the file, before any `fn` or `behavior`
declarations.

### Named imports

Import specific functions by name:

```doit
import greet from "./my_library"
```

This makes the function `greet` from `my_library.doit` available for use.
Multiple names can be imported from the same file:

```doit
import greet, farewell from "./my_library"
```

### Aliased imports

Rename an import with `as`:

```doit
import greet as hello from "./my_library"
```

### Glob imports

Import all public functions from a file with `*`:

```doit
import * from "./my_library"
```

Glob imports add all non-private functions to the current scope. If
multiple glob imports define the same name, the last one wins. Named
imports and same-file functions always take priority over glob imports.

Glob imports can be combined with named renames to import everything while
giving specific functions a different name:

```doit
import *, hello_world as hw from "./my_library"
```

This imports all public functions via `*`, but `hello_world` is only
accessible as `hw` — the rename replaces the original name. Other functions
from the file remain accessible under their original names.

### Namespace imports

Import an entire file as a namespace:

```doit
import "./my_library" as lib
```

Access functions with dot notation:

```doit
lib.greet
let me = lib.get_me
```

Namespace imports can be combined with named imports:

```doit
import greet from "./my_library" as lib
```

This imports `greet` directly and also makes the full namespace available
as `lib`.

### Import paths

Import paths must start with `./` (relative to current file), `../`
(parent directory), or `std:` (standard library). The `.doit` extension
is added automatically. Paths always use `/` as a separator regardless
of platform:

```doit
import helper from "./utils"       # resolves to ./utils.doit
import base from "../shared/base"  # resolves to ../shared/base.doit
```

### Private functions

Functions marked with `private` cannot be imported by other files:

```doit
private fn internal_helper() {
    notify "internal"
}
```

Attempting to import a private function (either by name or via namespace
access) is a compile error. Private functions are excluded from glob
imports.

### What can be imported

Only functions are importable. Behaviors are program entry points and cannot
be called or imported — behaviors in imported files are silently skipped.

### Import ordering and collisions

- Import statements must appear before all `fn` and `behavior` declarations
- Duplicate import names (across all import statements) are compile errors
- A same-file declaration name that conflicts with a named import or
  namespace name is a compile error
- A same-file declaration name that shadows a glob import is allowed
  (same-file wins)
- Behavior IDs never collide with imports — a behavior named `greet` and an
  import named `greet` can coexist
- Local variables can shadow imported names within their scope:

```doit
import greet from "./my_library"

behavior main {
    let greet = 1    # shadows the imported function in this scope
}
```

### Transitive dependencies

Imported functions can call other functions from their defining file. These
transitive dependencies are resolved automatically — you don't need to
import them explicitly:

```doit
# greet.doit
import say from "./say"
fn greet() { say }

# main.doit
import greet from "./greet"   # say is available transitively
behavior main { greet }
```

### Self-imports

Importing from the current file is a compile error.

### Circular imports

Circular import chains are detected and reported as compile errors:

```doit
# a.doit — imports from b.doit, which imports from a.doit → error
import helper from "./b"
```

### Re-exports

Re-exporting imported functions is not supported. If file A imports `greet`
from file B, file C cannot import `greet` through file A — it must import
directly from file B. (Transitive *dependencies* still work: if A defines a
function that calls `greet`, C can import that function and the call to
`greet` will resolve automatically.)

### Errors in imported files

Parse or compile errors in an imported file cause the entire compilation to
fail. Error messages include the imported file's path and point to the
correct source location within it.

### Stdin limitation

Import statements require a source file path for resolving relative paths.
When compiling from stdin, import statements are not supported (this is a
compile error).

## Constants

Constants are compile-time values defined with `const`:

```doit
const METAL = Item("metalbar")
const STACK_SIZE = 10
const METAL_STACK = METAL & STACK_SIZE
const GREETING = "hello"
const ORIGIN = Coordinate(0, 0)
```

Constants can appear after imports, interspersed with `fn` and `behavior`
declarations. A constant may reference earlier constants and literal
expressions, but not runtime variables or forward references.

Constant values are evaluated at compile time. Supported expressions include
number literals, string literals, boolean literals (`true`, `false`), `null`,
type constructors (`Item`, `Component`, `Technology`, `Value`, `Coordinate`,
`Range`), the `&` operator, arithmetic (`+`, `-`, `*`, `/`, `%`),
comparisons (`>`, `<`, `>=`, `<=`, `==`, `!=`), boolean operators
(`&&`, `||`, `!`), type checks (`is`), and `localize` blocks.

Function calls are also supported in constant expressions. The compiler traces
through the function body at compile time, evaluating pure computations:

```doit
fn double(x) { return x * 2 }
fn clamp(x, lo, hi) {
    if x < lo { return lo }
    else if x > hi { return hi }
    return x
}

const SPEED = double(5)           # evaluates to 10
const CLAMPED = clamp(50, 0, 10)  # evaluates to 10
```

The evaluator bails if the function body hits a runtime-only construct like
an `instruction` block or `wait` statement. Only functions defined before the
constant (or imported) can be called.

Constants are substituted at their use sites with their literal values. They
can be used anywhere an expression is accepted: function arguments, `let`/`var`
initializers, comparisons, and more.

### Private constants

Mark a constant as private to prevent it from being imported:

```doit
private const INTERNAL = 42
```

### Importing constants

Constants participate in the same import system as functions:

```doit
# Named import
import METAL, STACK_SIZE from "./config"

# Glob import (includes all non-private constants)
import * from "./config"

# Namespace import
import "./config" as cfg
let x = cfg.STACK_SIZE
```

## Enums

Enums define named groups of integer values:

```doit
enum Direction {
    North       # 0 (auto-increments from 0)
    South       # 1
    East        # 2
    West        # 3
}

enum Priority {
    Low             # 0
    Medium = 5      # explicit value
    High            # 6 (continues from previous)
    Critical = 100  # explicit value
}
```

Access members with the `::` operator:

```doit
let d = Direction::North
let p = Priority::High
```

Enum values are compile-time integer constants. They work anywhere a number
is accepted: arithmetic, comparisons, function arguments, and more:

```doit
let x = Direction::East + 1
let urgent = priority > Priority::Medium
set_number target, Priority::Critical
```

Using an enum name without `::` is a compile error:

```doit
let x = Direction   # error: enum "Direction" requires '::' member access
```

Members can be separated by newlines or commas, allowing compact
single-line definitions:

```doit
enum Color { Red, Green, Blue }
enum Priority { Low, Medium = 5, High, Critical = 100 }
```

Duplicate member names or values within an enum are compile errors.
Negative explicit values are supported (`Member = -1`).

### Private enums

Mark an enum as private to prevent it from being imported:

```doit
private enum Internal { A, B, C }
```

### Importing enums

Enums participate in the same import system as functions and constants:

```doit
# Named import
import Direction from "./types"

# Glob import (includes all non-private enums)
import * from "./types"

# Namespace import
import "./types" as types
let d = types.Direction::North
```

### Shared namespace

Functions, constants, and enums share a single namespace. Declaring two
things with the same name — by any combination of `fn`, `const`, and `enum`
— is a compile error:

```doit
fn greet() { notify "hi" }
fn greet() { notify "hello" }   # error: duplicate function "greet"
const greet = 5                 # error: constant "greet" conflicts with a function
enum greet { A }                # error: enum "greet" conflicts with a function
```

User-defined functions may override standard library functions with the same
name. Same-file declarations also override glob imports (but collide with
named imports).

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

All variables must be declared with `var` or `let` before use. Using an
undeclared variable name — whether as a function argument, in an expression,
or as an assignment target — is a compile error.

Declare a mutable variable with `var` and an initial numeric value:

```doit
var x = 1
```

Declare an immutable variable with `let`:

```doit
let x = 5
```

The right-hand side of `var` and `let` can be a number literal, a boolean
literal (`true`, `false`, `null`), another variable, a function call that
has a return value, a type constructor expression, an
[arithmetic expression](#arithmetic-expressions), a
[comparison expression](#comparison-expressions), a
[type check expression](#type-check-expressions), an
[`instruction`](instruction.md) expression, a
[mode block expression](#mode-block-expressions), or an
[if-expression](#if-expressions):

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
let done = true
var backup = loc
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

### Block scoping

Variables declared inside a block (`if`/`else`, `while`, `loop`, `for`,
`locked`, `unlocked`, `wait`) are scoped to that block. They are not
visible after the block ends:

```doit
if $p {
    let x = 5     # x exists only inside this if-block
    set_reg x
}
let x = 10        # a new x — the previous one is out of scope
set_reg x
```

Sibling blocks have independent scopes:

```doit
if $p {
    let x = 5
    set_reg x
} else {
    let x = 10    # no conflict with the x in the if-branch
    set_reg x
}
```

### Shadowing

Both `var` and `let` allow shadowing — you can redeclare a variable with the
same name, and the new declaration replaces the previous one:

```doit
let x = 5
let x = 10
var x = 15
```

The compiler emits a warning when a variable is redeclared at the same scope
level before being used. This helps catch accidental re-declarations that may
indicate a typo:

```doit
let x = 5
let x = 10     # warning: "x" shadows a previous declaration that was never used
set_reg x
```

If the variable is read between declarations, no warning is emitted:

```doit
let x = 5
set_reg x      # x is used here
let x = 10     # no warning
set_reg x
```

Redeclaring in a child scope (inside a block) never triggers the warning:

```doit
let x = 5
if $p {
    let x = 10   # no warning — different scope
    set_reg x
}
set_reg x
```

### Assignment

Assign a new value with `=`:

```doit
x = 2
x = true
x = null
x = other_var
x = get_self
x = Item("metalbar") & 5
x = a + b
x = a > b
x = a == b
x = me is Unit
x = a > 0 && a < 100
x = !flag
x = locked { get_self }
x = if a > 5 { a } else { b }
```

The right-hand side of `=` can be a number literal, a boolean literal
(`true`, `false`, `null`), another variable, a function call with a
return value, a type constructor expression, an
[arithmetic expression](#arithmetic-expressions), a
[comparison expression](#comparison-expressions), a
[type check expression](#type-check-expressions), a negation (`!`),
a boolean chain (`&&`, `||`), an
[`instruction`](instruction.md) expression, a
[mode block expression](#mode-block-expressions), or an
[if-expression](#if-expressions).

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

The right-hand side of `+=`, `-=`, `*=`, `/=`, `%=` accepts the full
expression language — arithmetic, function calls, comparisons, type
checks, and boolean expressions:

```doit
x += y + 1
x -= a * 2
x += get_resource_num y
x -= a > 5
x += (a > 5 && b < 10)
```

`++` adds 1, `--` subtracts 1.

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
let a, b = 1, separate_coordinate coord   # separate_coordinate returns 2 values;
                                           # b captures only the first
```

Binding lists support mixed `let`/`var` modifiers and `_` discards (same
syntax as multi-return function calls). This works at both behavior level
and in function bodies:

```doit
var a, let b, _, var c = 1, 2, 3, 4
```

Modifiers are sticky — bare identifiers inherit the most recent `let` or
`var`. `_` discards the value at that position without changing the active
modifier.

To call a function with a return value without binding any of its results,
use `_ =`:

```doit
_ = my_function args
```

This is equivalent to calling the function as a bare statement (which also
discards return values), but makes the intent explicit.

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

**Important:** At runtime, `null` and `false` are equivalent — both produce
an empty register. However, `0` is **not** the same: `var x = 0` produces a
value-bearing register (`{"num": 0}`) that is distinct from empty. The VM's
`compare_register` instruction sees `{"num": 0}` as truthy (it holds a value),
while `null`/`false` are falsy (empty). Numeric comparisons via `check_number`
collapse the distinction — both empty and `{"num": 0}` read as numeric 0.

This means:
- `x == null` and `x == false` test the same thing: whether the register is empty
- `x == 0` tests whether the numeric component is zero (true for both empty and `{"num": 0}`)
- `if x` tests whether the register holds any value — `{"num": 0}` passes, empty does not

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

## Strings

String literals are enclosed in double quotes:

```doit
notify "Hello, World!"
```

Strings support the following escape sequences:

| Escape | Character |
|--------|-----------|
| `\"` | Double quote |
| `\\` | Backslash |
| `\n` | Newline |
| `\t` | Tab |

Any other escape sequence (e.g., `\x`) is a compile error.

Strings have no runtime representation in the VM — they exist only at
compile time. They can be passed as function arguments (baked into
instruction text fields) but cannot be stored in registers. Attempting to
assign a string to a variable with `let` or `var` is a compile error.

## Control Flow

All control flow constructs work in both behavior bodies and function bodies.
In behavior bodies, control flow blocks support the full statement set
including `let`/`var` declarations, nested control flow, and `break` (inside
`loop` and `while`).

### `if` / `else if` / `else`

Conditions accept the full boolean expression language: comparisons with
variables or literals on both sides, `&&`/`||` chains, `is` type checks,
truthy (bare variable) checks, arithmetic sub-expressions, and function
calls.

```doit
if a < 9 {
    notify "a < 9!"
}

if a >= b {
    notify "a >= b"
} else {
    notify "a < b"
}

if a == 1 {
    notify "one"
} else if a > 1 {
    notify "more than one"
} else {
    notify "less than one"
}

# Boolean chains
if a > 5 && b < 10 {
    notify "in range"
}

# Type check
if x is Unit {
    notify "it's a unit"
}

# Truthy (non-empty) check
if x {
    notify "has value"
}

# Arithmetic in condition
if a + 1 >= b - 2 {
    notify "close enough"
}

# Function call in condition
if get_count x > 5 {
    notify "high count"
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

If-expressions support arithmetic and comparison continuation:

```doit
let x = if a > 0 { 10 } else { 20 } + 5
let y = if a > 0 { 10 } else { 20 } > threshold
```

If-expressions can be used as function call arguments:

```doit
set_reg if a > 0 { 10 } else { 20 }
```

If-expressions can be used in `return` items in function bodies:

```doit
fn pick(a) {
    return if a > 0 { 10 } else { 20 }
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
4. **Negation** (`!`) — prefix negation of any boolean sub-expression
5. **Boolean** (`&&`, `||`) — chain comparisons, function results, or
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

#### Unary minus

The `-` prefix negates a number or variable. For number literals, the
negation is resolved at compile time. For variables, the compiler emits
`sub(0, x)`:

```doit
let x = -5          # compile-time: {"num": -5}
let y = -x          # runtime: 0 - x
var z = 10
z = -z              # runtime: 0 - z
set_reg -x          # in function arguments
x += -3             # in compound assignment
let a = $p > -5     # in comparison RHS
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

#### Constant folding

Arithmetic on literal operands is computed at compile time, producing no
runtime instructions:

```doit
let x = 2 + 3           # compiles as let x = 5
let y = 10 * 2 + 5      # compiles as let y = 25
let c = Coordinate(1 + 2, 3 + 4)  # compiles as Coordinate(3, 7)
```

Comparisons on literals are also folded (`5 > 3` → `true`), as are
boolean chains and negations when all operands are compile-time constants.

#### Arithmetic in function arguments

Arithmetic expressions can appear directly in function call arguments:

```doit
notify_number b + 1
my_fn a * 2, b + c
```

#### Parenthesized comparisons in function arguments

Comparison and boolean expressions can be passed as function arguments
by wrapping them in parentheses:

```doit
set_reg (a > 5)
my_fn (x == y), (count > 0 && active)
```

A parenthesized expression containing only a simple value (no
comparison or boolean operator) is treated as a regular value with
optional arithmetic continuation:

```doit
set_reg (a + 1)    # arithmetic — same as set_reg a + 1
```

#### Nested function calls in arguments

Function calls with return values can appear directly as arguments to
other function calls. The compiler recognizes function names and
automatically calls them, passing the result to the outer function:

```doit
let me = set_reg get_self               # get_self() → set_reg
let loc = get_location get_self         # get_self() → get_location
add loc, get_resource_num x             # get_resource_num(x) → add's 2nd arg
```

Argument boundaries are always unambiguous because each function's
parameter count is fixed. `add get_resource_num x, 5` calls
`get_resource_num` with one argument (`x`), then passes the result
and `5` to `add`.

Deep nesting is supported — function calls can be nested to arbitrary
depth:

```doit
let t = set_reg get_type get_self       # get_self() → get_type() → set_reg
```

Nested calls compose with arithmetic — inner arguments can contain
arithmetic expressions:

```doit
add x, get_resource_num x + 5           # get_resource_num(x + 5) → add
```

Nested function calls work in both behavior bodies and function bodies.

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
- `!is` — negated type check (e.g., `x !is Unit`)

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
instruction). This is an important distinction: `Item("metalbar") & 5 > 3`
is true (compares only the numeric 5 vs 3), but
`Item("metalbar") & 5 == Item("copperbar") & 5` is false (different items,
even though the numbers match).

#### Type Check Expressions

The `is` operator checks whether a value is a specific data type,
producing 1 for true or empty (0) for false:

```doit
let me = get_self
let is_unit = me is Unit
let is_item = $input is Item
```

The negated form `!is` checks that a value is **not** a specific type:

```doit
let not_unit = me !is Unit    # equivalent to !(me is Unit)
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

A function call can appear as any term in a boolean chain. The function
always executes, and its result is tested for truthiness or used as
the LHS of a comparison:

```doit
let a = get_number x || d           # fn result OR'd with d
let b = get_number x > 5            # fn result compared to 5
let c = my_fn b + 1, c || d         # fn with arithmetic args, then ||
let d = active || get_flag           # fn call in non-first position
let e = active || get_count > 5      # fn call with comparison in non-first position
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

#### Negation operator (`!`)

The `!` prefix operator negates a boolean expression:

```doit
let not_empty = !x              # true if x is empty
let not_big = !(a > 100)        # true if a <= 100
let outside = !(a > 0 && a < 100)  # true if a <= 0 or a >= 100
let not_unit = !(me is Unit)    # true if me is not a Unit
let not_unit2 = me !is Unit    # shorthand for the above
```

`!` works with any boolean sub-expression: comparisons, type checks,
truthy checks, and `&&`/`||` chains. The `!is` shorthand is available
for negated type checks. Double negation (`!!x`) is allowed.

For chains, `!` applies De Morgan's law internally — `!(a && b)`
becomes `!a || !b`, and `!(a || b)` becomes `!a && !b`.

> **Not supported:** `is Number` (cannot distinguish from null).

### `while`

`while` loops while a condition holds. Conditions accept the same full
boolean expression language as `if` — comparisons, `&&`/`||` chains, `is`,
truthy checks, arithmetic, and function calls.

```doit
var i = 1
while i <= 5 {
    notify "While iteration"
    i++
}
```

The body executes as long as `i <= 5`, then execution continues past the loop.

```doit
# Variable RHS
while i < limit {
    i++
}

# Boolean chain
while i < 10 && active {
    process i
    i++
}
```

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

Both `loop` and `while` can be labeled with a `'label:` prefix. `break`
can target a specific label to exit an outer loop from within a nested
loop:

```doit
var i = 0
'outer: loop {
    loop {
        if i >= 5 {
            break 'outer
        }
        i += 1
    }
}
```

Without a label, `break` exits the innermost enclosing loop:

```doit
'outer: loop {
    var j = 0
    'inner: while j < 10 {
        if j >= 5 {
            break 'inner   # exits inner while loop
        }
        j += 1
    }
    # execution continues here after break 'inner
}
```

Labels work with `loop`, counted `loop`, `while`, and `for`:

```doit
'scan: while has_targets {
    loop 10 {
        if done {
            break 'scan   # exits the while loop
        }
        process_next
    }
}
```

The `'` sigil makes labels syntactically distinct from identifiers.
Label names follow the same rules as variable names (letters, digits,
underscores; must start with a letter or underscore). Duplicate labels
on nested loops are a compile error. `break` with an unknown label is a compile error. `break` outside of
any loop or exec block is also a compile error.

Labeled `break` works across exec block boundaries (continuation blocks
at call sites). The compiler automatically emits `jump`/`label` pairs
to escape the block at the VM level:

```doit
'outer: loop {
    for_component() {
        body { comp, idx ->
            if comp {
                break 'outer   # exits the outer loop
            }
        }
    }
}
```

See [Functions](functions.md#break-in-exec-blocks) for more details on
`break` behavior in exec blocks.

### `continue`

`continue` skips the rest of the current iteration and jumps to the next
iteration of the innermost enclosing loop:

```doit
var i = 0
loop {
    i += 1
    if i == 3 {
        continue   # skip the rest of this iteration
    }
    notify "processed"

    if i >= 5 {
        break
    }
}
```

`continue` works in all loop types:

- **`loop`** (infinite) — jumps back to the top of the loop body.
- **`loop N`** (counted) — increments the counter, then re-checks the
  limit.
- **`while`** — re-evaluates the condition.
- **`for` / `Range`** — re-dispatches to the iterator for the next
  value.
- **`for...in`** (iterators) — hands control back to the iterator for
  the next value.

```doit
# Skip even numbers in a counted loop
loop 10 {
    if $signal {
        continue
    }
    notify "tick"
}

# Skip a specific value in a for loop
for i in Range(10) {
    if i == 5 {
        continue
    }
    notify "value"
}
```

`continue` does not support labels — it always targets the innermost
loop. Use labeled `break` to exit a specific outer loop instead.

`continue` is not supported in exec blocks.
`continue` outside of any loop is a compile error.

### `for` loops

`for` iterates over a `Range`:

```doit
for i in Range(5) {
    notify "hello"      # runs 6 times, i = 0, 1, 2, 3, 4, 5
}
```

The iteration variable (`i`) is immutable — assigning to it inside the
loop body is a compile error.

`Range` accepts 1–3 arguments. Ranges are **inclusive** of the stop value,
matching the underlying `for_number` instruction:

| Form | Meaning |
|------|---------|
| `Range(stop)` | 0, 1, 2, …, stop |
| `Range(start, stop)` | start, start+1, …, stop |
| `Range(start, stop, step)` | start, start+step, …, up to and including stop |

Negative steps count downward:

```doit
for i in Range(10, 0, -2) {
    notify "countdown"   # i = 10, 8, 6, 4, 2, 0
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

`Range` loops compile to the `for_number` instruction, which handles
counter initialization, bounds checking, step direction, and increment
internally. This is the same instruction used by the `each_number`
iterator, so `for i in Range(5)` and
`for i in each_number(0, 5, step: 1)` produce identical output.

`for` loops support `break`, labeled `for`, and `break 'label`:

```doit
'outer: for i in Range(5) {
    for j in Range(3) {
        if j == 1 {
            break 'outer
        }
    }
}
```

`for` can also iterate over iterators — functions declared with `iter` (see
[Functions](functions.md#iterators)):

```doit
for comp, idx in each_component() {
    notify "component", value: comp
}
```

Iterators can yield multiple values per iteration. Bind as many variables as
you need — you don't have to bind all of them (prefix matching):

```doit
# each_component yields (comp, idx); bind only comp
for comp in each_component() {
    notify "component", value: comp
}
```

Binding more variables than the iterator yields is a compile error.

Iterators accept arguments like regular function calls, including keyword
arguments:

```doit
for i in each_number(0, 10, step: 2) {
    notify "counting"
}

for unit in each_signal(my_signal) {
    notify "found", value: unit
}
```

`break` and labeled `break` work the same way as in Range loops:

```doit
for item in each_inventory_item() {
    if item == null {
        break
    }
    notify "item", value: item
}

'outer: for comp in each_component() {
    for i in Range(3) {
        if i == 1 {
            break 'outer
        }
    }
}
```

`for` loops work in both behavior bodies and function bodies.

#### `iterator_instruction`

For inline iteration over raw VM instructions without defining an `iter`,
use `iterator_instruction` in the `for...in` position:

```doit
for comp, idx in iterator_instruction "for_component" {
    0: @1       // first iter variable ← slot 0
    1: @2       // second iter variable ← slot 1
    done: 2     // exhaustion exec slot
} {
    notify "found", value: comp
}
```

`@N` maps output slots to iteration variables (prefix matching — you can
bind fewer variables than outputs). `done:` specifies which exec slot the
VM fires when iteration is exhausted. `break`, `continue`, and labeled
`break` work like any other `for...in` loop.

This is equivalent to declaring an `iter` and calling it, but avoids the
extra declaration for one-off iteration.

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

Function call results work as condition expressions, including with
comparison and boolean continuation:

```doit
wait 5 { get_resource_num $r > 0 }
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

`wait 0` compiles to a `wait` instruction with zero ticks. In locked
mode, every instruction takes at least one tick to execute, so `wait 0`
effectively pauses for one tick. In unlocked mode, the behavior is
game-engine-dependent.

`wait` works in both behavior bodies and function bodies.

### `exit`

Immediately stops execution of the behavior. Unlike reaching the end of
a behavior (which restarts from the beginning), `exit` terminates
permanently — the behavior controller will not restart.

```doit
behavior patrol {
    if $done {
        exit
    }
    notify "patrolling"
}
```

`exit` works in both behavior bodies and function bodies:

```doit
fn maybe_stop(flag) {
    if flag {
        exit
    }
}
```

### `restart`

Restarts execution of the behavior from the beginning. Unlike `exit`
(which terminates permanently), `restart` causes the behavior controller
to re-enter the behavior from the top.

```doit
behavior patrol {
    move_to $waypoint
    restart
}
```

`restart` is terminal — the compiler warns about unreachable code after
it. It works in both behavior bodies and function bodies.

### `label` and `jump`

`label` defines a jump target. `jump` transfers execution to a matching
`label`. The game scans all instructions in the behavior for a `label`
whose value matches the jump expression.

**Named form** — the compiler manages label identity via the `'name` sigil:

```doit
behavior a {
    label 'start
    notify "hello"
    jump 'start
}
```

Named labels are the recommended form. The compiler:
- Allocates a unique internal value for each name
- Errors on duplicate `label 'name` declarations
- Errors on `jump 'name` with no matching `label 'name`
- Emits a runtime fallthrough error (`notify` + `exit`) in case the
  jump doesn't match at runtime — the player sees an error notification
  instead of undefined behavior

Named label scope is behavior-wide: a `jump` can reference a `label`
that appears earlier or later in the behavior (as long as it is not
skipped as unreachable code).

**Expression form** — for dynamic or computed targets:

```doit
behavior a {
    let target = 1
    label target
    notify "looping"
    jump target
}
```

Expression labels accept numbers, variables, or any runtime expression.
The compiler does not validate that expression-form jumps have matching
labels — that is the programmer's responsibility. Expression-form jumps
do not get runtime fallthrough protection.

`jump` can break out of looping instructions — including nested loops.
The VM follows the jump unconditionally, abandoning any active iterators:

```doit
label 'top
for i in Range(0, 100) {
    for j in Range(0, 100) {
        if i == 5 {
            if j == 3 {
                jump 'escape   # escapes both loops
            }
        }
        notify "inner"
    }
}
label 'escape
notify "escaped"
```

`label` is not terminal — execution continues past it. `jump` is
terminal — the compiler warns about unreachable code after it. Both work
in behavior bodies and function bodies.

> **Note:** `jump` and `label` are low-level goto primitives. Use
> structured control flow (`while`, `loop`, `for`) when possible.

> **String literals are not allowed.** Use `'name` for named labels
> or a numeric/variable expression. For raw instruction-level control,
> use the `instruction` intrinsic.

### `last`

Stops the current iterator. Used inside detached continuation blocks
(the block body of an iterator) to signal that iteration should end.

```doit
my_for_comp(entity) {
    body { comp, idx ->
        if comp == null {
            last
        }
        notify "comp", value: comp
    }
    done { notify "done" }
}
```

`last` is terminal — no code after it in the same block will execute
(the compiler warns about unreachable code after `last`).

> **Note:** In `for ... in` loops, `break` automatically handles
> iterator termination — you don't need `last` there. `last` is for
> raw continuation blocks where you manage the iterator directly.

### Unreachable Code

Code after `exit`, `restart`, `jump`, `break`, `last`, or `return` can
never execute. The compiler warns about unreachable code:

```doit
behavior a {
    exit
    notify "this is unreachable"   # warning: unreachable code after 'exit'
}
```

```doit
fn f() {
    return 5
    notify "this is unreachable"   # warning: unreachable code after 'return'
}
```

Unreachable code is detected inside nested blocks as well:

```doit
behavior a {
    if $done {
        exit
        notify "unreachable"       # warning
    }
    notify "still reachable"       # no warning — the exit is in a branch
}
```

The `-e` / `--error` compiler flag promotes warnings to errors.

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

Mode block expressions support arithmetic and comparison continuation:

```doit
let x = unlocked { add a, 1 } + 2
let y = unlocked { get_self } == me
```

Mode block expressions can be used as function call arguments:

```doit
notify unlocked { get_self }
```

Mode block expressions can be used in `return` items in function bodies:

```doit
fn get_unlocked() {
    return unlocked { get_self }
}
```

Mode block expressions are supported in `let`/`var` declarations,
assignments, function call arguments, and `return` items, at both
behavior level and in function bodies.

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
