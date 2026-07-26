// Package mcp implements a minimal Model Context Protocol server over stdio.
// It speaks newline-delimited JSON-RPC 2.0 (the MCP stdio transport) using only
// the standard library, exposing mac-lookup's cached registry as MCP tools.
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/nlink-jp/mac-lookup/internal/engine"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// defaultProtocolVersion is advertised when the client sends none.
const defaultProtocolVersion = "2025-06-18"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // absent/null ⇒ notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// toolResult is the tools/call payload.
type toolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResult(isErr bool, text string) toolResult {
	return toolResult{Content: []contentItem{{Type: "text", Text: text}}, IsError: isErr}
}

// server holds the engine and a modtime-keyed database cache so repeated tool
// calls do not reparse the ~58,000-entry store, while still picking up
// update_db changes.
type server struct {
	e       *engine.Engine
	version string
	db      *ouidb.DB
	dbMod   time.Time
}

// Serve runs the MCP protocol loop until in reaches EOF. It is safe to point in
// at os.Stdin and out at os.Stdout; diagnostics must go to stderr only.
func Serve(ctx context.Context, e *engine.Engine, version string, in io.Reader, out io.Writer) error {
	s := &server{e: e, version: version}
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			// The stream position is unrecoverable after malformed JSON; report
			// a parse error and stop rather than spin on the same bytes.
			_ = enc.Encode(response{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			return nil
		}
		resp, skip := s.handle(ctx, &req)
		if skip {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
}

func (s *server) handle(ctx context.Context, req *request) (response, bool) {
	// A missing or null id marks a JSON-RPC notification: never reply.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return response{}, true
	}
	switch req.Method {
	case "initialize":
		return s.ok(req.ID, s.initializeResult(req.Params)), false
	case "ping":
		return s.ok(req.ID, struct{}{}), false
	case "tools/list":
		return s.ok(req.ID, s.toolsList()), false
	case "tools/call":
		res, rerr := s.toolsCall(ctx, req.Params)
		if rerr != nil {
			return response{JSONRPC: "2.0", ID: req.ID, Error: rerr}, false
		}
		return s.ok(req.ID, res), false
	default:
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}, false
	}
}

func (s *server) ok(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *server) initializeResult(params json.RawMessage) any {
	pv := defaultProtocolVersion
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
			pv = p.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": pv,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "mac-lookup", "version": s.version},
		"instructions":    Instructions,
	}
}

// database returns the cached registry, reloading it when the store changed on
// disk (for example after update_db).
func (s *server) database() (*ouidb.DB, error) {
	fi, err := os.Stat(s.e.Cfg.StorePath)
	if err != nil {
		// Fall through to LoadDB so the caller gets the wrapped ErrNoDB message.
		return s.e.LoadDB()
	}
	if s.db != nil && fi.ModTime().Equal(s.dbMod) {
		return s.db, nil
	}
	db, err := s.e.LoadDB()
	if err != nil {
		return nil, err
	}
	s.db, s.dbMod = db, fi.ModTime()
	return db, nil
}
