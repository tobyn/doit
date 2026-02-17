# The doit language

The doit language is documented in `manual/`. `manual/index.md` is the entrypoint for the documentation.

## Language Constructs

- **`behavior`** — Top-level declaration: `behavior name { ... }`
- **`name`** — Sets the behavior's display name: `name "Hello World"`
- **`instruction`** — Emits an arbitrary game instruction (see `manual/instruction.md`)
- **`fn`** — Function definition (used in stdlib): `fn name(params) { ... }`
- **Function calls** — Calls to stdlib functions: `notify "Hello"` (one string literal per parameter)
- **Comments** — `//` and `#` line comments
