package codex

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// probeCatalogTTL bounds how long a discovered model catalog is reused by
// later probes of the same installed Codex. A client that has not probed
// (the pre-claim model check) still reads the live catalog.
const probeCatalogTTL = 10 * time.Minute

// A runtime probes the worker CLI before every attempt and every attempt
// builds a fresh Client. `--version` still runs each time, so a missing,
// broken or replaced binary is still caught; what is reused is what only
// depends on the exact installed binary: that its `exec --help` carries the
// required flags, and (for a bounded time) its optional model catalog.
type probeCacheEntry struct {
	helpVerified bool
	models       []ModelInfo
	defaultModel string
	catalogAt    time.Time
}

var probeCache = struct {
	sync.Mutex
	entries map[string]*probeCacheEntry
}{entries: map[string]*probeCacheEntry{}}

// probeCacheKey identifies one installed binary at one version. Only an
// absolute path to a regular file is cached; the key holds the resolved
// target's size and modification time, the reported version and CODEX_HOME.
func probeCacheKey(binary, version string) (string, bool) {
	if !filepath.IsAbs(binary) || version == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", false
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return binary + "\x00" + resolved + "\x00" + strconv.FormatInt(st.Size(), 10) + "\x00" + strconv.FormatInt(st.ModTime().UnixNano(), 10) + "\x00" + version + "\x00" + os.Getenv("CODEX_HOME"), true
}

func cachedProbe(key string) probeCacheEntry {
	probeCache.Lock()
	defer probeCache.Unlock()
	if entry := probeCache.entries[key]; entry != nil {
		return *entry
	}
	return probeCacheEntry{}
}

func rememberProbe(key string, update func(*probeCacheEntry)) {
	probeCache.Lock()
	defer probeCache.Unlock()
	entry := probeCache.entries[key]
	if entry == nil {
		if len(probeCache.entries) >= 64 {
			probeCache.entries = map[string]*probeCacheEntry{}
		}
		entry = &probeCacheEntry{}
		probeCache.entries[key] = entry
	}
	update(entry)
}
