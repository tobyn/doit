// Package lsp implements a Language Server Protocol server for the doit
// language. It provides semantic token highlighting via the compiler's
// tokenizer and communicates over JSON-RPC 2.0 on stdio.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/tobyn/doit/toolchain/compiler"
	"github.com/tobyn/doit/toolchain/formatter"
	"github.com/tobyn/doit/toolchain/syntax"
)

// Server is the doit language server.
type Server struct {
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex // protects writer

	// Open documents keyed by URI.
	docs   map[string]string
	docsMu sync.Mutex

	// Standard library filesystem for compilation.
	stdlib fs.FS
}

// NewServer creates a new language server using the given reader and writer
// for JSON-RPC communication. The stdlib parameter provides the standard
// library filesystem for compilation diagnostics; it may be nil.
func NewServer(r io.Reader, w io.Writer, stdlib fs.FS) *Server {
	return &Server{
		reader: bufio.NewReader(r),
		writer: w,
		docs:   make(map[string]string),
		stdlib: stdlib,
	}
}

// Run starts the language server, reading JSON-RPC messages from stdin
// and writing responses to stdout. It blocks until the exit notification
// is received or an I/O error occurs. The stdlib parameter provides the
// standard library filesystem for compilation diagnostics.
func Run(stdlib fs.FS) error {
	s := NewServer(os.Stdin, os.Stdout, stdlib)
	return s.Serve()
}

// Serve reads and dispatches JSON-RPC messages until exit.
func (s *Server) Serve() error {
	for {
		msg, err := s.readMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading message: %w", err)
		}

		exit, err := s.handleMessage(msg)
		if err != nil {
			return fmt.Errorf("handling message: %w", err)
		}
		if exit {
			return nil
		}
	}
}

// --- JSON-RPC types ---

type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- Message I/O ---

func (s *Server) readMessage() (*jsonrpcMessage, error) {
	// Read headers.
	var contentLength int
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line separates headers from body
		}
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(line[len("Content-Length:"):])
			contentLength, err = strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %s", val)
			}
		}
		// Ignore other headers (Content-Type, etc.)
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	// Read body.
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(s.reader, body); err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	var msg jsonrpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("parsing message: %w", err)
	}
	return &msg, nil
}

func (s *Server) sendResponse(id json.RawMessage, result any) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.writeMessage(resp)
}

func (s *Server) sendError(id json.RawMessage, code int, message string) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message},
	}
	s.writeMessage(resp)
}

func (s *Server) writeMessage(v any) {
	body, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

// --- Message dispatch ---

func (s *Server) handleMessage(msg *jsonrpcMessage) (exit bool, err error) {
	switch msg.Method {
	case "initialize":
		s.handleInitialize(msg)
	case "initialized":
		// No-op.
	case "shutdown":
		s.sendResponse(msg.ID, nil)
	case "exit":
		return true, nil
	case "textDocument/didOpen":
		s.handleDidOpen(msg)
	case "textDocument/didChange":
		s.handleDidChange(msg)
	case "textDocument/didClose":
		s.handleDidClose(msg)
	case "textDocument/documentSymbol":
		s.handleDocumentSymbol(msg)
	case "textDocument/hover":
		s.handleHover(msg)
	case "textDocument/signatureHelp":
		s.handleSignatureHelp(msg)
	case "textDocument/formatting":
		s.handleFormatting(msg)
	case "textDocument/onTypeFormatting":
		s.handleOnTypeFormatting(msg)
	case "textDocument/semanticTokens/full":
		s.handleSemanticTokensFull(msg)
	default:
		if msg.ID != nil {
			// Unknown request — respond with method not found.
			s.sendError(msg.ID, -32601, "method not found: "+msg.Method)
		}
		// Unknown notifications are silently ignored.
	}
	return false, nil
}

// --- LSP handlers ---

func (s *Server) handleInitialize(msg *jsonrpcMessage) {
	result := map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    1, // Full sync
			},
			"documentFormattingProvider": true,
			"documentSymbolProvider":     true,
			"hoverProvider":              true,
			"signatureHelpProvider": map[string]any{
				"triggerCharacters": []string{"(", ","},
			},
			"documentOnTypeFormattingProvider": map[string]any{
				"firstTriggerCharacter": "\n",
				"moreTriggerCharacter":  []string{"}"},
			},
			"semanticTokensProvider": map[string]any{
				"legend": map[string]any{
					"tokenTypes":     syntax.SemanticTokenTypes(),
					"tokenModifiers": syntax.SemanticTokenModifiers(),
				},
				"full": true,
			},
		},
		"serverInfo": map[string]any{
			"name":    "doit-language-server",
			"version": "0.1.0",
		},
	}
	s.sendResponse(msg.ID, result)
}

// --- Document management ---

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

func (s *Server) handleDidOpen(msg *jsonrpcMessage) {
	var params didOpenParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	s.docsMu.Lock()
	s.docs[params.TextDocument.URI] = params.TextDocument.Text
	s.docsMu.Unlock()
	s.publishDiagnostics(params.TextDocument.URI, params.TextDocument.Text)
}

type didChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

func (s *Server) handleDidChange(msg *jsonrpcMessage) {
	var params didChangeParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	if len(params.ContentChanges) == 0 {
		return
	}
	// Full sync: take the last content change.
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	s.docsMu.Lock()
	s.docs[params.TextDocument.URI] = text
	s.docsMu.Unlock()
	s.publishDiagnostics(params.TextDocument.URI, text)
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) handleDidClose(msg *jsonrpcMessage) {
	var params didCloseParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	s.docsMu.Lock()
	delete(s.docs, params.TextDocument.URI)
	s.docsMu.Unlock()
	// Clear diagnostics for the closed document.
	s.sendNotification("textDocument/publishDiagnostics", map[string]any{
		"uri":         params.TextDocument.URI,
		"diagnostics": []any{},
	})
}

// --- Diagnostics ---

// sendNotification sends a JSON-RPC notification (no id, no response expected).
func (s *Server) sendNotification(method string, params any) {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	s.writeMessage(msg)
}

// publishDiagnostics compiles the source to collect errors and warnings,
// then publishes them as LSP diagnostics.
func (s *Server) publishDiagnostics(uri, src string) {
	diags := []map[string]any{}

	var sourceFS fs.FS
	var sourcePath string
	if filePath := uriToPath(uri); filePath != "" {
		sourceFS = os.DirFS(filepath.Dir(filePath))
		sourcePath = filepath.Base(filePath)
	}

	_, warnings, err := compiler.Check(src, s.stdlib, sourceFS, sourcePath)
	if err != nil {
		if d := parseDiagnostic(err.Error(), 1); d != nil {
			diags = append(diags, d)
		}
	}
	for _, w := range warnings {
		if d := parseDiagnostic(w, 2); d != nil {
			diags = append(diags, d)
		}
	}

	s.sendNotification("textDocument/publishDiagnostics", map[string]any{
		"uri":         uri,
		"diagnostics": diags,
	})
}

// uriToPath converts a file:// URI to a local filesystem path.
// Returns empty string for non-file URIs or on parse failure.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	// On Windows, file URIs look like file:///C:/path — strip the leading /.
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// parseDiagnostic extracts a line:col prefixed message into an LSP diagnostic.
// severity: 1=Error, 2=Warning. Returns nil if the format is not recognized.
// Handles both "line:col: message" and "filename:line:col: message" formats.
func parseDiagnostic(msg string, severity int) map[string]any {
	// Strip any source annotation (starts with newline).
	if idx := strings.Index(msg, "\n"); idx >= 0 {
		msg = msg[:idx]
	}

	line, col, message, ok := parseLineColMessage(msg)
	if !ok {
		return nil
	}

	// Convert from 1-based to 0-based for LSP.
	line--
	col--
	if line < 0 {
		line = 0
	}
	if col < 0 {
		col = 0
	}

	return map[string]any{
		"range": map[string]any{
			"start": map[string]any{"line": line, "character": col},
			"end":   map[string]any{"line": line, "character": col},
		},
		"severity": severity,
		"source":   "doit",
		"message":  message,
	}
}

// parseLineColMessage extracts line, col, and message from a compiler error string.
// Handles "line:col: message" and "filename:line:col: message" formats.
func parseLineColMessage(msg string) (line, col int, message string, ok bool) {
	colon1 := strings.Index(msg, ":")
	if colon1 < 0 {
		return 0, 0, "", false
	}
	line, err := strconv.Atoi(msg[:colon1])
	if err != nil {
		// First field isn't a number — try filename:line:col: format.
		rest := msg[colon1+1:]
		return parseLineColMessage(rest)
	}
	rest := msg[colon1+1:]
	colon2 := strings.Index(rest, ":")
	if colon2 < 0 {
		return 0, 0, "", false
	}
	col, err = strconv.Atoi(rest[:colon2])
	if err != nil {
		return 0, 0, "", false
	}
	message = strings.TrimSpace(rest[colon2+1:])
	return line, col, message, true
}

// --- Document symbols ---

// handleDocumentSymbol returns an outline of top-level declarations.
func (s *Server) handleDocumentSymbol(msg *jsonrpcMessage) {
	var params semanticTokensParams // reuse — same shape (textDocument.uri)
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.sendError(msg.ID, -32602, "invalid params")
		return
	}

	s.docsMu.Lock()
	src, ok := s.docs[params.TextDocument.URI]
	s.docsMu.Unlock()
	if !ok {
		s.sendResponse(msg.ID, []any{})
		return
	}

	var sourceFS fs.FS
	var sourcePath string
	if filePath := uriToPath(params.TextDocument.URI); filePath != "" {
		sourceFS = os.DirFS(filepath.Dir(filePath))
		sourcePath = filepath.Base(filePath)
	}

	symbols, _, _ := compiler.Check(src, s.stdlib, sourceFS, sourcePath)

	var result []map[string]any
	for _, sym := range symbols {
		if sym.Line < 0 {
			continue // skip imported symbols
		}
		pos := map[string]any{
			"start": map[string]any{"line": sym.Line, "character": sym.Col},
			"end":   map[string]any{"line": sym.Line, "character": sym.Col},
		}
		result = append(result, map[string]any{
			"name":           sym.Name,
			"kind":           symbolKindToLSP(sym.Kind),
			"range":          pos,
			"selectionRange": pos,
		})
	}
	if result == nil {
		result = []map[string]any{}
	}
	s.sendResponse(msg.ID, result)
}

// symbolKindToLSP converts a compiler SymbolKind to an LSP SymbolKind number.
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#symbolKind
func symbolKindToLSP(kind compiler.SymbolKind) int {
	switch kind {
	case compiler.SymbolBehavior:
		return 5 // Class
	case compiler.SymbolFunction:
		return 12 // Function
	case compiler.SymbolIterator:
		return 12 // Function
	case compiler.SymbolConstant:
		return 14 // Constant
	case compiler.SymbolEnum:
		return 10 // Enum
	default:
		return 1 // File (fallback)
	}
}

// --- Hover ---

type hoverParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

func (s *Server) handleHover(msg *jsonrpcMessage) {
	var params hoverParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.sendError(msg.ID, -32602, "invalid params")
		return
	}

	s.docsMu.Lock()
	src, ok := s.docs[params.TextDocument.URI]
	s.docsMu.Unlock()
	if !ok {
		s.sendResponse(msg.ID, nil)
		return
	}

	// Find the identifier at the cursor position.
	offset := lineColToOffset(src, params.Position.Line, params.Position.Character)
	name := identAtOffset(src, offset)
	if name == "" {
		s.sendResponse(msg.ID, nil)
		return
	}

	// Look up the identifier in symbols.
	var sourceFS fs.FS
	var sourcePath string
	if filePath := uriToPath(params.TextDocument.URI); filePath != "" {
		sourceFS = os.DirFS(filepath.Dir(filePath))
		sourcePath = filepath.Base(filePath)
	}

	symbols, _, _ := compiler.Check(src, s.stdlib, sourceFS, sourcePath)

	for _, sym := range symbols {
		if sym.Name == name {
			hover := formatHover(sym)
			if hover != "" {
				s.sendResponse(msg.ID, map[string]any{
					"contents": map[string]any{
						"kind":  "markdown",
						"value": hover,
					},
				})
				return
			}
		}
	}

	s.sendResponse(msg.ID, nil)
}

// formatHover builds a markdown hover string for a symbol.
func formatHover(sym compiler.Symbol) string {
	var sb strings.Builder

	// Signature line.
	switch sym.Kind {
	case compiler.SymbolBehavior:
		sb.WriteString("```doit\nbehavior ")
		sb.WriteString(sym.Name)
		sb.WriteString("\n```")
	case compiler.SymbolFunction:
		sb.WriteString("```doit\nfn ")
		sb.WriteString(sym.Name)
		sb.WriteString(formatParams(sym.Params))
		sb.WriteString("\n```")
	case compiler.SymbolIterator:
		sb.WriteString("```doit\niter ")
		sb.WriteString(sym.Name)
		sb.WriteString(formatParams(sym.Params))
		sb.WriteString("\n```")
	case compiler.SymbolConstant:
		sb.WriteString("```doit\nconst ")
		sb.WriteString(sym.Name)
		sb.WriteString("\n```")
	case compiler.SymbolEnum:
		sb.WriteString("```doit\nenum ")
		sb.WriteString(sym.Name)
		sb.WriteString("\n```")
	}

	if sym.Doc != "" {
		sb.WriteString("\n\n")
		sb.WriteString(sym.Doc)
	}

	return sb.String()
}

// formatParams formats a parameter list for display.
func formatParams(params []compiler.ParamInfo) string {
	if len(params) == 0 {
		return "()"
	}
	var parts []string
	for _, p := range params {
		s := p.Name
		if p.Keyword != "" {
			s = p.Keyword + " " + p.Name
		}
		if p.Direction != "" && p.Direction != "in" {
			s += " [" + p.Direction + "]"
		}
		parts = append(parts, s)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// lineColToOffset converts 0-based line and column to a byte offset.
func lineColToOffset(src string, line, col int) int {
	offset := 0
	for l := 0; l < line && offset < len(src); offset++ {
		if src[offset] == '\n' {
			l++
		}
	}
	return offset + col
}

// identAtOffset extracts the identifier at the given byte offset in src.
// Returns empty string if offset is not on an identifier character.
func identAtOffset(src string, offset int) string {
	if offset < 0 || offset >= len(src) {
		return ""
	}
	if !isIdentChar(src[offset]) {
		return ""
	}
	// Walk backward to start of identifier.
	start := offset
	for start > 0 && isIdentChar(src[start-1]) {
		start--
	}
	// Walk forward to end of identifier.
	end := offset
	for end < len(src) && isIdentChar(src[end]) {
		end++
	}
	word := src[start:end]
	// Must start with a letter or underscore (not a digit).
	if len(word) > 0 && (word[0] >= 'a' && word[0] <= 'z' || word[0] >= 'A' && word[0] <= 'Z' || word[0] == '_') {
		return word
	}
	return ""
}

func isIdentChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// --- Signature help ---

type signatureHelpParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

func (s *Server) handleSignatureHelp(msg *jsonrpcMessage) {
	var params signatureHelpParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.sendError(msg.ID, -32602, "invalid params")
		return
	}

	s.docsMu.Lock()
	src, ok := s.docs[params.TextDocument.URI]
	s.docsMu.Unlock()
	if !ok {
		s.sendResponse(msg.ID, nil)
		return
	}

	offset := lineColToOffset(src, params.Position.Line, params.Position.Character)
	fnName, activeParam := findCallContext(src, offset)
	if fnName == "" {
		s.sendResponse(msg.ID, nil)
		return
	}

	// Look up the function in symbols.
	var sourceFS fs.FS
	var sourcePath string
	if filePath := uriToPath(params.TextDocument.URI); filePath != "" {
		sourceFS = os.DirFS(filepath.Dir(filePath))
		sourcePath = filepath.Base(filePath)
	}

	symbols, _, _ := compiler.Check(src, s.stdlib, sourceFS, sourcePath)

	for _, sym := range symbols {
		if sym.Name == fnName && len(sym.Params) > 0 {
			sigParams := make([]map[string]any, len(sym.Params))
			for i, p := range sym.Params {
				label := p.Name
				if p.Keyword != "" {
					label = p.Keyword + " " + p.Name
				}
				if p.Direction != "" && p.Direction != "in" {
					label += " [" + p.Direction + "]"
				}
				sigParams[i] = map[string]any{"label": label}
			}

			sigLabel := sym.Name + formatParams(sym.Params)

			sig := map[string]any{
				"label":      sigLabel,
				"parameters": sigParams,
			}
			if sym.Doc != "" {
				sig["documentation"] = map[string]any{
					"kind":  "markdown",
					"value": sym.Doc,
				}
			}

			s.sendResponse(msg.ID, map[string]any{
				"signatures":      []any{sig},
				"activeSignature": 0,
				"activeParameter": activeParam,
			})
			return
		}
	}

	s.sendResponse(msg.ID, nil)
}

// findCallContext scans backward from offset to find the enclosing function
// call. Returns the function name and the 0-based active parameter index.
// Returns ("", 0) if the cursor is not inside a parenthesized call.
func findCallContext(src string, offset int) (string, int) {
	if offset > len(src) {
		offset = len(src)
	}

	// Scan backward to find the matching '(' while counting commas.
	depth := 0
	commas := 0
	for i := offset - 1; i >= 0; i-- {
		c := src[i]
		switch c {
		case ')':
			depth++
		case '(':
			if depth > 0 {
				depth--
			} else {
				// Found the opening paren. The function name is the
				// identifier immediately before it.
				name := identEndingAt(src, i)
				return name, commas
			}
		case ',':
			if depth == 0 {
				commas++
			}
		case '"':
			// Skip backward over string literals.
			i--
			for i >= 0 && src[i] != '"' {
				if i > 0 && src[i-1] == '\\' {
					i--
				}
				i--
			}
		}
	}
	return "", 0
}

// identEndingAt returns the identifier that ends just before position pos.
func identEndingAt(src string, pos int) string {
	// Skip whitespace before the paren.
	end := pos
	for end > 0 && (src[end-1] == ' ' || src[end-1] == '\t') {
		end--
	}
	if end == 0 {
		return ""
	}
	// Walk backward through identifier characters.
	start := end
	for start > 0 && isIdentChar(src[start-1]) {
		start--
	}
	word := src[start:end]
	if len(word) > 0 && (word[0] >= 'a' && word[0] <= 'z' || word[0] >= 'A' && word[0] <= 'Z' || word[0] == '_') {
		return word
	}
	return ""
}

// --- Semantic tokens ---

type semanticTokensParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) handleSemanticTokensFull(msg *jsonrpcMessage) {
	var params semanticTokensParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.sendError(msg.ID, -32602, "invalid params")
		return
	}

	s.docsMu.Lock()
	src, ok := s.docs[params.TextDocument.URI]
	s.docsMu.Unlock()
	if !ok {
		s.sendResponse(msg.ID, map[string]any{"data": []int{}})
		return
	}

	tokens := syntax.Tokenize(src)
	data := encodeSemanticTokens(src, tokens)
	s.sendResponse(msg.ID, map[string]any{"data": data})
}

// --- Formatting ---

type formattingParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) handleFormatting(msg *jsonrpcMessage) {
	var params formattingParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.sendError(msg.ID, -32602, "invalid params")
		return
	}

	s.docsMu.Lock()
	src, ok := s.docs[params.TextDocument.URI]
	s.docsMu.Unlock()
	if !ok {
		s.sendResponse(msg.ID, []any{})
		return
	}

	formatted, err := formatter.Format(src)
	if err != nil {
		s.sendError(msg.ID, -32603, "format error: "+err.Error())
		return
	}

	if formatted == src {
		s.sendResponse(msg.ID, []any{})
		return
	}

	// Return a single whole-document replacement edit.
	endLine, endCol := offsetToLineCol(src, len(src))
	edits := []map[string]any{{
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": endLine, "character": endCol},
		},
		"newText": formatted,
	}}
	s.sendResponse(msg.ID, edits)
}

// --- On-type formatting ---

type onTypeFormattingParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
	Ch string `json:"ch"`
}

func (s *Server) handleOnTypeFormatting(msg *jsonrpcMessage) {
	var params onTypeFormattingParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.sendError(msg.ID, -32602, "invalid params")
		return
	}

	s.docsMu.Lock()
	src, ok := s.docs[params.TextDocument.URI]
	s.docsMu.Unlock()
	if !ok {
		s.sendResponse(msg.ID, []any{})
		return
	}

	line := params.Position.Line

	// Note: onTypeFormatting may arrive BEFORE didChange, so the document
	// in our map can be stale. We must not rely on the current line's
	// content. Instead, compute depth from the PREVIOUS line (which is
	// the same in both old and new documents) and use position.Character
	// from the request to know the replace range.

	if params.Ch == "\n" {
		// User typed Enter. Cursor is at (line, character) on the new line.
		// Compute depth from the line above (where Enter was pressed).
		prevLine := line - 1
		if prevLine < 0 {
			s.sendResponse(msg.ID, []any{})
			return
		}
		prevLineStart := lineColToOffset(src, prevLine, 0)
		prevLineEnd := prevLineStart
		for prevLineEnd < len(src) && src[prevLineEnd] != '\n' {
			prevLineEnd++
		}
		// When the document is stale (didChange hasn't arrived yet), the
		// previous line may still contain the auto-closed {} pair that the
		// user pressed Enter inside. Exclude the trailing } so depth
		// reflects the opening brace only — the } will move to its own
		// line after the editor processes the Enter.
		depthEnd := prevLineEnd
		trimmed := strings.TrimRight(src[prevLineStart:prevLineEnd], " \t")
		if strings.HasSuffix(trimmed, "{}") {
			depthEnd = prevLineStart + len(trimmed) - 1
		}
		depth := braceDepth(src[:depthEnd])
		if depth < 0 {
			depth = 0
		}

		desired := strings.Repeat("    ", depth)

		// Replace any auto-indent the editor may have inserted (from
		// char 0 to cursor position) with the correct indentation.
		edits := []map[string]any{{
			"range": map[string]any{
				"start": map[string]any{"line": line, "character": 0},
				"end":   map[string]any{"line": line, "character": params.Position.Character},
			},
			"newText": desired,
		}}
		s.sendResponse(msg.ID, edits)
		return
	}

	if params.Ch == "}" {
		// User typed }. Cursor is at (line, character) right after the }.
		// Compute depth at the start of this line from lines above it.
		lineStart := lineColToOffset(src, line, 0)
		depth := braceDepth(src[:lineStart])
		depth-- // } closes a level
		if depth < 0 {
			depth = 0
		}

		desired := strings.Repeat("    ", depth)

		// Replace whitespace before the } (columns 0 to character-2,
		// since character-1 is the } itself).
		wsEnd := params.Position.Character - 1
		if wsEnd < 0 {
			wsEnd = 0
		}

		edits := []map[string]any{{
			"range": map[string]any{
				"start": map[string]any{"line": line, "character": 0},
				"end":   map[string]any{"line": line, "character": wsEnd},
			},
			"newText": desired,
		}}
		s.sendResponse(msg.ID, edits)
		return
	}

	s.sendResponse(msg.ID, []any{})
}

// braceDepth counts unmatched '{' in src, skipping strings and comments.
func braceDepth(src string) int {
	depth := 0
	inString := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			if c == '"' {
				inString = false
			} else if c == '\\' && i+1 < len(src) {
				i++ // skip escaped character
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}

// encodeSemanticTokens converts absolute token positions to the LSP
// relative encoding: [deltaLine, deltaStartChar, length, tokenType, tokenModifiers].
func encodeSemanticTokens(src string, tokens []syntax.SemanticToken) []int {
	data := make([]int, 0, len(tokens)*5)
	prevLine := 0
	prevCol := 0

	for _, tok := range tokens {
		line, col := offsetToLineCol(src, tok.Offset)
		deltaLine := line - prevLine
		deltaCol := col
		if deltaLine == 0 {
			deltaCol = col - prevCol
		}
		data = append(data, deltaLine, deltaCol, tok.Length, int(tok.Type), int(tok.Modifiers))
		prevLine = line
		prevCol = col
	}
	return data
}

// offsetToLineCol converts a byte offset to 0-based line and column.
func offsetToLineCol(src string, offset int) (line, col int) {
	for i := 0; i < offset && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return line, col
}
