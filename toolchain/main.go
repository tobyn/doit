package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: doit <command> [arguments]\n")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "decode":
		err = cmdDecode(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdDecode(args []string) error {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	outputPath := fs.String("o", "", "output file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var input []byte
	if fs.NArg() == 0 {
		var err error
		input, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	} else {
		var err error
		input, err = os.ReadFile(fs.Arg(0))
		if err != nil {
			return err
		}
	}

	obj, err := codec.Decode(strings.TrimSpace(string(input)))
	if err != nil {
		return err
	}

	result := map[string]any{
		"type":  string(byte(obj.Type)),
		"value": obj.Value,
	}
	jsonBytes, err := json.MarshalIndent(result, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	jsonBytes = append(jsonBytes, '\n')

	if *outputPath == "" {
		_, err = os.Stdout.Write(jsonBytes)
		return err
	}
	return os.WriteFile(*outputPath, jsonBytes, 0o644)
}
