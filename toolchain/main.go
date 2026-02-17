package main

import (
	"bytes"
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
	case "encode":
		err = cmdEncode(os.Args[2:])
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
	flagB := fs.Bool("b", false, "require blueprint")
	flagC := fs.Bool("c", false, "require behavior")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *flagB && *flagC {
		return fmt.Errorf("-b and -c are mutually exclusive")
	}

	input, err := readInput(fs)
	if err != nil {
		return err
	}

	obj, err := codec.Decode(strings.TrimSpace(string(input)))
	if err != nil {
		return err
	}

	if *flagB && obj.Type != codec.Blueprint {
		return fmt.Errorf("expected Blueprint, got %s", obj.Type)
	}
	if *flagC && obj.Type != codec.Behavior {
		return fmt.Errorf("expected Behavior, got %s", obj.Type)
	}

	var output any
	if *flagB || *flagC {
		output = obj.Value
	} else {
		output = map[string]any{
			"type":  string(byte(obj.Type)),
			"value": obj.Value,
		}
	}

	return writeJSON(output, *outputPath)
}

func cmdEncode(args []string) error {
	fs := flag.NewFlagSet("encode", flag.ContinueOnError)
	outputPath := fs.String("o", "", "output file path")
	flagB := fs.Bool("b", false, "input is a blueprint value")
	flagC := fs.Bool("c", false, "input is a behavior value")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *flagB && *flagC {
		return fmt.Errorf("-b and -c are mutually exclusive")
	}

	input, err := readInput(fs)
	if err != nil {
		return err
	}

	var objType codec.ObjectType
	var value any

	if *flagB || *flagC {
		if *flagB {
			objType = codec.Blueprint
		} else {
			objType = codec.Behavior
		}
		value, err = decodeJSONValue(input)
		if err != nil {
			return err
		}
	} else {
		var envelope struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		}
		dec := json.NewDecoder(bytes.NewReader(input))
		dec.UseNumber()
		if err := dec.Decode(&envelope); err != nil {
			return fmt.Errorf("parsing JSON: %w", err)
		}
		if len(envelope.Type) != 1 {
			return fmt.Errorf("invalid type: %q", envelope.Type)
		}
		objType = codec.ObjectType(envelope.Type[0])
		value, err = convertJSONNumbers(envelope.Value)
		if err != nil {
			return err
		}
	}

	encoded, err := codec.Encode(&codec.Object{Type: objType, Value: value})
	if err != nil {
		return err
	}
	encoded += "\n"

	if *outputPath == "" {
		_, err = io.WriteString(os.Stdout, encoded)
		return err
	}
	return os.WriteFile(*outputPath, []byte(encoded), 0o644)
}

func readInput(fs *flag.FlagSet) ([]byte, error) {
	if fs.NArg() == 0 {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(fs.Arg(0))
}

func writeJSON(v any, outputPath string) error {
	jsonBytes, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	jsonBytes = append(jsonBytes, '\n')

	if outputPath == "" {
		_, err = os.Stdout.Write(jsonBytes)
		return err
	}
	return os.WriteFile(outputPath, jsonBytes, 0o644)
}

func decodeJSONValue(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	return convertJSONNumbers(raw)
}

func convertJSONNumbers(v any) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		for k, v := range val {
			cv, err := convertJSONNumbers(v)
			if err != nil {
				return nil, err
			}
			val[k] = cv
		}
		return val, nil
	case []any:
		for i, v := range val {
			cv, err := convertJSONNumbers(v)
			if err != nil {
				return nil, err
			}
			val[i] = cv
		}
		return val, nil
	case json.Number:
		if n, err := val.Int64(); err == nil {
			return int(n), nil
		}
		f, err := val.Float64()
		if err != nil {
			return nil, fmt.Errorf("invalid number %v", val)
		}
		return f, nil
	default:
		return val, nil
	}
}
