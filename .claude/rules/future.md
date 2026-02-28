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

## Language ergonomics audit (round 3)

Periodic review of the language for unintuitive syntax, surprising
semantics, or potential footguns. The process: read all manual pages,
all test cases, and the parser/emitter source end-to-end, then
identify anything that would surprise a developer coming from
Go/Python/JS/Rust. Previous rounds produced actionable fixes; this
section captures the remaining items from the third pass.

To repeat this audit in a future session, ask Claude to "examine the
language and call out any syntax or semantics that might be unintuitive
or result in unexpected behavior from a normal developer" — the same
prompt that generated rounds 1–3. Cross-reference against the items
below and any that have been resolved since.

### Medium priority

- **`let` shadowing is silent at the same scope level.**
  `let a = 1; let a = 2` compiles without warning. Most languages with
  `let` either error on same-scope re-declaration or require an
  explicit re-binding keyword. Silent shadowing can mask typos. A
  warning or error for same-scope `let` re-declaration would catch
  this. Needs thought about interaction with blocks (variables leak
  from blocks, so re-declaring after a block is common).

- **Variables leak from block scopes.** Variables declared inside
  `if`/`while`/`loop`/`locked`/`unlocked` blocks are visible after the
  block ends because all blocks share the parent `symbolTable`.
  Developers from Go/Rust/JS would expect block scoping. This is a
  deeper design issue — fixing it requires scope stacking in the symbol
  table — but it interacts with the `let` shadowing issue above.

### Low priority

- **Error message for `let x = "string"` could explain why.** The
  current message (`expected number, function call, or constructor
  after '='`) is accurate but doesn't explain that strings have no
  runtime representation and cannot be stored in variables.
