#!/usr/bin/env node
//
// CLI wrapper around the vendored reference JavaScript codec from
// Desynced's developers (codec/tests/reference/dsconvert.js).
//
// Usage:
//   node codec/refcodec.js decode [file]   — decode a DS-prefixed Base62 string to JSON
//   node codec/refcodec.js encode [file]   — encode JSON back to a DS-prefixed Base62 string
//
// Reads from the given file, or from stdin if no file is provided.
// The decode output includes the object type prefix on the first line
// (matching the .decoded test file format).

"use strict";

const fs = require("fs");
const path = require("path");
const vm = require("vm");

// Load the reference codec into a sandbox that provides the globals it
// needs (require, WebAssembly, process, TextDecoder, TextEncoder, etc.).
// The file defines DesyncedStringToObject and ObjectToDesyncedString as
// global functions in the sandbox.
const codecPath = path.join(__dirname, "tests", "reference", "dsconvert.js");
const sandbox = vm.createContext({
    require,
    WebAssembly,
    process,
    TextDecoder,
    TextEncoder,
    Uint8Array,
    Uint16Array,
    Uint32Array,
    DataView,
    Array,
    Object,
    Number,
    String,
    Error,
    Math,
    console,
});
vm.runInContext(fs.readFileSync(codecPath, "utf8"), sandbox, { filename: codecPath });
const { DesyncedStringToObject, ObjectToDesyncedString } = sandbox;

// JSON serializer that matches the reference codec's output conventions:
// - undefined values become JSON null (filtered to omit the key)
// - objects with only numeric keys and "length" are arrays
function serialize(val) {
    return JSON.stringify(val, (_, v) => v === undefined ? null : v, 4);
}

function readInput(fileArg) {
    if (fileArg) {
        return fs.readFileSync(fileArg, "utf8").trim();
    }
    return fs.readFileSync(0, "utf8").trim();
}

const [cmd, fileArg] = process.argv.slice(2);

if (cmd === "decode") {
    const input = readInput(fileArg);
    const info = {};
    const obj = DesyncedStringToObject(input, info);
    // Output in .decoded format: type char on first line, then JSON.
    process.stdout.write(info.type + "\n" + serialize(obj) + "\n");
} else if (cmd === "encode") {
    const input = readInput(fileArg);
    // Parse .decoded format: first line is the type char, rest is JSON.
    const newline = input.indexOf("\n");
    let type, obj;
    if (newline !== -1 && newline <= 2) {
        type = input.slice(0, newline).trim();
        obj = JSON.parse(input.slice(newline + 1));
    } else {
        // Plain JSON without type prefix — default to behavior type 'C'.
        type = "C";
        obj = JSON.parse(input);
    }
    process.stdout.write(ObjectToDesyncedString(obj, type) + "\n");
} else {
    process.stderr.write(
        "Usage: node codec/refcodec.js decode [file]\n" +
        "       node codec/refcodec.js encode [file]\n"
    );
    process.exit(1);
}
