# Future Ideas

Ideas to revisit later. These are not committed designs — just things
worth thinking about when the time is right.

## Compound doc comments from nested calls

When a function call has a `#!` comment and the expanded instructions also
have their own `#!` comments, it might be useful to build compound
comments that combine both levels (e.g., `"Greeting sequence / Says
hello"`). The syntax for this hasn't been decided yet.

## AST optimizations

Constant folding is now implemented (arithmetic, comparisons, boolean
chains, negation). Remaining optimization ideas:

- **Dead code elimination**: Remove unreachable statements after
  `return`/`break`.
- **Opportunistic partial evaluation**: Extend compile-time evaluation
  beyond `const` declarations to optimize regular expressions when all
  operands happen to be known at compile time.

## Branch clauses for branching instructions

Currently ~70 stdlib instructions are control-flow stubs because they
have `[exec]` branch slots the compiler can't express. The design
direction is **call-site branch blocks** — the caller provides named
blocks that the compiler wires to exec slots on the underlying
instruction.

### Call-site syntax

```
check_number(a, b) larger {
    // exec slot 0
} smaller {
    // exec slot 1
}
// fall-through is the implicit "next" path

// Works as expressions — each branch has a tail value:
let result = check_number(a, b) larger { 10 } smaller { 20 }
```

### Function signature declares branches

Branches are declared in the function signature so the compiler doesn't
need magic to discover them. The body binds declared branches to
instruction exec slots or control flow:

```
fn check_number(value, target, &branches(larger, smaller)) {
    instruction "check_number" {
        2: value,
        3: target,
        exec 0: branches.larger,
        exec 1: branches.smaller,
    }
}
```

### Pure-logic functions can branch too

`return br.name` routes execution to a caller-provided branch without
any instruction involvement, making the mechanism fully general:

```
fn my_branch(a, &b(next_one, next_two)) {
    if a > 3 {
        return b.next_one
    } else {
        return b.next_two
    }
}

let a = my_branch(5) next_one { 1 } next_two { 2 }  // == 1
```

### `instruction` with branches directly

`instruction` itself should support branch blocks so it's usable with
branching instructions as a normal statement or expression, not only
inside stdlib wrapper functions:

```
let a = instruction "some_op" { ... } branch1 { ... } branch2 { ... }
```

### Open questions

- **Branches + return values**: Can a function have both `@N` data
  outputs and exec branches? Likely orthogonal (data vs control flow)
  but needs explicit design.
- **Fall-through as expression**: When used as an expression, do
  uncovered branches produce `null` (like if-expressions without
  `else`), or are they compile errors?
- **Syntax for declaration**: `&branches(...)` is a placeholder. `&`
  is already the numeric attachment operator in expressions — a keyword
  like `branch` or `exec` might read better. Parsing implications TBD.
- **Branch passthrough**: Wrappers should be able to forward branches
  to inner calls. The proposed `return br.name` handles this.

### Relationship to `match` / `when`

A `match` expression (multi-way branch on type or value) is still
desirable as sugar over `if`/`else if` chains. Branch clauses address
a different problem — exposing VM-level exec branches. A `match` on
a `value_type` call with branch clauses could be a natural combination
of both features.

### Design status

This is early-stage exploration. The core idea (call-site blocks wired
to exec slots, declared in signatures, bound in bodies) is solid, but
parsing, semantics, and edge cases haven't been worked through yet.

## Subroutine calls instead of inlining

Functions are currently always inlined — every call site duplicates the
function body. The behavior VM has a `call` instruction that supports
subroutines, which could allow true function calls without duplication.
This would also enable recursion. Needs investigation into how the
`call` instruction works (it's currently a not-implementable stub in
the stdlib).
