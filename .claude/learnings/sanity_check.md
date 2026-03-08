# Sanity Check

End-to-end verification that the compiler produces correct game behavior.
The developer imports compiled base62 into Desynced and reports the output
parameter value.

**Do not record run-specific results here.** No test counts, pass/fail
status, or bugs-found-and-fixed per run. That's changelog noise that
wastes context. Only update this file to improve the *process* itself
(new diagnostic techniques, corrected procedures, lessons about how
to write better tests).

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
  passing tests. Use diagnostics (see below) to isolate failures.
- **Result is 0 or null** → likely a very early failure or a problem
  with parameter assignment. Start with a minimal diagnostic (e.g.,
  just set Result = 42 and exit) to verify basics work.

## Diagnostic Techniques

Each round-trip with the developer is expensive — they have to import
a behavior, run it, and report the result. Maximize the information
gained per round-trip.

### Bitmask diagnostics (preferred)

Use **bitmask encoding** to identify multiple failures in a single
behavior. Assign each test a power of 2:

```
var step = 0
if <test A passes> { step += 1 }
if <test B passes> { step += 2 }
if <test C passes> { step += 4 }
if <test D passes> { step += 8 }
$result = set_reg step
exit
```

Decode the result as binary: each 0 bit is a failing test. Example
with 4 tests: result 13 (1101) = test B fails; result 7 (0111) =
test D fails; result 15 (1111) = all pass.

This is the primary diagnostic tool. Use it whenever the candidate
set is small enough (up to ~10 tests per behavior to stay within
integer precision). Prefer fewer diagnostics with more bits over
many diagnostics with one test each.

### Binary search narrowing

When the failure set is too large for a single bitmask behavior (the
main test has dozens of tests), use halving to narrow down first:

1. Split the tests into halves. Create two behaviors, each with one
   half, using `step++` counting.
2. The developer tests both. The results reveal which half(s) contain
   failures and how many.
3. Continue halving until the candidate set is ≤10 tests, then switch
   to bitmask diagnostics to identify exact failures.

Goal: reach bitmask diagnostics as fast as possible. Halving is just
the funnel to get there.

### Value capture diagnostics

When a test fails and the cause is unclear (wrong expectation vs
compiler bug), output the **actual computed value** instead of a
pass/fail flag:

```
var actual = 0
for i in Range(2, 5) { actual += i }
$result = set_reg actual
exit
```

The developer reports the actual value, which directly reveals whether
the test expectation or the compiled code is wrong.

### Presenting compiled JSON

When asking the developer to inspect compiled output, present an
annotated table mapping frame numbers to instructions and control
flow. This makes the behavior readable without requiring the developer
to mentally parse raw JSON:

```
| Frame | Instruction                | Notes                          |
|-------|----------------------------|--------------------------------|
| 1     | set_number step = 0        |                                |
| 2     | set_number a = 0           |                                |
| 3     | compare_register a, false  | different→4, equal(next)→5     |
| 4     | add step += 1              |                                |
| 5     | ...                        |                                |
```

### General diagnostic principles

- Do not overwrite existing diagnostic files — use incrementing
  numbers (diag1, diag2, ...).
- Batch diagnostics when possible — compile and present multiple
  behaviors per round so the developer can test them in one session.
- A failing test in the main behavior that passes in isolation
  suggests an interaction effect (variable collision, instruction
  budget, execution order), not a standalone bug.

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

**Test language constructs, not game instructions.** Verify values using
language-level operations — comparisons (`>`, `==`), arithmetic (`+`),
type checks (`is`) — not game-specific instructions whose runtime
semantics we may not fully understand. For example, to verify
`Item("metalbar") & 5` has numeric component 5, use `item >= 5 &&
item <= 5` (numeric comparison), not `get_resource_num` (game
instruction with unknown semantics).

The behavior ends with:

```
$result = set_reg step
exit
```

## Library Patterns

`lib.doit` should exercise:
- `const` declarations (including imported constants accessed via namespace)
- `fn` with various signatures (positional args, inout, multiple returns)
- `private fn` (accessed indirectly through a public wrapper)
- Control flow in function bodies (while, if/else if/else, early return)
- Transitive function calls (function A calling function B)

## Workflow Notes

See `in_game_testing.md` for general guidelines (parameters for I/O,
exit at end, compiled output to scratch/, avoiding `2>&1`).

