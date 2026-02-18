# The `instruction` Intrinsic

doit has an `instruction` intrinsic that can be used to emit a single, arbitrary behavior
instruction, even if the language doesn't support it. This can be used to generate instructions
in the base game that aren't supported by the stdlib (yet), or to generate instructions added by mods.

## Example

`.doit` source:

```doit
instruction "notify" {
    txt: "Hello, World!"
}
```

compiled behavior JSON:

```json
{
    "op": "notify",
    "txt": "Hello, World!"
}
```
