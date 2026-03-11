# The doit Language

doit ("do it") is a programming language that targets
[Desynced](https://www.desyncedgame.com/)'s behavior controllers. It compiles
to the Base62 behavior strings that Desynced uses for import and export.

## Hello World

```doit
behavior hello_world {
    @name "Hello World"
    notify "Hello, World!"
}
```

This defines a behavior called `hello_world` that displays the name "Hello
World" in-game and sends a notification with the message "Hello, World!".

## Manual

- [Language](language.md) — Program structure, imports, variables, control flow, behavior calls, and events
- [Functions](functions.md) — Calling and defining functions, the standard library
- [The `instruction` Intrinsic](instruction.md) — Emitting arbitrary game instructions
- [Toolchain](toolchain.md) — Using the `doit` CLI
