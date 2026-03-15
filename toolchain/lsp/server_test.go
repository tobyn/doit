package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tobyn/doit/toolchain/syntax"
)

func TestInitializeResponse(t *testing.T) {
	resp := sendRequest(t, "initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	caps, ok := resp["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("missing capabilities in response")
	}
	semTokens, ok := caps["semanticTokensProvider"].(map[string]any)
	if !ok {
		t.Fatal("missing semanticTokensProvider")
	}
	legend, ok := semTokens["legend"].(map[string]any)
	if !ok {
		t.Fatal("missing legend")
	}
	types, ok := legend["tokenTypes"].([]any)
	if !ok || len(types) == 0 {
		t.Error("missing or empty tokenTypes")
	}
	mods, ok := legend["tokenModifiers"].([]any)
	if !ok || len(mods) == 0 {
		t.Error("missing or empty tokenModifiers")
	}
}

func TestSemanticTokensFull(t *testing.T) {
	src := `let x = 5`
	resp := roundtrip(t, src)

	data, ok := resp["data"].([]any)
	if !ok {
		t.Fatal("missing data in response")
	}
	// Should have tokens: let, x, =, 5 → 4 tokens × 5 values = 20
	if len(data) != 20 {
		t.Fatalf("expected 20 data values, got %d: %v", len(data), data)
	}

	// First token: "let" at line 0, col 0, length 3, type=keyword
	assertData(t, data, 0, 0, 0, 3, int(syntax.TokenKeyword), 0)
	// Second token: "x" at line 0, col 4, length 1, type=variable
	assertData(t, data, 1, 0, 4, 1, int(syntax.TokenVariable), int(syntax.ModDeclaration|syntax.ModReadonly))
}

func TestSemanticTokensMultiLine(t *testing.T) {
	src := "let x = 5\nlet y = 10"
	resp := roundtrip(t, src)

	data, ok := resp["data"].([]any)
	if !ok {
		t.Fatal("missing data in response")
	}

	// line 0: let(3), x(1), =(1), 5(1) — 4 tokens
	// line 1: let(3), y(1), =(1), 10(2) — 4 tokens
	// 8 tokens × 5 = 40
	if len(data) != 40 {
		t.Fatalf("expected 40 data values, got %d", len(data))
	}

	// Token 4 (second "let"): deltaLine=1, deltaCol=0
	assertData(t, data, 4, 1, 0, 3, int(syntax.TokenKeyword), 0)
}

func TestDocumentLifecycle(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"

	// didOpen
	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": "let x = 1"},
	})
	// semanticTokens/full
	writeRequest(&input, 1, "textDocument/semanticTokens/full", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	// didChange
	writeNotification(&input, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "var y = 2"}},
	})
	// semanticTokens/full again (should reflect change)
	writeRequest(&input, 2, "textDocument/semanticTokens/full", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	// didClose
	writeNotification(&input, "textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	// semanticTokens/full after close (should return empty)
	writeRequest(&input, 3, "textDocument/semanticTokens/full", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	// exit
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseResponses(t, output.Bytes())
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}

	// First response: tokens for "let x = 1"
	data1, _ := responses[0]["data"].([]any)
	if len(data1) != 20 { // 4 tokens
		t.Errorf("response 1: expected 20 data values, got %d", len(data1))
	}

	// Second response: tokens for "var y = 2" (changed)
	data2, _ := responses[1]["data"].([]any)
	if len(data2) != 20 { // 4 tokens
		t.Errorf("response 2: expected 20 data values, got %d", len(data2))
	}
	// "var" keyword should have the same type as "let" (TokenKeyword)
	assertData(t, data2, 0, 0, 0, 3, int(syntax.TokenKeyword), 0)

	// Third response: empty after close
	data3, _ := responses[2]["data"].([]any)
	if len(data3) != 0 {
		t.Errorf("response 3: expected 0 data values, got %d", len(data3))
	}
}

func TestFormattingCapabilityAdvertised(t *testing.T) {
	resp := sendRequest(t, "initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	caps, ok := resp["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("missing capabilities")
	}
	fmtProvider, ok := caps["documentFormattingProvider"]
	if !ok {
		t.Fatal("documentFormattingProvider not advertised")
	}
	if fmtProvider != true {
		t.Errorf("documentFormattingProvider = %v, want true", fmtProvider)
	}
}

func TestFormattingReturnsEdits(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"
	src := "behavior foo {\nexit\n}\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeRequest(&input, 1, "textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"options":      map[string]any{"tabSize": 4, "insertSpaces": true},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// Parse raw response (formatting returns an array, not a map).
	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var edits []map[string]any
	if err := json.Unmarshal(responses[0], &edits); err != nil {
		t.Fatalf("unmarshal edits: %v (raw: %s)", err, responses[0])
	}
	if len(edits) == 0 {
		t.Fatal("expected formatting edits, got empty array")
	}

	newText, _ := edits[0]["newText"].(string)
	want := "behavior foo {\n    exit\n}\n"
	if newText != want {
		t.Errorf("formatted text =\n%q\nwant\n%q", newText, want)
	}
}

func TestFormattingNoChanges(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"
	src := "behavior foo {\n    exit\n}\n" // already formatted

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeRequest(&input, 1, "textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"options":      map[string]any{"tabSize": 4, "insertSpaces": true},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var edits []map[string]any
	if err := json.Unmarshal(responses[0], &edits); err != nil {
		t.Fatalf("unmarshal edits: %v", err)
	}
	if len(edits) != 0 {
		t.Errorf("expected no edits for already-formatted file, got %d", len(edits))
	}
}

func TestOnTypeFormattingNewline(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"
	// User typed Enter after the opening brace on line 0.
	// The document now has a newline and cursor is on line 1.
	src := "behavior foo {\n\n}\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeRequest(&input, 1, "textDocument/onTypeFormatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 0},
		"ch":           "\n",
		"options":      map[string]any{"tabSize": 4, "insertSpaces": true},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var edits []map[string]any
	if err := json.Unmarshal(responses[0], &edits); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, responses[0])
	}
	if len(edits) == 0 {
		t.Fatal("expected indentation edit, got empty array")
	}
	newText, _ := edits[0]["newText"].(string)
	if newText != "    " {
		t.Errorf("newText = %q, want %q", newText, "    ")
	}
}

func TestOnTypeFormattingWithDidChange(t *testing.T) {
	// Simulates real flow: didOpen with original content, then didChange
	// after user types Enter, then onTypeFormatting.
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"
	original := "behavior foo {\n    exit\n}\n"

	// Step 1: open with original content
	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": original},
	})

	// Step 2: user types Enter after { on line 0. Editor inserts newline.
	// New document has the blank line at line 1, old line 1 becomes line 2.
	changed := "behavior foo {\n\n    exit\n}\n"
	writeNotification(&input, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": changed}},
	})

	// Step 3: onTypeFormatting for the new line
	writeRequest(&input, 1, "textDocument/onTypeFormatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 0},
		"ch":           "\n",
		"options":      map[string]any{"tabSize": 4, "insertSpaces": true},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var edits []map[string]any
	if err := json.Unmarshal(responses[0], &edits); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, responses[0])
	}
	if len(edits) == 0 {
		t.Fatal("expected indentation edit, got empty array")
	}
	newText, _ := edits[0]["newText"].(string)
	if newText != "    " {
		t.Errorf("newText = %q, want %q", newText, "    ")
	}
}

func TestOnTypeFormattingStaleDoc(t *testing.T) {
	// Simulates the case where didChange has NOT arrived before onTypeFormatting.
	// The document still has the old content. The handler should still compute
	// correct indentation based on the position.
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"
	// Original doc — user is about to type Enter after { on line 0.
	// But didChange hasn't arrived yet.
	original := "behavior foo {\n    exit\n}\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": original},
	})

	// onTypeFormatting arrives with position on what the CLIENT thinks is line 1
	// (the new line after Enter), but the SERVER still has old content where
	// line 1 is "    exit".
	writeRequest(&input, 1, "textDocument/onTypeFormatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 0},
		"ch":           "\n",
		"options":      map[string]any{"tabSize": 4, "insertSpaces": true},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var edits []map[string]any
	if err := json.Unmarshal(responses[0], &edits); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, responses[0])
	}

	// Even with stale doc, we compute depth from line 0 (the previous line)
	// and return an insert at (1, 0). This works because the previous line
	// is the same in both old and new documents.
	if len(edits) == 0 {
		t.Fatal("expected indentation edit even with stale doc, got empty array")
	}
	newText, _ := edits[0]["newText"].(string)
	if newText != "    " {
		t.Errorf("newText = %q, want %q", newText, "    ")
	}
}

func TestOnTypeFormattingCloseBrace(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"
	// User typed } on line 2 (wrong indentation — 8 spaces instead of 0).
	src := "behavior foo {\n    exit\n        }\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeRequest(&input, 1, "textDocument/onTypeFormatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 2, "character": 9},
		"ch":           "}",
		"options":      map[string]any{"tabSize": 4, "insertSpaces": true},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var edits []map[string]any
	if err := json.Unmarshal(responses[0], &edits); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, responses[0])
	}
	if len(edits) == 0 {
		t.Fatal("expected indentation edit, got empty array")
	}
	newText, _ := edits[0]["newText"].(string)
	if newText != "" {
		t.Errorf("newText = %q, want %q (no indent for top-level close brace)", newText, "")
	}
}

func TestShutdownExit(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	writeRequest(&input, 1, "shutdown", nil)
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
}

func TestOffsetToLineCol(t *testing.T) {
	src := "abc\ndef\nghi"
	tests := []struct {
		offset int
		line   int
		col    int
	}{
		{0, 0, 0},
		{1, 0, 1},
		{3, 0, 3},
		{4, 1, 0},
		{7, 1, 3},
		{8, 2, 0},
	}
	for _, tt := range tests {
		line, col := offsetToLineCol(src, tt.offset)
		if line != tt.line || col != tt.col {
			t.Errorf("offset %d: expected (%d,%d), got (%d,%d)", tt.offset, tt.line, tt.col, line, col)
		}
	}
}

// --- Test helpers ---

// roundtrip opens a document, requests semantic tokens, then exits.
func roundtrip(t *testing.T, src string) map[string]any {
	t.Helper()
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	uri := "file:///test.doit"
	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeRequest(&input, 1, "textDocument/semanticTokens/full", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	return responses[0]
}

// sendRequest sends a single request and returns the result.
func sendRequest(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output)

	writeRequest(&input, 1, method, params)
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	return responses[0]
}

func writeRequest(buf *bytes.Buffer, id int, method string, params any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	body, _ := json.Marshal(msg)
	_, _ = fmt.Fprintf(buf, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func writeNotification(buf *bytes.Buffer, method string, params any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	body, _ := json.Marshal(msg)
	_, _ = fmt.Fprintf(buf, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func parseResponses(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	str := string(data)
	var results []map[string]any

	for str != "" {
		idx := strings.Index(str, "Content-Length:")
		if idx < 0 {
			break
		}
		str = str[idx:]
		nlIdx := strings.Index(str, "\r\n")
		if nlIdx < 0 {
			break
		}
		lenStr := strings.TrimSpace(str[len("Content-Length:"):nlIdx])
		var cLen int
		_, _ = fmt.Sscanf(lenStr, "%d", &cLen)

		bodyStart := strings.Index(str, "\r\n\r\n")
		if bodyStart < 0 {
			break
		}
		bodyStart += 4
		if bodyStart+cLen > len(str) {
			break
		}
		body := str[bodyStart : bodyStart+cLen]
		str = str[bodyStart+cLen:]

		var resp struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			continue
		}
		if resp.Result == nil {
			results = append(results, nil)
			continue
		}
		var result map[string]any
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			results = append(results, nil)
			continue
		}
		results = append(results, result)
	}
	return results
}

// parseRawResponses extracts raw JSON result values from LSP responses.
// Unlike parseResponses, this preserves the result as json.RawMessage
// so it works for both array and object results.
func parseRawResponses(t *testing.T, data []byte) []json.RawMessage {
	t.Helper()
	str := string(data)
	var results []json.RawMessage

	for str != "" {
		idx := strings.Index(str, "Content-Length:")
		if idx < 0 {
			break
		}
		str = str[idx:]
		nlIdx := strings.Index(str, "\r\n")
		if nlIdx < 0 {
			break
		}
		lenStr := strings.TrimSpace(str[len("Content-Length:"):nlIdx])
		var cLen int
		_, _ = fmt.Sscanf(lenStr, "%d", &cLen)

		bodyStart := strings.Index(str, "\r\n\r\n")
		if bodyStart < 0 {
			break
		}
		bodyStart += 4
		if bodyStart+cLen > len(str) {
			break
		}
		body := str[bodyStart : bodyStart+cLen]
		str = str[bodyStart+cLen:]

		var resp struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			continue
		}
		results = append(results, resp.Result)
	}
	return results
}

func assertData(t *testing.T, data []any, tokenIndex int, deltaLine, deltaCol, length, tokenType, tokenMods int) {
	t.Helper()
	base := tokenIndex * 5
	if base+4 >= len(data) {
		t.Fatalf("token %d: data too short (have %d values)", tokenIndex, len(data))
	}
	got := [5]int{
		int(data[base].(float64)),
		int(data[base+1].(float64)),
		int(data[base+2].(float64)),
		int(data[base+3].(float64)),
		int(data[base+4].(float64)),
	}
	want := [5]int{deltaLine, deltaCol, length, tokenType, tokenMods}
	if got != want {
		t.Errorf("token %d: got [dLine=%d dCol=%d len=%d type=%d mods=%d], want [dLine=%d dCol=%d len=%d type=%d mods=%d]",
			tokenIndex, got[0], got[1], got[2], got[3], got[4],
			want[0], want[1], want[2], want[3], want[4])
	}
}
