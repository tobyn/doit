# Design Decisions

Non-obvious choices and their rationale. Helps future sessions make
consistent decisions without re-deriving past conclusions.

## Stdlib function signatures

**Output parameters → return values**: Functions with output slots use
`return instruction` with `@1` instead of taking an output parameter.
This gives callers natural `let x = get_self` syntax instead of
`get_self x`. The `@1` marker in the instruction frame drives
`hasReturn()` — the `return` keyword is syntactic sugar for readability
in the stdlib file.

**Optional inputs as keyword params**: Optional inputs use keyword
parameters (`keyword varname`) rather than positional params. This avoids
forcing callers to pass placeholder values for unused optional slots.

**`"c"` mode fields omitted**: Mode/combo fields are not parameterized.
They use the game default. The language has no way to express enum-style
mode selection yet, so exposing them would be premature.

**`"txt"` fields as positional strings**: Text fields (like `notify`'s
message) are positional string parameters, matching the natural
call-site syntax (`notify "Hello"`).

## Multiple return values

The `instruction` intrinsic is semantically a function call — it takes
inputs and returns outputs. `@N` markers in the instruction block are
positional return values. `return` passes them through as the wrapping
function's returns.

**Callee (definition):**
```
fn separate_coordinate(coordinate) {
    return instruction "separate_coordinate" {
        0: coordinate
        1: @1
        2: @2
    }
}
```

**Caller (destructuring):**
```
let x, y = separate_coordinate coord
```

`@1` → first binding (`x`), `@2` → second (`y`). The positional order
in the destructuring matches `@N` numbering.

**Contiguous sequence required**: `@N` values in an instruction block
must form a contiguous sequence starting from `@1`. A gap (e.g., `@1`
and `@3` with no `@2`) is a compile error.

**Prefix matching**: Callers can bind fewer returns than a function
produces. `let x = separate_coordinate coord` captures only `@1`;
`@2` receives `false`. Binding count must be <= `returnCount()`.

**`_` as discard**: `let _, y = separate_coordinate coord` skips the
first output. `_` maps to `false` in the compiled instruction slot.

**Mixed modifier binding lists** (behavior level only):
`var a, b, _, let c, var d = fn args`. Modifiers are sticky — bare
idents inherit the active modifier. `_` does not change the active
modifier. Bare idents with no active modifier assign to existing
variables. Binding lists must start with `_`, `let`, or `var`
(bare-ident-first is deferred due to parsing ambiguity).

**Multi-value `return` in function bodies**: `return x, y, z` with
comma-separated identifiers, number literals, or `null`. Literals are
desugared into synthetic body calls with `@retK` names.

## Type literal constructors and `&`

**Constructor syntax over factory functions**: Type constructors use
capitalized names with parentheses (`Item("metalbar")`) rather than
lower-case factory functions (`item("metalbar")`). The capitalized
convention distinguishes type construction from function calls and
reserves the lowercase namespace for future use.

**Namespace prefixes hidden**: `Component`, `Technology`, and `Value`
automatically prepend `c_`, `t_`, `v_` to the user-supplied id. Users
write the short name without the game's namespace prefix. This is less
error-prone and matches how players think about these types.

**`&` as a binary operator**: The `&` operator was chosen for attaching
numeric components because it's visually distinct from arithmetic and
connotes "combining" two things. It mirrors the VM's register composite
model: every register holds (typed_value, number). `Item("metalbar") & 5`
reads naturally as "metalbar with count 5".

**Compile-time vs runtime paths**: Constructors and `&` are resolved at
compile time when all operands are literals, producing inline JSON values
with no runtime instructions. When any operand is a variable, the
compiler emits the appropriate stdlib call (`combine_coordinate` for
`Coordinate`, `set_number` for `&`). Function bodies only support the
compile-time path — runtime constructors require the `frameBuilder`
infrastructure that only exists at behavior level.

**`parseConstructorForTarget` optimization**: For `let`/`var`
declarations, the compiler avoids an extra `set_reg` copy by passing the
declared variable name directly as the output target for runtime
constructor instructions. The general-purpose `parseArgValue` path
(used in function call arguments and assignments) allocates a temporary
variable instead, which is simpler but produces one extra frame for
runtime cases.

## Control flow stubs

Control-flow instructions (branches, loops, terminals, jump/label) are
left as empty-body stubs with a `# control flow:` comment rather than
being implemented with `instruction`. They require compiler-level support
(emitting branch targets, loop structures) that the `instruction`
intrinsic can't express. The comment categorizes the type of control flow
for future implementation planning.

## Test file conventions

**Graph isomorphism for comparison**: Test `.json` files are compared
using graph isomorphism (`matchBehaviors`) rather than exact JSON
equality. This lets the compiler emit frames in any order without
breaking tests. Frame numbering is not semantically meaningful.

**Reference format in .json files**: Test `.json` files use the reference
JS codec's 0-based key format and are not modified programmatically. The
`refToNative` conversion in the test harness bridges to our 1-based
native format.
