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

## Architecture

- **`toolchain/`** — The doit language's Go-based toolchain implementation
