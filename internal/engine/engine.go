// Package engine ties configuration, downloading, and the on-disk registry
// store together. The CLI and the MCP server both drive the same Engine so
// their behaviour cannot diverge.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/nlink-jp/mac-lookup/internal/config"
	"github.com/nlink-jp/mac-lookup/internal/ieee"
	"github.com/nlink-jp/mac-lookup/internal/macaddr"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// StaleAfter is the age past which the cached registry is reported as stale by
// `status` (independent of the auto-refetch TTL). IEEE regenerates the files
// about once a day, so a week of drift is enough to miss new assignments.
const StaleAfter = 7 * 24 * time.Hour

// Errors surfaced to callers for friendly handling.
var (
	// ErrNoDB means no registry has been downloaded yet; the caller should
	// suggest `update`.
	ErrNoDB = errors.New("no local IEEE registry cache")
	// ErrInvalidAddress means the queried string is not a MAC address, BSSID, or
	// registry prefix.
	ErrInvalidAddress = macaddr.ErrInvalid
)

// StatusNotApplicable extends ouidb.MatchStatus for addresses no registry can
// speak to: broadcast, multicast, and locally administered addresses that match
// no CID. It is a distinct answer from "not found" — nothing was missed, there
// is simply nothing to find.
const StatusNotApplicable ouidb.MatchStatus = "not_applicable"

// Engine performs load, update, and resolution against the configured store.
type Engine struct {
	Cfg     *config.Config
	Fetcher ieee.Fetcher
	Now     func() time.Time // injectable clock; defaults to time.Now
}

// New returns an Engine with the given config and fetcher.
func New(cfg *config.Config, fetcher ieee.Fetcher) *Engine {
	return &Engine{Cfg: cfg, Fetcher: fetcher, Now: time.Now}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// LoadDB reads and opens the local registry store. It returns ErrNoDB (wrapped)
// when the file does not exist.
func (e *Engine) LoadDB() (*ouidb.DB, error) {
	data, err := os.ReadFile(e.Cfg.StorePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNoDB, e.Cfg.StorePath)
		}
		return nil, err
	}
	return ouidb.Open(data)
}

// UpdateResult reports what an Update produced.
//
// Unchanged and unreachable are kept apart deliberately: both leave the cached
// entries in place, but "nothing changed upstream" and "we could not reach
// IEEE and are serving week-old data" call for very different reactions.
type UpdateResult struct {
	Counts      map[ouidb.Registry]int
	Total       int
	Skipped     int
	NotModified []ouidb.Registry // served from cache after a 304
	Fallback    []ouidb.Registry // served from cache after a failed fetch
	Warnings    []string
	Generated   time.Time
	StorePath   string
	Downloaded  int // registries actually re-parsed
}

// Update refetches every registry and atomically replaces the local store.
//
// Each registry is fetched conditionally, so an unchanged file costs a 304
// rather than a download. A registry that 304s, or that fails to fetch when a
// previous copy exists, keeps its cached entries — losing MA-S on a transient
// error would silently turn precise vendor answers into "subdivided", so the
// old data is strictly better than none. Only MA-L is indispensable: without it
// there is no database at all.
func (e *Engine) Update(ctx context.Context) (UpdateResult, error) {
	prev, prevErr := e.LoadDB()
	if prevErr != nil && !errors.Is(prevErr, ErrNoDB) {
		// A corrupt store must not block a refresh; rebuild from scratch.
		prev = nil
	}

	res := UpdateResult{Counts: map[ouidb.Registry]int{}, StorePath: e.Cfg.StorePath}
	var entries []ouidb.Entry
	var sources []ouidb.Source

	for _, rf := range ieee.RegistryFiles {
		url := ieee.URLFor(e.Cfg.BaseURL, rf.Path)
		got, src, skipped, err := e.fetchRegistry(ctx, rf, url, prev)
		if err != nil {
			if rf.Registry == ouidb.RegistryMAL {
				return UpdateResult{}, fmt.Errorf("%s is required: %w", rf.Registry, err)
			}
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", rf.Registry, err))
			continue
		}
		switch {
		case src.ReuseErr != nil:
			res.Fallback = append(res.Fallback, rf.Registry)
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: fetch failed, kept the cached copy: %v", rf.Registry, src.ReuseErr))
		case src.Reused:
			res.NotModified = append(res.NotModified, rf.Registry)
		default:
			res.Downloaded++
		}
		entries = append(entries, got...)
		sources = append(sources, src.Source)
		res.Counts[rf.Registry] = len(got)
		res.Skipped += skipped
	}

	generatedUnix := e.now().Unix()
	db := ouidb.New(entries, sources, generatedUnix)
	if err := e.writeStore(db); err != nil {
		return UpdateResult{}, err
	}

	res.Total = db.Len()
	res.Generated = db.Generated()
	return res, nil
}

// registrySource pairs a source record with how it was obtained.
type registrySource struct {
	Source ouidb.Source
	// Reused means the entries came from the previous store rather than the
	// wire. ReuseErr, when set, is the fetch failure that forced it — the
	// difference between "unchanged" and "unreachable".
	Reused   bool
	ReuseErr error
}

// fetchRegistry retrieves one registry, reusing the previous copy on 304 or on
// a soft failure.
func (e *Engine) fetchRegistry(ctx context.Context, rf ieee.RegistryFile, url string, prev *ouidb.DB) ([]ouidb.Entry, registrySource, int, error) {
	cond := ieee.Conditional{}
	if prev != nil {
		for _, s := range prev.Sources {
			if s.Registry == rf.Registry && s.URL == url {
				cond = ieee.Conditional{ETag: s.ETag, LastModified: s.LastModified}
				break
			}
		}
	}

	resp, err := e.Fetcher.Fetch(ctx, url, cond)
	if err != nil {
		if cached, src, ok := cachedRegistry(prev, rf.Registry, url); ok {
			// Keep what we had: stale entries beat a hole in the database. The
			// error travels with them so the caller can say the data is stale
			// rather than merely unchanged.
			return cached, registrySource{Source: src, Reused: true, ReuseErr: err}, 0, nil
		}
		return nil, registrySource{}, 0, err
	}
	if resp.NotModified {
		if cached, src, ok := cachedRegistry(prev, rf.Registry, url); ok {
			return cached, registrySource{Source: src, Reused: true}, 0, nil
		}
		// A 304 with nothing cached should not happen (we only send validators
		// we hold), but refetching unconditionally is the safe recovery.
		resp, err = e.Fetcher.Fetch(ctx, url, ieee.Conditional{})
		if err != nil {
			return nil, registrySource{}, 0, err
		}
	}
	defer resp.Body.Close()

	got, skipped, err := ouidb.ParseCSV(resp.Body, rf.Registry)
	if err != nil {
		return nil, registrySource{}, 0, err
	}
	return got, registrySource{Source: ouidb.Source{
		Registry:     rf.Registry,
		URL:          url,
		ETag:         resp.ETag,
		LastModified: resp.LastModified,
		Count:        len(got),
	}}, skipped, nil
}

// cachedRegistry extracts one registry's entries and source record from a
// previously loaded database.
func cachedRegistry(prev *ouidb.DB, reg ouidb.Registry, url string) ([]ouidb.Entry, ouidb.Source, bool) {
	if prev == nil {
		return nil, ouidb.Source{}, false
	}
	var src ouidb.Source
	found := false
	for _, s := range prev.Sources {
		if s.Registry == reg && s.URL == url {
			src, found = s, true
			break
		}
	}
	if !found {
		return nil, ouidb.Source{}, false
	}
	var out []ouidb.Entry
	for _, en := range prev.Entries {
		if en.Registry == reg {
			out = append(out, en)
		}
	}
	if len(out) == 0 {
		return nil, ouidb.Source{}, false
	}
	src.Count = len(out)
	return out, src, true
}

// writeStore serializes db to the store path via temp + rename so a crash
// mid-write never leaves a truncated store to be read back.
func (e *Engine) writeStore(db *ouidb.DB) error {
	dir := filepath.Dir(e.Cfg.StorePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "ouidb-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	err = ouidb.Serialize(tmp, db)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, e.Cfg.StorePath); err != nil {
		return fmt.Errorf("install store: %w", err)
	}
	return nil
}

// EnsureFresh returns a usable DB, refetching first when the cached registry is
// missing or older than ttl. A refetch failure is non-fatal when a cached copy
// already exists: the stale DB is returned alongside the error so the caller can
// warn and continue offline. Only a total absence of data is a hard error.
func (e *Engine) EnsureFresh(ctx context.Context, ttl time.Duration) (db *ouidb.DB, refreshed bool, err error) {
	db, loadErr := e.LoadDB()
	switch {
	case loadErr == nil:
		if e.now().Sub(db.Generated()) <= ttl {
			return db, false, nil // fresh enough
		}
	case errors.Is(loadErr, ErrNoDB):
		db = nil // must fetch
	default:
		return nil, false, loadErr
	}

	if _, uerr := e.Update(ctx); uerr != nil {
		return db, false, uerr // db may be a stale fallback, or nil
	}
	fresh, lerr := e.LoadDB()
	if lerr != nil {
		return nil, false, lerr
	}
	return fresh, true, nil
}

// IsStale reports whether a store generated at gen is older than StaleAfter
// relative to the engine's clock, and the age.
func (e *Engine) IsStale(gen time.Time) (bool, time.Duration) {
	age := e.now().Sub(gen)
	return age > StaleAfter, age
}

// Result is the outcome of resolving one address.
type Result struct {
	Address macaddr.Address
	// Status is a ouidb.MatchStatus, or StatusNotApplicable.
	Status ouidb.MatchStatus
	// Entry is the matched assignment; valid only when HasEntry is set.
	Entry    ouidb.Entry
	HasEntry bool
	// WellKnown names a reserved or protocol range covering the address.
	WellKnown    macaddr.WellKnown
	HasWellKnown bool
}

// VendorName returns the registrant name when one was actually found, and "" in
// every other case. Callers must not print an empty vendor as if the lookup
// merely missed — check Status.
func (r Result) VendorName() string {
	if r.HasEntry && !r.Entry.Private() && !r.Entry.RegistryHeld() {
		return r.Entry.Organization
	}
	return ""
}

// Resolve classifies an address and, where that makes sense, matches it against
// the registries.
//
// The order is the product: broadcast and multicast have no manufacturer;
// a locally administered address has no IEEE assignment and is checked only
// against CID, the registry that exists precisely for such addresses; and only
// a universally administered unicast address is matched longest-prefix-first
// against the OUI registries.
func Resolve(db *ouidb.DB, input string) (Result, error) {
	addr, err := macaddr.Parse(input)
	if err != nil {
		return Result{}, err
	}
	r := Result{Address: addr}
	if wk, ok := addr.WellKnown(); ok {
		r.WellKnown, r.HasWellKnown = wk, true
	}

	switch {
	case addr.Cast != macaddr.CastUnicast:
		// Broadcast and multicast destinations identify a group, not a device.
		r.Status = StatusNotApplicable
	case addr.Administration == macaddr.AdminLocal:
		// Randomized MACs, virtual NICs, containers. CID is the only registry
		// that can name an assigner here, and most such addresses match nothing.
		entry, st := db.LookupCID(addr.Hex)
		if st == ouidb.MatchNotFound {
			r.Status = StatusNotApplicable
		} else {
			r.Status, r.Entry, r.HasEntry = st, entry, true
		}
	default:
		entry, st := db.Lookup(addr.Hex)
		r.Status = st
		if st != ouidb.MatchNotFound {
			r.Entry, r.HasEntry = entry, true
		}
	}
	return r, nil
}

// SearchVendor finds assignments whose registrant name contains query.
func SearchVendor(db *ouidb.DB, query string, limit int) ([]ouidb.Entry, int) {
	return db.Search(query, limit)
}

// SortedRegistries returns the registry names present in counts, in the fixed
// order the registries are documented in, so output is stable.
func SortedRegistries(counts map[ouidb.Registry]int) []ouidb.Registry {
	order := map[ouidb.Registry]int{
		ouidb.RegistryMAL: 0, ouidb.RegistryMAM: 1, ouidb.RegistryMAS: 2,
		ouidb.RegistryIAB: 3, ouidb.RegistryCID: 4,
	}
	out := make([]ouidb.Registry, 0, len(counts))
	for r := range counts {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oki := order[out[i]]
		oj, okj := order[out[j]]
		if oki && okj {
			return oi < oj
		}
		return out[i] < out[j]
	})
	return out
}
