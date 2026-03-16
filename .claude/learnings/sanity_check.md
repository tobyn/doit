# Sanity Check

> Drift status: clean

End-to-end verification that the compiler produces correct game behavior.
The developer imports compiled base62 into Desynced and reports the output
parameter values.

**Do not record run-specific results here.** No test counts, pass/fail
status, or bugs-found-and-fixed per run. That's changelog noise that
wastes context. Only update this file to improve the *process* itself
(new diagnostic techniques, corrected procedures, lessons about how
to write better tests).

## When to Run

The developer will say "do a sanity check" (or similar). Run through
the procedure below.

## Test Artifacts

All source files live in `toolchain/sanity_check/` (checked into the
repo):

- **`sanity_check/lib.doit`** — Library file imported by the test behavior.
- **`sanity_check/test.doit`** — Main sanity check behavior (plus a
  small `call_helper` behavior for testing `call`).
- **`sanity_check/listener.doit`** — Listener behavior for multi-unit tests.

Compiled output (`.b62` files) go in `toolchain/sanity_check/` itself
(`.b62` and ad-hoc `.json` are gitignored there; golden files are tracked).

- **`sanity_check/check.go`** — Drift checker program. Compiles the
  source files, compares JSON output against golden files, and updates
  the drift status line above.
- **`sanity_check/test.golden.json`** — Golden compiled output for the
  main sanity check behavior (including embedded `call_helper`).
- **`sanity_check/listener.golden.json`** — Golden compiled output for
  the listener behavior.

These files are **persistent and incremental** — do not delete or
rewrite them from scratch. New features get new tests appended;
removed features get their tests deleted.

## Procedure

### 1. Review what's changed

Check what language features have been added or modified since the
last sanity check update:

```sh
git log --oneline toolchain/sanity_check/
git log --oneline manual/
```

Read the manual pages for any new or changed features to understand
what needs testing.

### 2. Update test artifacts

- **New features**: Append new numbered tests to `test.doit` (or
  `listener.doit` if multi-unit). Add any needed library functions
  to `lib.doit`. Update the expected result count in the file header.
- **Changed features**: Update existing tests if semantics changed.
- **Removed features**: Delete tests for removed features. Renumber
  if desired, but gaps are fine.

### 3. Compile

```sh
cd toolchain
go run . compile -b sanity_check sanity_check/test.doit > sanity_check/test.b62
go run . compile sanity_check/listener.doit > sanity_check/listener.b62
```

Note: `test.doit` contains a second behavior (`call_helper`) used by
the `call` test — it's embedded automatically during compilation, no
separate compile needed. The `-b` flag selects the main behavior.

If compilation fails, fix the test code (not the compiler) unless the
error reveals a compiler bug.

### 4. Ask the developer to test

Tell the developer:
- The base62 strings are in `toolchain/sanity_check/`
- Unit A needs a radio transmitter with its band and value registers
  linked to the Radio Band and Radio Value output parameters
- Unit B just needs the listener behavior loaded
- Start both behaviors, then change the Trigger parameter on Unit A
  to any value at some point during the run
- Expected results (check the file headers for current values):
  - Unit A: Result = expected count, Failed = 0
  - Unit B: Result = expected count, Failed = 0

### 5. Interpret the result

Both behaviors have two output parameters:
- **Result** — number of tests that passed (incremented live during
  execution, doubles as a progress meter)
- **Failed** — the test number that failed, or 0 if all passed

Interpreting:
- **Failed = 0, Result = expected** — all tests pass. Done.
- **Failed = N** — test N failed. The behavior exited immediately
  at that test. Look up test N in the source to identify the feature.
- **Result = expected - 1, Failed = 0** — all synchronous tests
  passed but the parameter event (Trigger) didn't fire. The developer
  forgot to change the Trigger parameter, or event binding is broken.

## Diagnostic Techniques

The `Failed` parameter identifies failing tests immediately, so most
diagnostics from earlier are no longer needed. These techniques are
still useful for investigating *why* a test fails.

### Value capture diagnostics

When a test fails and the cause is unclear (wrong expectation vs
compiler bug), temporarily replace the test's assertion with output
of the **actual computed value**:

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
| 3     | compare_register a, false  | different->4, equal(next)->5   |
| 4     | add step += 1              |                                |
| 5     | ...                        |                                |
```

### General diagnostic principles

- Diagnostic files go in `scratch/` (diag1.doit, diag2.doit, ...).
- A failing test in the main behavior that passes in isolation
  suggests an interaction effect (variable collision, instruction
  budget, execution order), not a standalone bug.

## Writing New Tests

Each test follows this pattern in `test.doit`:

```
# N: short description of what's being tested
<setup code>
if <expected condition> { $result += 1 } else { $failed = N; exit }
```

Important rules:
- **Hardcode the test number** in `$failed = N`. Do not derive it
  from `$result` — async events (like the parameter event handler)
  can increment `$result` at unpredictable times, so it doesn't
  reliably reflect position.
- **Use `unlocked { }` blocks** for pure-computation tests. Group
  them by section. Leave tests that interact with game state (faction
  registers, unit registers, radio, get_self, stdlib calls) in normal
  mode.
- **Keep loops small** inside unlocked blocks to stay within the
  instruction budget. 3 iterations is enough to verify loop mechanics.
- Tests should be independent — each test's variables should not
  depend on side effects from previous tests.

**Test language constructs, not game instructions.** Verify values using
language-level operations — comparisons (`>`, `==`), arithmetic (`+`),
type checks (`is`) — not game-specific instructions whose runtime
semantics we may not fully understand. For example, to verify
`Item("metalbar") & 5` has numeric component 5, use `item >= 5 &&
item <= 5` (numeric comparison), not `get_resource_num` (game
instruction with unknown semantics).

## Multi-Unit Communication

The test and listener behaviors communicate via faction registers:

- **Unit A → Unit B**: Unit A writes `%sanity_test_val`,
  `%sanity_test_counter`, `%sanity_test_flag` during its tests.
  The listener waits 50 ticks then reads them.
- **Unit B → Unit A**: The listener writes `%sanity_listener_result`
  with its pass count. Unit A polls this in a loop (20 × 5 ticks)
  and breaks early when it arrives.

This eliminates manual bridging — both behaviors run autonomously.

## Library Patterns

`lib.doit` should exercise:
- `const` declarations (including imported constants accessed via namespace)
- `fn` with various signatures (positional args, inout, multiple returns)
- `private fn` (accessed indirectly through a public wrapper)
- Control flow in function bodies (while, if/else if/else, early return)
- Transitive function calls (function A calling function B)

## Drift Checker

The drift checker (`go run ./sanity_check` from `toolchain/`) is a
smoke test that detects when compiler output changes relative to the
last in-game-verified golden files. It is **informational, not a gate**
— drift does not block commits.

### How it works

1. Reads the `> Drift status:` line in this file.
2. If already drifted, exits immediately (no recompilation).
3. If clean, compiles `test.doit` and `listener.doit`, compares JSON
   output against `test.golden.json` and `listener.golden.json`.
4. If output differs, updates the status line to
   `> Drift status: drifted after <commit>` (the HEAD commit at the
   time drift was detected).

### Updating golden files

After a successful in-game test, regenerate the golden files and reset
the status:

```sh
cd toolchain
go run ./sanity_check -update
```

This recompiles the source, writes new golden JSON, and marks the
status line clean.

## Workflow Notes

See `in_game_testing.md` for general guidelines (parameters for I/O,
exit at end, compiled output to scratch/, avoiding `2>&1`).
