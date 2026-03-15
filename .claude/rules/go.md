---
paths:
  - "**/*.go"
---

# Go Code Style

## Formatting

All Go source files must be `gofmt`-clean. Run `gofmt -l .` from
`toolchain/` to check. If any files are listed, run `gofmt -w` on
them before considering the change done.

## Error Handling

All errors must be handled, even if the handling is to explicitly discard them. Never leave an error
implicitly unhandled — use `_ =` to discard intentionally.

- **`fmt.Fprintf`** and similar write functions: discard the error explicitly
  (e.g., `_, _ = fmt.Fprintf(...)`).
- **`Close()` on writable files**: do not use `defer f.Close()` for files opened for writing.
  Instead, use named return values and close explicitly so that the close error is captured. See
  <https://www.joeshaw.org/dont-defer-close-on-writable-files/> for strategies.
