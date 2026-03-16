//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/tobyn/doit/toolchain/codec"
	"github.com/tobyn/doit/toolchain/compiler"
	"github.com/tobyn/doit/toolchain/formatter"
	"github.com/tobyn/doit/toolchain/stdlib"
)

func main() {
	js.Global().Set("doitCompile", js.FuncOf(compile))
	js.Global().Set("doitDecode", js.FuncOf(decode))
	js.Global().Set("doitEncode", js.FuncOf(encode))
	js.Global().Set("doitFormat", js.FuncOf(format))

	// Block forever so the Go runtime stays alive.
	select {}
}

// compile(source: string, behaviorID?: string) => {result?: string, json?: string, warnings?: string[], error?: string}
func compile(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("compile requires a source argument")
	}
	src := args[0].String()
	behaviorID := ""
	if len(args) > 1 && args[1].Type() == js.TypeString {
		behaviorID = args[1].String()
	}

	obj, warnings, err := compiler.CompileString(src, stdlib.FS, behaviorID, "", nil, "")
	if err != nil {
		return errResult(err.Error())
	}
	if obj == nil {
		return jsResult(nil)
	}

	encoded, err := codec.EncodeString(obj)
	if err != nil {
		return errResult(err.Error())
	}

	jsonBytes, err := json.MarshalIndent(obj.Value, "", "    ")
	if err != nil {
		return errResult(err.Error())
	}

	result := map[string]any{
		"result": encoded,
		"json":   string(jsonBytes),
	}
	if len(warnings) > 0 {
		result["warnings"] = stringsToJS(warnings)
	}
	return js.ValueOf(result)
}

// decode(encoded: string) => {type?: string, json?: string, error?: string}
func decode(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("decode requires an encoded string argument")
	}
	obj, err := codec.DecodeString(args[0].String())
	if err != nil {
		return errResult(err.Error())
	}

	jsonBytes, err := json.MarshalIndent(obj.Value, "", "    ")
	if err != nil {
		return errResult(err.Error())
	}

	return js.ValueOf(map[string]any{
		"type": string(obj.Type),
		"json": string(jsonBytes),
	})
}

// encode(jsonStr: string, type: string) => {result?: string, error?: string}
func encode(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errResult("encode requires json and type arguments")
	}
	jsonStr := args[0].String()
	objType := args[1].String()

	if len(objType) != 1 {
		return errResult("type must be a single character ('B' or 'C')")
	}

	value, err := codec.UnmarshalJSON([]byte(jsonStr))
	if err != nil {
		return errResult(err.Error())
	}

	encoded, err := codec.EncodeString(&codec.Object{
		Type:  codec.ObjectType(objType[0]),
		Value: value,
	})
	if err != nil {
		return errResult(err.Error())
	}

	return js.ValueOf(map[string]any{
		"result": encoded,
	})
}

// format(source: string) => {result?: string, error?: string}
func format(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("format requires a source argument")
	}
	out, err := formatter.Format(args[0].String())
	if err != nil {
		return errResult(err.Error())
	}
	return js.ValueOf(map[string]any{
		"result": out,
	})
}

func errResult(msg string) any {
	return js.ValueOf(map[string]any{
		"error": msg,
	})
}

func jsResult(v any) any {
	if v == nil {
		return js.ValueOf(map[string]any{})
	}
	return js.ValueOf(v)
}

func stringsToJS(ss []string) any {
	arr := make([]any, len(ss))
	for i, s := range ss {
		arr[i] = s
	}
	return arr
}
