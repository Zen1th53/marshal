package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
	"github.com/Zen1th53/code-evidence/internal/lexical"
	"github.com/Zen1th53/code-evidence/internal/rank"
	"github.com/Zen1th53/code-evidence/internal/render"
	"github.com/Zen1th53/code-evidence/internal/store"
)

const renderVersion = render.Version

// Ranking constants (part of rank.Version semantics).
const (
	codeHitWeight     = 0.5
	codeHitIDFCap     = 5.0
	testPenalty       = 0.7
	hopSeeds          = 8
	hopFanout         = 6
	hopFactor         = 0.35
	maxExcerpt        = 12 // excerpt lines at normal budgets
	fullEntries       = 2  // excerpt-tier entries at normal budgets
	maxLineBytes      = 200
	itemsShown        = 4
	itemsShownNoBound = 2 // name-matched sets are noisy; show fewer
	minBudget         = 1024
)

type candidate struct {
	ref      goanalyzer.DeclRef
	score    float64
	eligible bool
	terms    []string
	code     []string
	hop      string
	ident    []string
}

func clampBudget(b, def, max int) int {
	if b <= 0 {
		return def
	}
	return min(b, max)
}

// Provide builds a dossier and durably records its receipt.
func (e *Engine) Provide(ctx context.Context, req ProvideRequest) (*Dossier, error) {
	budget := clampBudget(req.Budget, DefaultContextBytes, MaxContextBytes)
	if budget < minBudget {
		return nil, fmt.Errorf("evidence: budget below minimum (%d bytes)", minBudget)
	}
	maxRec := clampBudget(req.MaxRecords, MaxRecords, MaxRecords)
	configs := e.configs
	if len(req.Configs) > 0 {
		if err := checkConfigs(req.Configs); err != nil {
			return nil, err
		}
		configs = req.Configs
	}
	if strings.TrimSpace(req.Task) == "" {
		return nil, errors.New("evidence: task is empty")
	}
	var lay layout
	switch req.Recipe {
	case "", RecipeNameDossier:
		if req.Profile != "" {
			return nil, errors.New("evidence: a cost profile applies to the locator recipe only")
		}
	case RecipeNameLocator:
		prof, err := LookupProfile(req.Profile)
		if err != nil {
			return nil, err
		}
		lay.locator, lay.profile = true, prof
	default:
		return nil, fmt.Errorf("evidence: unknown recipe %q (dossier or locator)", req.Recipe)
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		d, ok, err := e.provideOnce(ctx, req.Task, budget, maxRec, configs, req.CoChange, lay)
		if err != nil || ok {
			return d, err
		}
	}
	return nil, errUnstable
}

// provideOnce captures, ranks and renders. ok is false when a cited file
// changed on disk after capture (the caller retries); nothing is recorded
// in that case.
// layout selects the renderer.
type layout struct {
	locator bool
	profile CostProfile
}

func (e *Engine) provideOnce(ctx context.Context, task string, budget, maxRec int, configs []Config, co *CoChange, lay layout) (*Dossier, bool, error) {
	v, err := e.capture(ctx, e.repo, configs)
	if err != nil {
		return nil, false, err
	}
	cands, pointers, prior, revoked, err := e.rankTask(ctx, v, task, configs, co)
	if err != nil {
		return nil, false, err
	}
	var d *Dossier
	var rcpt *Receipt
	if lay.locator {
		d, rcpt, err = e.renderLocator(v, task, budget, maxRec, configs, cands, prior, revoked, lay.profile)
	} else {
		d, rcpt, err = e.renderDossier(v, task, budget, maxRec, configs, cands, pointers, prior, revoked)
	}
	if err != nil {
		return nil, false, err
	}
	// Certification: every file the dossier cites (spans, predicate items,
	// free-text pointers) is re-read and re-hashed now, whatever the stat
	// cache said.
	if !e.recheck(e.repo, v.snap, citedPaths(rcpt, d.FreeText)) {
		return nil, false, nil
	}
	if err := e.persistSnapshot(ctx, v.snap); err != nil {
		return nil, false, err
	}
	body, err := json.Marshal(rcpt)
	if err != nil {
		return nil, false, err
	}
	blobs := derivedBlobs(v.snap, rcpt.Blobs(), rcpt.Predicates)
	if err := e.store.PutReceipt(ctx, rcpt.ID, rcpt.Snapshot.ID, rcpt.PolicyID, rcpt.ObservedAt, body, blobs); err != nil {
		return nil, false, err
	}
	if err := e.recordDelivery(ctx, &Delivery{Receipt: rcpt.ID, Kind: DeliveryDossier, ObservedAt: rcpt.ObservedAt,
		SnapshotID: rcpt.Snapshot.ID, Records: len(d.Entries), Bytes: d.Bytes, Truncated: d.Truncated}, blobs); err != nil {
		return nil, false, err
	}
	return d, true, nil
}

// rankTask ranks the declarations of v for task. Forgotten files take no
// part in ranking: they are hidden from the lexical search (so they cannot
// use up its hit caps) and dropped from the rank index (BM25 statistics,
// name tables), the graph hop, structural neighbours and the co-change
// prior, so the ranking does not depend on their contents.
func (e *Engine) rankTask(ctx context.Context, v *view, task string, configs []Config, co *CoChange) ([]candidate, []Pointer, *coPrior, func(string) bool, error) {
	revoked, err := e.revokedFiles(ctx, v)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	terms := uniqueSorted(rank.Tokenize(task))
	lex, err := e.lex.Search(ctx, e.repo.Abs(), withoutFiles{v.snap, revoked}, terms, lexical.Limits{})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	prior := newCoPrior(v, co, revoked)
	cands, pointers := e.rankDecls(v, configs, task, terms, lex, prior, revoked)
	return cands, pointers, prior, revoked, nil
}

// withoutFiles is a lexical source with the files for which skip reports
// true hidden.
type withoutFiles struct {
	src  lexical.Source
	skip func(string) bool
}

func (w withoutFiles) Paths() []string {
	var out []string
	for _, p := range w.src.Paths() {
		if !w.skip(p) {
			out = append(out, p)
		}
	}
	return out
}

func (w withoutFiles) Bytes(p string) ([]byte, bool) {
	if w.skip(p) {
		return nil, false
	}
	return w.src.Bytes(p)
}

// revokedFiles returns a predicate for visible files whose captured blob
// was revoked by forget: they are never delivered as excerpts again.
func (e *Engine) revokedFiles(ctx context.Context, v *view) (func(string) bool, error) {
	tombs, err := e.store.Tombstones(ctx)
	if err != nil {
		return nil, err
	}
	return func(p string) bool {
		if len(tombs) == 0 {
			return false
		}
		h, ok := v.snap.SHA256(p)
		return ok && tombs[store.BlobTombstone(h)]
	}, nil
}

// citedPaths lists every path a receipt or dossier names.
func citedPaths(r *Receipt, ptrs []Pointer) []string {
	var out []string
	for _, f := range r.ReadSet {
		out = append(out, f.Path)
	}
	for _, p := range r.Predicates {
		for _, it := range p.Items {
			if it.Path != "" {
				out = append(out, it.Path)
			}
		}
	}
	for _, p := range ptrs {
		out = append(out, p.Path)
	}
	return uniqueSorted(out)
}

// derivedBlobs returns the blobs evidence was derived from: the given
// (delivered) blobs plus the captured blobs of every file a predicate item
// points into. Revoking any of them revokes the evidence.
func derivedBlobs(snap interface {
	SHA256(string) (string, bool)
}, delivered []string, preds []Predicate) []string {
	out := append([]string(nil), delivered...)
	for _, p := range preds {
		for _, it := range p.Items {
			if h, ok := snap.SHA256(it.Path); ok {
				out = append(out, h)
			}
		}
	}
	return uniqueSorted(out)
}

func uniqueSorted(xs []string) []string {
	sort.Strings(xs)
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func configsKey(cs []Config) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.String() + ";")
	}
	return b.String()
}

// rankIndex returns the ranking index over the declarations of v that are
// in some requested configuration and not in a forgotten file. It is
// cached per (snapshot, configurations, forgotten files): another process
// sharing the store may forget a blob without this engine's caches being
// dropped, so the forgotten set is part of the key.
func (e *Engine) rankIndex(v *view, configs []Config, revoked func(string) bool) *rankCache {
	var gone strings.Builder
	for _, f := range v.snap.Files {
		if revoked(f.Path) {
			gone.WriteString(f.Path + "\x00")
		}
	}
	key := v.snap.ID + "\x00" + configsKey(configs) + "\x00" + sha([]byte(gone.String()))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastRk != nil && e.lastRk.key == key {
		return e.lastRk
	}
	var docs []rank.Doc
	var refs []goanalyzer.DeclRef
	rc := &rankCache{key: key, names: map[string][]int{}, quals: map[string][]int{}, dirs: map[string]bool{}}
	genFile := map[int]bool{}
	v.ix.AllDecls(func(r goanalyzer.DeclRef) {
		if !v.ix.InAnyConfig(r.File) {
			return
		}
		ff, d := v.ix.Decl(r)
		if revoked(ff.Path) {
			return
		}
		i := len(docs)
		docs = append(docs, rank.Doc{
			Name: append(rank.Tokenize(d.Name), rank.Tokenize(d.Recv)...),
			Path: rank.Tokenize(strings.TrimSuffix(ff.Path, ".go")),
			Doc:  rank.Tokenize(d.Doc),
		})
		refs = append(refs, r)
		rc.names[d.Name] = append(rc.names[d.Name], i)
		if d.Recv != "" {
			rc.quals[d.Recv+"."+d.Name] = append(rc.quals[d.Recv+"."+d.Name], i)
		} else if ff.Package != "" {
			rc.quals[ff.Package+"."+d.Name] = append(rc.quals[ff.Package+"."+d.Name], i)
		}
		rc.dirs[ff.Dir] = true
		g, ok := genFile[r.File]
		if !ok {
			src, _ := v.snap.Bytes(ff.Path)
			g = generated(src)
			genFile[r.File] = g
		}
		rc.gen = append(rc.gen, g)
	})
	rc.ix, rc.refs = rank.New(docs), refs
	e.lastRk = rc
	return rc
}

// enclosingDecl returns the index of the top-level decl containing off,
// or -1. Decls are sorted by start and do not nest.
func enclosingDecl(ff *goanalyzer.FileFacts, off int) int {
	ds := ff.Decls
	i := sort.Search(len(ds), func(i int) bool { return ds[i].Start > off }) - 1
	if i >= 0 && off < ds[i].End {
		return i
	}
	return -1
}

func (e *Engine) rankDecls(v *view, configs []Config, task string, terms []string, lex lexical.Result, prior *coPrior, revoked func(string) bool) ([]candidate, []Pointer) {
	rc := e.rankIndex(v, configs, revoked)
	docOf := make(map[goanalyzer.DeclRef]int, len(rc.refs))
	for i, r := range rc.refs {
		docOf[r] = i
	}
	hits := rc.ix.Score(terms)

	// Attribute lexical hits.
	codeTerms := map[int]map[string]bool{}
	type ptrKey struct {
		path string
		line int
	}
	ptrs := map[ptrKey]*Pointer{}
	addPtr := func(path string, line int, sym, term string) {
		k := ptrKey{path, line}
		p := ptrs[k]
		if p == nil {
			p = &Pointer{Path: path, Line: line, Symbol: sym}
			ptrs[k] = p
		}
		for _, t := range p.Terms {
			if t == term {
				return
			}
		}
		p.Terms = append(p.Terms, term)
	}
	for _, h := range lex.Hits {
		if revoked(h.Path) {
			continue
		}
		ff, fi, ok := v.ix.File(h.Path)
		if !ok || !v.ix.InAnyConfig(fi) {
			addPtr(h.Path, h.Line, "", h.Term)
			continue
		}
		di := enclosingDecl(ff, h.Offset)
		sym := ""
		if di >= 0 {
			sym = ff.Decls[di].ID
		}
		if ff.InFreeText(h.Offset) || di < 0 {
			addPtr(h.Path, h.Line, sym, h.Term)
			continue
		}
		doc, ok := docOf[goanalyzer.DeclRef{File: fi, Decl: di}]
		if !ok {
			continue
		}
		if codeTerms[doc] == nil {
			codeTerms[doc] = map[string]bool{}
		}
		codeTerms[doc][h.Term] = true
	}

	byDoc := map[int]*candidate{}
	get := func(i int) *candidate {
		c := byDoc[i]
		if c == nil {
			c = &candidate{ref: rc.refs[i]}
			byDoc[i] = c
		}
		return c
	}
	for i, h := range hits {
		c := get(i)
		c.score = h.Score
		c.terms = uniqueSorted(append([]string(nil), h.MatchedTerm...))
		c.eligible = h.NameOrPath
	}
	for i, ts := range codeTerms {
		c := get(i)
		for t := range ts {
			c.code = append(c.code, t)
		}
		sort.Strings(c.code)
		for _, t := range c.code {
			c.score += codeHitWeight * min(rc.ix.IDF(t), codeHitIDFCap)
		}
		c.eligible = true
	}
	// Identifier mentions: the task spells a declaration's name.
	for _, m := range identMentions(task) {
		var docs []int
		label := m.name
		if m.qual != "" {
			docs, label = rc.quals[m.qual+"."+m.name], m.qual+"."+m.name
		} else {
			docs = rc.names[m.name]
		}
		if len(docs) == 0 || len(docs) > identMaxDocs {
			continue
		}
		w := identScore(len(rc.refs), len(docs))
		for _, i := range docs {
			c := get(i)
			if !slices.Contains(c.ident, label) {
				c.ident = append(c.ident, label)
				c.score += w
				c.eligible = true
			}
		}
	}
	scopes := taskScopes(task)
	mentioned := mentionedDirs(task, rc.dirs)
	for i, c := range byDoc {
		ff := v.ix.Files[c.ref.File]
		if ff.IsTest {
			c.score *= testPenalty
		}
		if rc.gen[i] {
			c.score *= generatedFactor
		}
		c.score *= scopeFactor(scopes, ff.Dir, ff.Path)
		if mentioned[ff.Dir] {
			c.score *= mentionFactor
		}
		sort.Strings(c.ident)
	}
	order := func(cs []*candidate) {
		sort.Slice(cs, func(i, j int) bool { return less(v, cs[i], cs[j]) })
	}
	var elig []*candidate
	for _, c := range byDoc {
		if c.eligible {
			elig = append(elig, c)
		}
	}
	order(elig)

	// Bounded graph expansion from the top seeds over scope-resolved
	// edges, plus structural neighbours (structure.go).
	xp := defaultExpand
	bump := func(nid string, hs float64, via string) {
		for _, r := range v.ix.DeclsByID(nid) {
			di, ok := docOf[r]
			if !ok {
				continue
			}
			c := get(di)
			if !c.eligible {
				c.eligible = true
				c.hop = via
				c.score = max(c.score, hs)
			} else if hs > c.score {
				c.score = hs
			}
		}
	}
	for s := 0; s < min(xp.seeds, len(elig)); s++ {
		seed := elig[s]
		_, sd := v.ix.Decl(seed.ref)
		for _, nid := range v.ix.NeighborsExcept(sd.ID, xp.fanout, revoked) {
			bump(nid, xp.hop*seed.score, sd.ID)
		}
		if xp.refs > 0 && sd.Kind != goanalyzer.KindMethod {
			if res, ok := v.ix.Evaluate(goanalyzer.RelReferences, sd.ID); ok && !res.Unsupported {
				n := 0
				for _, it := range res.Items {
					if n >= 2*xp.fanout {
						break
					}
					if it.Symbol == "" || it.Symbol == sd.ID || revoked(it.Path) {
						continue
					}
					if ff, _, ok := v.ix.File(it.Path); ok && ff.IsTest {
						continue
					}
					n++
					bump(it.Symbol, xp.refs*seed.score, sd.ID)
				}
			}
		}
	}
	var extra []*candidate
	if xp.twin > 0 || prior != nil {
		has := map[int]bool{}
		for _, c := range byDoc {
			if c.eligible {
				has[c.ref.File] = true
			}
		}
		seedFiles := map[int]float64{}
		var seedOrder []int
		for _, c := range elig {
			if len(seedOrder) < xp.twinSeeds {
				if _, ok := seedFiles[c.ref.File]; !ok {
					seedFiles[c.ref.File] = c.score
					seedOrder = append(seedOrder, c.ref.File)
				}
			}
			if len(seedOrder) >= xp.twinSeeds {
				break
			}
		}
		addRep := func(fi int, score float64, via string) {
			ff := v.ix.Files[fi]
			di := representative(ff)
			if di < 0 {
				return
			}
			ref := goanalyzer.DeclRef{File: fi, Decl: di}
			if d, ok := docOf[ref]; ok {
				c := get(d)
				if !c.eligible {
					c.eligible, c.hop, c.score = true, via, max(c.score, score)
				}
			} else {
				extra = append(extra, &candidate{ref: ref, score: score, eligible: true, hop: via})
			}
			has[fi] = true
		}
		if prior != nil && len(seedOrder) > 0 {
			var seeds []string
			var ws []float64
			top := seedFiles[seedOrder[0]]
			for _, sf := range seedOrder {
				seeds = append(seeds, v.ix.Files[sf].Path)
				ws = append(ws, seedFiles[sf]/top)
			}
			cs := prior.scores(seeds, ws)
			for _, c := range byDoc {
				if c.eligible {
					if p := cs[v.ix.Files[c.ref.File].Path]; p > 0 {
						c.score *= 1 + xp.ccBoost*p
					}
				}
			}
			if xp.ccRep > 0 {
				paths := make([]string, 0, len(cs))
				for p := range cs {
					paths = append(paths, p)
				}
				sort.Strings(paths)
				for _, p := range paths {
					ff, fi, ok := v.ix.File(p)
					if !ok || ff.IsTest || has[fi] || !v.ix.InAnyConfig(fi) || revoked(p) {
						continue
					}
					addRep(fi, xp.ccRep*cs[p]*top, "cochange")
				}
			}
		}
		for fi, ff := range v.ix.Files {
			if ff.IsTest || has[fi] || revoked(ff.Path) {
				continue
			}
			if xp.twin > 0 {
				for _, sf := range seedOrder {
					sff := v.ix.Files[sf]
					if sff.Dir == ff.Dir && variantSiblings(sff.Path, ff.Path) {
						addRep(fi, xp.twin*seedFiles[sf], "variant "+path.Base(sff.Path))
						break
					}
				}
			}
		}
	}
	elig = elig[:0]
	var docOnly []*candidate
	for _, c := range byDoc {
		if c.eligible {
			elig = append(elig, c)
		} else if c.score > 0 {
			docOnly = append(docOnly, c)
		}
	}
	elig = append(elig, extra...)
	order(elig)
	order(docOnly)
	out := make([]candidate, len(elig))
	for i, c := range elig {
		out[i] = *c
	}
	// Doc-only decls are free-text matches: pointers, never pushed spans.
	for _, c := range docOnly {
		ff, d := v.ix.Decl(c.ref)
		line := d.DocLine
		if line == 0 {
			line = d.StartLine
		}
		for _, t := range c.terms {
			addPtr(ff.Path, line, d.ID, t)
		}
	}
	plist := make([]Pointer, 0, len(ptrs))
	for _, p := range ptrs {
		sort.Strings(p.Terms)
		plist = append(plist, *p)
	}
	sort.Slice(plist, func(i, j int) bool {
		a, b := plist[i], plist[j]
		if len(a.Terms) != len(b.Terms) {
			return len(a.Terms) > len(b.Terms)
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	if len(plist) > 50 {
		plist = plist[:50]
	}
	return out, plist
}

func less(v *view, a, b *candidate) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	fa, da := v.ix.Decl(a.ref)
	fb, db := v.ix.Decl(b.ref)
	if da.ID != db.ID {
		return da.ID < db.ID
	}
	if fa.Path != fb.Path {
		return fa.Path < fb.Path
	}
	return da.Start < db.Start
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
