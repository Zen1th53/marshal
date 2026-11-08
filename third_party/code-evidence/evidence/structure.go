package evidence

import (
	"path"
	"strings"

	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
)

// Structural candidate expansion (ranker bm25f-hop1/3). From the top
// seeds, besides call edges (Neighbors):
//
//   - reference edges: declarations whose body uses a seed type, var or
//     const (or takes a seed function as a value), non-test only;
//   - build-variant twins: non-test files in a seed file's directory
//     that share its stem across a build-variant suffix (x.go,
//     x_linux.go, x_default.go, x_pro.go), whether or not they are in
//     a requested configuration;
//   - with a co-change prior (experimental, opt-in): files that changed
//     together with the seed files are boosted, and those without a
//     candidate get a representative declaration.
type expandParams struct {
	seeds, fanout int
	hop           float64 // call-edge hop: factor × seed score
	refs          float64 // reference edges; 0 = off
	twin          float64 // build-variant twins; 0 = off
	twinSeeds     int     // seed files for twins and co-change
	ccBoost       float64 // co-change: candidate factor 1 + ccBoost·P
	ccRep         float64 // co-change: representative for files without candidates
}

var defaultExpand = expandParams{seeds: hopSeeds, fanout: hopFanout, hop: hopFactor, refs: 0.3, twin: 0.5, twinSeeds: 3, ccBoost: 1, ccRep: 0.1}

// fileStem is the file name without ".go" and "_test".
func fileStem(p string) string {
	b := strings.TrimSuffix(path.Base(p), ".go")
	return strings.TrimSuffix(b, "_test")
}

// variantSiblings reports whether two files in one directory are build
// variants of one stem: one name is the other's stem plus "_suffix", or
// both share the part before their last "_" (x_default.go, x_pro.go;
// x_linux.go, x_other.go).
func variantSiblings(a, b string) bool {
	sa, sb := fileStem(a), fileStem(b)
	if sa == sb {
		return false
	}
	if strings.HasPrefix(sb, sa+"_") || strings.HasPrefix(sa, sb+"_") {
		return true
	}
	ia, ib := strings.LastIndexByte(sa, '_'), strings.LastIndexByte(sb, '_')
	return ia > 0 && ib > 0 && sa[:ia] == sb[:ib]
}

// representative picks the declaration that stands for a file with no
// matching candidate: its first exported function, method or type, else
// its first declaration.
func representative(ff *goanalyzer.FileFacts) int {
	for i, d := range ff.Decls {
		if d.Name != "" && d.Name[0] >= 'A' && d.Name[0] <= 'Z' && (d.Kind == goanalyzer.KindFunc || d.Kind == goanalyzer.KindMethod || d.Kind == goanalyzer.KindType) {
			return i
		}
	}
	if len(ff.Decls) > 0 {
		return 0
	}
	return -1
}

// coPrior is a co-change prior restricted to the policy-visible snapshot.
type coPrior struct {
	commits [][]string // visible Go paths per past commit, sorted; ≥ 2 each
	digest  string
	files   map[string][]int // path → commit indexes
}

// maxCoChangeFiles skips past commits touching more visible files: bulk
// edits say little about which files belong together.
const maxCoChangeFiles = 30

// newCoPrior filters a host-provided co-change history to paths visible
// in the snapshot. Paths that are denied, missing or not Go files are
// dropped identically, so the prior and its digest depend on the visible
// snapshot only (noninterference). Forgotten files are dropped the same
// way, so they do not influence ranking through the prior.
func newCoPrior(v *view, cc *CoChange, revoked func(string) bool) *coPrior {
	if cc == nil {
		return nil
	}
	p := &coPrior{files: map[string][]int{}}
	var b strings.Builder
	for _, c := range cc.Commits {
		var vis []string
		for _, f := range c {
			if !strings.HasSuffix(f, ".go") {
				continue
			}
			if _, ok := v.snap.File(f); ok && !revoked(f) {
				vis = append(vis, f)
			}
		}
		vis = uniqueSorted(vis)
		if len(vis) < 2 || len(vis) > maxCoChangeFiles {
			continue
		}
		i := len(p.commits)
		p.commits = append(p.commits, vis)
		for _, f := range vis {
			p.files[f] = append(p.files[f], i)
		}
		b.WriteString(strings.Join(vis, "\x00") + "\n")
	}
	p.digest = sha([]byte(b.String()))
	return p
}

// scores returns, for every file that changed together with a seed file,
// Σ_seed w_seed · Σ_{commits with seed and file} 1/(|commit|−1) / n(seed):
// a size-normalised conditional co-change frequency.
func (p *coPrior) scores(seeds []string, weights []float64) map[string]float64 {
	out := map[string]float64{}
	for si, s := range seeds {
		idx := p.files[s]
		if len(idx) == 0 {
			continue
		}
		for _, ci := range idx {
			c := p.commits[ci]
			w := weights[si] / float64(len(c)-1) / float64(len(idx))
			for _, f := range c {
				if f != s {
					out[f] += w
				}
			}
		}
	}
	return out
}
