package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/mac-lookup/internal/config"
	"github.com/nlink-jp/mac-lookup/internal/ieee"
	"github.com/nlink-jp/mac-lookup/internal/macaddr"
	"github.com/nlink-jp/mac-lookup/internal/ouidb"
)

// --- test fetcher -----------------------------------------------------------

// fakeFetcher serves canned registry bodies and records what was requested, so
// the engine can be exercised without touching the network.
type fakeFetcher struct {
	bodies      map[string]string  // URL suffix → CSV body
	etags       map[string]string  // URL suffix → ETag to advertise
	fail        map[string]error   // URL suffix → error to return
	notModified map[string]bool    // URL suffix → answer 304
	seen        []ieee.Conditional // conditionals received, in order
	seenURLs    []string           //
	calls       map[string]int     // URL suffix → call count
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		bodies:      map[string]string{},
		etags:       map[string]string{},
		fail:        map[string]error{},
		notModified: map[string]bool{},
		calls:       map[string]int{},
	}
}

func key(rawURL string) string {
	i := strings.Index(rawURL, "://")
	rest := rawURL[i+3:]
	return rest[strings.Index(rest, "/"):]
}

func (f *fakeFetcher) Fetch(_ context.Context, rawURL string, cond ieee.Conditional) (*ieee.Response, error) {
	k := key(rawURL)
	f.calls[k]++
	f.seen = append(f.seen, cond)
	f.seenURLs = append(f.seenURLs, k)
	if err := f.fail[k]; err != nil {
		return nil, err
	}
	if f.notModified[k] && cond.ETag != "" {
		return &ieee.Response{NotModified: true, ETag: cond.ETag, LastModified: cond.LastModified}, nil
	}
	body, ok := f.bodies[k]
	if !ok {
		body = "Registry,Assignment,Organization Name,Organization Address\n"
	}
	return &ieee.Response{
		Body:         io.NopCloser(strings.NewReader(body)),
		ETag:         f.etags[k],
		LastModified: "Sun, 26 Jul 2026 00:01:23 GMT",
	}, nil
}

func header() string {
	return "Registry,Assignment,Organization Name,Organization Address\n"
}

// populate gives the fetcher a small but structurally faithful registry set:
// 8C:1F:64 is an IEEE-held block subdivided into MA-S assignments, 74:1A:E0 is
// subdivided into a Private MA-M assignment, and 28:6F:B9 is a plain MA-L.
func (f *fakeFetcher) populate() {
	f.bodies["/oui/oui.csv"] = header() +
		"MA-L,286FB9,\"Nokia Shanghai Bell Co., Ltd.\",Shanghai CN\n" +
		"MA-L,8C1F64,IEEE Registration Authority,445 Hoes Lane Piscataway NJ US 08554\n" +
		"MA-L,741AE0,IEEE Registration Authority,445 Hoes Lane Piscataway NJ US 08554\n"
	f.bodies["/oui28/mam.csv"] = header() + "MA-M,741AE09,Private,\n"
	f.bodies["/oui36/oui36.csv"] = header() + "MA-S,8C1F64AFA,\"DATA ELECTRONIC DEVICES, INC\",SALEM NH US\n"
	f.bodies["/iab/iab.csv"] = header() + "IAB,0050C2F71,RF Code,Austin TX US\n"
	f.bodies["/cid/cid.csv"] = header() + "CID,EA2701,ACCE Technology Corp.,Hsinchu TW\n"
	for k := range f.bodies {
		f.etags[k] = `"etag-` + k + `"`
	}
}

func newEngine(t *testing.T, f ieee.Fetcher) *Engine {
	t.Helper()
	cfg := &config.Config{
		BaseURL:    "https://registry.invalid",
		StorePath:  filepath.Join(t.TempDir(), "ouidb.json"),
		TTL:        config.DefaultTTL,
		AutoUpdate: true,
	}
	e := New(cfg, f)
	e.Now = func() time.Time { return time.Unix(1753488000, 0).UTC() }
	return e
}

// --- store lifecycle --------------------------------------------------------

func TestLoadDBWithoutStore(t *testing.T) {
	e := newEngine(t, newFakeFetcher())
	_, err := e.LoadDB()
	if !errors.Is(err, ErrNoDB) {
		t.Fatalf("LoadDB error = %v, want ErrNoDB", err)
	}
	// The message must name the path so the operator can see where it looked.
	if !strings.Contains(err.Error(), e.Cfg.StorePath) {
		t.Errorf("error does not name the store path: %v", err)
	}
}

func TestUpdateFetchesEveryRegistry(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	e := newEngine(t, f)

	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if res.Total != 7 {
		t.Errorf("Total = %d, want 7", res.Total)
	}
	if res.Downloaded != 5 {
		t.Errorf("Downloaded = %d, want 5", res.Downloaded)
	}
	for _, rf := range ieee.RegistryFiles {
		if res.Counts[rf.Registry] == 0 {
			t.Errorf("registry %q contributed no entries", rf.Registry)
		}
	}
	if _, err := os.Stat(e.Cfg.StorePath); err != nil {
		t.Errorf("store was not written: %v", err)
	}
}

func TestUpdateSendsValidatorsOnRefresh(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatalf("first Update returned error: %v", err)
	}
	// The first pass has nothing to revalidate against.
	for i, c := range f.seen {
		if c.ETag != "" {
			t.Fatalf("first Update sent a validator for %s", f.seenURLs[i])
		}
	}

	f.seen, f.seenURLs = nil, nil
	for k := range f.bodies {
		f.notModified[k] = true
	}
	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("second Update returned error: %v", err)
	}
	for i, c := range f.seen {
		if c.ETag == "" {
			t.Errorf("second Update sent no validator for %s", f.seenURLs[i])
		}
	}
	// A 304 must reuse the cached entries, not empty the database.
	if len(res.NotModified) != 5 || res.Downloaded != 0 {
		t.Errorf("NotModified = %v, Downloaded = %d; want 5 / 0", res.NotModified, res.Downloaded)
	}
	if res.Total != 7 {
		t.Errorf("Total after 304s = %d, want 7", res.Total)
	}
	// Nothing was wrong, so nothing may be warned about.
	if len(res.Fallback) != 0 || len(res.Warnings) != 0 {
		t.Errorf("a clean 304 produced Fallback=%v Warnings=%v", res.Fallback, res.Warnings)
	}
}

func TestUpdateKeepsCachedRegistryOnSoftFailure(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatalf("first Update returned error: %v", err)
	}

	// MA-S goes away transiently. Dropping it would silently downgrade precise
	// vendor answers to "subdivided", so the cached copy must survive.
	f.fail["/oui36/oui36.csv"] = errors.New("connection reset")
	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if res.Counts[ouidb.RegistryMAS] != 1 {
		t.Errorf("MA-S count = %d, want the cached 1", res.Counts[ouidb.RegistryMAS])
	}
	db, err := e.LoadDB()
	if err != nil {
		t.Fatal(err)
	}
	if _, st := db.Lookup("8C1F64AFA001"); st != ouidb.MatchVendor {
		t.Errorf("vendor lost after a transient MA-S failure: status = %q", st)
	}

	// Serving cached data because IEEE was unreachable must not look like a
	// clean "nothing changed" — the operator has to know the data is stale.
	if len(res.Fallback) != 1 || res.Fallback[0] != ouidb.RegistryMAS {
		t.Errorf("Fallback = %v, want [MA-S]", res.Fallback)
	}
	if len(res.NotModified) != 0 {
		t.Errorf("NotModified = %v, want the failure not to be counted as unchanged", res.NotModified)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "MA-S") || !strings.Contains(res.Warnings[0], "connection reset") {
		t.Errorf("Warnings = %v, want one naming MA-S and the cause", res.Warnings)
	}
}

func TestUpdateFailsHardWithoutMAL(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	f.fail["/oui/oui.csv"] = errors.New("connection reset")
	e := newEngine(t, f)

	if _, err := e.Update(context.Background()); err == nil {
		t.Fatal("Update succeeded without MA-L")
	}
	// Nothing may be written when the indispensable registry is missing.
	if _, err := os.Stat(e.Cfg.StorePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("a store was written despite the MA-L failure")
	}
}

func TestUpdateWarnsOnOptionalRegistryLoss(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	// No prior cache, and CID is unavailable: the update proceeds, degraded.
	f.fail["/cid/cid.csv"] = errors.New("503")
	e := newEngine(t, f)

	res, err := e.Update(context.Background())
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "CID") {
		t.Errorf("Warnings = %v, want one naming CID", res.Warnings)
	}
	if res.Counts[ouidb.RegistryCID] != 0 {
		t.Errorf("CID count = %d, want 0", res.Counts[ouidb.RegistryCID])
	}
}

func TestEnsureFreshRefetchesStale(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Still fresh: no refetch.
	before := f.calls["/oui/oui.csv"]
	if _, refreshed, err := e.EnsureFresh(context.Background(), config.DefaultTTL); err != nil || refreshed {
		t.Errorf("EnsureFresh refreshed a fresh store (refreshed=%v, err=%v)", refreshed, err)
	}
	if f.calls["/oui/oui.csv"] != before {
		t.Error("a fresh store still triggered a fetch")
	}

	// Move the clock past the TTL: a refetch is expected.
	e.Now = func() time.Time { return time.Unix(1753488000, 0).Add(48 * time.Hour) }
	_, refreshed, err := e.EnsureFresh(context.Background(), config.DefaultTTL)
	if err != nil {
		t.Fatalf("EnsureFresh returned error: %v", err)
	}
	if !refreshed {
		t.Error("a stale store was not refreshed")
	}
}

func TestEnsureFreshStaysUsableWhenOffline(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Offline with a stale cache: the lookup must still work. Every registry
	// falls back to its cached copy, so the refresh completes in a degraded
	// form rather than failing.
	e.Now = func() time.Time { return time.Unix(1753488000, 0).Add(48 * time.Hour) }
	for k := range f.bodies {
		f.fail[k] = errors.New("network unreachable")
	}
	db, _, err := e.EnsureFresh(context.Background(), config.DefaultTTL)
	if err != nil {
		t.Fatalf("EnsureFresh returned error: %v", err)
	}
	if db == nil {
		t.Fatal("no database returned")
	}
	if _, st := db.Lookup("286FB9000001"); st != ouidb.MatchVendor {
		t.Errorf("cached database is unusable offline: status = %q", st)
	}
}

func TestEnsureFreshFailsWithNoCacheAndNoNetwork(t *testing.T) {
	f := newFakeFetcher()
	f.populate()
	f.fail["/oui/oui.csv"] = errors.New("network unreachable")
	e := newEngine(t, f)

	// Nothing cached and nothing reachable is the one genuinely fatal case.
	db, refreshed, err := e.EnsureFresh(context.Background(), config.DefaultTTL)
	if err == nil {
		t.Fatal("EnsureFresh succeeded with no data at all")
	}
	if refreshed {
		t.Error("refreshed = true despite the failure")
	}
	if db != nil {
		t.Error("a database was returned with nothing cached")
	}
}

func TestIsStale(t *testing.T) {
	e := newEngine(t, newFakeFetcher())
	now := e.Now()
	if stale, _ := e.IsStale(now.Add(-time.Hour)); stale {
		t.Error("an hour-old store reported stale")
	}
	stale, age := e.IsStale(now.Add(-8 * 24 * time.Hour))
	if !stale {
		t.Error("an eight-day-old store not reported stale")
	}
	if age < 8*24*time.Hour {
		t.Errorf("age = %v, want at least 8 days", age)
	}
}

// --- resolution -------------------------------------------------------------

func loadedDB(t *testing.T) *ouidb.DB {
	t.Helper()
	f := newFakeFetcher()
	f.populate()
	e := newEngine(t, f)
	if _, err := e.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := e.LoadDB()
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestResolveVendor(t *testing.T) {
	db := loadedDB(t)
	r, err := Resolve(db, "8c:1f:64:af:a0:01")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if r.Status != ouidb.MatchVendor {
		t.Fatalf("Status = %q, want %q", r.Status, ouidb.MatchVendor)
	}
	if r.VendorName() != "DATA ELECTRONIC DEVICES, INC" {
		t.Errorf("VendorName = %q", r.VendorName())
	}
	if r.Entry.PrefixBits != 36 {
		t.Errorf("matched %d bits, want the 36-bit assignment", r.Entry.PrefixBits)
	}
	if !r.Address.VendorLookupApplicable() {
		t.Error("VendorLookupApplicable = false for a universal unicast address")
	}
}

func TestResolveSubdividedIsNotAVendor(t *testing.T) {
	db := loadedDB(t)
	r, err := Resolve(db, "8C:1F:64:00:00:01")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if r.Status != ouidb.MatchSubdivided {
		t.Fatalf("Status = %q, want %q", r.Status, ouidb.MatchSubdivided)
	}
	// "IEEE Registration Authority" is a block holder, not a manufacturer, and
	// must never be reported as the vendor.
	if r.VendorName() != "" {
		t.Errorf("VendorName = %q, want empty for a registry-held block", r.VendorName())
	}
}

func TestResolvePrivateIsNotAVendor(t *testing.T) {
	db := loadedDB(t)
	r, err := Resolve(db, "74:1A:E0:90:00:01")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if r.Status != ouidb.MatchPrivate {
		t.Fatalf("Status = %q, want %q", r.Status, ouidb.MatchPrivate)
	}
	if !r.HasEntry {
		t.Error("HasEntry = false; a Private row is still an assignment")
	}
	if r.VendorName() != "" {
		t.Errorf("VendorName = %q, want empty when the name is withheld", r.VendorName())
	}
}

func TestResolveLocallyAdministered(t *testing.T) {
	db := loadedDB(t)
	// A randomized MAC: there is nothing to find, and that is the answer.
	r, err := Resolve(db, "DA:A1:19:12:34:56")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if r.Status != StatusNotApplicable {
		t.Errorf("Status = %q, want %q", r.Status, StatusNotApplicable)
	}
	if r.Address.Administration != macaddr.AdminLocal {
		t.Errorf("Administration = %q, want %q", r.Address.Administration, macaddr.AdminLocal)
	}
	if r.Address.VendorLookupApplicable() {
		t.Error("VendorLookupApplicable = true for a locally administered address")
	}
}

func TestResolveCIDNamesTheAssignerOfALocalAddress(t *testing.T) {
	db := loadedDB(t)
	// CID exists precisely for locally administered addresses, so it is the one
	// registry consulted on that path.
	r, err := Resolve(db, "EA:27:01:00:00:01")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if r.Address.Administration != macaddr.AdminLocal {
		t.Fatalf("Administration = %q, want %q", r.Address.Administration, macaddr.AdminLocal)
	}
	if r.Status != ouidb.MatchVendor || r.VendorName() != "ACCE Technology Corp." {
		t.Errorf("Status = %q, VendorName = %q; want the CID assignee", r.Status, r.VendorName())
	}
	if r.Entry.Registry != ouidb.RegistryCID {
		t.Errorf("Registry = %q, want CID", r.Entry.Registry)
	}
}

func TestResolveMulticastAndBroadcast(t *testing.T) {
	db := loadedDB(t)
	tests := []struct {
		in       string
		cast     macaddr.Cast
		wellKnow string
	}{
		{"33:33:00:00:00:FB", macaddr.CastMulticast, "IPv6 multicast (RFC 2464)"},
		{"01:80:C2:00:00:00", macaddr.CastMulticast, "IEEE 802.1D reserved (STP, LLDP, PAUSE, ...)"},
		{"FF:FF:FF:FF:FF:FF", macaddr.CastBroadcast, "Broadcast"},
	}
	for _, tt := range tests {
		r, err := Resolve(db, tt.in)
		if err != nil {
			t.Fatalf("Resolve(%q) returned error: %v", tt.in, err)
		}
		if r.Status != StatusNotApplicable {
			t.Errorf("Resolve(%q).Status = %q, want %q", tt.in, r.Status, StatusNotApplicable)
		}
		if r.Address.Cast != tt.cast {
			t.Errorf("Resolve(%q).Cast = %q, want %q", tt.in, r.Address.Cast, tt.cast)
		}
		if !r.HasWellKnown || r.WellKnown.Name != tt.wellKnow {
			t.Errorf("Resolve(%q) well-known = %q, want %q", tt.in, r.WellKnown.Name, tt.wellKnow)
		}
	}
}

func TestResolveNotFound(t *testing.T) {
	db := loadedDB(t)
	r, err := Resolve(db, "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	// AA has the U/L bit set, so this is a locally administered address with no
	// CID: not applicable rather than not found.
	if r.Status != StatusNotApplicable {
		t.Errorf("Status = %q, want %q", r.Status, StatusNotApplicable)
	}
	// A universal address with no assignment is a genuine miss.
	r, err = Resolve(db, "AC:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if r.Status != ouidb.MatchNotFound {
		t.Errorf("Status = %q, want %q", r.Status, ouidb.MatchNotFound)
	}
}

func TestResolveInvalidInput(t *testing.T) {
	db := loadedDB(t)
	if _, err := Resolve(db, "not-a-mac"); !errors.Is(err, ErrInvalidAddress) {
		t.Errorf("error = %v, want ErrInvalidAddress", err)
	}
}

func TestSearchVendor(t *testing.T) {
	db := loadedDB(t)
	got, total := SearchVendor(db, "nokia", 0)
	if total != 1 || len(got) != 1 {
		t.Fatalf("got %d/%d, want 1/1", len(got), total)
	}
	if got[0].Assignment != "286FB9" {
		t.Errorf("Assignment = %q", got[0].Assignment)
	}
}

func TestSortedRegistries(t *testing.T) {
	got := SortedRegistries(map[ouidb.Registry]int{
		ouidb.RegistryCID: 1, ouidb.RegistryMAL: 1, ouidb.RegistryMAS: 1,
	})
	want := []ouidb.Registry{ouidb.RegistryMAL, ouidb.RegistryMAS, ouidb.RegistryCID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortedRegistries = %v, want %v", got, want)
		}
	}
}
