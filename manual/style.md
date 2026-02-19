# Project Style

Claude must ensure any changes it makes comply with these rules. If it notices deviations in files
it hasn't changed, it should offer to bring them into compliance. Any exceptions to the rules should
be documented here.

## Go Source Code

Go code must pass `go vet` and be formatted with `go fmt`.

## Markdown and Plain Text Files

Files meant to be printed to a terminal (e.g., `toolchain/usage/*.txt`) should be line-wrapped at
80 characters, unless wrapping would make the output less comprehensible. For example, a long
command synopsis should stay on one line rather than being broken up with shell escape characters.

All other files should be line-wrapped at 100 characters, with the same clarity exception.

## Vendored Files

Vendored files (e.g., `toolchain/codec/tests/reference/**/*`) are exempt from project style rules
and must not be modified for style compliance.
