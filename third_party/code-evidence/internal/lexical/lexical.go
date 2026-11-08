// Package lexical implements literal, case-insensitive term search over
// the captured snapshot. Results are always pinned to captured bytes:
// the pure-Go backend scans the captured blobs directly; the ripgrep
// backend runs over the live files and every hit is re-verified against
// the captured blob (unverifiable hits are dropped and that file is
// rescanned from captured bytes).
package lexical

import (
	"bytes"
	"context"
	"runtime"
	"sort"
	"sync"
)

// Source supplies visible captured bytes.
type Source interface {
	Paths() []string
	Bytes(path string) ([]byte, bool)
}

// Hit is one verified match in captured bytes.
type Hit struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Line   int    `json:"line"`
	Term   string `json:"term"`
}

// Limits bound result sizes deterministically.
type Limits struct {
	PerFileTerm int // max hits per (file, term); default 16
	Total       int // max hits overall; default 20000
}

func (l Limits) norm() Limits {
	if l.PerFileTerm <= 0 {
		l.PerFileTerm = 16
	}
	if l.Total <= 0 {
		l.Total = 20000
	}
	return l
}

// Result is a search result.
type Result struct {
	Backend   string
	Hits      []Hit // sorted by (path, offset, term)
	Truncated bool
	// Rescanned counts files whose rg hits did not match captured bytes.
	Rescanned int
}

// Searcher is a lexical backend.
type Searcher interface {
	Name() string
	Search(ctx context.Context, root string, src Source, terms []string, lim Limits) (Result, error)
}

// GoSearcher scans captured bytes in-process (deterministic, exact over
// the snapshot).
type GoSearcher struct{}

// Name implements Searcher.
func (GoSearcher) Name() string { return "go-scan/1" }

// Search implements Searcher.
func (GoSearcher) Search(ctx context.Context, _ string, src Source, terms []string, lim Limits) (Result, error) {
	lim = lim.norm()
	terms = NormalizeTerms(terms)
	paths := src.Paths()
	per := make([][]Hit, len(paths))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < min(runtime.GOMAXPROCS(0), 16); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				b, ok := src.Bytes(paths[i])
				if !ok {
					continue
				}
				per[i] = scanBlob(paths[i], b, terms, lim.PerFileTerm)
			}
		}()
	}
	for i := range paths {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return merge("go-scan/1", per, lim.Total, 0), nil
}

func merge(backend string, per [][]Hit, total, rescanned int) Result {
	r := Result{Backend: backend, Rescanned: rescanned}
	for _, hs := range per {
		for _, h := range hs {
			if len(r.Hits) >= total {
				r.Truncated = true
				return r
			}
			r.Hits = append(r.Hits, h)
		}
	}
	return r
}

// IsBinary reports whether b looks binary (NUL in the first 8000 bytes).
func IsBinary(b []byte) bool {
	n := min(len(b), 8000)
	return bytes.IndexByte(b[:n], 0) >= 0
}

// NormalizeTerms lowercases (ASCII), drops terms shorter than 3 bytes and
// sorts/dedupes.
func NormalizeTerms(terms []string) []string {
	var out []string
	for _, t := range terms {
		t = string(asciiLower([]byte(t)))
		if len(t) >= 3 {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	j := 0
	for i, t := range out {
		if i == 0 || t != out[j-1] {
			out[j] = t
			j++
		}
	}
	return out[:j]
}

func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

func scanBlob(path string, b []byte, terms []string, perTerm int) []Hit {
	if IsBinary(b) {
		return nil
	}
	low := asciiLower(b)
	var hits []Hit
	for _, t := range terms {
		tb := []byte(t)
		n, from := 0, 0
		for n < perTerm {
			i := bytes.Index(low[from:], tb)
			if i < 0 {
				break
			}
			off := from + i
			hits = append(hits, Hit{Path: path, Offset: off, Term: t})
			n++
			from = off + len(tb)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Offset != hits[j].Offset {
			return hits[i].Offset < hits[j].Offset
		}
		return hits[i].Term < hits[j].Term
	})
	// Line numbers in one pass.
	line, pos := 1, 0
	for k := range hits {
		line += bytes.Count(b[pos:hits[k].Offset], []byte{'\n'})
		pos = hits[k].Offset
		hits[k].Line = line
	}
	return hits
}

// Verify reports whether captured bytes contain term at off.
func Verify(b []byte, off int, term string) bool {
	if off < 0 || off+len(term) > len(b) {
		return false
	}
	return string(asciiLower(b[off:off+len(term)])) == term
}
