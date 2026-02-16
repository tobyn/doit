# Codec Tests

This directory contains test cases for converting between blueprint/behavior
strings and their JSON representations. Each test case consists of a pair of
files that share the same name:

The `.encoded` file contains the string representation of the blueprint/behavior
as imported/exported by the game.

The `.decoded` file has a single character on the first line indicating whether
it's a blueprint (`B`) or behavior (`C`). The remaining lines in the file are
the JSON representation of the blueprint or behavior.

The testing process verifies that decoding the `.encoded` file produces a result
that matches the contents of the `.decoded` file and vice versa.

The `reference` directory contains a vendored copy of Stage Games'
[JavaScript codec implementation](https://github.com/StageGames/DesyncedJavaScriptUtils).
Its `index.html` can be opened in a browser and used as an interface for
creating the `.decoded` files in new test cases.
