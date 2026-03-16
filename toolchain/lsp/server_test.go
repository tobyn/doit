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
	s := NewServer(&input, &output, nil)

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
	s := NewServer(&input, &output, nil)

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
	s := NewServer(&input, &output, nil)

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
	s := NewServer(&input, &output, nil)

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
	s := NewServer(&input, &output, nil)

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
	s := NewServer(&input, &output, nil)

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

func TestOnTypeFormattingStaleDocAutoPair(t *testing.T) {
	// Simulates Enter pressed between auto-closed {} when didChange hasn't
	// arrived yet. The stale document has "if a {}" on one line. The handler
	// must exclude the trailing } from depth computation so it returns depth 1
	// (not 0).
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	// Stale doc: the {} are still on the same line.
	original := "behavior foo {\n    if a {}\n}\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": original},
	})

	// Client sends onTypeFormatting for Enter on line 2 (the new line
	// between { and }), but the server still sees "    if a {}" on line 1.
	writeRequest(&input, 1, "textDocument/onTypeFormatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 2, "character": 0},
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
	// Depth should be 2 (behavior { + if a {), not 1 (which would happen
	// if the trailing } in "if a {}" were counted).
	newText, _ := edits[0]["newText"].(string)
	if newText != "        " {
		t.Errorf("newText = %q, want %q (8 spaces for depth 2)", newText, "        ")
	}
}

func TestOnTypeFormattingCloseBrace(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

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

func TestDiagnosticsOnOpen(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	// Source with a syntax error (unterminated block).
	src := "behavior foo {\n    exit\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	notifs := parseNotifications(t, output.Bytes(), "textDocument/publishDiagnostics")
	if len(notifs) != 1 {
		t.Fatalf("expected 1 diagnostics notification, got %d", len(notifs))
	}

	diags, ok := notifs[0]["diagnostics"].([]any)
	if !ok {
		t.Fatal("missing diagnostics array")
	}
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic for syntax error")
	}
	d := diags[0].(map[string]any)
	if sev := d["severity"].(float64); sev != 1 {
		t.Errorf("severity = %v, want 1 (Error)", sev)
	}
	if src := d["source"]; src != "doit" {
		t.Errorf("source = %v, want \"doit\"", src)
	}
}

func TestDiagnosticsClearOnClose(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	src := "behavior foo {\n    exit\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeNotification(&input, "textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	notifs := parseNotifications(t, output.Bytes(), "textDocument/publishDiagnostics")
	if len(notifs) != 2 {
		t.Fatalf("expected 2 diagnostics notifications (open + close), got %d", len(notifs))
	}

	// Second notification (on close) should have empty diagnostics.
	diags, ok := notifs[1]["diagnostics"].([]any)
	if !ok {
		t.Fatal("missing diagnostics array in close notification")
	}
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics on close, got %d", len(diags))
	}
}

func TestDiagnosticsCleanSource(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	// Valid source — no errors. Uses skip prelude since tests have no stdlib.
	src := "skip prelude\nconst x = 5\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	notifs := parseNotifications(t, output.Bytes(), "textDocument/publishDiagnostics")
	if len(notifs) != 1 {
		t.Fatalf("expected 1 diagnostics notification, got %d", len(notifs))
	}

	diags, ok := notifs[0]["diagnostics"].([]any)
	if !ok {
		t.Fatal("missing diagnostics array")
	}
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for clean source, got %d", len(diags))
	}
}

func TestDiagnosticsOnChange(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	// Start with broken source.
	src := "behavior foo {"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	// Fix the source.
	writeNotification(&input, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri},
		"contentChanges": []map[string]any{{"text": "skip prelude\nconst x = 5\n"}},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	notifs := parseNotifications(t, output.Bytes(), "textDocument/publishDiagnostics")
	if len(notifs) != 2 {
		t.Fatalf("expected 2 diagnostics notifications (open + change), got %d", len(notifs))
	}

	// First: should have errors.
	diags1 := notifs[0]["diagnostics"].([]any)
	if len(diags1) == 0 {
		t.Error("expected diagnostics on open with broken source")
	}

	// Second: should be clean.
	diags2 := notifs[1]["diagnostics"].([]any)
	if len(diags2) != 0 {
		t.Errorf("expected 0 diagnostics after fix, got %d", len(diags2))
	}
}

func TestDocumentSymbols(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	src := "skip prelude\nconst MAX = 10\nfn greet() {\n}\nenum Color {\n    Red\n    Blue\n}\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	writeRequest(&input, 1, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var symbols []map[string]any
	if err := json.Unmarshal(responses[0], &symbols); err != nil {
		t.Fatalf("unmarshal symbols: %v (raw: %s)", err, responses[0])
	}
	if len(symbols) != 3 {
		t.Fatalf("expected 3 symbols (const, fn, enum), got %d: %v", len(symbols), symbols)
	}

	// Check names and kinds.
	wantNames := []string{"MAX", "greet", "Color"}
	wantKinds := []float64{14, 12, 10} // Constant, Function, Enum
	for i, sym := range symbols {
		name := sym["name"].(string)
		kind := sym["kind"].(float64)
		if name != wantNames[i] {
			t.Errorf("symbol %d: name = %q, want %q", i, name, wantNames[i])
		}
		if kind != wantKinds[i] {
			t.Errorf("symbol %d: kind = %v, want %v", i, kind, wantKinds[i])
		}
	}
}

func TestHover(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	// #! doc comment on the const declaration.
	src := "skip prelude\n#! The maximum count.\nconst MAX = 10\nfn greet() {\n}\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	// Hover over "MAX" on line 2, col 6 (within "MAX").
	writeRequest(&input, 1, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 2, "character": 6},
	})
	// Hover over "greet" on line 3, col 3 (within "greet").
	writeRequest(&input, 2, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 3, "character": 3},
	})
	// Hover over empty space — should return null.
	writeRequest(&input, 3, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": 0},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}

	// Response 1: hover on MAX — should have doc comment.
	var hover1 map[string]any
	if err := json.Unmarshal(responses[0], &hover1); err != nil {
		t.Fatalf("unmarshal hover1: %v", err)
	}
	contents1, ok := hover1["contents"].(map[string]any)
	if !ok {
		t.Fatal("missing contents in hover1")
	}
	value1, _ := contents1["value"].(string)
	if !strings.Contains(value1, "const MAX") {
		t.Errorf("hover1 value should contain 'const MAX', got: %s", value1)
	}
	if !strings.Contains(value1, "The maximum count.") {
		t.Errorf("hover1 value should contain doc comment, got: %s", value1)
	}

	// Response 2: hover on greet.
	var hover2 map[string]any
	if err := json.Unmarshal(responses[1], &hover2); err != nil {
		t.Fatalf("unmarshal hover2: %v", err)
	}
	contents2, ok := hover2["contents"].(map[string]any)
	if !ok {
		t.Fatal("missing contents in hover2")
	}
	value2, _ := contents2["value"].(string)
	if !strings.Contains(value2, "fn greet") {
		t.Errorf("hover2 value should contain 'fn greet', got: %s", value2)
	}

	// Response 3: hover on non-identifier — should be null.
	if responses[2] != nil && string(responses[2]) != "null" {
		t.Errorf("expected null for non-identifier hover, got: %s", responses[2])
	}
}

func TestSignatureHelp(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

	uri := "file:///test.doit"
	src := "skip prelude\nfn add(a, b) {\n}\nbehavior test {\n    add(\n}\n"

	writeNotification(&input, "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	})
	// Cursor right after "add(" — first parameter.
	writeRequest(&input, 1, "textDocument/signatureHelp", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 4, "character": 8},
	})
	writeNotification(&input, "exit", nil)

	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := parseRawResponses(t, output.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var result map[string]any
	if err := json.Unmarshal(responses[0], &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	sigs, ok := result["signatures"].([]any)
	if !ok || len(sigs) == 0 {
		t.Fatal("expected at least one signature")
	}
	sig := sigs[0].(map[string]any)
	label := sig["label"].(string)
	if !strings.Contains(label, "add") {
		t.Errorf("signature label should contain 'add', got: %s", label)
	}
	if activeParam := int(result["activeParameter"].(float64)); activeParam != 0 {
		t.Errorf("activeParameter = %d, want 0", activeParam)
	}
}

func TestSignatureHelpCapabilityAdvertised(t *testing.T) {
	resp := sendRequest(t, "initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	caps := resp["capabilities"].(map[string]any)
	sigHelp, ok := caps["signatureHelpProvider"].(map[string]any)
	if !ok {
		t.Fatal("signatureHelpProvider not advertised")
	}
	triggers, ok := sigHelp["triggerCharacters"].([]any)
	if !ok || len(triggers) == 0 {
		t.Error("missing triggerCharacters")
	}
}

func TestFindCallContext(t *testing.T) {
	tests := []struct {
		src       string
		offset    int
		wantName  string
		wantParam int
	}{
		{"foo(", 4, "foo", 0},
		{"foo(a, ", 7, "foo", 1},
		{"foo(a, b, ", 10, "foo", 2},
		{"bar(foo(x), ", 12, "bar", 1},
		{"let x = 5", 5, "", 0},
		{"foo(\"a,b\", ", 11, "foo", 1}, // comma inside string
	}
	for _, tt := range tests {
		name, param := findCallContext(tt.src, tt.offset)
		if name != tt.wantName || param != tt.wantParam {
			t.Errorf("findCallContext(%q, %d) = (%q, %d), want (%q, %d)",
				tt.src, tt.offset, name, param, tt.wantName, tt.wantParam)
		}
	}
}

func TestHoverCapabilityAdvertised(t *testing.T) {
	resp := sendRequest(t, "initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	caps := resp["capabilities"].(map[string]any)
	if caps["hoverProvider"] != true {
		t.Error("hoverProvider not advertised")
	}
}

func TestIdentAtOffset(t *testing.T) {
	src := "let foo = bar(baz)"
	tests := []struct {
		offset int
		want   string
	}{
		{0, "let"},
		{1, "let"},
		{3, ""}, // space
		{4, "foo"},
		{6, "foo"},
		{8, ""}, // =
		{10, "bar"},
		{13, ""}, // (
		{14, "baz"},
		{17, ""}, // )
	}
	for _, tt := range tests {
		got := identAtOffset(src, tt.offset)
		if got != tt.want {
			t.Errorf("identAtOffset(%d) = %q, want %q", tt.offset, got, tt.want)
		}
	}
}

func TestDocumentSymbolsCapabilityAdvertised(t *testing.T) {
	resp := sendRequest(t, "initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	caps := resp["capabilities"].(map[string]any)
	if caps["documentSymbolProvider"] != true {
		t.Error("documentSymbolProvider not advertised")
	}
}

func TestParseDiagnostic(t *testing.T) {
	tests := []struct {
		msg      string
		severity int
		wantLine int
		wantCol  int
		wantMsg  string
	}{
		{
			msg:      "3:10: unexpected token",
			severity: 1,
			wantLine: 2, wantCol: 9, // 0-based
			wantMsg: "unexpected token",
		},
		{
			msg:      "1:1: something wrong\n  1 | some code\n    | ^",
			severity: 2,
			wantLine: 0, wantCol: 0,
			wantMsg: "something wrong",
		},
		{
			msg:      "libs/helper.doit:5:3: undefined variable",
			severity: 1,
			wantLine: 4, wantCol: 2, // 0-based
			wantMsg: "undefined variable",
		},
	}
	for _, tt := range tests {
		d := parseDiagnostic(tt.msg, tt.severity)
		if d == nil {
			t.Errorf("parseDiagnostic(%q) returned nil", tt.msg)
			continue
		}
		r := d["range"].(map[string]any)
		start := r["start"].(map[string]any)
		if line := int(start["line"].(int)); line != tt.wantLine {
			t.Errorf("line = %d, want %d", line, tt.wantLine)
		}
		if col := int(start["character"].(int)); col != tt.wantCol {
			t.Errorf("character = %d, want %d", col, tt.wantCol)
		}
		if msg := d["message"].(string); msg != tt.wantMsg {
			t.Errorf("message = %q, want %q", msg, tt.wantMsg)
		}
		if sev := d["severity"].(int); sev != tt.severity {
			t.Errorf("severity = %d, want %d", sev, tt.severity)
		}
	}

	// Non-parseable messages should return nil.
	if d := parseDiagnostic("no line info here", 1); d != nil {
		t.Errorf("expected nil for unparseable message, got %v", d)
	}
}

func TestShutdownExit(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	s := NewServer(&input, &output, nil)

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
	s := NewServer(&input, &output, nil)

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
	s := NewServer(&input, &output, nil)

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
			ID     json.RawMessage `json:"id,omitempty"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			continue
		}
		if resp.ID == nil {
			continue // skip notifications (e.g., diagnostics)
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
			ID     json.RawMessage `json:"id,omitempty"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			continue
		}
		if resp.ID == nil {
			continue // skip notifications (e.g., diagnostics)
		}
		results = append(results, resp.Result)
	}
	return results
}

// parseNotifications extracts notification params for the given method from raw output.
func parseNotifications(t *testing.T, data []byte, method string) []map[string]any {
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

		var msg struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(body), &msg); err != nil {
			continue
		}
		if msg.Method != method {
			continue
		}
		var params map[string]any
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			continue
		}
		results = append(results, params)
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
