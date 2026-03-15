// Package lsp implements a Language Server Protocol server for the doit
// language. It provides semantic token highlighting via the compiler's
// tokenizer and communicates over JSON-RPC 2.0 on stdio.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

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
}

// NewServer creates a new language server using the given reader and writer
// for JSON-RPC communication.
func NewServer(r io.Reader, w io.Writer) *Server {
	return &Server{
		reader: bufio.NewReader(r),
		writer: w,
		docs:   make(map[string]string),
	}
}

// Run starts the language server, reading JSON-RPC messages from stdin
// and writing responses to stdout. It blocks until the exit notification
// is received or an I/O error occurs.
func Run() error {
	s := NewServer(os.Stdin, os.Stdout)
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
	s.docsMu.Lock()
	s.docs[params.TextDocument.URI] = params.ContentChanges[len(params.ContentChanges)-1].Text
	s.docsMu.Unlock()
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
