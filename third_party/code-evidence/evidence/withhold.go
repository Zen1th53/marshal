package evidence

import (
	"fmt"
	"path"
	"strings"

	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
)

// Forgotten evidence is never delivered in any form (SPEC §3, forget):
// predicate items inside files whose captured blob was revoked are
// withheld from dossiers, expansions and validation output. Only their
// number is reported. A predicate with withheld items is no longer the
// relation the caller can check, so an exact bound drops to a lower bound
// (every shown item is in the relation; more exist) and its coverage is
// marked incomplete.

// withhold removes the items of res that lie in revoked files and returns
// the reduced result and how many items it removed.
func withhold(res goanalyzer.Result, revoked func(string) bool) (goanalyzer.Result, int) {
	if revoked == nil {
		return res, 0
	}
	n := 0
	for _, it := range res.Items {
		if it.Path != "" && revoked(it.Path) {
			n++
		}
	}
	if n == 0 {
		return res, 0
	}
	keep := make([]goanalyzer.Item, 0, len(res.Items)-n)
	for _, it := range res.Items {
		if it.Path == "" || !revoked(it.Path) {
			keep = append(keep, it)
		}
	}
	res.Items = keep
	if res.Bound == goanalyzer.BoundExact {
		res.Bound = goanalyzer.BoundLower
	}
	res.Coverage.Complete = false
	return res, n
}

// withheldNote is the only trace of withheld items: a count, no paths.
func withheldNote(n int) string {
	return fmt.Sprintf("+%d items withheld (revoked)", n)
}

// predCoverageNote describes a predicate's coverage, naming withheld
// items instead of a parse failure when that is why it is incomplete.
func predCoverageNote(c Coverage, withheld int) string {
	if withheld == 0 {
		return coverageNote(c)
	}
	if c.Failed == 0 && c.Skipped == 0 && c.Parsed == c.GoFiles {
		return withheldNote(withheld)
	}
	c.Complete = false
	return coverageNote(c) + "; " + withheldNote(withheld)
}

// hopLabel is c's "hop" provenance, or "" when it would name a
// declaration or file that exists only in forgotten files.
func hopLabel(v *view, c candidate, revoked func(string) bool) string {
	h := c.hop
	if h == "" || h == "cochange" || revoked == nil {
		return h
	}
	if base, ok := strings.CutPrefix(h, "variant "); ok {
		if revoked(path.Join(v.ix.Files[c.ref.File].Dir, base)) {
			return ""
		}
		return h
	}
	refs := v.ix.DeclsByID(h)
	for _, r := range refs {
		if !revoked(v.ix.Files[r.File].Path) {
			return h
		}
	}
	if len(refs) == 0 {
		return h
	}
	return ""
}
