package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nlink-jp/mcp/client"

	"github.com/nlink-jp/mac-lookup/internal/ieee"
)

// waitingFetcher stands in for a download still in flight: it returns only
// when its ctx is cancelled (or after a while), and says which.
type waitingFetcher struct{ cancelled chan bool }

func (w waitingFetcher) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		w.cancelled <- true
		return ctx.Err()
	case <-time.After(3 * time.Second):
		w.cancelled <- false
		return errors.New("not cancelled")
	}
}

func (w waitingFetcher) Fetch(ctx context.Context, _ string, _ ieee.Conditional) (*ieee.Response, error) {
	return nil, w.wait(ctx)
}

// An update cancelled midway (notifications/cancelled) stops its download and
// leaves the stored registry as it was, with no temporary file beside it
// (ADR-0001 §5; server-migration §5). Detaching the update from the call's ctx
// would let the download run on, and fails here.
func TestACancelledUpdateLeavesTheStoreAsItWas(t *testing.T) {
	e, _ := newServer(t, true)
	before, err := os.ReadFile(e.Cfg.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	wf := waitingFetcher{cancelled: make(chan bool, 4)}
	e.Fetcher = wf
	cl := openWith(t, func(ctx context.Context, in io.Reader, w io.Writer) error {
		return Serve(ctx, e, "test", in, w)
	}, client.Options{CancelNotice: true})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := cl.CallTool(ctx, "update_db", nil, nil); err == nil {
		t.Fatal("the cancelled update_db returned a result")
	}
	select {
	case got := <-wf.cancelled:
		if !got {
			t.Error("the download did not see the cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the download never returned")
	}
	time.Sleep(500 * time.Millisecond) // the handler finishing whatever follows the download
	after, err := os.ReadFile(e.Cfg.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the store changed although the update was cancelled")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(e.Cfg.StorePath), "*.tmp"))
	if len(left) != 0 {
		t.Errorf("temporary files left beside the store: %v", left)
	}
}
