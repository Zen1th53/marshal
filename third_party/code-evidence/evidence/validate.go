package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/code-evidence/internal/fsread"
	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
	"github.com/Zen1th53/code-evidence/internal/render"
	"github.com/Zen1th53/code-evidence/internal/snapshot"
)

// Validate re-checks receipts against the candidate tree in req.Head (the
// exact candidate merge tree; "" = engine root). Spans and predicates are
// reported separately. Changed dependencies only schedule re-evaluation;
// "invalidated" always comes with an anchored counterexample.
//
// Validation is output-sensitive: for receipts from the engine's store it
// re-analyzes only the files that changed since the receipt's snapshot
// (for coverage) and the closure of files that can influence the
// receipt's predicates and spans (goanalyzer.Closure). req.Full forces a
// full re-analysis of the head; the two are equivalent (differential
// test).
func (e *Engine) Validate(ctx context.Context, req ValidateRequest) (*ValidationReport, error) {
	repo := e.repo
	if req.Head != "" {
		r, err := fsread.Open(req.Head)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		repo = r
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		rep, cited, err := e.validateOnce(ctx, repo, req)
		if err != nil {
			return nil, err
		}
		if e.recheck(repo, rep.snap, cited) {
			return rep.ValidationReport, nil
		}
	}
	return nil, errUnstable
}

type loadedReceipt struct {
	r *Receipt
	// trusted receipts come from the engine's own store, so their
	// recorded coverage and manifest may be relied on.
	trusted bool
	old     map[string]string // receipt snapshot manifest: path -> sha
	changed map[string]bool   // head paths whose bytes differ from old
	mixed   bool              // deliveries observed at a different snapshot require full evaluation
	inc     bool              // eligible for incremental validation
}

type validation struct {
	*ValidationReport
	snap *snapshot.Snapshot
}

func (e *Engine) validateOnce(ctx context.Context, repo *fsread.Repo, req ValidateRequest) (*validation, []string, error) {
	snap, err := e.captureSnap(ctx, repo)
	if err != nil {
		return nil, nil, err
	}
	rep := &ValidationReport{Head: HeadRef{SnapshotID: snap.ID, Universe: snap.Universe, Files: len(snap.Files)}, Receipts: []ReceiptVerdict{}}
	empty := func(id, status string) ReceiptVerdict {
		return ReceiptVerdict{Receipt: id, Status: status, Spans: []SpanVerdict{}, Predicates: []PredVerdict{}}
	}
	var receipts []*loadedReceipt
	for _, id := range req.Receipts {
		r, err := e.loadReceipt(ctx, id)
		switch {
		case errors.Is(err, errRevoked):
			rep.Receipts = append(rep.Receipts, empty(id, "evidence_revoked"))
		case err != nil:
			rep.Receipts = append(rep.Receipts, empty(id, "not_found"))
		default:
			lr := &loadedReceipt{r: r, trusted: true}
			rows, err := e.store.Deliveries(ctx, r.ID)
			if err != nil {
				return nil, nil, err
			}
			// Work on a copy: receipt identity remains the original signed body.
			copyReceipt := *r
			copyReceipt.Spans = append([]Span(nil), r.Spans...)
			copyReceipt.Predicates = append([]Predicate(nil), r.Predicates...)
			lr.r = &copyReceipt
			for _, row := range rows {
				var d Delivery
				if err := json.Unmarshal(row.Body, &d); err != nil {
					return nil, nil, err
				}
				if d.Kind != DeliveryExpand {
					continue
				}
				lr.mixed = lr.mixed || d.SnapshotID != r.Snapshot.ID
				lr.r.Spans = append(lr.r.Spans, d.Spans...)
				lr.r.Predicates = append(lr.r.Predicates, d.Predicates...)
			}
			lr.r.Spans = distinctFacts(lr.r.Spans)
			lr.r.Predicates = distinctFacts(lr.r.Predicates)
			receipts = append(receipts, lr)
		}
	}
	for _, b := range req.Bodies {
		r, err := ParseReceipt(b)
		if err != nil {
			return nil, nil, err
		}
		if r.PolicyID != e.pol.ID() {
			rep.Receipts = append(rep.Receipts, empty(r.ID, "policy_mismatch"))
			continue
		}
		receipts = append(receipts, &loadedReceipt{r: r})
	}

	// Classify receipts and compute the head coverage: from the manifest
	// of a receipt with complete coverage (parsing only changed files),
	// else from the per-file facts of every Go file (cached when warm).
	var headCov *Coverage
	anyInc := false
	for _, lr := range receipts {
		lr.old, lr.changed = e.changedSince(ctx, lr.r, snap)
		if lr.mixed {
			// The original manifest cannot bound changes to later deliveries.
			lr.changed = nil
		}
		lr.inc = !req.Full && !lr.mixed && lr.trusted && lr.old != nil && lr.r.Versions.Analyzer == goanalyzer.Version
		anyInc = anyInc || lr.inc
		if fastPath(lr, snap) {
			// Same snapshot: the head coverage is the recorded one.
			c := lr.r.Predicates[0].Coverage
			headCov = &c
		}
	}
	for _, lr := range receipts {
		if lr.inc && headCov == nil && receiptCoverageComplete(lr.r) {
			c := e.coverageFrom(snap, lr.old)
			headCov = &c
		}
	}
	if headCov == nil && anyInc {
		c := e.coverageFrom(snap, nil)
		headCov = &c
	}
	views := map[string]*view{}
	if headCov == nil {
		v := e.analyze(snap, e.configs)
		views[configsKey(e.configs)] = v
		c := v.ix.Coverage
		headCov = &c
	}
	rep.Head.Coverage = *headCov

	// One index per configuration set: the full head index if any receipt
	// of the set needs it, else a partial index over the union closure.
	groups := map[string][]*loadedReceipt{}
	var order []string
	for _, lr := range receipts {
		if fastPath(lr, snap) {
			continue // nothing changed: see below
		}
		k := configsKey(lr.r.Config.Effective)
		if groups[k] == nil {
			order = append(order, k)
		}
		groups[k] = append(groups[k], lr)
	}
	for _, k := range order {
		if views[k] != nil {
			continue
		}
		g := groups[k]
		full := false
		var subjects, extra []string
		for _, lr := range g {
			full = full || !lr.inc
			for _, sp := range lr.r.Spans {
				extra = append(extra, sp.Path)
				if sp.Symbol != "" {
					subjects = append(subjects, sp.Symbol)
				}
			}
			for _, p := range lr.r.Predicates {
				subjects = append(subjects, p.Subject)
			}
		}
		cs := g[0].r.Config.Effective
		if full {
			views[k] = e.analyze(snap, cs)
			continue
		}
		cache := e.factsCache()
		inc := goanalyzer.Closure(snap, cache, uniqueSorted(subjects), uniqueSorted(extra))
		views[k] = &view{snap: snap, ix: goanalyzer.BuildPartial(snap, cs, cache, inc, *headCov)}
	}

	// Forgotten files never appear in validation output either.
	revokedHead, err := e.revokedFiles(ctx, &view{snap: snap})
	if err != nil {
		return nil, nil, err
	}
	var cited []string
	for _, lr := range receipts {
		r := lr.r
		rv := ReceiptVerdict{Receipt: r.ID, Status: "checked", Spans: []SpanVerdict{}, Predicates: []PredVerdict{},
			Summary: VerdictSummary{Spans: map[string]int{}, Predicates: map[string]int{}}}
		blobs := r.Blobs()
		if lr.trusted {
			stored, err := e.store.ReceiptBlobs(ctx, r.ID)
			if err != nil {
				return nil, nil, err
			}
			blobs = append(blobs, stored...)
		}
		revoked, err := e.store.Revoked(ctx, blobs)
		if err != nil {
			return nil, nil, err
		}
		same := fastPath(lr, snap)
		v := views[configsKey(r.Config.Effective)]
		switch {
		case same:
			e.valFast.Add(1)
		case v.ix.Partial():
			e.valPartial.Add(1)
		default:
			e.valFull.Add(1)
		}
		for i, s := range r.Spans {
			cited = append(cited, s.Path)
			sv := SpanVerdict{Index: i, Path: s.Path, Symbol: s.Symbol}
			switch {
			case revoked[s.Blob]:
				sv.Verdict, sv.Detail = SpanUnknown, "evidence_revoked"
			case same:
				sv.Verdict, sv.NowLine = SpanUnchanged, s.StartLine
			default:
				sv.Verdict, sv.Detail, sv.NowLine = checkSpan(v, s, revokedHead)
			}
			rv.Spans = append(rv.Spans, sv)
			rv.Summary.Spans[sv.Verdict]++
		}
		for i, p := range r.Predicates {
			pv := PredVerdict{Index: i, Relation: p.Relation, Subject: p.Subject}
			switch {
			case len(revoked) > 0:
				pv.Verdict, pv.Reason = PredUnknown, "evidence_revoked"
			case p.Withheld > 0:
				pv.Verdict, pv.Reason = PredUnknown, "evidence_withheld: "+withheldNote(p.Withheld)
			case same && !p.Coverage.Complete:
				pv.Verdict, pv.Reason = PredUnknown, coverageReason(p.Coverage)
			case same:
				// Same snapshot, same analyzer and configurations: the
				// result is the recorded one.
				pv.Verdict = PredHolds
			default:
				pv.Verdict, pv.Reason, pv.Anchors = checkPredicate(v, p, lr.changed)
				pv.Reason, pv.Anchors = withholdAnchors(pv.Reason, pv.Anchors, revokedHead)
			}
			for _, a := range pv.Anchors {
				cited = append(cited, a.Path)
			}
			rv.Predicates = append(rv.Predicates, pv)
			rv.Summary.Predicates[pv.Verdict]++
		}
		if len(revoked) > 0 {
			rv.Status = "evidence_revoked"
		}
		rep.Receipts = append(rep.Receipts, rv)
	}
	rep.Text = validationText(rep)
	return &validation{ValidationReport: rep, snap: snap}, uniqueSorted(cited), nil
}

// Identical deliveries count as one fact; observations with different bytes
// or relation results remain independent obligations.
func distinctFacts[T Span | Predicate](facts []T) []T {
	seen := make(map[string]bool, len(facts))
	out := facts[:0]
	for _, fact := range facts {
		b, _ := json.Marshal(fact) // Span and Predicate contain only JSON values.
		if key := string(b); !seen[key] {
			seen[key] = true
			out = append(out, fact)
		}
	}
	return out
}

// fastPath: the head is the receipt's own snapshot (same analyzer and
// configurations), so every span is unchanged and every predicate holds,
// or is unknown for the recorded incomplete coverage, without
// re-evaluation.
func fastPath(lr *loadedReceipt, snap *snapshot.Snapshot) bool {
	return lr.inc && lr.r.Snapshot.ID == snap.ID && len(lr.r.Predicates) > 0
}

func coverageReason(cov Coverage) string {
	return fmt.Sprintf("coverage_incomplete: %d go files failed to parse, %d skipped at head", cov.Failed, cov.Skipped)
}

// receiptCoverageComplete reports whether the receipt recorded complete
// coverage (all of its predicates share the provide-time coverage).
func receiptCoverageComplete(r *Receipt) bool {
	if len(r.Predicates) == 0 {
		return false
	}
	for _, p := range r.Predicates {
		if !p.Coverage.Complete {
			return false
		}
	}
	return true
}

// coverageFrom computes the head coverage exactly. With old, a manifest
// whose coverage was complete, it parses only Go files that differ from
// it: every unchanged file parsed then and parses now (facts are a pure
// function of path and bytes). With old == nil it consults the facts of
// every Go file.
func (e *Engine) coverageFrom(snap *snapshot.Snapshot, old map[string]string) Coverage {
	var c Coverage
	cache := e.factsCache()
	for _, f := range snap.Files {
		if !goanalyzer.Analyzable(f.Path) {
			continue
		}
		c.GoFiles++
		if _, ok := snap.Bytes(f.Path); !ok {
			c.Skipped++
			continue
		}
		if old[f.Path] == f.SHA256 {
			c.Parsed++
			continue
		}
		if ff := goanalyzer.FactsOf(snap, cache, f.Path); ff == nil {
			c.Skipped++
		} else if ff.Failed() {
			c.Failed++
		} else {
			c.Parsed++
		}
	}
	c.Complete = c.Failed == 0 && c.Skipped == 0
	return c
}

// changedSince loads the receipt's snapshot manifest and returns it with
// the head paths whose bytes differ from it (nil, nil if the manifest is
// unavailable).
func (e *Engine) changedSince(ctx context.Context, r *Receipt, head *snapshot.Snapshot) (map[string]string, map[string]bool) {
	rows, err := e.store.SnapshotFiles(ctx, r.Snapshot.ID)
	if err != nil || len(rows) == 0 {
		return nil, nil
	}
	old := make(map[string]string, len(rows))
	for _, f := range rows {
		old[f.Path] = f.SHA256
	}
	out := map[string]bool{}
	for _, f := range head.Files {
		if old[f.Path] != f.SHA256 {
			out[f.Path] = true
		}
	}
	return old, out
}

// checkSpan compares a delivered span with the head. revoked reports
// head files whose captured blob was forgotten: their bytes are never
// compared and their paths never named (a moved declaration whose only
// new location is forgotten says so by count).
func checkSpan(v *view, s Span, revoked func(string) bool) (string, string, int) {
	f, ok := v.snap.File(s.Path)
	if !ok {
		return SpanRemoved, "file removed", 0
	}
	if f.Skipped != "" {
		return SpanUnknown, "file not captured: " + f.Skipped, 0
	}
	if revoked(s.Path) {
		return SpanUnknown, "evidence_revoked at head", 0
	}
	if f.SHA256 == s.Blob {
		return SpanUnchanged, "", s.StartLine
	}
	src, _ := v.snap.Bytes(s.Path)
	length := s.End - s.Start
	if s.Symbol == "" {
		if s.End <= len(src) && sha(src[s.Start:s.End]) == s.Content {
			return SpanUnchanged, "blob changed, span bytes identical", s.StartLine
		}
		return SpanChanged, "", 0
	}
	if _, _, ok := v.ix.File(s.Path); !ok {
		return SpanUnknown, "file no longer parses", 0
	}
	var elsewhere string
	found, withheld := false, 0
	for _, r := range v.ix.DeclsByID(s.Symbol) {
		ff, d := v.ix.Decl(r)
		if ff.Path != s.Path {
			if revoked(ff.Path) {
				withheld++
			} else if elsewhere == "" {
				elsewhere = ff.Path
			}
			continue
		}
		found = true
		base := d.Start
		if d.DocStart > 0 && d.DocLine > 0 {
			base = d.DocStart
		}
		st := base + s.RelStart
		en := st + length
		if st >= 0 && en <= len(src) && sha(src[st:en]) == s.Content {
			line := 1 + strings.Count(string(src[:st]), "\n")
			detail := ""
			if line != s.StartLine {
				detail = fmt.Sprintf("moved from line %d", s.StartLine)
			}
			return SpanUnchanged, detail, line
		}
	}
	if found {
		return SpanChanged, "declaration edited", 0
	}
	if elsewhere != "" {
		return SpanChanged, "declaration moved to " + render.Path(elsewhere), 0
	}
	if withheld > 0 {
		return SpanChanged, fmt.Sprintf("declaration moved; +%d locations withheld (revoked)", withheld), 0
	}
	return SpanRemoved, "declaration removed", 0
}

func checkPredicate(v *view, p Predicate, changed map[string]bool) (string, string, []Anchor) {
	cov := v.ix.Coverage
	if !cov.Complete {
		return PredUnknown, coverageReason(cov), nil
	}
	res, ok := v.ix.Evaluate(p.Relation, p.Subject)
	if !ok {
		return PredUnknown, "subject_not_found at head", nil
	}
	if res.Unsupported {
		return PredUnknown, "unsupported at head (subject kind changed)", nil
	}
	if res.Digest() == p.ResultDigest {
		if p.Deps != "" && res.Deps != p.Deps {
			var anchors []Anchor
			_, d := v.ix.Decl(v.ix.DeclsByID(p.Subject)[0])
			for _, it := range v.ix.MethodDecls(d.Name) {
				if changed == nil || changed[it.Path] {
					anchors = append(anchors, Anchor{Change: "new_method", Path: it.Path, Line: it.Line, Symbol: it.Symbol})
				}
			}
			return PredUnknown, "rebinding_possible: the set of methods named " + d.Name + " changed; tier-1 cannot resolve receivers", anchors
		}
		return PredHolds, "", nil
	}
	old := make(map[string]bool, len(p.Items))
	for _, it := range p.Items {
		old[it.Key] = true
	}
	now := make(map[string]bool, len(res.Items))
	var anchors []Anchor
	for _, it := range res.Items {
		now[it.Key] = true
		// With truncated stored items, only occurrences in changed files
		// can be new (syntactic monotonicity over occurrences).
		if !old[it.Key] && (!p.ItemsTruncated || changed == nil || changed[it.Path]) {
			anchors = append(anchors, Anchor{Change: "added", Path: it.Path, Line: it.Line, Symbol: it.Symbol})
		}
	}
	for _, it := range p.Items {
		if !now[it.Key] {
			anchors = append(anchors, Anchor{Change: "removed", Path: it.Path, Line: it.Line})
		}
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		if anchors[i].Change != anchors[j].Change {
			return anchors[i].Change < anchors[j].Change
		}
		if anchors[i].Path != anchors[j].Path {
			return anchors[i].Path < anchors[j].Path
		}
		return anchors[i].Line < anchors[j].Line
	})
	reason := fmt.Sprintf("%d added, %d removed (%d items before, %d now)", countChange(anchors, "added"), countChange(anchors, "removed"), p.ItemCount, len(res.Items))
	if p.ItemsTruncated {
		reason += "; receipt kept only the first items, anchors limited to changed files"
	}
	return PredInvalidated, reason, anchors
}

func countChange(as []Anchor, c string) int {
	n := 0
	for _, a := range as {
		if a.Change == c {
			n++
		}
	}
	return n
}

func summaryText(m map[string]int, order []string) string {
	var parts []string
	for _, k := range order {
		if m[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", m[k], k))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func anchorText(a Anchor) string {
	s := render.Path(a.Path) + fmt.Sprintf(":%d", a.Line)
	if a.Symbol != "" {
		s += " (" + render.Path(a.Symbol) + ")"
	}
	return s
}

func validationText(rep *ValidationReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# code_validate head %s (%d files; %s)\n", shortID(rep.Head.SnapshotID), rep.Head.Files, coverageNote(rep.Head.Coverage))
	for _, rv := range rep.Receipts {
		if rv.Status != "checked" {
			fmt.Fprintf(&b, "receipt %s: %s\n", rv.Receipt, rv.Status)
			if rv.Status != "evidence_revoked" {
				continue
			}
		} else {
			fmt.Fprintf(&b, "receipt %s: spans %s; predicates %s\n", rv.Receipt,
				summaryText(rv.Summary.Spans, []string{SpanUnchanged, SpanChanged, SpanRemoved, SpanUnknown}),
				summaryText(rv.Summary.Predicates, []string{PredHolds, PredInvalidated, PredUnknown}))
		}
		for _, s := range rv.Spans {
			if s.Verdict == SpanUnchanged {
				continue
			}
			fmt.Fprintf(&b, "  span #%d %s %s: %s", s.Index, render.Path(s.Path), render.Path(s.Symbol), s.Verdict)
			if s.Detail != "" {
				b.WriteString(" (" + s.Detail + ")")
			}
			b.WriteString("\n")
		}
		for _, p := range rv.Predicates {
			if p.Verdict == PredHolds {
				continue
			}
			fmt.Fprintf(&b, "  pred #%d %s(%s): %s", p.Index, p.Relation, render.Path(p.Subject), p.Verdict)
			if p.Reason != "" {
				b.WriteString(" - " + p.Reason)
			}
			b.WriteString("\n")
			for i, a := range p.Anchors {
				if i == 5 {
					fmt.Fprintf(&b, "    +%d more\n", len(p.Anchors)-5)
					break
				}
				fmt.Fprintf(&b, "    %s %s\n", a.Change, anchorText(a))
			}
		}
	}
	return b.String()
}

// withholdAnchors drops counterexample anchors inside forgotten files and
// notes how many were withheld.
func withholdAnchors(reason string, as []Anchor, revoked func(string) bool) (string, []Anchor) {
	n := 0
	keep := as[:0:0]
	for _, a := range as {
		if revoked(a.Path) {
			n++
			continue
		}
		keep = append(keep, a)
	}
	if n == 0 {
		return reason, as
	}
	return reason + "; " + fmt.Sprintf("+%d anchors withheld (revoked)", n), keep
}
