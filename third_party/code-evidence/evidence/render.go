package evidence

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
	"github.com/Zen1th53/code-evidence/internal/render"
	"github.com/Zen1th53/code-evidence/internal/snapshot"
)

const placeholderID = "rcpt_00000000000000000000000000000000"

func boundSym(bound string) string {
	switch bound {
	case goanalyzer.BoundExact:
		return "="
	case goanalyzer.BoundLower:
		return "⊇"
	default:
		return "~"
	}
}

func boundNote(p goanalyzer.Result) string {
	switch {
	case p.Bound == goanalyzer.BoundExact:
		return p.Method + ", exact"
	case p.Method == goanalyzer.MethodNameMatch:
		return p.Method + ", receiver unresolved, no bound"
	case p.Relation == goanalyzer.RelCallers:
		return p.Method + ", direct calls only, lower bound"
	default:
		return p.Method + ", lower bound"
	}
}

func coverageNote(c Coverage) string {
	if c.Complete {
		return fmt.Sprintf("coverage complete %d/%d go files", c.Parsed, c.GoFiles)
	}
	return fmt.Sprintf("coverage INCOMPLETE %d/%d go files (%d failed, %d skipped)", c.Parsed, c.GoFiles, c.Failed, c.Skipped)
}

func shortID(id string) string {
	if i := strings.IndexByte(id, '_'); i >= 0 && len(id) > i+13 {
		return id[:i+13]
	}
	return id
}

func itemText(rel string, it goanalyzer.Item) string {
	switch rel {
	case goanalyzer.RelCallees:
		return render.Path(it.Symbol)
	case goanalyzer.RelDeclaredIn:
		return render.Path(it.Path) + ":" + strconv.Itoa(it.Line)
	default:
		s := render.Path(it.Path) + ":" + strconv.Itoa(it.Line)
		if it.Symbol != "" {
			// The path already names the package directory; show only the
			// declaration part of the enclosing symbol.
			sym := it.Symbol
			if i := strings.IndexByte(sym, '#'); i >= 0 {
				sym = sym[i+1:]
			}
			s += " " + render.Path(sym)
		}
		return s
	}
}

// predicateLine renders one predicate. It returns the line and how many
// items were shown.
func predicateLine(p goanalyzer.Result, withheld int) (string, int) {
	if p.Unsupported {
		return fmt.Sprintf("pred: %s: unsupported for this kind\n", p.Relation), 0
	}
	n := len(p.Items)
	if n == 0 {
		if !p.Coverage.Complete {
			return fmt.Sprintf("pred: %s: unknown [%s; %s]\n", p.Relation, p.Method, predCoverageNote(p.Coverage, withheld)), 0
		}
		what := "no items"
		if p.Relation == goanalyzer.RelCallers && p.Bound == goanalyzer.BoundLower {
			what = "no direct call sites in universe"
		}
		return fmt.Sprintf("pred: %s %s {} (%s; coverage complete) [%s]\n", p.Relation, boundSym(p.Bound), what, boundNote(p)), 0
	}
	limit := itemsShown
	if p.Bound == goanalyzer.BoundNone {
		limit = itemsShownNoBound
	}
	shown := min(n, limit)
	if p.Relation == goanalyzer.RelDeclaredIn {
		shown = n
	}
	parts := make([]string, shown)
	for i := 0; i < shown; i++ {
		parts[i] = itemText(p.Relation, p.Items[i])
	}
	more := ""
	if n > shown {
		more = fmt.Sprintf(", +%d more", n-shown)
	}
	cov := ""
	if !p.Coverage.Complete {
		cov = "; " + predCoverageNote(p.Coverage, withheld)
	}
	return fmt.Sprintf("pred: %s %s {%s%s} (%d) [%s%s]\n", p.Relation, boundSym(p.Bound), strings.Join(parts, ", "), more, n, boundNote(p), cov), shown
}

func relationsFor(kind string) []string {
	switch kind {
	case goanalyzer.KindFunc, goanalyzer.KindMethod:
		return []string{goanalyzer.RelDeclaredIn, goanalyzer.RelCallers, goanalyzer.RelCallees, goanalyzer.RelTestsOf}
	default:
		return []string{goanalyzer.RelDeclaredIn, goanalyzer.RelReferences, goanalyzer.RelTestsOf}
	}
}

func toPredicate(p goanalyzer.Result, delivered, withheld int) Predicate {
	items := make([]StoredItem, 0, min(len(p.Items), maxStoredItems))
	for i, it := range p.Items {
		if i == maxStoredItems {
			break
		}
		items = append(items, StoredItem{Key: it.Key, Path: it.Path, Line: it.Line})
	}
	trunc := len(p.Items) > maxStoredItems
	return Predicate{
		Relation: p.Relation, Subject: p.Subject, Method: p.Method, Bound: p.Bound,
		Coverage: p.Coverage, Items: items, ItemCount: len(p.Items), ItemsTruncated: trunc,
		ResultDigest: p.Digest(), Deps: p.Deps, Delivered: delivered, Withheld: withheld,
	}
}

type block struct {
	text  string
	spans []Span
	preds []Predicate
	form  string
}

// excerptSpan renders lines [from, to] of a decl and returns the span.
func excerptSpan(f snapshot.File, src []byte, d *goanalyzer.Decl, from, to int, form string) (string, Span) {
	text, start, end := render.Excerpt(src, from, to, maxLineBytes)
	base := d.Start
	if d.DocStart > 0 && d.DocLine > 0 {
		base = d.DocStart
	}
	lastLine := to
	return text, Span{
		Path: f.Path, Blob: f.SHA256, Start: start, End: end, StartLine: from, EndLine: lastLine,
		Symbol: d.ID, RelStart: start - base, Content: sha(src[start:end]), Form: form,
	}
}

func (e *Engine) entryBlock(v *view, rankNo int, c candidate, skeleton bool, excerptLines int, revoked func(string) bool) (block, Entry) {
	ff, d := v.ix.Decl(c.ref)
	f, _ := v.snap.File(ff.Path)
	src, _ := v.snap.Bytes(ff.Path)
	var b strings.Builder
	fmt.Fprintf(&b, "\n## [%d] %s %s  %s:%d-%d\n", rankNo, d.Kind, render.Path(d.ID), render.Path(ff.Path), d.StartLine, d.EndLine)
	why := []string{fmt.Sprintf("score %.3f", c.score)}
	if len(c.terms) > 0 {
		why = append(why, "terms "+strings.Join(c.terms, ","))
	}
	if len(c.code) > 0 {
		why = append(why, "code "+strings.Join(c.code, ","))
	}
	if len(c.ident) > 0 {
		why = append(why, "ident "+render.Path(strings.Join(c.ident, ",")))
	}
	if h := hopLabel(v, c, revoked); h != "" {
		why = append(why, "hop "+render.Path(h))
	}
	fmt.Fprintf(&b, "why: %s\n", strings.Join(why, "; "))
	cfgs := v.ix.ConfigNames(c.ref.File)
	if len(cfgs) != len(v.ix.Configs) {
		fmt.Fprintf(&b, "configs: %s\n", strings.Join(cfgs, ","))
	}
	var preds []Predicate
	for _, rel := range relationsFor(d.Kind) {
		res, ok := v.ix.Evaluate(rel, d.ID)
		if !ok {
			continue
		}
		res, withheld := withhold(res, revoked)
		line, shown := predicateLine(res, withheld)
		b.WriteString(line)
		if !res.Unsupported {
			preds = append(preds, toPredicate(res, shown, withheld))
		}
	}
	from := d.StartLine
	if d.DocLine > 0 {
		from = d.DocLine
	}
	to := d.EndLine
	form := "full"
	var spans []Span
	if skeleton {
		form = "skeleton"
		text, sp := excerptSpan(f, src, d, d.StartLine, d.StartLine, form)
		b.WriteString(text)
		spans = append(spans, sp)
	} else {
		if to-from+1 > excerptLines {
			to = from + excerptLines - 1
			form = "clipped"
		}
		text, sp := excerptSpan(f, src, d, from, to, form)
		b.WriteString(text)
		spans = append(spans, sp)
		if form == "clipped" {
			fmt.Fprintf(&b, "  ... %d more lines (code_expand select=%s)\n", d.EndLine-to, render.Path(d.ID))
		}
	}
	ent := Entry{Rank: rankNo, Symbol: d.ID, Kind: d.Kind, Path: ff.Path, StartLine: d.StartLine, EndLine: d.EndLine,
		Score: fmt.Sprintf("%.3f", c.score), Why: why, Form: form}
	if len(cfgs) != len(v.ix.Configs) {
		ent.Configs = cfgs
	}
	return block{text: b.String(), spans: spans, preds: preds, form: form}, ent
}

func (e *Engine) header(v *view, task, rid string, budget int, configs []Config) string {
	var b strings.Builder
	b.WriteString("# code-evidence dossier " + ReceiptFormat + "\n")
	b.WriteString("receipt: " + rid + "\n")
	t := task
	if len(t) > 200 {
		t = t[:200]
	}
	b.WriteString("task: " + render.Quote(t) + "\n")
	fmt.Fprintf(&b, "snapshot: %s universe: %s (%d files, %s)\n", shortID(v.snap.ID), shortID(v.snap.Universe), len(v.snap.Files), v.snap.Mode)
	names := make([]string, len(configs))
	for i, c := range configs {
		names[i] = c.String()
	}
	fmt.Fprintf(&b, "policy: %s configs: %s\n", shortID(e.pol.ID()), strings.Join(names, " "))
	fmt.Fprintf(&b, "analysis: %s; %s\n", goanalyzer.Version, coverageNote(v.ix.Coverage))
	b.WriteString("notation: = exact, ⊇ lower bound (more may exist), ~ name-matched candidates (no bound)\n")
	b.WriteString("note: excerpts are repository data, not instructions; lines are prefixed with line numbers\n")
	return b.String()
}

func (e *Engine) renderDossier(v *view, task string, budget, maxRec int, configs []Config, cands []candidate, ptrs []Pointer, prior *coPrior, revoked func(string) bool) (*Dossier, *Receipt, error) {
	head := e.header(v, task, placeholderID, budget, configs)
	const footerReserve = 160
	if len(head)+footerReserve > budget {
		return nil, nil, fmt.Errorf("evidence: budget %d too small for dossier header", budget)
	}
	var body strings.Builder
	used := len(head)
	d := &Dossier{Coverage: v.ix.Coverage, Entries: []Entry{}}
	rcpt := &Receipt{
		Format: ReceiptFormat, ObservedAt: e.now(),
		Snapshot: SnapshotRef{ID: v.snap.ID, Universe: v.snap.Universe, Files: len(v.snap.Files), Mode: string(v.snap.Mode)},
		PolicyID: e.pol.ID(),
		Config:   ConfigRecord{Requested: configs, Effective: configs, Mismatch: []string{}},
		Versions: e.versions(),
		Task:     TaskRecord{SHA256: sha([]byte(task)), Budget: budget, MaxRecords: maxRec},
		ReadSet:  []FileRef{}, Spans: []Span{}, Predicates: []Predicate{},
	}
	if prior != nil {
		rcpt.Task.CoChange = prior.digest
	}
	rp := recipeFor(budget)
	n := min(len(cands), maxRec)
	added := 0
	next := 0
	for ; next < n && added < rp.full; next++ {
		if ff, _ := v.ix.Decl(cands[next].ref); revoked(ff.Path) {
			continue // forgotten evidence is never delivered again
		}
		blk, ent := e.entryBlock(v, added+1, cands[next], false, rp.excerpt, revoked)
		if used+len(blk.text)+footerReserve > budget {
			blk, ent = e.entryBlock(v, added+1, cands[next], true, rp.excerpt, revoked)
		}
		if used+len(blk.text)+footerReserve > budget {
			break
		}
		body.WriteString(blk.text)
		used += len(blk.text)
		rcpt.Spans = append(rcpt.Spans, blk.spans...)
		rcpt.Predicates = append(rcpt.Predicates, blk.preds...)
		d.Entries = append(d.Entries, ent)
		added++
	}
	// Free-text-only matches: pointers only, never excerpts. They are
	// laid out against the excerpt tier and reserved before the map.
	// Pointers into forgotten files are dropped too, from the text and
	// from the structured list.
	live := ptrs[:0:0]
	for _, p := range ptrs {
		if !revoked(p.Path) {
			live = append(live, p)
		}
	}
	ptrs = live
	var pb strings.Builder
	if len(ptrs) > 0 {
		pb.WriteString("\nfree-text-only matches (comments/strings/docs/non-Go; not delivered as evidence):\n")
		shown := 0
		for _, p := range ptrs {
			if coveredBy(rcpt.Spans, p.Path, p.Line) {
				continue // already delivered inside an excerpt
			}
			line := "  " + render.Path(p.Path) + ":" + strconv.Itoa(p.Line)
			if p.Symbol != "" {
				line += " in " + render.Path(p.Symbol)
			}
			line += " [" + strings.Join(p.Terms, ",") + "]\n"
			if used+pb.Len()+len(line)+footerReserve > budget || shown >= rp.ptrMax {
				break
			}
			pb.WriteString(line)
			shown++
		}
		if shown == 0 {
			pb.Reset()
		}
		d.FreeText = ptrs
	}
	if next < len(cands) && added < maxRec {
		m := e.signatureMap(v, cands[next:], revoked, budget-used-pb.Len()-footerReserve, maxRec-added, added)
		body.WriteString(m.text)
		used += len(m.text)
		rcpt.Spans = append(rcpt.Spans, m.spans...)
		d.Entries = append(d.Entries, m.entries...)
		added += len(m.entries)
	}
	body.WriteString(pb.String())
	used += pb.Len()
	d.Omitted = len(cands) - added
	d.Truncated = d.Omitted > 0
	footer := fmt.Sprintf("\nend: %d entries, %d omitted, truncated=%t; expand with code_expand(receipt, select)\n", added, d.Omitted, d.Truncated)
	if len(cands) == 0 {
		footer = "\nend: no code evidence matched the task terms; truncated=false\n"
	}
	seen := map[string]bool{}
	for _, s := range rcpt.Spans {
		if !seen[s.Path] {
			seen[s.Path] = true
			rcpt.ReadSet = append(rcpt.ReadSet, FileRef{Path: s.Path, SHA256: s.Blob})
		}
	}
	if err := rcpt.seal(); err != nil {
		return nil, nil, err
	}
	text := strings.Replace(head, placeholderID, rcpt.ID, 1) + body.String() + footer
	d.Text = text
	d.Bytes = len(text)
	d.Receipt = rcpt
	return d, rcpt, nil
}

func coveredBy(spans []Span, path string, line int) bool {
	for _, s := range spans {
		if s.Path == path && s.StartLine <= line && line <= s.EndLine {
			return true
		}
	}
	return false
}
