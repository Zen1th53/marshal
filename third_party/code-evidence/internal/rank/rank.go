// Package rank implements deterministic BM25F scoring over declaration
// documents (identifier, path and doc-line fields).
package rank

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Version is the ranker version (part of the receipt version vector).
// /3 adds reference edges and build-variant twins (evidence/structure.go).
const Version = "bm25f-hop1/3"

// Field weights.
const (
	WeightName = 3.0
	WeightPath = 1.0
	WeightDoc  = 1.0
	k1         = 1.2
	bParam     = 0.75
)

var stop = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "into": true, "that": true,
	"this": true, "are": true, "was": true, "how": true, "what": true, "where": true, "when": true,
	"why": true, "does": true, "add": true, "fix": true, "make": true, "use": true, "get": true,
	"of": true, "to": true, "in": true, "on": true, "is": true, "it": true, "an": true, "a": true,
	"or": true, "be": true, "by": true, "at": true, "as": true, "we": true, "go": true,
}

// IsStopWord reports whether t (lowercase) is a stop word.
func IsStopWord(t string) bool { return stop[t] }

// Tokenize splits text into lowercase, lightly stemmed terms. Identifiers
// are split at camelCase and digit boundaries.
func Tokenize(s string) []string {
	var out []string
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		for _, part := range splitCamel(word) {
			t := stem(strings.ToLower(part))
			if len(t) >= 2 && !stop[t] {
				out = append(out, t)
			}
		}
		word = word[:0]
	}
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func splitCamel(w []rune) []string {
	var parts []string
	start := 0
	for i := 1; i < len(w); i++ {
		prev, cur := w[i-1], w[i]
		boundary := false
		switch {
		case unicode.IsLower(prev) && unicode.IsUpper(cur):
			boundary = true
		case unicode.IsDigit(prev) != unicode.IsDigit(cur):
			boundary = true
		case unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(w) && unicode.IsLower(w[i+1]):
			boundary = true
		}
		if boundary {
			parts = append(parts, string(w[start:i]))
			start = i
		}
	}
	return append(parts, string(w[start:]))
}

func stem(t string) string {
	switch {
	case len(t) > 4 && strings.HasSuffix(t, "ies"):
		return t[:len(t)-3] + "y"
	case len(t) > 3 && strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss") && !strings.HasSuffix(t, "us"):
		return t[:len(t)-1]
	}
	return t
}

// Doc is one ranked document with tokenized fields.
type Doc struct {
	Name []string
	Path []string
	Doc  []string
}

type posting struct {
	doc               int
	name, path, docTF float64
}

// Index is an inverted BM25F index.
type Index struct {
	n      int
	avgLen float64
	length []float64
	post   map[string][]posting
}

// New builds an index. Document order defines doc numbers.
func New(docs []Doc) *Index {
	ix := &Index{n: len(docs), length: make([]float64, len(docs)), post: map[string][]posting{}}
	total := 0.0
	for i, d := range docs {
		tf := map[string]*posting{}
		get := func(t string) *posting {
			p := tf[t]
			if p == nil {
				p = &posting{doc: i}
				tf[t] = p
			}
			return p
		}
		for _, t := range d.Name {
			get(t).name++
		}
		for _, t := range d.Path {
			get(t).path++
		}
		for _, t := range d.Doc {
			get(t).docTF++
		}
		l := WeightName*float64(len(d.Name)) + WeightPath*float64(len(d.Path)) + WeightDoc*float64(len(d.Doc))
		ix.length[i] = l
		total += l
		keys := make([]string, 0, len(tf))
		for t := range tf {
			keys = append(keys, t)
		}
		sort.Strings(keys)
		for _, t := range keys {
			ix.post[t] = append(ix.post[t], *tf[t])
		}
	}
	if ix.n > 0 {
		ix.avgLen = total / float64(ix.n)
	}
	if ix.avgLen == 0 {
		ix.avgLen = 1
	}
	return ix
}

// IDF returns the BM25 idf of a term.
func (ix *Index) IDF(t string) float64 {
	df := float64(len(ix.post[t]))
	return math.Log(1 + (float64(ix.n)-df+0.5)/(df+0.5))
}

// Hit describes which fields matched for a document.
type Hit struct {
	Score       float64
	NameOrPath  bool // an identifier or path field matched (code signal)
	DocOnly     bool // only the free-text doc field matched
	MatchedTerm []string
}

// Score scores all documents matching any query term. Query terms must
// already be tokenized; duplicates count once.
func (ix *Index) Score(query []string) map[int]*Hit {
	seen := map[string]bool{}
	out := map[int]*Hit{}
	q := append([]string(nil), query...)
	sort.Strings(q)
	for _, t := range q {
		if seen[t] {
			continue
		}
		seen[t] = true
		idf := ix.IDF(t)
		for _, p := range ix.post[t] {
			tf := WeightName*p.name + WeightPath*p.path + WeightDoc*p.docTF
			norm := tf * (k1 + 1) / (tf + k1*(1-bParam+bParam*ix.length[p.doc]/ix.avgLen))
			h := out[p.doc]
			if h == nil {
				h = &Hit{}
				out[p.doc] = h
			}
			h.Score += idf * norm
			if p.name > 0 || p.path > 0 {
				h.NameOrPath = true
			}
			h.MatchedTerm = append(h.MatchedTerm, t)
		}
	}
	for _, h := range out {
		h.DocOnly = !h.NameOrPath
	}
	return out
}
