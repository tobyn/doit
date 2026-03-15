package compiler_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
	doitFiles, err := filepath.Glob("tests/*.doit")
	if err != nil {
		t.Fatal(err)
	}
	if len(doitFiles) == 0 {
		t.Fatal("no test cases found in tests/*.doit")
	}

	stdlib := os.DirFS("../stdlib")

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

			// Check for directives in early comment lines.
			locale := ""
			release := false
			scanner := bufio.NewScanner(f)
			lineNum := 0
			for scanner.Scan() && lineNum < 5 {
				line := scanner.Text()
				if after, ok := strings.CutPrefix(line, "# locale: "); ok {
					locale = strings.TrimSpace(after)
				}
				if strings.TrimSpace(line) == "# release" {
					release = true
				}
				lineNum++
			}
			if _, err := f.Seek(0, 0); err != nil {
				t.Fatal(err)
			}

			// Derive sourceFS and sourcePath for import resolution and assert file paths.
			absPath, _ := filepath.Abs(doitFile)
			testDir := filepath.Dir(absPath)
			testSourceFS := os.DirFS(testDir)
			testSourcePath := filepath.Base(absPath)
			obj, _, err := compiler.Compile(f, stdlib, behaviorID, locale, testSourceFS, testSourcePath, release)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			wantBytes, err := os.ReadFile(jsonFile)
			if err != nil {
				t.Fatal(err)
			}
			wantVal, err := codec.UnmarshalJSON(wantBytes)
			if err != nil {
				t.Fatalf("UnmarshalJSON error: %v", err)
			}
			matchBehaviors(t, obj.Value.(map[string]any), wantVal.(map[string]any))
		})
	}
}

// matchBehaviors compares two behavior maps using graph isomorphism.
// Frame numbers may differ between got and want; the comparison builds a
// bijective frame-number mapping via BFS from frame "0".
func matchBehaviors(t *testing.T, got, want map[string]any) {
	t.Helper()

	// 1. Compare "name" directly.
	if got["name"] != want["name"] {
		t.Errorf("name mismatch: got %v, want %v", got["name"], want["name"])
		return
	}

	// Compare "parameters" and "pnames" if present.
	for _, key := range []string{"parameters", "pnames"} {
		gv, gok := got[key]
		wv, wok := want[key]
		if gok != wok {
			t.Errorf("%s presence mismatch: got %v, want %v", key, gok, wok)
			return
		}
		if gok && !reflect.DeepEqual(gv, wv) {
			t.Errorf("%s mismatch: got %v, want %v", key, gv, wv)
			return
		}
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

	addMapping("0", "0")

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		gFrame, gOk := got[p.g].(map[string]any)
		wFrame, wOk := want[p.w].(map[string]any)
		if !gOk && !wOk {
			// Both reference nonexistent frames (e.g., dangling "next" past
			// the last frame). This is fine — both behaviors end the same way.
			continue
		}
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

// goldenSortedKeys returns map keys sorted with numeric keys first (in numeric
// order) followed by non-numeric keys in alphabetical order. This matches the
// key ordering used in the reference test JSON files.
func goldenSortedKeys(m map[string]any) []string {
	var numKeys []string
	var strKeys []string
	for k := range m {
		if _, err := strconv.Atoi(k); err == nil {
			numKeys = append(numKeys, k)
		} else {
			strKeys = append(strKeys, k)
		}
	}
	sort.Slice(numKeys, func(i, j int) bool {
		a, _ := strconv.Atoi(numKeys[i])
		b, _ := strconv.Atoi(numKeys[j])
		return a < b
	})
	sort.Strings(strKeys)
	return append(numKeys, strKeys...)
}

// writeGoldenJSON writes a value as indented JSON with numeric-ordered keys.
func writeGoldenJSON(buf *bytes.Buffer, v any, prefix string) {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			buf.WriteString("{}")
			return
		}
		keys := goldenSortedKeys(val)
		buf.WriteString("{\n")
		newPrefix := prefix + "  "
		for i, k := range keys {
			if i > 0 {
				buf.WriteString(",\n")
			}
			buf.WriteString(newPrefix)
			_, _ = fmt.Fprintf(buf, "%q: ", k)
			writeGoldenJSON(buf, val[k], newPrefix)
		}
		buf.WriteByte('\n')
		buf.WriteString(prefix)
		buf.WriteByte('}')
	case []any:
		if len(val) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteString("[\n")
		newPrefix := prefix + "  "
		for i, elem := range val {
			if i > 0 {
				buf.WriteString(",\n")
			}
			buf.WriteString(newPrefix)
			writeGoldenJSON(buf, elem, newPrefix)
		}
		buf.WriteByte('\n')
		buf.WriteString(prefix)
		buf.WriteByte(']')
	case string:
		data, _ := json.Marshal(val)
		buf.Write(data)
	case int:
		buf.WriteString(strconv.Itoa(val))
	case float64:
		buf.WriteString(strconv.FormatFloat(val, 'f', -1, 64))
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		data, _ := json.Marshal(val)
		buf.Write(data)
	}
}

// TestUpdateGolden regenerates all test JSON files from their .doit sources.
// Gated behind DOIT_UPDATE_GOLDEN=1 to avoid accidental overwrites.
func TestUpdateGolden(t *testing.T) {
	if os.Getenv("DOIT_UPDATE_GOLDEN") != "1" {
		t.Skip("set DOIT_UPDATE_GOLDEN=1 to update golden files")
	}

	doitFiles, err := filepath.Glob("tests/*.doit")
	if err != nil {
		t.Fatal(err)
	}
	if len(doitFiles) == 0 {
		t.Fatal("no test cases found in tests/*.doit")
	}

	stdlib := os.DirFS("../stdlib")

	for _, doitFile := range doitFiles {
		name := strings.TrimSuffix(filepath.Base(doitFile), ".doit")
		jsonFile := strings.TrimSuffix(doitFile, ".doit") + ".json"

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

			// Check for a "# locale: <tag>" directive on the second line.
			locale := ""
			sc := bufio.NewScanner(f)
			lineNum := 0
			for sc.Scan() && lineNum < 2 {
				line := sc.Text()
				if after, ok := strings.CutPrefix(line, "# locale: "); ok {
					locale = strings.TrimSpace(after)
				}
				lineNum++
			}
			if _, err := f.Seek(0, 0); err != nil {
				t.Fatal(err)
			}

			// Derive sourceFS and sourcePath for import resolution.
			absPath, _ := filepath.Abs(doitFile)
			testDir := filepath.Dir(absPath)
			testSourceFS := os.DirFS(testDir)
			testSourcePath := filepath.Base(absPath)
			obj, _, err := compiler.Compile(f, stdlib, behaviorID, locale, testSourceFS, testSourcePath)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}

			var buf bytes.Buffer
			writeGoldenJSON(&buf, obj.Value, "")
			buf.WriteByte('\n')

			if err := os.WriteFile(jsonFile, buf.Bytes(), 0644); err != nil {
				t.Fatalf("WriteFile error: %v", err)
			}
		})
	}
}
