import fs from "fs";
import { fileURLToPath } from "url";
import path from "path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Load Go's WASM glue.
const execPath = path.join(__dirname, "..", "wasm_exec.js");
await import("file://" + execPath.replace(/\\/g, "/"));

const go = new Go();
const wasmPath = path.join(__dirname, "..", "doit.wasm");
const wasmBuffer = fs.readFileSync(wasmPath);
const { instance } = await WebAssembly.instantiate(wasmBuffer, go.importObject);

// Run Go main (registers global functions, then blocks).
go.run(instance);

// Give Go a tick to set up.
await new Promise((r) => setTimeout(r, 100));

// Test compile.
const src = `behavior Test {
    notify "Hello, WASM!"
}`;
console.log("=== Compile ===");
const compiled = doitCompile(src);
console.log(JSON.stringify(compiled, null, 2));

// Test decode the compiled result.
if (compiled.result) {
    console.log("\n=== Decode ===");
    const decoded = doitDecode(compiled.result);
    console.log(JSON.stringify(decoded, null, 2));
}

// Test format.
console.log("\n=== Format ===");
const ugly = `behavior   Ugly{notify   "hi"  ;  notify  "bye"}`;
const formatted = doitFormat(ugly);
console.log(JSON.stringify(formatted, null, 2));

// Test compile error.
console.log("\n=== Compile Error ===");
const bad = doitCompile("behavior X { invalid_stuff }");
console.log(JSON.stringify(bad, null, 2));

process.exit(0);
