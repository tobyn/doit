# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Project Overview

This project defines and implements a programming language called doit ("do it") that targets
[Desynced](https://www.desyncedgame.com/)'s behavior controllers

## Generating Behaviors

When asked to generate a behavior, prefer writing doit source code that
compiles into the behavior over generating the JSON directly. Dog-fooding
the language helps guide its evolution. If something isn't supported by the
language or doesn't seem to work correctly, ask the developer for help or
permission to fall back to raw JSON.

## Project Memory

The `.claude/rules/` files are the source of truth for how the codebase
works. After implementing a feature or making a design change, update the
relevant rules files to reflect the new state. This keeps future sessions
accurate — they rely on these files rather than re-exploring the codebase.

During long sessions, write important design decisions and intermediate
conclusions to rules files promptly rather than waiting until the end.
Context from earlier in the conversation may be compressed, so anything
worth remembering should be persisted to files before it's needed later.

## Architecture

- **`toolchain/`** — The doit language's Go-based toolchain implementation
