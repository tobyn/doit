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

Type constructors create typed game values. Six constructors are available:

| Constructor | Example | Compiled value |
|-------------|---------|----------------|
| `Item("id")` | `Item("metalbar")` | `{"id": "metalbar"}` |
| `Component("id")` | `Component("behavior")` | `{"id": "c_behavior"}` |
| `Technology("id")` | `Technology("signals2")` | `{"id": "t_signals2"}` |
| `Value("id")` | `Value("pentagon")` | `{"id": "v_pentagon"}` |
| `Coordinate(x, y)` | `Coordinate(3, 7)` | `{"coord": {"x": 3, "y": 7}}` |
| `Range(stop)` | `Range(5)` | `{"coord": {"x": 0, "y": 5}, "num": 1}` |

`Component`, `Technology`, and `Value` automatically add their namespace prefix
(`c_`, `t_`, `v_`) — use the short id without the prefix.

`Coordinate` accepts number literals or variables. With literals, the coordinate
is resolved at compile time. With variables, the compiler emits a
`combine_coordinate` instruction at runtime.

`Range` accepts 1–3 arguments: `Range(stop)`, `Range(start, stop)`, or
`Range(start, stop, step)`. The range value is stored as a coordinate+number
composite where x=start, y=stop, num=step. With all-literal arguments, it
resolves at compile time. With variable arguments, the compiler emits a
`combine_register` instruction at runtime. `Range` is primarily used with
`for` loops (see [Language](language.md#for-loops)).

Constructor names are reserved — they cannot be used as variable or function
names. `Unit` is also reserved (it has no constructor but is used as a type
name in `is` expressions).

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

## Mode Selectors

Many game instructions have a mode selector — a dropdown in the game UI
that picks a variant of the instruction (e.g., sync vs async movement,
bitwise operation type, slot counting category). The standard library
exposes these via keyword arguments and enum types.

### Mode enums

The standard library defines enums for each mode selector. Values are
1-based to match the game's internal combo indexing:

| Enum | Values | Used by |
|------|--------|---------|
| `MoveMode` | `Sync`, `Async` | `domove`, `moveaway_range`, `scout` |
| `AmountMode` | `Specified`, `UpTo` | `dodrop`, `dopickup`, `request_item`, `request_wait` |
| `BitwiseMode` | `And`, `Or`, `Xor`, `Not`, `ShiftLeft`, `ShiftRight`, `CompareEqual`, `CompareLarger`, `CompareLargerOrEqual`, `Add`, `Subtract`, `Multiply`, `Divide`, `Modulo` | `bitwise_op` |
| `LockSlotMode` | `OnlyUnfixed`, `OverrideFixed` | `lock_slots` |
| `CountSlotType` | `All`, `Storage`, `Gas`, `Virus`, `Anomaly`, `Drone`, `Garage`, `Alien`, `Satellite` | `count_slots` |
| `CountItemMode` | `Remaining`, `Reserved` | `count_item` |
| `UnitInfoStat` | `Durability`, `VisibilityRange`, `MovementSpeed` | `get_unit_info` |
| `PowerInfoStat` | `Producing`, `Requiring`, `Efficiency`, `Consuming`, `Receiving`, `Transmitting` | `get_unit_power_info` |
| `ItemInfoStat` | `MaxStack`, `AttackRange`, `MinRange`, `Damage`, `DamageType`, `BlastRadius`, `MoveAndFire`, `DPS`, `PowerStorage`, `DrainRate`, `ChargeRate`, `Bandwidth`, `DroneRange`, `Power` | `get_item_info` |
| `SignalFilterMode` | `Match`, `Exact`, `NotExact`, `LessThan`, `ExactOrLessThan`, `MoreThan`, `ExactOrMoreThan` | `each_signal_match` |

### Passing mode selectors

Pass mode selectors as keyword arguments using the enum value:

```doit
# Bitwise XOR
let result = bitwise_op a, b: b, mode: BitwiseMode::Xor

# Async movement
domove target, mode: MoveMode::Async

# Count storage slots
let n = count_slots type: CountSlotType::Storage

# Get unit durability
let hp = get_unit_info me, stat: UnitInfoStat::Durability
```

The mode keyword argument is optional — omitting it lets the game use its
built-in default (typically the first option in the dropdown).

These enums are standard library definitions, not language builtins. They
are automatically available to all programs (like all stdlib symbols) and
can be used in imported files.

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

The `return` statement declares the function's return value:

```doit
fn locate_self() {
    let me = get_self
    let coord = get_location me
    return coord
}
```

When `return` appears at the end of the function body and all return values
are variable names, it is a zero-copy compile-time binding — the compiler
maps the named locals directly to the caller's return targets with no extra
instructions. At call sites, the caller provides the return target via
assignment syntax (`let loc = locate_self`).

`return` can also appear inside control flow blocks for early exit:

```doit
fn pick(x) {
    if x > 5 {
        return x
    }
    return null
}
```

When there are multiple `return` statements, or `return` appears inside
a block, the compiler emits jump instructions to transfer control to the
end of the function body.

`return` accepts the full expression language — arithmetic, comparisons,
boolean chains, negation, type checks, function calls, constructors,
mode block expressions, if-expressions, and parenthesized expressions:

```doit
fn locate() {
    return get_self
}

fn adder(a, b) {
    return a + b
}

fn is_big(a) {
    return a > 5
}

fn is_empty(a) {
    return !a
}

fn in_range(a) {
    return a > 0 && a < 100
}
```

The maximum return arity across all `return` statements in a function
determines the function's return count. Branches that return fewer values
fill the remaining slots with `null`.

### Expressions in function bodies

Function bodies support the same expressions as behavior bodies:

```doit
fn compute(a, b) {
    let sum = a + b             # arithmetic
    let bigger = a > b          # comparison
    let both = a > 0 && b > 0   # boolean chain
    let is_coord = a is Coordinate  # type check
    return sum
}
```

Arithmetic (`+`, `-`, `*`, `/`, `%`), comparison (`>`, `<`, `>=`, `<=`, `==`,
`!=`), type checks (`is`), boolean operators (`&&`, `||`), and
parenthesized grouping all work inside `fn` bodies.

Multi-binding expression lists also work in function bodies:

```doit
fn setup() {
    let a, b = 1, 2
    let me, coord = get_self, get_location me
}
```

### Mutable variables (`var`)

Use `var` to declare a mutable local variable in a function body:

```doit
fn increment(inout x) {
    var count = 0
    count += 1       # compound assignment
    count++          # increment
    x = count        # assignment
}
```

`let` declarations are immutable — assigning to a `let` variable is a
compile error. `var` variables can be reassigned, used with compound
assignment (`+=`, `-=`, `*=`, `/=`), and incremented/decremented (`++`,
`--`).

Assignment to `in` parameters is also a compile error. `out` and `inout`
parameters can be assigned to.

### Control flow in function bodies

Function bodies support `if`/`else if`/`else`, `while`, `loop`/`break`,
`for`, `wait`, and `locked`/`unlocked` blocks:

```doit
fn clamp(a, min, max) {
    var result = a
    if a < min {
        result = min
    } else if a > max {
        result = max
    }
    return result
}

fn sum_to(n) {
    var total = 0
    var i = 1
    while i <= n {
        total += i
        i++
    }
    return total
}

fn find_threshold(inout x) {
    loop {
        if x >= 10 {
            break
        }
        x += 3
    }
}
```

`for` loops, `wait` statements, and `locked`/`unlocked` blocks also work
in function bodies — see [Language](language.md#for-loops),
[Language](language.md#the-wait-keyword), and
[Language](language.md#execution-mode) for details.

Control flow conditions support the same expressions as behavior-level
conditions: comparisons, type checks, boolean chains, and parenthesized
grouping.

`return` can appear inside control flow blocks (`if`, `while`, `loop`,
`for`) for early exit. When there are multiple `return` paths, the compiler
emits jump instructions to ensure control reaches the end of the function.

### Branching Functions (Continuations)

Some game instructions branch execution — they choose between multiple
paths based on a condition (e.g., `check_number` picks "larger", "smaller",
or "equal"). Functions wrapping these instructions declare their
continuation names with `exec`:

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

#### Calling branching functions

At the call site, provide continuation blocks in braces. Each block runs
when its named path is chosen:

```doit
my_check(a, b) {
    larger { notify "a > b" }
    smaller { notify "a < b" }
    equal { notify "a == b" }
}
```

Unprovided continuations bridge directly to the code after the call.
Order of named blocks doesn't matter.

#### Data bindings

When a branching function provides output data on its exec paths,
declare it with `@N` references in the instruction block:

```doit
fn scan_thing(target) exec(found, not_found) {
    instruction "scan" {
        0: target
        1: @1
        2: @2
        exec 3: found(@1, @2)
        next: not_found
    }
}
```

At the call site, bind the data with Kotlin-style `->` syntax:

```doit
scan_thing(enemy) {
    found { entity, distance ->
        notify "found", value: entity
    }
    not_found {
        notify "miss"
    }
}
```

The `@N` numbers refer to instruction output slots. The continuation
argument list (`found(@1, @2)`) declares which outputs are available
to the block. The block's bindings (`entity, distance ->`) name them.

#### Detached continuations

> **Note:** For iterator-style loops, prefer `iter` declarations and
> `for ... in` syntax (see [Iterators](#iterators)) over `fn...exec` with
> continuation blocks. The continuation syntax below is still used for
> non-iterator branching functions.

Some instructions are iterators — they dispatch a body block
repeatedly. These are called *detached* continuations because the VM
re-dispatches them internally rather than bridging back to the caller.
The compiler derives detached status from the function definition — no
annotation is needed at the call site:

```doit
my_for_comp(entity) {
    body { comp, idx ->
        notify "comp", value: comp
    }
    done { notify "done" }
}
```

#### `break` and `last` in exec blocks

`break` inside an exec block means "exit this block invocation":

- **Detached blocks**: `break` exits the current iteration. The
  iterator continues with the next element. To *stop* the iterator,
  use the `last` keyword — it is terminal, so no `break` is needed:

  ```doit
  my_for_comp(entity) {
      body { comp, idx ->
          if comp == null {
              last      # stop the iterator (terminal)
          }
          notify "comp", value: comp
      }
      done { notify "done" }
  }
  ```

  A bare `break` without `last` skips the rest of the block body but
  lets the iterator dispatch the next element normally.

- **Bridging blocks**: `break` jumps to the join point after all
  continuation blocks, the same as normal block completion:

  ```doit
  my_check(a, b) {
      larger {
          if a == null {
              break     # skip to join point
          }
          notify "larger"
      }
      smaller { notify "smaller" }
      equal { notify "equal" }
  }
  ```

Labeled `break` can target loops **outside** the exec block. The
compiler emits `jump`/`label` pairs to escape the block at the VM
level:

```doit
'outer: loop {
    my_iter(e) {
        body { break 'outer }   # exits the outer loop
    }
}
```

This works across multiple exec block boundaries — for example,
breaking out of a loop from inside nested continuation blocks.

Loops declared *inside* an exec block can use `break` and labeled
`break` normally — these compile to the standard `@break` placeholder
mechanism without needing `jump`/`label`.

#### Collapsed form

When a function has a single continuation you care about, you can omit
the continuation name. The block binds to the leftmost `exec` name.
Connection type (bridging/detached) is inherited from the function
definition:

```doit
# These are equivalent:
my_check(a, b) { notify "a > b" }
my_check(a, b) { larger { notify "a > b" } }
```

#### Pure-logic branching

Functions without `instruction` blocks can also branch using
`return <continuation_name>`:

```doit
fn is_big(a) exec(yes, no) {
    if a > 5 {
        return yes
    }
    return no
}

is_big(x) {
    yes { notify "big" }
    no { notify "small" }
}
```

The `return yes` and `return no` statements dispatch to the caller's
continuation blocks. This works identically to instruction-based branching
from the caller's perspective.

Pure-logic branching can also pass data to continuation blocks using
`return <continuation_name>(args...)`:

```doit
fn classify(a) exec(big, small) {
    if a > 5 {
        return big(a, 1)
    }
    return small(a, 0)
}

classify(x) {
    big { v, flag -> notify "big", value: v }
    small { v, flag -> notify "small", value: v }
}
```

The arguments use the full expression language — variables, arithmetic,
function calls, constructors, and literals all work. Different
continuations can pass different numbers of arguments:

```doit
fn check(a) exec(yes, no) {
    if a > 5 {
        return yes(a, 1)   # 2 args
    }
    return no(a)            # 1 arg
}
```

The same continuation must always pass the same number of arguments
across all `return` statements — inconsistent counts are a compile
error. `return yes()` with empty parentheses is also an error; use
`return yes` for control-only dispatch.

#### Expression form

Branching calls can be used as expressions, following the same rules as
if-expressions. Each continuation block has a tail value (the last
expression in the block):

```doit
let result = check_number(a, b) {
    larger { 1 }
    smaller { -1 }
    equal { 0 }
}
```

Unprovided continuations produce `null`, like an if-expression without
`else`. Expression form works in both behavior bodies and function bodies:

```doit
fn classify(a) {
    let result = is_big(a) {
        yes { 1 }
        no { 0 }
    }
    return result
}
```

Expression form is restricted to all-bridging calls — a compile error is
reported if any continuation is detached (detached blocks don't produce
values).

#### Standard library branching functions

Most standard library functions that wrap branching game instructions
declare `exec` continuations. You can call them directly with
continuation blocks:

```doit
# Pure conditional — three-way branch
check_number(health, 50) {
    larger { notify "healthy" }
    smaller { notify "critical" }
    equal { notify "half" }
}

# Failable getter — success on fall-through, exec on failure
let item = get_inventory_item() {
    no_items { notify "empty" }
}

# Iterator — detached body, bridging done
for_component() {
    body { comp, idx ->
        notify "component", value: comp
    }
    done { notify "done iterating" }
}

# Action with outcome
mine(resource) {
    cannot_mine { notify "blocked" }
    full { notify "inventory full" }
}
```

The continuation names are documented in the `# frame:` comments in
the standard library source (`toolchain/stdlib/instructions.doit`).
Functions like `check_number`, `compare_register`, and `value_type`
also serve as the compiler's implementation of `if`/`while` conditions
and `is` expressions — both uses coexist.

### Private Functions

A function defined with `private fn` is only visible within the file that
defines it:

```doit
private fn my_notify(txt) {
    notify txt
}
```

## Iterators

Iterators are functions that produce a sequence of values, consumed by
`for ... in` loops (see [Language](language.md#for-loops)).

### Declaring iterators

Declare an iterator with `iter`. The `-> names` after the parameter list
declares the output variables yielded each iteration:

```doit
iter active_components() -> comp {
    for c, idx in each_component() {
        if c != null {
            yield c
        }
    }
}
```

### `yield`

`yield` produces values for one iteration of the calling `for` loop. The
number of yield values must exactly match the number of declared outputs:

```doit
iter pair_components() -> comp, idx {
    for c, i in each_component() {
        yield c, i    # must yield exactly 2 values
    }
}
```

`yield` is only valid inside `iter` bodies — using it in a regular `fn`
body is a compile error.

### Static sequence iterators

When an iter body consists entirely of `yield` statements, each yield
produces one value per tick using a state machine. This enables iterators
that emit a fixed sequence of values:

```doit
iter countdown() -> val {
    yield 3
    yield 2
    yield 1
}

for v in countdown() {
    notify "tick", value: v
}
```

Yield expressions can reference iter parameters, use literals, arithmetic,
and constructors:

```doit
iter pair(a, b) -> val {
    yield a
    yield b
}

for v in pair(10, 20) {
    notify "got", value: v
}
```

Multi-output yields also work:

```doit
iter pairs() -> x, y {
    yield 1, 10
    yield 2, 20
}

for a, b in pairs() {
    notify "pair", value: a
}
```

### Instruction-backed iterators

The standard library defines iterators backed by game instructions. These
use a simplified `instruction` block with `done: N` syntax:

```doit
iter each_component() -> comp, idx {
    instruction "for_component" {
        0: comp
        1: idx
        done: 2
    }
}
```

The `done: N` field specifies which exec slot signals iterator exhaustion.
Output names from `->` map directly to numbered instruction slots.

### Using iterators

Call iterators with `for ... in`:

```doit
for comp, idx in each_component() {
    notify "component", value: comp
}

for i in each_number(0, 10, step: 2) {
    notify "counting"
}
```

### Private iterators

Like functions, iterators can be declared `private`:

```doit
private iter my_filter() -> val {
    for c in each_component() {
        if c != null {
            yield c
        }
    }
}
```

Private iterators are only visible within the file that defines them.

## The `instruction` Intrinsic

The [`instruction` intrinsic](instruction.md) emits arbitrary game
instructions directly. It works as a general expression — in behavior bodies,
in `let`/`var` declarations and assignments, and inside function bodies. The
standard library uses `return instruction` to wrap game instructions as
functions.
