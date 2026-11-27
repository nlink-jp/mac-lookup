package app

import (
	"context"
	"io"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"
)

// SIGINT and SIGTERM end the MCP server through its ctx, even while stdin is
// open (server-migration §2); both are registered. Served on context.Background(), a signal
// killed the process instead, and this waits in vain.
func TestASignalEndsTheMCPServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	defer func(orig func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)) {
		notifyContext = orig
	}(notifyContext)
	var registered []os.Signal
	notifyContext = func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
		registered = sigs
		ctx, cancel := context.WithCancel(parent)
		go func() { time.Sleep(100 * time.Millisecond); cancel() }() // the signal arrives
		return ctx, cancel
	}
	inR, inW := io.Pipe()
	defer func() { _ = inW.Close() }()
	defer func(in io.Reader, out io.Writer) { mcpStdin, mcpStdout = in, out }(mcpStdin, mcpStdout)
	mcpStdin, mcpStdout = inR, io.Discard
	done := make(chan int, 1)
	go func() { done <- cmdMCP(nil, "test") }()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the MCP server did not end on the signal")
	}
	for _, want := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !slices.Contains(registered, want) {
			t.Errorf("%v is not among the signals that end the server: %v", want, registered)
		}
	}
}
