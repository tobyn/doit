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
`Coordinate`, `set_number` for `&`). This works uniformly at both
behavior level and in function bodies. At behavior level, runtime
constructors emit frames via `expandCall` with `frameBuilder`. In fn
bodies, they emit synthetic `fnBodyCall` entries with `@ctorN` temp
variable names (using the same `@`-prefix convention as `@retK` return
desugaring to avoid collisions with user identifiers).

**`parseConstructorForTarget` optimization**: For `let`/`var`
declarations, the compiler avoids an extra `set_reg` copy by passing the
declared variable name directly as the output target for runtime
constructor instructions. The general-purpose `parseArgValue` path
(used in function call arguments and assignments) allocates a temporary
variable instead, which is simpler but produces one extra frame for
runtime cases.

## Instruction metadata limitations

The compiler cannot assume that all instructions are defined in the
standard library. Desynced supports user mods that add custom
instructions. The `instruction` intrinsic exists partly to support
these modded instructions — users can emit arbitrary instruction frames
without a stdlib wrapper. This means the compiler cannot rely on stdlib
metadata (like input/output slot direction) for enforcement that needs
to cover all instructions. Any analysis that requires per-slot knowledge
(e.g., which slots are written to) would miss inline `instruction`
blocks for modded opcodes.

## Bare instruction direction checking

Direction enforcement does not apply to bare `instruction` blocks in fn
bodies. The compiler cannot distinguish input from output slots in an
arbitrary instruction frame — the slot numbering is opaque (the same
limitation described in "Instruction metadata limitations" above). This
means a function like `fn writer(out x) { instruction "notify" { txt: x } }`
reads an `out` parameter without error. Direction checks are only applied
to function calls (where parameter metadata is available), not to raw
instruction emissions. This is an acceptable trade-off: `instruction` is
a low-level escape hatch, and users who reach for it accept responsibility
for correct slot usage.

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

## Call-site direction annotations

**Mandatory annotation for out/inout**: Call sites must annotate `out`
and `inout` arguments explicitly to match the parameter's direction.
`fn my_fn(out foo, inout my_kw kw)` must be called as
`my_fn out x, inout my_kw: y`. This makes argument direction visible at
the call site without looking up the function definition.

**`in` is the default**: No annotation is needed for `in` parameters
(the common case), but explicit `in` is accepted for clarity:
`my_fn in x` is equivalent to `my_fn x` when the parameter is `in`.

**Annotation must match exactly**: An annotation that doesn't match the
parameter's effective direction is a compile error. `out` on an `in`
parameter, `in` on an `out` parameter, etc. are all rejected.

**Direction keywords are fully reserved**: `in`, `out`, and `inout`
cannot be used as variable names or parameter names. This avoids parsing
ambiguity — when the parser sees `out` before an argument value, it
always means a direction annotation.

**Uniform enforcement**: The same annotation rules apply at both behavior
level (`parseFnCallArgs`) and in fn bodies (`parseFnBodyCall`), for both
positional and keyword arguments. The shared `checkCallAnnotation` helper
validates the annotation against the parameter definition.
