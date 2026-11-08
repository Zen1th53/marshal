package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/Zen1th53/code-evidence/internal/fsread"
)

// DefaultRacyWindow is how recent a file's mtime or ctime may be (relative
// to the start of the capture that read it) before its cache entry is
// considered racy and not stored. Filesystem timestamps are coarse, so a
// file rewritten within the same tick with the same size could otherwise
// keep a stale StatKey (the "racy git" problem).
const DefaultRacyWindow = 2 * time.Second

// StatCache maps visible paths to their last captured (redacted) bytes and
// hash, keyed by (size, mtime_ns, ctime_ns, inode, device). It is bound to
// one policy: a capture under a different policy id clears it.
//
// The cache is an optimization only. It never certifies bytes: delivered
// and cited files are re-hashed from disk after capture (Recheck).
type StatCache struct {
	// RacyWindow overrides DefaultRacyWindow when > 0; a negative value
	// disables the racy check (tests only).
	RacyWindow time.Duration

	mu       sync.Mutex
	policyID string
	m        map[string]statEntry
}

type statEntry struct {
	key  fsread.StatKey
	f    File
	data []byte
}

// NewStatCache returns an empty cache.
func NewStatCache() *StatCache { return &StatCache{m: map[string]statEntry{}} }

func (c *StatCache) bind(policyID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.policyID != policyID {
		c.policyID = policyID
		c.m = map[string]statEntry{}
	}
}

func (c *StatCache) lookup(p string, key fsread.StatKey) (File, []byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[p]
	if !ok || e.key != key {
		return File{}, nil, false
	}
	return e.f, e.data, true
}

func (c *StatCache) store(p string, key fsread.StatKey, f File, data []byte, captureStart time.Time) {
	w := c.RacyWindow
	if w == 0 {
		w = DefaultRacyWindow
	}
	if w > 0 {
		limit := captureStart.Add(-w).UnixNano()
		if key.MtimeNS >= limit || key.CtimeNS >= limit {
			return // racy: too recent to trust its StatKey later
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 1<<20 {
		c.m = map[string]statEntry{}
	}
	c.m[p] = statEntry{key: key, f: f, data: data}
}

// Invalidate drops entries for paths (after a failed Recheck).
func (c *StatCache) Invalidate(paths []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range paths {
		delete(c.m, p)
	}
}

// Clear drops every entry (e.g. after forget).
func (c *StatCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = map[string]statEntry{}
}

// Len reports the number of entries.
func (c *StatCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// Override replaces the cached file entry and bytes of path while keeping
// its StatKey, simulating a stale entry (a file rewritten without a
// visible stat change). It reports whether an entry existed. It exists for
// fault-injection tests of the recheck rule.
func (c *StatCache) Override(path string, data []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[path]
	if !ok {
		return false
	}
	sum := sha256.Sum256(data)
	e.f.SHA256, e.f.Size, e.data = hex.EncodeToString(sum[:]), int64(len(data)), data
	c.m[path] = e
	return true
}
