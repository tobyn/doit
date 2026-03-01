# Sanity Check

End-to-end verification that the compiler produces correct game behavior.
The developer imports compiled base62 into Desynced and reports the output
parameter value.

## When to Run

The developer will say "do a sanity check" (or similar). Run through
the procedure below.

## Test Artifacts

All artifacts live in `scratch/` (gitignored, may not exist — recreate
if needed):

- **`scratch/lib.doit`** — Library file imported by the test behavior.
- **`scratch/test.doit`** — Main sanity check behavior.
- **`scratch/test.b62`** — Compiled base62 of `test.doit`.

Delete any existing sanity check artifacts before starting. Each sanity
check is written from scratch.

## Procedure

### 1. Learn the language

Read `manual/language.md` and `manual/functions.md` to understand the
full set of language features and standard library functions available.
This ensures the test exercises the current state of the language, not
a stale snapshot from a previous session.

### 2. Write the test behavior

Write `scratch/lib.doit` and `scratch/test.doit` from scratch,
exercising as many language constructs as possible. Follow the patterns
described in "Writing New Tests" and "Library Patterns" below.

### 3. Compile

```sh
cd toolchain
go run . compile ../scratch/test.doit > ../scratch/test.b62
```

If compilation fails, fix the test code (not the compiler) unless the
error reveals a compiler bug.

### 4. Ask the developer to test

Tell the developer:
- The base62 string is in `scratch/test.b62`
- What to set the input parameters to
- What the expected Result value is

### 5. Interpret the result

- **Result matches expected** → sanity check passes. Done.
- **Result is less than expected** → the result IS the number of
  passing tests. Use binary search with smaller diagnostic behaviors
  (`scratch/diag*.doit`) to isolate the first failing test. Investigate
  and fix the compiler bug. Recompile and re-test.
- **Result is 0 or null** → likely a very early failure or a problem
  with parameter assignment. Start with a minimal diagnostic (e.g.,
  just set Result = 42 and exit) to verify basics work.

### 6. Binary search diagnostics

When isolating a failure:

1. Create `scratch/diagN.doit` with a subset of tests from the main
   behavior (e.g., first 10, first 20).
2. Compile to `scratch/diagN.b62`.
3. Ask the developer to test. The result tells you how many tests
   pass in that subset.
4. Narrow down to the first failing test number, then create a minimal
   isolated reproduction.
5. Once the bug is found and fixed, recompile the full test and re-test.

Do not overwrite existing diagnostic files — use incrementing numbers.

## Writing New Tests

Each test follows this pattern in `test.doit`:

```
# N: short description of what's being tested
<setup code>
if <expected condition> { step++ }
```

Tests should be independent — each test's variables should not depend on
side effects from previous tests (except `step` itself). Use fresh
variable names per test to avoid collisions.

The behavior ends with:

```
$result = set_reg step
instruction "exit" {}
```

## Library Patterns

`lib.doit` should exercise:
- `const` declarations (including imported constants accessed via namespace)
- `fn` with various signatures (positional args, inout, multiple returns)
- `private fn` (accessed indirectly through a public wrapper)
- Control flow in function bodies (while, if/else if/else, early return)
- Transitive function calls (function A calling function B)
