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
(now specific error explaining step field conflict).

### Medium priority (design decisions needed)

- **`-Werror` style flag for promoting warnings to errors.** The
  compiler now supports warnings (returned alongside compiled output).
  A flag to treat warnings as errors would be useful for CI/strict
  mode. Needs a CLI flag (`-Werror` or similar) and plumbing through
  the `Compile`/`CompileString` API.

### Low priority

- **Error message for `let x = "string"` could explain why.** The
  current message (`expected number, function call, or constructor
  after '='`) is accurate but doesn't explain that strings have no
  runtime representation and cannot be stored in variables.
