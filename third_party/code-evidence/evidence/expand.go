package evidence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
	"github.com/Zen1th53/code-evidence/internal/render"
	"github.com/Zen1th53/code-evidence/internal/snapshot"
)

type cursorState struct {
	Receipt string `json:"r"`
	Select  string `json:"s,omitempty"`
	View    string `json:"v,omitempty"`
	Offset  int    `json:"o"`
}

func encodeCursor(c cursorState) string {
	b, _ := json.Marshal(c)
	return "cur_" + base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorState, error) {
	var c cursorState
	raw, ok := strings.CutPrefix(s, "cur_")
	if !ok {
		return c, errors.New("evidence: bad cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &c) != nil || c.Offset < 0 {
		return c, errors.New("evidence: bad cursor")
	}
	return c, nil
}

var viewRelation = map[string]string{
	"callers":    goanalyzer.RelCallers,
	"callees":    goanalyzer.RelCallees,
	"references": goanalyzer.RelReferences,
	"tests":      goanalyzer.RelTestsOf,
	"declared":   goanalyzer.RelDeclaredIn,
}

// Expand serves bounded evidence for a receipt. The receipt is
// re-authorized against the engine policy on every call; selections that
// are not visible under the policy yield the same error as missing ones.
func (e *Engine) Expand(ctx context.Context, req ExpandRequest) (*Expansion, error) {
	st := cursorState{Receipt: req.Receipt, Select: req.Select, View: req.View}
	if req.Cursor != "" {
		c, err := decodeCursor(req.Cursor)
		if err != nil {
			return nil, err
		}
		st = c
	}
	if st.View == "" {
		st.View = "source"
	}
	budget := clampBudget(req.Budget, DefaultExpandBytes, MaxExpandBytes)
	maxRec := clampBudget(req.MaxRecords, MaxRecords, MaxRecords)
	rcpt, err := e.loadReceipt(ctx, st.Receipt)
	if err != nil {
		return nil, err
	}
	if rev, err := e.store.Revoked(ctx, rcpt.Blobs()); err != nil {
		return nil, err
	} else if len(rev) > 0 {
		return nil, errRevoked
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		v, err := e.capture(ctx, e.repo, rcpt.Config.Effective)
		if err != nil {
			return nil, err
		}
		revoked, err := e.revokedFiles(ctx, v)
		if err != nil {
			return nil, err
		}
		out, dl, err := e.expandIn(v, rcpt, st, budget, maxRec, revoked)
		if err != nil {
			return nil, err
		}
		cited := make([]string, 0, len(out.Records)+len(rcpt.ReadSet))
		for _, r := range out.Records {
			cited = append(cited, r.Path)
		}
		for _, f := range rcpt.ReadSet {
			cited = append(cited, f.Path)
		}
		if !e.recheck(e.repo, v.snap, uniqueSorted(cited)) {
			continue
		}
		var blobs []string
		for _, s := range dl.Spans {
			blobs = append(blobs, s.Blob)
		}
		for _, r := range out.Records {
			if r.Blob != "" {
				blobs = append(blobs, r.Blob)
			}
		}
		blobs = derivedBlobs(v.snap, uniqueSorted(blobs), dl.Predicates)
		if err := e.recordDelivery(ctx, dl, blobs); err != nil {
			return nil, err
		}
		out.Delivery = dl.ID
		return out, nil
	}
	return nil, errUnstable
}

// expandIn renders one expansion against view v and describes what it
// delivers.
func (e *Engine) expandIn(v *view, rcpt *Receipt, st cursorState, budget, maxRec int, revoked func(string) bool) (*Expansion, *Delivery, error) {
	out := &Expansion{Receipt: rcpt.ID, SnapshotID: v.snap.ID}
	dl := &Delivery{Receipt: rcpt.ID, Kind: DeliveryExpand, ObservedAt: e.now(), SnapshotID: v.snap.ID,
		Select: st.Select, View: st.View, Offset: st.Offset}
	for _, f := range rcpt.ReadSet {
		if cur, ok := v.snap.File(f.Path); !ok || cur.SHA256 != f.SHA256 {
			out.Changed = append(out.Changed, f.Path)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# code_expand receipt=%s view=%s select=%s\n", rcpt.ID, st.View, render.Quote(st.Select))
	if len(out.Changed) > 0 {
		quoted := make([]string, len(out.Changed))
		for i, p := range out.Changed {
			quoted[i] = render.Path(p)
		}
		fmt.Fprintf(&b, "changed since receipt: %s (showing current bytes)\n", strings.Join(quoted, ", "))
	}
	reserve := 120
	limit := budget - reserve

	switch {
	case st.Select == "" && st.View == "source":
		// Overview of the receipt's spans.
		for i := st.Offset; i < len(rcpt.Spans); i++ {
			s := rcpt.Spans[i]
			line := fmt.Sprintf("#%d %s %s:%d-%d (%s)\n", i, render.Path(s.Symbol), render.Path(s.Path), s.StartLine, s.EndLine, s.Form)
			if b.Len()+len(line) > limit || len(out.Records) >= maxRec {
				out.Truncated, out.Cursor = true, encodeCursor(cursorState{rcpt.ID, st.Select, st.View, i})
				break
			}
			b.WriteString(line)
			out.Records = append(out.Records, Record{Path: s.Path, StartLine: s.StartLine, EndLine: s.EndLine, Symbol: s.Symbol, Blob: s.Blob})
		}
	case st.View == "source":
		path, from, to, sym, err := e.resolveSelect(v, rcpt, st.Select)
		if err != nil {
			return nil, nil, err
		}
		src, ok := v.snap.Bytes(path)
		if !ok {
			return nil, nil, errNotVisible
		}
		f, _ := v.snap.File(path)
		if revoked(path) {
			return nil, nil, errRevoked
		}
		if st.Offset > 0 {
			from = st.Offset
		}
		fmt.Fprintf(&b, "%s %s:%d-%d blob %s\n", render.Path(sym), render.Path(path), from, to, f.SHA256[:12])
		text, _, _ := render.Excerpt(src, from, to, maxLineBytes)
		ln := from
		for _, l := range strings.SplitAfter(text, "\n") {
			if l == "" {
				continue
			}
			if b.Len()+len(l) > limit {
				out.Truncated, out.Cursor = true, encodeCursor(cursorState{rcpt.ID, st.Select, st.View, ln})
				break
			}
			b.WriteString(l)
			ln++
		}
		out.Records = append(out.Records, Record{Path: path, StartLine: from, EndLine: ln - 1, Symbol: sym, Blob: f.SHA256})
		if ln > from {
			dl.Spans = append(dl.Spans, deliveredSpan(v, f, src, sym, from, ln-1))
		}
	default:
		rel, ok := viewRelation[st.View]
		if !ok {
			return nil, nil, fmt.Errorf("evidence: unknown view %q", st.View)
		}
		_, _, _, sym, err := e.resolveSelect(v, rcpt, st.Select)
		if err != nil {
			return nil, nil, err
		}
		if sym == "" {
			return nil, nil, errors.New("evidence: relation views need a symbol selection")
		}
		res, ok := v.ix.Evaluate(rel, sym)
		if !ok {
			return nil, nil, errNotVisible
		}
		if res.Unsupported {
			fmt.Fprintf(&b, "%s: unsupported for %s\n", rel, render.Path(sym))
			break
		}
		res, withheld := withhold(res, revoked)
		fmt.Fprintf(&b, "%s %s (%d) [%s; %s]\n", rel, boundSym(res.Bound), len(res.Items), boundNote(res), predCoverageNote(res.Coverage, withheld))
		shown := 0
		for i := st.Offset; i < len(res.Items); i++ {
			it := res.Items[i]
			line := "  " + itemText(rel, it) + "\n"
			if b.Len()+len(line) > limit || len(out.Records) >= maxRec {
				out.Truncated, out.Cursor = true, encodeCursor(cursorState{rcpt.ID, st.Select, st.View, i})
				break
			}
			b.WriteString(line)
			out.Records = append(out.Records, Record{Path: it.Path, StartLine: it.Line, Symbol: it.Symbol})
			shown++
		}
		dl.Predicates = append(dl.Predicates, pagePredicate(res, st.Offset, shown, withheld))
	}
	if out.Truncated {
		b.WriteString("truncated: continue with cursor\n")
	} else {
		b.WriteString("end\n")
	}
	out.Text = b.String()
	out.Bytes = len(out.Text)
	dl.Records, dl.Bytes, dl.Truncated = len(out.Records), out.Bytes, out.Truncated
	return out, dl, nil
}

// pagePredicate records the page of a relation that an expansion showed:
// the delivered items themselves, plus the full count and digest.
func pagePredicate(res goanalyzer.Result, offset, shown, withheld int) Predicate {
	p := toPredicate(res, shown, withheld)
	items := []StoredItem{}
	for i := offset; i < offset+shown && i < len(res.Items); i++ {
		it := res.Items[i]
		items = append(items, StoredItem{Key: it.Key, Path: it.Path, Line: it.Line})
	}
	p.Items = items
	p.ItemsTruncated = len(items) < len(res.Items)
	return p
}

// deliveredSpan describes delivered lines [from, to] of path as a span,
// relocatable like a dossier span when it belongs to a declaration.
func deliveredSpan(v *view, f snapshot.File, src []byte, sym string, from, to int) Span {
	_, start, end := render.Excerpt(src, from, to, maxLineBytes)
	sp := Span{Path: f.Path, Blob: f.SHA256, Start: start, End: end, StartLine: from, EndLine: to,
		Content: sha(src[start:end]), Form: "expand"}
	if sym == "" {
		return sp
	}
	for _, r := range v.ix.DeclsByID(sym) {
		ff, d := v.ix.Decl(r)
		if ff.Path != f.Path {
			continue
		}
		base := d.Start
		if d.DocStart > 0 && d.DocLine > 0 {
			base = d.DocStart
		}
		sp.Symbol, sp.RelStart = sym, start-base
		break
	}
	return sp
}

// resolveSelect maps a selection to (path, fromLine, toLine, symbol).
func (e *Engine) resolveSelect(v *view, rcpt *Receipt, sel string) (string, int, int, string, error) {
	switch {
	case strings.HasPrefix(sel, "#"):
		n, err := strconv.Atoi(sel[1:])
		if err != nil || n < 0 || n >= len(rcpt.Spans) {
			return "", 0, 0, "", errNotVisible
		}
		s := rcpt.Spans[n]
		if s.Symbol != "" {
			return e.resolveSelect(v, rcpt, s.Symbol)
		}
		return s.Path, s.StartLine, s.EndLine, "", nil
	case strings.Contains(sel, "#"):
		refs := v.ix.DeclsByID(sel)
		for _, r := range refs {
			if !v.ix.InAnyConfig(r.File) {
				continue
			}
			ff, d := v.ix.Decl(r)
			from := d.StartLine
			if d.DocLine > 0 {
				from = d.DocLine
			}
			return ff.Path, from, d.EndLine, d.ID, nil
		}
		return "", 0, 0, "", errNotVisible
	default:
		// path:L1-L2 or path
		path, rng, _ := strings.Cut(sel, ":")
		if _, ok := v.snap.Bytes(path); !ok {
			return "", 0, 0, "", errNotVisible
		}
		from, to := 1, 1<<30
		if rng != "" {
			a, bpart, _ := strings.Cut(rng, "-")
			var err1, err2 error
			from, err1 = strconv.Atoi(a)
			to = from
			if bpart != "" {
				to, err2 = strconv.Atoi(bpart)
			}
			if err1 != nil || err2 != nil || from < 1 || to < from {
				return "", 0, 0, "", errors.New("evidence: bad line range")
			}
		}
		return path, from, to, "", nil
	}
}

// CursorReceipt returns the receipt named by a continuation so hosts can
// authorize ownership before any expansion or delivery is recorded.
func CursorReceipt(cursor string) (string, error) {
	c, err := decodeCursor(cursor)
	return c.Receipt, err
}
