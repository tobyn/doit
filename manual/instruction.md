# The `instruction` Intrinsic

doit has an `instruction` intrinsic that can be used to emit a single, arbitrary behavior
instruction, regardless of language support.

## Example

`.doit` source command:

```doit
instruction "notify" {
    txt: "Hello, World!"
}
```

compiled output JSON:

```json
{
    "op": "notify",
    "txt": "Hello, World!"
}
```
