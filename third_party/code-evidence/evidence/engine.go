package evidence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/code-evidence/internal/fsread"
	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
	"github.com/Zen1th53/code-evidence/internal/lexical"
	"github.com/Zen1th53/code-evidence/internal/policy"
	"github.com/Zen1th53/code-evidence/internal/rank"
	"github.com/Zen1th53/code-evidence/internal/snapshot"
	"github.com/Zen1th53/code-evidence/internal/store"
)

// Options configure an Engine. Root, policy and configurations bind when
// the engine (session) is built, never per call.
type Options struct {
	// Root is the repository root (read-only).
	Root string
	// StoreDir is the host-owned store directory; "" uses
	// $XDG_CACHE_HOME/code-evidence/<repo-id>. It must lie outside Root.
	StoreDir string
	// Policy (nil = DefaultPolicy()).
	Policy *Policy
	// Configs (nil = DefaultConfigs()).
	Configs []Config
	// Enumeration is "auto" (default), "git" or "walk".
	Enumeration string
	// Lexical is "go" (default, exact over the snapshot), "rg" or "auto".
	Lexical string
	// Clock supplies observation times (nil = time.Now). Conformance
	// fixtures fix it.
	Clock func() time.Time
	// NoStatCache disables the stat cache, so every call re-reads and
	// re-hashes every visible file.
	NoStatCache bool
}

// Engine implements Host for one repository.
type Engine struct {
	opts    Options
	repo    *fsread.Repo
	store   *store.Store
	pol     *policy.Policy
	configs []Config
	lex     lexical.Searcher
	cache   *goanalyzer.Cache

	mu     sync.Mutex
	lastRk *rankCache
	stat   *snapshot.StatCache // nil when disabled
	views  []*view             // most recent first, full indexes only

	// Validation mode counters (receipts checked by fast path, partial
	// closure index, full head index).
	valFast, valPartial, valFull atomic.Int64
}

// maxViews bounds the analyzed-view cache.
const maxViews = 4

// maxAttempts bounds capture retries when a cited file changed on disk
// between capture and recheck.
const maxAttempts = 3

// errUnstable is returned when cited files keep changing during a call.
var errUnstable = errors.New("evidence: repository files changed during capture; retry")

type rankCache struct {
	key  string
	ix   *rank.Index
	refs []goanalyzer.DeclRef
	// Per-doc facts for task signals (signals.go).
	names map[string][]int // decl name -> docs
	quals map[string][]int // "Recv.Name" / "pkg.Name" -> docs
	dirs  map[string]bool
	gen   []bool // doc lives in a generated file
}

var _ Host = (*Engine)(nil)

// Open opens an engine.
func Open(ctx context.Context, opts Options) (*Engine, error) {
	if opts.Root == "" {
		return nil, errors.New("evidence: Options.Root is required")
	}
	repo, err := fsread.Open(opts.Root)
	if err != nil {
		return nil, err
	}
	pol := opts.Policy
	if pol == nil {
		pol = policy.Default()
	}
	configs := opts.Configs
	if len(configs) == 0 {
		configs = goanalyzer.DefaultConfigs()
	}
	if err := checkConfigs(configs); err != nil {
		repo.Close()
		return nil, err
	}
	dir := opts.StoreDir
	if dir == "" {
		if dir, err = store.DefaultDir(repo.Abs()); err != nil {
			repo.Close()
			return nil, err
		}
	}
	st, err := store.Open(ctx, dir, repo.Abs())
	if err != nil {
		repo.Close()
		return nil, err
	}
	var lex lexical.Searcher = lexical.GoSearcher{}
	switch opts.Lexical {
	case "", "go":
	case "rg":
		rg := lexical.RipgrepSearcher{}
		if !rg.Available() {
			st.Close()
			repo.Close()
			return nil, errors.New("evidence: ripgrep (rg) not found on PATH")
		}
		lex = rg
	case "auto":
		if rg := (lexical.RipgrepSearcher{}); rg.Available() {
			lex = rg
		}
	default:
		st.Close()
		repo.Close()
		return nil, fmt.Errorf("evidence: unknown lexical backend %q", opts.Lexical)
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	e := &Engine{opts: opts, repo: repo, store: st, pol: pol, configs: configs, lex: lex, cache: goanalyzer.NewCache()}
	if !opts.NoStatCache {
		e.stat = snapshot.NewStatCache()
	}
	return e, nil
}

func checkConfigs(cs []Config) error {
	seen := map[string]bool{}
	for _, c := range cs {
		if c.Name == "" || seen[c.Name] {
			return fmt.Errorf("evidence: configuration names must be unique and non-empty (%q)", c.Name)
		}
		seen[c.Name] = true
		if _, err := goanalyzer.ParseConfig(c.String()); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) factsCache() *goanalyzer.Cache {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cache
}

// Close releases resources.
func (e *Engine) Close() error {
	err1 := e.store.Close()
	err2 := e.repo.Close()
	return errors.Join(err1, err2)
}

// Policy returns the bound policy.
func (e *Engine) Policy() *Policy { return e.pol }

// StorePath returns the store database path.
func (e *Engine) StorePath() string { return e.store.Path() }

func (e *Engine) versions() VersionVector {
	return VersionVector{
		Module:   Version,
		Analyzer: goanalyzer.Version,
		Recipe:   RecipeVersion,
		Ranker:   rank.Version,
		Renderer: renderVersion,
		Redactor: e.pol.RedactorVersion(),
		Lexical:  e.lex.Name(),
	}
}

func (e *Engine) now() string { return e.opts.Clock().UTC().Format(time.RFC3339) }

func (e *Engine) mode() fsread.Mode { return fsread.Mode(e.opts.Enumeration) }

// view is an analyzed capture.
type view struct {
	snap *snapshot.Snapshot
	ix   *goanalyzer.Index
	key  string
}

// captureSnap captures repo, using the stat cache for the engine root.
func (e *Engine) captureSnap(ctx context.Context, repo *fsread.Repo) (*snapshot.Snapshot, error) {
	var c *snapshot.StatCache
	if repo == e.repo {
		e.mu.Lock()
		c = e.stat
		e.mu.Unlock()
	}
	return snapshot.CaptureCached(ctx, repo, e.pol, e.mode(), c)
}

// analyze returns the full index of snap under configs. Indexes are a
// pure function of (snapshot id, configurations), so recent ones are
// reused.
func (e *Engine) analyze(snap *snapshot.Snapshot, configs []Config) *view {
	key := snap.ID + "\x00" + configsKey(configs)
	e.mu.Lock()
	for i, v := range e.views {
		if v.key == key {
			copy(e.views[1:i+1], e.views[:i])
			e.views[0] = v
			e.mu.Unlock()
			return &view{snap: snap, ix: v.ix, key: key}
		}
	}
	e.mu.Unlock()
	v := &view{snap: snap, ix: goanalyzer.Build(snap, configs, e.factsCache()), key: key}
	e.mu.Lock()
	e.views = append([]*view{v}, e.views...)
	if len(e.views) > maxViews {
		e.views = e.views[:maxViews]
	}
	e.mu.Unlock()
	return v
}

func (e *Engine) capture(ctx context.Context, repo *fsread.Repo, configs []Config) (*view, error) {
	snap, err := e.captureSnap(ctx, repo)
	if err != nil {
		return nil, err
	}
	return e.analyze(snap, configs), nil
}

// recheck re-reads and re-hashes the cited paths from disk, bypassing the
// stat cache (SPEC §3: the cache never certifies delivered bytes). It
// returns false, after dropping their cache entries, if any differ.
func (e *Engine) recheck(repo *fsread.Repo, snap *snapshot.Snapshot, paths []string) bool {
	bad := snapshot.Recheck(repo, e.pol, snap, paths)
	if len(bad) == 0 {
		return true
	}
	if repo == e.repo {
		e.mu.Lock()
		c := e.stat
		e.mu.Unlock()
		c.Invalidate(bad)
	}
	return false
}

func (e *Engine) dropCaches() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache = goanalyzer.NewCache()
	e.lastRk = nil
	e.views = nil
	e.stat.Clear()
}

func (e *Engine) persistSnapshot(ctx context.Context, s *snapshot.Snapshot) error {
	rows := make([]store.FileRow, len(s.Files))
	for i, f := range s.Files {
		rows[i] = store.FileRow{Path: f.Path, SHA256: f.SHA256, Size: f.Size, Skipped: f.Skipped}
	}
	return e.store.PublishGeneration(ctx, store.Generation{ID: s.ID, PolicyID: s.PolicyID, Universe: s.Universe, Mode: string(s.Mode)}, rows)
}

// IndexReport summarizes an index run.
type IndexReport struct {
	SnapshotID  string         `json:"snapshot_id"`
	Universe    string         `json:"universe"`
	Enumeration string         `json:"enumeration"`
	Files       int            `json:"files"`
	Bytes       int64          `json:"bytes"`
	Skipped     int            `json:"skipped"`
	Redactions  int            `json:"redactions"`
	Coverage    Coverage       `json:"coverage"`
	Decls       int            `json:"decls"`
	Uses        int            `json:"uses"`
	Configs     []string       `json:"configs"`
	PerConfig   map[string]int `json:"files_per_config"`
	StorePath   string         `json:"store_path"`
	StoreBytes  int64          `json:"store_bytes"`
	Reused      int            `json:"reused_from_stat_cache"`
	CaptureMS   int64          `json:"capture_ms"`
	AnalyzeMS   int64          `json:"analyze_ms"`
	PersistMS   int64          `json:"persist_ms"`
}

// Index captures and analyzes the repository and persists the snapshot
// manifest (structure and hashes only).
func (e *Engine) Index(ctx context.Context) (*IndexReport, error) {
	t0 := time.Now()
	snap, err := e.captureSnap(ctx, e.repo)
	if err != nil {
		return nil, err
	}
	t1 := time.Now()
	ix := e.analyze(snap, e.configs).ix
	t2 := time.Now()
	if err := e.persistSnapshot(ctx, snap); err != nil {
		return nil, err
	}
	t3 := time.Now()
	r := &IndexReport{
		SnapshotID: snap.ID, Universe: snap.Universe, Enumeration: string(snap.Mode),
		Files: len(snap.Files), Bytes: snap.TotalBytes(), Coverage: ix.Coverage, Reused: snap.Reused,
		PerConfig: map[string]int{}, StorePath: e.store.Path(),
		CaptureMS: t1.Sub(t0).Milliseconds(), AnalyzeMS: t2.Sub(t1).Milliseconds(), PersistMS: t3.Sub(t2).Milliseconds(),
	}
	for _, f := range snap.Files {
		if f.Skipped != "" {
			r.Skipped++
		}
		r.Redactions += f.Redactions
	}
	for fi, ff := range ix.Files {
		r.Decls += len(ff.Decls)
		r.Uses += len(ff.Uses)
		for _, n := range ix.ConfigNames(fi) {
			r.PerConfig[n]++
		}
	}
	for _, c := range e.configs {
		r.Configs = append(r.Configs, c.String())
	}
	if st, err := e.store.Stats(ctx); err == nil {
		r.StoreBytes = st.Bytes
	}
	return r, nil
}

// Forget revokes a blob, then purges it. Revocation is durable and
// overrides reader pins: receipts and deliveries that used the blob
// validate as unknown (evidence_revoked) and expansions refuse them. The
// purge then deletes every dependent row (receipts, ledger entries,
// generations whose manifest contains the blob, artifacts), vacuums the
// database and truncates the WAL. The two steps are acknowledged
// separately: Revoked is true once revocation is durable, Purged once the
// bytes are gone from the store files.
func (e *Engine) Forget(ctx context.Context, blob string) (*ForgetReport, error) {
	if len(blob) != 64 || strings.Trim(blob, "0123456789abcdef") != "" {
		return nil, errors.New("evidence: blob must be a 64-hex SHA-256")
	}
	rv, err := e.store.Revoke(ctx, blob, e.now())
	if err != nil {
		return nil, err
	}
	e.dropCaches()
	ids := rv.Receipts
	if ids == nil {
		ids = []string{}
	}
	fr := &ForgetReport{Blob: blob, Revoked: true, ReceiptsInvalidated: ids, PinsRevoked: rv.PinsRevoked}
	pr, err := e.store.Purge(ctx, blob)
	if err != nil {
		fr.Note = "revoked and invalidated; purge incomplete: " + err.Error()
		return fr, nil
	}
	fr.Purged = true
	fr.Purge = &PurgeReport{Receipts: pr.Receipts, Deliveries: pr.Deliveries, Generations: pr.Generations,
		Artifacts: pr.Artifacts, Vacuumed: pr.Vacuumed, WALTruncated: pr.Checkpointed}
	fr.Note = "revoked, invalidated and purged (database vacuumed, WAL truncated)"
	return fr, nil
}

// GC collects unpinned generations that no stored receipt references,
// keeping the keep most recent, and every unreferenced artifact.
func (e *Engine) GC(ctx context.Context, keep int) (store.GCReport, error) {
	return e.store.GC(ctx, keep)
}

// PinSnapshot takes a reader pin on a published snapshot generation (see
// store.Pin); Unpin releases it.
func (e *Engine) PinSnapshot(ctx context.Context, snapshotID, holder string, ttl time.Duration) (string, error) {
	return e.store.Pin(ctx, snapshotID, holder, ttl)
}

// Unpin releases a pin taken with PinSnapshot.
func (e *Engine) Unpin(ctx context.Context, pin string) error { return e.store.Unpin(ctx, pin) }

// errNotVisible is the single error for anything outside the visible
// snapshot, so denied and missing are indistinguishable.
var errNotVisible = errors.New("not found in visible snapshot")

// errReceipt is the single error for unknown or unauthorized receipts.
var errReceipt = errors.New("receipt not found or not authorized under this policy")

// errRevoked is returned for receipts purged by a revocation.
var errRevoked = errors.New("evidence_revoked: receipt references revoked evidence")

func (e *Engine) loadReceipt(ctx context.Context, id string) (*Receipt, error) {
	if len(id) < 8 {
		return nil, errReceipt
	}
	full := id
	if len(id) != len("rcpt_")+32 {
		var err error
		if full, err = e.store.ResolveReceiptPrefix(ctx, id); err != nil {
			return nil, errReceipt
		}
	}
	b, err := e.store.Receipt(ctx, full)
	if err != nil {
		if gone, _ := e.store.ReceiptRevoked(ctx, full); gone {
			return nil, errRevoked
		}
		return nil, errReceipt
	}
	r, err := ParseReceipt(b)
	if err != nil {
		return nil, err
	}
	if r.PolicyID != e.pol.ID() {
		return nil, errReceipt
	}
	return r, nil
}
