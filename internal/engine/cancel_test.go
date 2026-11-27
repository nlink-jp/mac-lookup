package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nlink-jp/mac-lookup/internal/ieee"
)

// cancellingFetcher serves the fake registries and cancels the update at the
// n-th registry: before answering (inFetch) or part-way through its body.
type cancellingFetcher struct {
	*fakeFetcher
	n       int
	inFetch bool
	cancel  context.CancelFunc
	count   int
}

func (c *cancellingFetcher) Fetch(ctx context.Context, rawURL string, cond ieee.Conditional) (*ieee.Response, error) {
	c.count++
	if c.count != c.n {
		return c.fakeFetcher.Fetch(ctx, rawURL, cond)
	}
	if c.inFetch {
		c.cancel()
		return nil, ctx.Err()
	}
	resp, err := c.fakeFetcher.Fetch(ctx, rawURL, cond)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(io.MultiReader(io.LimitReader(resp.Body, 20), &cancelReader{c.cancel}))
	return resp, nil
}

type cancelReader struct{ cancel context.CancelFunc }

func (r *cancelReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, context.Canceled
}

// A cancellation at any registry, not only MA-L, stops the update and leaves
// the store as it was. It used to stop only at MA-L: one at MA-M or later
// was dropped with a warning and the store was rewritten without the
// registries still to come — MA-S lost, precise answers turned "subdivided".
func TestACancelledUpdateAtAnyRegistryLeavesTheStore(t *testing.T) {
	for n := 1; n <= len(ieee.RegistryFiles); n++ {
		for _, inFetch := range []bool{true, false} {
			f := newFakeFetcher()
			f.populate()
			e := newEngine(t, f)
			if _, err := e.Update(context.Background()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(e.Cfg.StorePath)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			e.Fetcher = &cancellingFetcher{fakeFetcher: f, n: n, inFetch: inFetch, cancel: cancel}
			e.Now = func() time.Time { return time.Unix(1753488000+3600, 0).UTC() }
			_, err = e.Update(ctx)
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Errorf("registry %d (in fetch %v): err = %v, want context.Canceled", n, inFetch, err)
			}
			after, _ := os.ReadFile(e.Cfg.StorePath)
			if !bytes.Equal(before, after) {
				t.Errorf("registry %d (in fetch %v): the store was rewritten", n, inFetch)
			}
		}
	}
}
