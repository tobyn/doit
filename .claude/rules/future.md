# Future Ideas

Ideas to revisit later. These are not committed designs — just things
worth thinking about when the time is right.

## Compound doc comments from nested calls

When a function call has a `#!` comment and the expanded instructions also
have their own `#!` comments, it might be useful to build compound
comments that combine both levels (e.g., `"Greeting sequence / Says
hello"`). The syntax for this hasn't been decided yet.

## AST optimizations

Potential optimization passes (would need a new optimization file):

- **Constant folding**: Evaluate compile-time-computable expressions.
- **Dead code elimination**: Remove unreachable statements after
  `return`/`break`.

## Subroutine calls instead of inlining

Functions are currently always inlined — every call site duplicates the
function body. The behavior VM has a `call` instruction that supports
subroutines, which could allow true function calls without duplication.
This would also enable recursion. Needs investigation into how the
`call` instruction works (it's currently a not-implementable stub in
the stdlib).

## Language ergonomics audit (rounds 3–5)

Periodic review of the language for unintuitive syntax, surprising
semantics, or potential footguns. The process: read all manual pages,
all test cases, and the parser/emitter source end-to-end, then
identify anything that would surprise a developer coming from
Go/Python/JS/Rust. Previous rounds produced actionable fixes; this
section captures the remaining items.

To repeat this audit in a future session, ask Claude to "examine the
language and call out any syntax or semantics that might be unintuitive
or result in unexpected behavior from a normal developer" — the same
prompt that generated rounds 1–5. Cross-reference against the items
below and any that have been resolved since.

Resolved in rounds 3–4: `break` with misspelled label (now errors),
`describe()` keyword vs identifier (now distinguishes), single `|`
error (now hints `||`), `=` in boolean context (now hints `==`),
`//` comments (now hints `#`), `return`/`else` at behavior level
(now specific errors), "unknown statement" (now "unknown function"),
nested `fn`/`behavior` definitions (now specific errors),
`let` shadowing is now warned at same scope level (unused
re-declaration warning), variables no longer leak from block scopes
(block scoping implemented).

Resolved in round 5: `index.md` hello world `name` → `@name`,
`language.md` examples using `//` → `#`, missing `checkVarName` in
fn body `let`/`var` declarations (now errors on keywords/constructors),
`++`/`--`/`=` on keywords/constructors (now errors), assignment error
positions always 1:1 (now correct), `continue` gives "unknown function"
(now specific error with hint), `Range(...) & n` gives confusing error
(now specific error explaining step field conflict), `let x = y`
at behavior level errors as "unknown function" (now allows variable
and parameter copies like fn bodies), `is Number` and `is Range`
give generic "unknown type" error (now explain why not supported),
string escape sequences undocumented (now in manual).

Resolved in round 6: `let x = true`/`let x = false`/`let x = null`
and `x = true`/`x = false`/`x = null` at behavior level gave
"unknown function 'true'" (now correctly produces literal values
matching fn body and function argument behavior). The fix was in
`parseBhvVarInit` and `parseBhvDefaultStmt` in `bhvast.go` — both
now check for `"null"`, `"false"`, `"true"` identifiers before the
function lookup path and emit the appropriate `LiteralExpr`.

### Medium priority (design decisions needed)

- **`-Werror` style flag for promoting warnings to errors.** The
  compiler now supports warnings (returned alongside compiled output).
  A flag to treat warnings as errors would be useful for CI/strict
  mode. Needs a CLI flag (`-Werror` or similar) and plumbing through
  the `Compile`/`CompileString` API.

- **Undeclared variable names silently succeed as function arguments.**
  `set_reg completely_undeclared_var` compiles without error or warning.
  The compiler treats the name as a runtime register reference. A typo
  in a variable name passed as a function argument has no compile-time
  feedback. Adding a warning for names not in the symbol table would
  catch the common typo case. The challenge is backward compatibility
  and distinguishing intentional "dynamic" register names from typos.

- **`&` operator on plain variables gives confusing error.** `let x = myvar & 5`
  gives "expected statement, got '&'" instead of explaining that `&`
  requires a typed value (constructor) on the left side. The error message
  should clarify what `&` does and when it is valid.

### Low priority

- **Error message for `let x = "string"` could explain why.** The
  current message (`expected number, function call, or constructor
  after '='`) is accurate but doesn't explain that strings have no
  runtime representation and cannot be stored in variables.

- **`for` loop iteration variable is accessible after the loop** with its
  final value. Developers from Go/Rust expect the iteration variable to
  be scoped to the loop body. The current behavior is undocumented. Either
  document it explicitly or scope the variable to the loop.

- **`wait 0` semantics are undocumented.** The manual doesn't explain
  whether zero ticks is a no-op or has a minimum tick wait.
