// Package snapshot captures the policy-visible bytes of a repository and
// commits to them: per-file SHA-256 of the (redacted) captured bytes, a
// Merkle snapshot id over (path, hash) and a Merkle universe commitment
// over the sorted path list. Both identifiers are policy-scoped: they are
// computed from visible files only and mixed with the policy id.
package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/code-evidence/internal/fsread"
	"github.com/Zen1th53/code-evidence/internal/policy"
)

// FormatVersion is mixed into snapshot and universe identifiers.
const FormatVersion = "cec/snapshot/v1"

// Skip reasons for visible files whose bytes were not captured.
const (
	SkipTooLarge   = "too_large"
	SkipNotRegular = "not_regular"
	SkipUnreadable = "unreadable"
)

// File is one entry of the visible snapshot.
type File struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size"`
	Skipped string `json:"skipped,omitempty"`
	// Redactions counts redactor hits (bytes were masked before hashing).
	Redactions int `json:"redactions,omitempty"`
}

// Snapshot is an immutable capture.
type Snapshot struct {
	ID       string
	Universe string
	PolicyID string
	Mode     fsread.Mode
	Files    []File   // sorted by Path
	Gaps     []string // visible directories that could not be listed
	// Reused counts files taken from a stat cache (not re-read).
	Reused int
	index  map[string]int
	data   map[string][]byte
}

// Capture enumerates and reads every visible file. Denied files are
// filtered during enumeration and never opened.
func Capture(ctx context.Context, repo *fsread.Repo, pol *policy.Policy, mode fsread.Mode) (*Snapshot, error) {
	return CaptureCached(ctx, repo, pol, mode, nil)
}

// CaptureCached is Capture with an optional stat cache: a visible file
// whose StatKey matches a non-racy cache entry is not re-read or
// re-hashed. The cache only saves work; it never certifies bytes. Callers
// that deliver or cite a file must Recheck it (SPEC §3).
func CaptureCached(ctx context.Context, repo *fsread.Repo, pol *policy.Policy, mode fsread.Mode, cache *StatCache) (*Snapshot, error) {
	l, err := repo.Enumerate(ctx, mode, pol)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	cache.bind(pol.ID())
	files := make([]File, len(l.Paths))
	data := make([][]byte, len(l.Paths))
	var reused atomic.Int64
	var wg sync.WaitGroup
	next := make(chan int)
	workers := min(runtime.GOMAXPROCS(0), 16)
	red := pol.Redactor()
	var keys []fsread.StatKey
	var keyOK []bool
	if cache != nil {
		keys, keyOK = repo.StatKeys(l.Paths, workers)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				p := l.Paths[i]
				if cache != nil && keyOK[i] {
					if f, b, hit := cache.lookup(p, keys[i]); hit {
						files[i], data[i] = f, b
						reused.Add(1)
						continue
					}
				}
				f, b, key, stable := captureOne(repo, pol, red, p)
				files[i], data[i] = f, b
				if cache != nil && stable && f.Skipped == "" {
					cache.store(p, key, f, b, start)
				}
			}
		}()
	}
	for i := range l.Paths {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := build(pol.ID(), l.Mode, files, data, l.Gaps)
	s.Reused = int(reused.Load())
	return s, nil
}

// captureOne reads, redacts and hashes one visible file.
func captureOne(repo *fsread.Repo, pol *policy.Policy, red policy.Redactor, p string) (File, []byte, fsread.StatKey, bool) {
	f := File{Path: p}
	b, key, stable, err := repo.ReadFileKeyed(p, pol.MaxFileBytes())
	switch {
	case errors.Is(err, fsread.ErrTooLarge):
		f.Skipped = SkipTooLarge
		if st, serr := repo.Lstat(p); serr == nil {
			f.Size = st.Size()
		}
	case errors.Is(err, fsread.ErrNotRegular):
		f.Skipped = SkipNotRegular
	case err != nil:
		f.Skipped = SkipUnreadable
	default:
		rb, n := red.Redact(p, b)
		sum := sha256.Sum256(rb)
		f.SHA256 = hex.EncodeToString(sum[:])
		f.Size = int64(len(rb))
		f.Redactions = n
		return f, rb, key, stable
	}
	return f, nil, key, false
}

// Recheck re-reads, re-redacts and re-hashes paths from disk, bypassing
// any cache, and returns the paths whose current state differs from the
// snapshot entry (including paths that vanished or appeared). Paths not
// in the snapshot are compared against "absent".
func Recheck(repo *fsread.Repo, pol *policy.Policy, s *Snapshot, paths []string) []string {
	red := pol.Redactor()
	var uniq []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, err := fsread.CleanRel(p); err != nil || !pol.Visible(p) {
			continue
		}
		uniq = append(uniq, p)
	}
	bad := make([]bool, len(uniq))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < min(runtime.GOMAXPROCS(0), 8, max(1, len(uniq))); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				p := uniq[i]
				old, inSnap := s.File(p)
				f, _, _, _ := captureOne(repo, pol, red, p)
				if !inSnap {
					bad[i] = f.Skipped == "" || f.Skipped == SkipTooLarge // appeared
					continue
				}
				bad[i] = f.SHA256 != old.SHA256 || f.Skipped != old.Skipped
			}
		}()
	}
	for i := range uniq {
		next <- i
	}
	close(next)
	wg.Wait()
	var out []string
	for i, b := range bad {
		if b {
			out = append(out, uniq[i])
		}
	}
	sort.Strings(out)
	return out
}

// FromFiles builds a snapshot from already captured content (used by tests
// and by hosts that capture bytes themselves, e.g. from a git tree).
// content maps path to redacted bytes; files must be visible paths.
func FromFiles(policyID string, mode fsread.Mode, content map[string][]byte) *Snapshot {
	paths := make([]string, 0, len(content))
	for p := range content {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	files := make([]File, len(paths))
	data := make([][]byte, len(paths))
	for i, p := range paths {
		b := content[p]
		sum := sha256.Sum256(b)
		files[i] = File{Path: p, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))}
		data[i] = b
	}
	return build(policyID, mode, files, data, nil)
}

func build(policyID string, mode fsread.Mode, files []File, data [][]byte, gaps []string) *Snapshot {
	s := &Snapshot{
		PolicyID: policyID,
		Mode:     mode,
		Files:    files,
		Gaps:     gaps,
		index:    make(map[string]int, len(files)),
		data:     make(map[string][]byte, len(files)),
	}
	leaves := make([][]byte, len(files))
	uleaves := make([][]byte, len(files))
	for i, f := range files {
		s.index[f.Path] = i
		if data[i] != nil {
			s.data[f.Path] = data[i]
		}
		v := f.SHA256
		if f.Skipped != "" {
			v = "skipped:" + f.Skipped
		}
		leaves[i] = []byte(f.Path + "\x00" + v)
		uleaves[i] = []byte(f.Path)
	}
	root := MerkleRoot(leaves)
	uroot := MerkleRoot(uleaves)
	s.ID = "snap_" + scoped("snapshot", policyID, root)
	s.Universe = "uni_" + scoped("universe", policyID, uroot)
	return s
}

func scoped(kind, policyID string, root [32]byte) string {
	h := sha256.New()
	h.Write([]byte(FormatVersion + "\x00" + kind + "\x00" + policyID + "\x00"))
	h.Write(root[:])
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// UniverseOf computes a policy-scoped universe commitment over paths
// (which must be sorted and unique).
func UniverseOf(policyID string, paths []string) string {
	leaves := make([][]byte, len(paths))
	for i, p := range paths {
		leaves[i] = []byte(p)
	}
	return "uni_" + scoped("universe", policyID, MerkleRoot(leaves))
}

// File returns the entry for path.
func (s *Snapshot) File(path string) (File, bool) {
	i, ok := s.index[path]
	if !ok {
		return File{}, false
	}
	return s.Files[i], true
}

// Bytes returns the captured (redacted) bytes for path.
func (s *Snapshot) Bytes(path string) ([]byte, bool) {
	b, ok := s.data[path]
	return b, ok
}

// SHA256 returns the hash of the captured bytes of path (goanalyzer.Hasher).
func (s *Snapshot) SHA256(path string) (string, bool) {
	f, ok := s.File(path)
	return f.SHA256, ok && f.SHA256 != ""
}

// Paths returns the sorted visible paths.
func (s *Snapshot) Paths() []string {
	out := make([]string, len(s.Files))
	for i, f := range s.Files {
		out[i] = f.Path
	}
	return out
}

// TotalBytes sums captured sizes.
func (s *Snapshot) TotalBytes() int64 {
	var n int64
	for _, f := range s.Files {
		if f.Skipped == "" {
			n += f.Size
		}
	}
	return n
}
