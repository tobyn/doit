---
paths:
  - "toolchain/syntax/**/*"
  - "toolchain/compiler/scanner.go"
  - "editors/doit.tmLanguage.json"
  - "editors/**/*"
---

# Syntax Sources of Truth

The language's syntax is defined in multiple places that must stay in
sync. When adding or changing syntax (keywords, operators, type
constructors, escape sequences, literal forms), update **all** of
them.

## Sources

1. **Scanner** — `toolchain/syntax/scanner.go`
   The authoritative source. `Keywords` map, `IsConstructor()`,
   operator cases in `RawNext()`, escape sequences in `scanString()`,
   identifier rules in `IsIdentStart`/`IsIdentCont`.

2. **TextMate grammar** — `editors/doit.tmLanguage.json`
   Regex-based syntax highlighting. Keyword lists in six pattern
   groups (`control-flow-keywords`, `declaration-keywords`,
   `mode-keywords`, `literal-keywords`, `operator-keywords`,
   `other-keywords`), type constructors, operators, string escapes.
   Cannot share code with the scanner — must be updated manually.

3. **Semantic tokenizer** — `toolchain/syntax/highlight.go`
   Uses `RawNext()` for scanning (shared with compiler), so operator
   and literal scanning stays in sync automatically. But the
   `classifyIdentDefault` switch lists context-setting keywords
   (`fn`, `iter`, `behavior`, etc.) separately. Missing a keyword
   there only affects context tracking, not highlighting — the
   `Keywords[word]` fallback catches it as a keyword.

## Checklist

When adding a **keyword**: update `Keywords` map in
`syntax/scanner.go`, add to the appropriate TextMate keyword group,
optionally add context handling in `highlight.go`'s
`classifyIdentDefault`.

When adding a **type constructor**: update `IsConstructor()` in
`syntax/scanner.go`, add to TextMate `type-constructors` pattern,
add to `Keywords` map.

When adding an **operator**: update `RawNext()` in
`syntax/scanner.go` (add token kind and case), add to TextMate
`operators` patterns. The tokenizer picks it up automatically via
`RawNext()`.

When adding a **string escape**: update `scanString()` in
`syntax/scanner.go`, update the TextMate `strings` escape pattern
character class.

## Sync tests

`toolchain/syntax/grammar_sync_test.go` enforces sync between
the scanner and TextMate grammar for keywords, type constructors,
and escape sequences. These tests fail loudly if a keyword or type
is added to one source but not the other.
