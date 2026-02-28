# Language Ergonomics Audit

Periodic review of the doit language for unintuitive syntax, surprising
semantics, or potential footguns.

## Process

1. **Start from the open items below.** Fix all open items that don't
   require the developer's input first — work through them in priority
   order (HIGH, then MEDIUM), committing each fix separately. Skip any
   item that requires a design decision or developer input.
2. **Only after all actionable open items are done**, run a new audit
   round: read all manual pages (`manual/`), all test cases
   (`toolchain/compiler/tests/`), and the parser/emitter source
   (`toolchain/compiler/`) end-to-end. Identify anything that would
   surprise a developer coming from Go/Python/JS/Rust.
3. Categorize each finding as HIGH (likely bugs/confusion), MEDIUM
   (design decision needed), or LOW (minor gap). Cross-reference
   against the resolved list below to avoid re-reporting fixed issues.
4. Add new findings to the open items list in this file.
5. Repeat from step 1 until no open items remain above LOW priority.
6. Keep working autonomously — do not stop to ask the developer for
   confirmation. The developer will interrupt if needed.

## Resolved items

### Rounds 1–2

(Pre-date this file. No detailed records.)

### Rounds 3–4

- `break` with misspelled label (now errors)
- `describe()` keyword vs identifier (now distinguishes)
- Single `|` error (now hints `||`)
- `=` in boolean context (now hints `==`)
- `//` comments (now hints `#`)
- `return`/`else` at behavior level (now specific errors)
- "unknown statement" (now "unknown function")
- Nested `fn`/`behavior` definitions (now specific errors)
- `let` shadowing warned at same scope level (unused re-declaration
  warning)
- Variables no longer leak from block scopes (block scoping implemented)

### Round 5

- `index.md` hello world `name` → `@name`
- `language.md` examples using `//` → `#`
- Missing `checkVarName` in fn body `let`/`var` declarations (now errors
  on keywords/constructors)
- `++`/`--`/`=` on keywords/constructors (now errors)
- Assignment error positions always 1:1 (now correct)
- `continue` gives "unknown function" (now specific error with hint)
- `Range(...) & n` gives confusing error (now specific error explaining
  step field conflict)
- `let x = y` at behavior level errors as "unknown function" (now allows
  variable and parameter copies like fn bodies)
- `is Number` and `is Range` give generic "unknown type" error (now
  explain why not supported)
- String escape sequences undocumented (now in manual)

### Round 6

- `let x = true`/`let x = false`/`let x = null` and assignment forms
  at behavior level gave "unknown function" (now correctly produces
  literal values matching fn body and function argument behavior)

### Round 7

- `_ = fn_call` syntax undocumented (now in manual)

### Round 8

- `for` loop iteration variable scoping (already fixed by block scoping
  implementation — push/pop scope in both parser and emitter)
- `&` on plain variables gives confusing error (now specific error
  explaining `&` requires a type constructor)
- `let x = fn_call > 5` intermediate write (fn result now goes to temp
  variable, only comparison result is written to target)
- `let x = "string"` error message now explains strings have no runtime
  representation

## Open items

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

- **`private fn` visibility is not enforced.** The compiler parses
  `private fn` but does not restrict its visibility — it is callable
  from any behavior in the same compilation unit. The current
  architecture (single source string, no file boundaries) makes
  file-level scoping structurally impossible. Either document the
  limitation, remove the feature, or repurpose it.

### Low priority

- **`wait 0` semantics are undocumented.** The manual doesn't explain
  whether zero ticks is a no-op or has a minimum tick wait.
