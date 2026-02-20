package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/codec"
	"github.com/tobyn/doit/toolchain/compiler"
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

		// If the name contains "__", the part after it is the behavior ID.
		behaviorID := ""
		if idx := strings.Index(name, "__"); idx >= 0 {
			behaviorID = name[idx+2:]
		}

		t.Run(name, func(t *testing.T) {
			f, err := os.Open(doitFile)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			encoded, err := Compile(f, stdlib, behaviorID, "")
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
			// Convert reference implementation output (0-based keys) to our
			// native format (1-based keys).
			wantVal = refToNative(wantVal)

			matchBehaviors(t, obj.Value.(map[string]any), wantVal.(map[string]any))
		})
	}
}

func TestCompileErrors(t *testing.T) {
	stdlib := os.DirFS("stdlib")

	t.Run("multiple_behaviors_no_selection", func(t *testing.T) {
		src := `behavior a { @name "A" } behavior b { @name "B" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "multiple behaviors") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("behavior_not_found", func(t *testing.T) {
		src := `behavior a { @name "A" } behavior b { @name "B" }`
		_, err := compiler.CompileString(src, stdlib, "c", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate_name", func(t *testing.T) {
		src := `behavior a { @name "A" @name "B" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "duplicate @name") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no_behaviors", func(t *testing.T) {
		src := `fn greet() { notify "hi" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no behavior") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("localized_name_with_locale", func(t *testing.T) {
		src := `behavior a { @name { en_US "English" ja "日本語" } notify "hi" }`
		obj, err := compiler.CompileString(src, stdlib, "", "ja")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		name := obj.Value.(map[string]any)["name"]
		if name != "日本語" {
			t.Fatalf("expected name %q, got %q", "日本語", name)
		}
	})

	t.Run("localized_name_no_match", func(t *testing.T) {
		src := `behavior a { @name { en_US "English" ja "日本語" } notify "hi" }`
		obj, err := compiler.CompileString(src, stdlib, "", "fr")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		name := obj.Value.(map[string]any)["name"]
		if name != "English" {
			t.Fatalf("expected name %q, got %q", "English", name)
		}
	})

	t.Run("localized_name_empty_block", func(t *testing.T) {
		src := `behavior a { @name {} notify "hi" }`
		_, err := compiler.CompileString(src, stdlib, "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("doc_comment_inherit", func(t *testing.T) {
		src := "fn greet() { notify \"Hello\" }\nbehavior a {\n#! Greeting\ngreet\n}"
		obj, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if frame["cmt"] != "Greeting" {
			t.Fatalf("expected cmt %q, got %v", "Greeting", frame["cmt"])
		}
	})

	t.Run("doc_comment_override", func(t *testing.T) {
		src := "fn greet() {\n#! Inner\nnotify \"Hello\"\nnotify \"World\"\n}\nbehavior a {\n#! Outer\ngreet\n}"
		obj, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v := obj.Value.(map[string]any)
		f1 := v["1"].(map[string]any)
		f2 := v["2"].(map[string]any)
		if f1["cmt"] != "Inner" {
			t.Fatalf("frame 1: expected cmt %q, got %v", "Inner", f1["cmt"])
		}
		if f2["cmt"] != "Outer" {
			t.Fatalf("frame 2: expected cmt %q, got %v", "Outer", f2["cmt"])
		}
	})

	t.Run("no_doc_comment", func(t *testing.T) {
		src := `behavior a { notify "Hello" }`
		obj, err := compiler.CompileString(src, stdlib, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		frame := obj.Value.(map[string]any)["1"].(map[string]any)
		if _, exists := frame["cmt"]; exists {
			t.Fatalf("expected no cmt field, got %v", frame["cmt"])
		}
	})
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
		// Convert reference implementation output (0-based keys) to our
		// native format (1-based keys).
		wantVal = refToNative(wantVal)

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

// refToNative converts reference implementation decoded output to match our
// codec's native format. The reference JS codec stores Lua integer table keys
// as 0-based strings (k - 1); our codec preserves Lua's native 1-based
// numbering. This function shifts all numeric string keys in maps by +1.
func refToNative(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, child := range val {
			newKey := k
			if n, err := strconv.Atoi(k); err == nil {
				newKey = strconv.Itoa(n + 1)
			}
			result[newKey] = refToNative(child)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = refToNative(elem)
		}
		return result
	default:
		return v
	}
}

// matchBehaviors compares two behavior maps using graph isomorphism.
// Frame numbers may differ between got and want; the comparison builds a
// bijective frame-number mapping via BFS from frame "1".
func matchBehaviors(t *testing.T, got, want map[string]any) {
	t.Helper()

	// 1. Compare "name" directly.
	if got["name"] != want["name"] {
		t.Errorf("name mismatch: got %v, want %v", got["name"], want["name"])
		return
	}

	// 2. Count numeric-string keys (frames). Must be equal.
	gotFrames := numericKeys(got)
	wantFrames := numericKeys(want)
	if len(gotFrames) != len(wantFrames) {
		gotJSON, _ := json.MarshalIndent(got, "", "    ")
		t.Errorf("frame count mismatch: got %d, want %d\ngot:\n%s",
			len(gotFrames), len(wantFrames), gotJSON)
		return
	}

	if len(gotFrames) == 0 {
		return
	}

	// 3. BFS from ("1", "1").
	g2w := map[string]string{} // got frame key → want frame key
	w2g := map[string]string{} // want frame key → got frame key

	type framePair struct{ g, w string }
	var queue []framePair
	failed := false

	addMapping := func(g, w string) {
		if ew, ok := g2w[g]; ok {
			if ew != w {
				t.Errorf("mapping conflict: got frame %s already mapped to want frame %s, cannot map to %s", g, ew, w)
				failed = true
			}
			return
		}
		if eg, ok := w2g[w]; ok {
			if eg != g {
				t.Errorf("mapping conflict: want frame %s already mapped to got frame %s, cannot map from %s", w, eg, g)
				failed = true
			}
			return
		}
		g2w[g] = w
		w2g[w] = g
		queue = append(queue, framePair{g, w})
	}

	addMapping("1", "1")

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		gFrame, gOk := got[p.g].(map[string]any)
		wFrame, wOk := want[p.w].(map[string]any)
		if !gOk || !wOk {
			t.Errorf("frames got:%s / want:%s: expected maps", p.g, p.w)
			failed = true
			continue
		}

		// Verify same set of keys.
		if !sameKeySet(gFrame, wFrame) {
			t.Errorf("frames got:%s / want:%s: key sets differ: got %v, want %v",
				p.g, p.w, sortedKeys(gFrame), sortedKeys(wFrame))
			failed = true
			continue
		}

		// Compare "op".
		if gFrame["op"] != wFrame["op"] {
			t.Errorf("frames got:%s / want:%s: op mismatch: got %v, want %v",
				p.g, p.w, gFrame["op"], wFrame["op"])
			failed = true
		}

		// Handle "next" field.
		gNext, gHas := gFrame["next"]
		wNext, wHas := wFrame["next"]

		if !gHas && !wHas {
			// Implicit sequential: enqueue (gKey+1, wKey+1) if next frames exist.
			gn := strconv.Itoa(mustAtoi(p.g) + 1)
			wn := strconv.Itoa(mustAtoi(p.w) + 1)
			_, gnExists := got[gn].(map[string]any)
			_, wnExists := want[wn].(map[string]any)
			if gnExists && wnExists {
				addMapping(gn, wn)
			} else if gnExists != wnExists {
				t.Errorf("frames got:%s / want:%s: implicit next existence mismatch", p.g, p.w)
				failed = true
			}
		} else if gHas && wHas {
			switch gv := gNext.(type) {
			case bool:
				if wv, ok := wNext.(bool); !ok || gv != wv {
					t.Errorf("frames got:%s / want:%s: next mismatch: got %v, want %v",
						p.g, p.w, gNext, wNext)
					failed = true
				}
			case int:
				if wv, ok := wNext.(int); ok {
					addMapping(strconv.Itoa(gv), strconv.Itoa(wv))
				} else {
					t.Errorf("frames got:%s / want:%s: next type mismatch: got int, want %T",
						p.g, p.w, wNext)
					failed = true
				}
			default:
				t.Errorf("frames got:%s / want:%s: unexpected next type %T",
					p.g, p.w, gNext)
				failed = true
			}
		} else {
			t.Errorf("frames got:%s / want:%s: next field presence mismatch", p.g, p.w)
			failed = true
		}

		// Compare remaining fields.
		for k, gVal := range gFrame {
			if k == "op" || k == "next" {
				continue
			}
			wVal := wFrame[k]

			switch gv := gVal.(type) {
			case string:
				if wv, ok := wVal.(string); !ok || gv != wv {
					t.Errorf("frames got:%s / want:%s field %q: got %q, want %v",
						p.g, p.w, k, gv, wVal)
					failed = true
				}
			case bool:
				if wv, ok := wVal.(bool); !ok || gv != wv {
					t.Errorf("frames got:%s / want:%s field %q: got %v, want %v",
						p.g, p.w, k, gv, wVal)
					failed = true
				}
			case map[string]any:
				if !reflect.DeepEqual(gVal, wVal) {
					t.Errorf("frames got:%s / want:%s field %q: got %v, want %v",
						p.g, p.w, k, gVal, wVal)
					failed = true
				}
			case int:
				wv, ok := wVal.(int)
				if !ok {
					t.Errorf("frames got:%s / want:%s field %q: type mismatch: got int, want %T",
						p.g, p.w, k, wVal)
					failed = true
				} else if gv != wv {
					// Unequal integers → treat as frame references.
					addMapping(strconv.Itoa(gv), strconv.Itoa(wv))
				}
				// Equal integers → data values, OK.
			default:
				if !reflect.DeepEqual(gVal, wVal) {
					t.Errorf("frames got:%s / want:%s field %q: got %v, want %v",
						p.g, p.w, k, gVal, wVal)
					failed = true
				}
			}
		}
	}

	// 4. Check for orphaned frames not reached by BFS.
	var unmappedGot, unmappedWant []string
	for _, k := range gotFrames {
		if _, ok := g2w[k]; !ok {
			unmappedGot = append(unmappedGot, k)
		}
	}
	for _, k := range wantFrames {
		if _, ok := w2g[k]; !ok {
			unmappedWant = append(unmappedWant, k)
		}
	}

	// Match orphans by content: same op + same non-integer fields.
	for _, gk := range unmappedGot {
		gFrame := got[gk].(map[string]any)
		matched := false
		for i, wk := range unmappedWant {
			wFrame := want[wk].(map[string]any)
			if frameContentMatches(gFrame, wFrame, g2w) {
				g2w[gk] = wk
				w2g[wk] = gk
				unmappedWant = append(unmappedWant[:i], unmappedWant[i+1:]...)
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("orphaned got frame %s: no matching want frame", gk)
			failed = true
		}
	}
	if len(unmappedWant) > 0 {
		t.Errorf("orphaned want frames with no match: %v", unmappedWant)
		failed = true
	}

	if failed {
		gotJSON, _ := json.MarshalIndent(got, "", "    ")
		t.Errorf("full got:\n%s", gotJSON)
	}
}

// numericKeys returns the keys of m that are valid integer strings, sorted numerically.
func numericKeys(m map[string]any) []string {
	var keys []string
	for k := range m {
		if _, err := strconv.Atoi(k); err == nil {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	return keys
}

func sameKeySet(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// frameContentMatches checks if two frames have the same op and identical
// non-integer fields. For integer fields, it checks consistency with the
// existing mapping.
func frameContentMatches(gFrame, wFrame map[string]any, g2w map[string]string) bool {
	if !sameKeySet(gFrame, wFrame) {
		return false
	}
	if gFrame["op"] != wFrame["op"] {
		return false
	}
	for k, gVal := range gFrame {
		if k == "op" {
			continue
		}
		wVal := wFrame[k]
		switch gv := gVal.(type) {
		case int:
			wv, ok := wVal.(int)
			if !ok {
				return false
			}
			if gv == wv {
				continue
			}
			// Check mapping consistency.
			gs := strconv.Itoa(gv)
			ws := strconv.Itoa(wv)
			if ew, ok := g2w[gs]; ok && ew != ws {
				return false
			}
		default:
			if !reflect.DeepEqual(gVal, wVal) {
				return false
			}
		}
	}
	return true
}
