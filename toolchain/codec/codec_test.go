package codec_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/codec"
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

func TestDecodeErrors(t *testing.T) {
	t.Run("empty_input", func(t *testing.T) {
		_, err := codec.DecodeString("")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too short") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("too_short", func(t *testing.T) {
		_, err := codec.DecodeString("DS")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "too short") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing_prefix", func(t *testing.T) {
		_, err := codec.DecodeString("XXCabcde")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "does not begin with") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid_base62_character", func(t *testing.T) {
		_, err := codec.DecodeString("DSC!!!!!!!!!")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("bad_checksum", func(t *testing.T) {
		// Take a valid-looking string and corrupt the checksum (last char)
		// The format is DS + type + base62_u32(decompressLen) + base62_data + checksum_char
		// Construct a minimal one with a wrong checksum
		_, err := codec.DecodeString("DSC0a0b0c0d0")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("corrupted_zlib", func(t *testing.T) {
		// A string that claims to have compressed data but the data is garbage.
		// The decompressLen > 0 triggers zlib decompression.
		// "DSC" + base62 for a non-zero decompressLen + some base62 data
		// We'll use a known-encoded string and corrupt the data portion.
		_, err := codec.DecodeString("DSCz00000000000z")
		if err == nil {
			t.Fatal("expected error")
		}
	})
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
