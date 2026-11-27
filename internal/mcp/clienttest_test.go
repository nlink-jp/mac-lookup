package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/mcp"
	"github.com/nlink-jp/mcp/client"
	"github.com/nlink-jp/mcp/probe"
)

// serveFunc runs the server under test on in and out until ctx ends or in
// reaches EOF — a closure over Serve with the test's engine.
type serveFunc func(ctx context.Context, in io.Reader, out io.Writer) error

// openWith serves the server over pipes and opens it with nlink-jp/mcp's
// client, the way a dual-era MCP client does (server-migration §6). Nothing
// here writes the handshake by hand.
func openWith(t *testing.T, serve serveFunc, opts client.Options) *client.Client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, inR, outW)
		_ = outW.Close()
	}()
	kill := func() { cancel(); _ = inW.Close(); _ = outR.Close() }
	// Registered before Open, so a failed open leaves no goroutine or pipe.
	var cl *client.Client
	t.Cleanup(func() {
		if cl != nil {
			_ = cl.Close()
		} else {
			kill()
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after the client closed")
		}
		kill()
	})
	conn, err := client.NewLineConn(outR, inW, kill, client.LineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	octx, ocancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ocancel()
	opts.Client = mcp.Implementation{Name: "test", Version: "0"}
	c, err := client.Open(octx, conn, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cl = c
	return cl
}

// serveOverClient replays newline-separated JSON-RPC requests — the tests'
// own request lines — through nlink-jp/mcp's client against the server, and
// returns the answers in the JSON-RPC shape the tests read. initialize is
// answered from what the client learnt when it opened the server; a
// notification gets nothing. Protocol details (parse errors, unknown methods,
// version negotiation) are the module's to answer and are tested there; a
// request for anything else fails the test.
func serveOverClient(t *testing.T, serve serveFunc, input string) *bytes.Buffer {
	t.Helper()
	cl := openWith(t, serve, client.Options{})
	var out bytes.Buffer
	sc := bufio.NewScanner(strings.NewReader(input))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			t.Fatalf("not a request: %s", line)
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue // a notification: nothing to answer
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		answer := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			answer["result"] = map[string]any{
				"protocolVersion": cl.Version(),
				"serverInfo":      cl.Server(),
				"instructions":    cl.Instructions(),
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}
		case "ping":
			answer["result"] = map[string]any{}
		case "tools/list":
			list, err := cl.ListTools(ctx)
			if err != nil {
				t.Fatalf("tools/list: %v", err)
			}
			tools := []json.RawMessage{}
			for _, tl := range list.Tools {
				tools = append(tools, tl.Raw)
			}
			answer["result"] = map[string]any{"tools": tools}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			var args any
			if len(p.Arguments) > 0 {
				args = p.Arguments
			}
			res, err := cl.CallTool(ctx, p.Name, args, nil)
			var rpcErr *mcp.Error
			switch {
			case errors.As(err, &rpcErr):
				answer["error"] = map[string]any{"code": rpcErr.Code, "message": rpcErr.Message}
			case err != nil:
				t.Fatalf("tools/call %s: %v", p.Name, err)
			default:
				result := map[string]any{"content": res.Content}
				if res.IsError {
					result["isError"] = true
				}
				answer["result"] = result
			}
		default:
			t.Fatalf("%s is the protocol module's to answer; it is tested there", req.Method)
		}
		cancel()
		b, err := json.Marshal(answer)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(append(b, '\n'))
	}
	return &out
}

// probeMatrix runs the three probe clients of ADR-030 §7 — legacy only,
// dual-era, modern only — against the server, expecting the outcomes the
// specification's compatibility matrix gives for a dual-era server.
func probeMatrix(t *testing.T, serve serveFunc) {
	t.Helper()
	for _, kind := range []probe.ClientKind{probe.ClientLegacyOnly, probe.ClientDual, probe.ClientModernOnly} {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = serve(ctx, inR, outW); _ = outW.Close(); close(done) }()
		r := probe.Run(ctx, kind, outR, inW, probe.ClientOptions{StepTimeout: 2 * time.Second})
		cancel()
		_ = inW.Close()
		_ = outR.Close()
		<-done
		if want := probe.Expect(kind, probe.ServerDual); r.Outcome != want || len(r.Violations) != 0 || len(r.Warnings) != 0 {
			t.Errorf("%v probe client: outcome %s (want %s), violations %v, warnings %v, error %s",
				kind, r.Outcome, want, r.Violations, r.Warnings, r.Error)
		}
	}
}
