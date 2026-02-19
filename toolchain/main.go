package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/tobyn/doit/toolchain/codec"
	"github.com/tobyn/doit/toolchain/compiler"
)

//go:embed usage/*.txt
var usageFS embed.FS

//go:embed stdlib
var stdlibFS embed.FS

func main() {
	if len(os.Args) < 2 {
		_, _ = fmt.Fprintf(os.Stderr, "usage: doit <command> [arguments]\n")
		_, _ = fmt.Fprintf(os.Stderr, "Run 'doit help' for a list of commands.\n")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "compile":
		err = cmdCompile(os.Args[2:])
	case "decode":
		err = cmdDecode(os.Args[2:])
	case "encode":
		err = cmdEncode(os.Args[2:])
	case "help":
		err = cmdHelp(os.Args[2:])
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		_, _ = fmt.Fprintf(os.Stderr, "Run 'doit help' for a list of commands.\n")
		os.Exit(1)
	}

	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// Compile compiles doit source from r using the given stdlib and returns the
// encoded string.
func Compile(r io.Reader, stdlib fs.FS, behaviorID string) (string, error) {
	obj, err := compiler.Compile(r, stdlib, behaviorID)
	if err != nil {
		return "", err
	}
	if obj == nil {
		return "", nil
	}
	return codec.EncodeString(obj)
}

func cmdCompile(args []string) (err error) {
	flags := flag.NewFlagSet("compile", flag.ContinueOnError)
	behaviorID := flags.String("b", "", "behavior ID to compile")
	outputPath := flags.String("o", "", "output file path")
	stdlibPath := flags.String("stdlib", "", "override stdlib path")
	jsonFlag := flags.Bool("j", false, "output JSON instead of Base62")
	jsonLongFlag := flags.Bool("json", false, "output JSON instead of Base62")
	if err := flags.Parse(args); err != nil {
		return err
	}

	r, err := openInput(flags)
	if err != nil {
		return
	}
	defer func() {
		closeErr := r.Close()
		if err == nil {
			err = closeErr
		}
	}()

	var stdlib fs.FS
	if *stdlibPath != "" {
		stdlib = os.DirFS(*stdlibPath)
	} else {
		stdlib, _ = fs.Sub(stdlibFS, "stdlib")
	}

	if *jsonFlag || *jsonLongFlag {
		obj, compileErr := compiler.Compile(r, stdlib, *behaviorID)
		if compileErr != nil {
			err = compileErr
			return
		}
		if obj == nil {
			return
		}
		err = writeJSON(obj.Value, *outputPath)
		return
	}

	encoded, compileErr := Compile(r, stdlib, *behaviorID)
	if compileErr != nil {
		err = compileErr
		return
	}
	if encoded == "" {
		return
	}
	encoded += "\n"

	if *outputPath == "" {
		_, err = io.WriteString(os.Stdout, encoded)
		return
	}
	err = os.WriteFile(*outputPath, []byte(encoded), 0o644)
	return
}

func cmdDecode(args []string) (err error) {
	flags := flag.NewFlagSet("decode", flag.ContinueOnError)
	outputPath := flags.String("o", "", "output file path")
	flagB := flags.Bool("b", false, "require blueprint")
	flagC := flags.Bool("c", false, "require behavior")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *flagB && *flagC {
		return fmt.Errorf("-b and -c are mutually exclusive")
	}

	r, err := openInput(flags)
	if err != nil {
		return
	}
	defer func() {
		closeErr := r.Close()
		if err == nil {
			err = closeErr
		}
	}()

	obj, err := codec.Decode(r)
	if err != nil {
		return
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
			"type":  string(obj.Type),
			"value": obj.Value,
		}
	}

	err = writeJSON(output, *outputPath)
	return
}

func cmdEncode(args []string) error {
	flags := flag.NewFlagSet("encode", flag.ContinueOnError)
	outputPath := flags.String("o", "", "output file path")
	flagB := flags.Bool("b", false, "input is a blueprint value")
	flagC := flags.Bool("c", false, "input is a behavior value")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *flagB && *flagC {
		return fmt.Errorf("-b and -c are mutually exclusive")
	}

	input, err := readInput(flags)
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
		value, err = codec.UnmarshalJSON(input)
		if err != nil {
			return fmt.Errorf("parsing JSON: %w", err)
		}
	} else {
		raw, err := codec.UnmarshalJSON(input)
		if err != nil {
			return fmt.Errorf("parsing JSON: %w", err)
		}
		envelope, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("expected JSON object")
		}
		typ, ok := envelope["type"].(string)
		if !ok || len(typ) != 1 {
			return fmt.Errorf("invalid type: %v", envelope["type"])
		}
		objType = codec.ObjectType(typ[0])
		value = envelope["value"]
	}

	encoded, err := codec.EncodeString(&codec.Object{Type: objType, Value: value})
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

func cmdHelp(args []string) error {
	if len(args) == 0 {
		return printUsageSummary()
	}
	content, err := usageFS.ReadFile("usage/" + args[0] + ".txt")
	if err != nil {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	_, err = os.Stdout.Write(content)
	return err
}

func printUsageSummary() error {
	entries, err := usageFS.ReadDir("usage")
	if err != nil {
		return err
	}
	fmt.Println("The doit toolchain. Available commands:")
	fmt.Println()
	for _, e := range entries {
		content, err := usageFS.ReadFile("usage/" + e.Name())
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(e.Name(), ".txt")
		summary, _, _ := strings.Cut(string(content), "\n")
		fmt.Printf("  %-10s %s\n", name, summary)
	}
	fmt.Println()
	fmt.Println("Run 'doit help <command>' for detailed usage.")
	return nil
}

func openInput(flags *flag.FlagSet) (io.ReadCloser, error) {
	if flags.NArg() == 0 {
		return os.Stdin, nil
	}
	return os.Open(flags.Arg(0))
}

func readInput(flags *flag.FlagSet) ([]byte, error) {
	if flags.NArg() == 0 {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(flags.Arg(0))
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
