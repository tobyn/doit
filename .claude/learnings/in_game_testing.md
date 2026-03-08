# In-Game Testing Guidelines

How to build behaviors that work well for in-game verification — whether
for the sanity check process, ad hoc hypothesis testing, or any other
scenario where the developer imports a behavior and reports results.

## Use parameters for I/O

Parameters are the high-bandwidth interface between Claude and the
developer. They appear as external registers on the Behavior Controller
component, readable and writable from the game UI without opening the
behavior editor.

- **Outputs** (test results): Write to a parameter declared with `true`
  in the `parameters` array. In doit code, use `@param out Result`.
  In hand-crafted JSON, declare `"parameters": [true], "pnames":
  ["Result"]` and write to the parameter index (1-based integer in
  instruction slots), NOT a variable string. `"2": 1` writes to
  parameter 1; `"2": "Result"` writes to a variable named "Result"
  that the developer can't see externally.
- **Inputs** (test configuration): Declare input parameters (`false`
  in the `parameters` array). The developer sets them via the game UI
  before running the behavior.

Avoid using `notify` for test results. Notifications flash briefly on
screen and disappear — they're easy to miss and can't be inspected
after the fact. Parameters persist and are always visible.

## Exit at the end

Always end the behavior with `exit`. Without it, the behavior restarts
from the beginning on the next tick, overwriting results and making
single-run tests impossible. The developer should be able to:

1. Import the behavior
2. Set input parameters
3. Run it once
4. Read the output parameter

If the behavior loops, the developer has to time their read or pause
the game, which is error-prone.

## Compiled output goes in scratch/

Write base62 output to `scratch/*.b62` files. Terminal copying is
painful — long base62 strings wrap awkwardly and are hard to select.
The developer can open the file directly.

Never use `2>&1` when compiling to `.b62` files. Compiler warnings go
to stderr; merging them into the output corrupts the base62 silently
(the game imports garbage without error). Use `2>/dev/null` to suppress
warnings or omit the redirect entirely.
