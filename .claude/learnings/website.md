# Website

## Status

Not yet started. VitePress project needs to be set up.

## Hosting

GitHub Pages with artifact-based deployment. WASM binary and platform
binaries are built in CI, never checked into the repo. Releases are
tag-driven; the site is rebuilt as part of the release process.

## Static Site Generator

VitePress (Node.js + Vue). Chosen for its markdown-first approach,
built-in search, native `.md` link rewriting, and easy custom
component support for the interactive playground.

## Landing Page

Two key elements:

- **Interactive compiler widget** — preloaded with a hello-world
  example, compiles via the WASM build. Precomputed output for the
  default input; WASM lazy-loaded on first user edit.
- **Download links** — detect user's OS/arch and present the right
  binary. Link to an "all downloads" page with the full matrix:
  (Windows | macOS | Linux) × (x64 | ARM64). Archives: `.zip` for
  Windows, `.tar.gz` for others. Naming: `doit-{os}-{arch}.{ext}`.

## Docs

The `manual/` markdown is the source of truth. It stays
GitHub-readable with relative `.md` links. The doc site renders the
same files with better formatting, search, and doit syntax
highlighting. A link rewrite pass strips `.md` extensions for the
site's URL scheme.

### Stdlib / API Docs (future)

Placeholder for now. Need to design:

1. A doc comment format (building on existing `#!` syntax)
2. A `doit doc` tool that reads `.doit` source and emits structured
   docs (markdown for the site, JSON for the LSP)
3. LSP integration so hover/completion uses the same doc data

The doc tool should be general-purpose — not stdlib-specific — so mod
developers and library authors can generate docs for their own code
using the same tooling.

## Aesthetic

Developer chic: readability and functionality over form. Clean
typography, sensible spacing, dark-mode friendly. Not flashy.
