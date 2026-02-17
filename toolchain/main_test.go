package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/codec"
)

func TestCompile(t *testing.T) {
	doitFiles, err := filepath.Glob("compiler/tests/*.doit")
	if err != nil {
		t.Fatal(err)
	}
	if len(doitFiles) == 0 {
		t.Fatal("no test cases found in compiler/tests/*.doit")
	}

	stdlib := os.DirFS("stdlib")

	for _, doitFile := range doitFiles {
		name := strings.TrimSuffix(filepath.Base(doitFile), ".doit")
		jsonFile := strings.TrimSuffix(doitFile, ".doit") + ".json"

		t.Run(name, func(t *testing.T) {
			f, err := os.Open(doitFile)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			encoded, err := Compile(f, stdlib)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			obj, err := codec.Decode(strings.NewReader(encoded))
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			wantBytes, err := os.ReadFile(jsonFile)
			if err != nil {
				t.Fatal(err)
			}
			wantVal, err := codec.UnmarshalJSON(wantBytes)
			if err != nil {
				t.Fatalf("UnmarshalJSON error: %v", err)
			}

			if !reflect.DeepEqual(obj.Value, wantVal) {
				gotJSON, _ := json.MarshalIndent(obj.Value, "", "    ")
				t.Errorf("value mismatch.\ngot:\n%s", string(gotJSON))
			}
		})
	}
}

func TestCodec(t *testing.T) {
	encodedFiles, err := filepath.Glob("codec/tests/*.encoded")
	if err != nil {
		t.Fatal(err)
	}
	if len(encodedFiles) == 0 {
		t.Fatal("no test cases found in codec/tests/*.encoded")
	}

	for _, encodedFile := range encodedFiles {
		name := strings.TrimSuffix(filepath.Base(encodedFile), ".encoded")
		decodedFile := strings.TrimSuffix(encodedFile, ".encoded") + ".decoded"

		f, err := os.Open(encodedFile)
		if err != nil {
			t.Fatal(err)
		}
		obj, err := codec.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s: Decode error: %v", name, err)
		}

		decodedBytes, err := os.ReadFile(decodedFile)
		if err != nil {
			t.Fatal(err)
		}
		wantType, wantVal := parseDecodedFile(t, string(decodedBytes))

		t.Run(name+"/decode", func(t *testing.T) {
			if obj.Type != wantType {
				t.Errorf("type: got %s, want %s", obj.Type, wantType)
			}
			if !reflect.DeepEqual(obj.Value, wantVal) {
				gotJSON, _ := json.MarshalIndent(obj.Value, "", "    ")
				t.Errorf("value mismatch.\ngot:\n%s", string(gotJSON))
			}
		})

		t.Run(name+"/roundtrip", func(t *testing.T) {
			encoded, err := codec.EncodeString(obj)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			obj2, err := codec.Decode(strings.NewReader(encoded))
			if err != nil {
				t.Fatalf("Decode(re-encoded) error: %v", err)
			}

			if obj2.Type != wantType {
				t.Errorf("type: got %s, want %s", obj2.Type, wantType)
			}
			if !reflect.DeepEqual(obj2.Value, wantVal) {
				gotJSON, _ := json.MarshalIndent(obj2.Value, "", "    ")
				t.Errorf("value mismatch.\ngot:\n%s", string(gotJSON))
			}
		})
	}
}

func parseDecodedFile(t *testing.T, content string) (codec.ObjectType, any) {
	t.Helper()
	s := strings.TrimSpace(content)
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) != 2 {
		t.Fatal("decoded file must have type line + JSON")
	}
	typ := codec.ObjectType(lines[0][0])
	val, err := codec.UnmarshalJSON([]byte(strings.TrimSpace(lines[1])))
	if err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	return typ, val
}
