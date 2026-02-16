package codec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodec(t *testing.T) {
	encodedFiles, err := filepath.Glob("tests/*.encoded")
	if err != nil {
		t.Fatal(err)
	}
	if len(encodedFiles) == 0 {
		t.Fatal("no test cases found in tests/*.encoded")
	}

	for _, encodedFile := range encodedFiles {
		name := strings.TrimSuffix(filepath.Base(encodedFile), ".encoded")
		decodedFile := strings.TrimSuffix(encodedFile, ".encoded") + ".decoded"

		encodedBytes, err := os.ReadFile(encodedFile)
		if err != nil {
			t.Fatal(err)
		}
		encodedStr := strings.TrimSpace(string(encodedBytes))

		obj, err := Decode(encodedStr)
		if err != nil {
			t.Fatalf("%s: Decode error: %v", name, err)
		}

		decodedBytes, err := os.ReadFile(decodedFile)
		if err != nil {
			t.Fatal(err)
		}
		wantType, wantVal := parseDecodedFile(t, string(decodedBytes))

		t.Run(name+"/decode", func(t *testing.T) {
			compareDecoded(t, obj, wantType, wantVal)
		})

		t.Run(name+"/roundtrip", func(t *testing.T) {
			encoded, err := Encode(obj)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			obj2, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode(re-encoded) error: %v", err)
			}

			compareDecoded(t, obj2, wantType, wantVal)
		})
	}
}

func compareDecoded(t *testing.T, obj *Object, wantType ObjectType, wantVal any) {
	t.Helper()

	if obj.Type != wantType {
		t.Errorf("type: got %s, want %s", obj.Type, wantType)
	}

	if !reflect.DeepEqual(obj.Value, wantVal) {
		gotJSON, _ := json.MarshalIndent(obj.Value, "", "    ")
		t.Errorf("decoded value mismatch.\ngot:\n%s", string(gotJSON))
	}
}

func parseDecodedFile(t *testing.T, content string) (ObjectType, any) {
	t.Helper()
	s := strings.TrimSpace(content)
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) != 2 {
		t.Fatal("decoded file must have type line + JSON")
	}
	typ := ObjectType(lines[0][0])
	val, err := unmarshalJSON([]byte(strings.TrimSpace(lines[1])))
	if err != nil {
		t.Fatalf("unmarshalJSON error: %v", err)
	}
	return typ, val
}

func unmarshalJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return convertJSON(raw)
}

func convertJSON(v any) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		for k, v := range val {
			cv, err := convertJSON(v)
			if err != nil {
				return nil, err
			}
			val[k] = cv
		}
		return val, nil
	case []any:
		for i, v := range val {
			cv, err := convertJSON(v)
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
