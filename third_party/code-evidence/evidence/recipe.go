package evidence

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/code-evidence/internal/goanalyzer"
	"github.com/Zen1th53/code-evidence/internal/render"
	"github.com/Zen1th53/code-evidence/internal/snapshot"
)

// Recipe spans+predicates+map/2 (docs/M2-REPORT.md §10). A dossier has
// two tiers:
//
//   - excerpts: the top entries with predicates and a clipped excerpt;
//   - a signature map: every further candidate that fits, one signature
//     line each, grouped by file. A breadth pass first lists the best
//     candidate of each of the next mapBreadth files; a depth pass then
//     fills the rest of the budget in score order.
//
// The map costs about 60–120 bytes per declaration against 0.5–1.5 KiB
// for an excerpt entry, so a budget delivers several times more
// candidate files and declarations.
const (
	smallBudget    = 4096 // budgets below this use the small layout
	mapBreadth     = 16
	mapScan        = 600 // candidates the map considers
	smallExcerpt   = 8
	smallPointers  = 2
	normalPointers = 3
)

// recipe holds the layout parameters for one budget.
type recipe struct {
	full    int // entries rendered with a full (clipped) excerpt
	excerpt int // excerpt line cap
	ptrMax  int // free-text pointer lines
}

func recipeFor(budget int) recipe {
	if budget < smallBudget {
		return recipe{full: 1, excerpt: smallExcerpt, ptrMax: smallPointers}
	}
	return recipe{full: fullEntries, excerpt: maxExcerpt, ptrMax: normalPointers}
}

type sigMap struct {
	text    string
	spans   []Span
	entries []Entry
}

const mapHeader = "\nmap: further candidates, signature line only (code_expand select=path:L1-L2 for source):\n"

// signatureMap lays out further candidates as one signature line each,
// within room bytes and at most maxEnt entries. A breadth pass takes the
// best remaining candidate of each of the next mapBreadth files; a depth
// pass fills the remaining room in score order. Each pass is grouped by file in order of the file's first
// line. Ranks continue after base and follow display order.
func (e *Engine) signatureMap(v *view, cands []candidate, revoked func(string) bool, room, maxEnt, base int) sigMap {
	type line struct {
		c    candidate
		text string
		span Span
	}
	type pass struct {
		order  []string
		byFile map[string][]line
	}
	cost := len(mapHeader)
	if room < cost+64 {
		return sigMap{}
	}
	taken := make([]bool, min(len(cands), mapScan))
	perFile := map[string]int{}
	count := 0
	run := func(limit, files int, header string) pass {
		p := pass{byFile: map[string][]line{}}
		hdr := false
		for i := range taken {
			if taken[i] || count >= maxEnt {
				continue
			}
			c := cands[i]
			ff, d := v.ix.Decl(c.ref)
			if perFile[ff.Path] >= limit || revoked(ff.Path) {
				continue // forgotten evidence is never delivered, not even as a map line
			}
			if len(p.byFile[ff.Path]) == 0 && len(p.order) >= files {
				continue
			}
			f, _ := v.snap.File(ff.Path)
			src, _ := v.snap.Bytes(ff.Path)
			text, sp := excerptSpanClip(f, src, d, maxLineBytes)
			add := len(text)
			if len(p.byFile[ff.Path]) == 0 {
				add += len(render.Path(ff.Path)) + 1
				if cfgs := v.ix.ConfigNames(c.ref.File); len(cfgs) != len(v.ix.Configs) {
					add += len(" (configs: " + configLabel(cfgs) + ")")
				}
			}
			if !hdr {
				add += len(header)
			}
			if cost+add > room {
				continue
			}
			cost += add
			hdr = true
			if len(p.byFile[ff.Path]) == 0 {
				p.order = append(p.order, ff.Path)
			}
			p.byFile[ff.Path] = append(p.byFile[ff.Path], line{c, text, sp})
			perFile[ff.Path]++
			taken[i] = true
			count++
		}
		return p
	}
	passes := []pass{run(1, mapBreadth, ""), run(mapScan, mapScan, mapMore)}
	if count == 0 {
		return sigMap{}
	}
	var b strings.Builder
	b.WriteString(mapHeader)
	out := sigMap{}
	for pi, p := range passes {
		if pi > 0 && len(p.order) > 0 {
			b.WriteString(mapMore)
		}
		for _, path := range p.order {
			ls := p.byFile[path]
			cfgs := v.ix.ConfigNames(ls[0].c.ref.File)
			partial := len(cfgs) != len(v.ix.Configs)
			b.WriteString(render.Path(path))
			if partial {
				b.WriteString(" (configs: " + configLabel(cfgs) + ")")
			}
			b.WriteString("\n")
			for _, l := range ls {
				b.WriteString(l.text)
				_, d := v.ix.Decl(l.c.ref)
				why := []string{fmt.Sprintf("score %.3f", l.c.score)}
				if h := hopLabel(v, l.c, revoked); h != "" {
					why = append(why, "hop "+h)
				}
				ent := Entry{Rank: base + len(out.entries) + 1, Symbol: d.ID, Kind: d.Kind, Path: path,
					StartLine: d.StartLine, EndLine: d.StartLine, Score: fmt.Sprintf("%.3f", l.c.score), Why: why, Form: formSignature}
				if partial {
					ent.Configs = append([]string{}, cfgs...)
				}
				out.spans = append(out.spans, l.span)
				out.entries = append(out.entries, ent)
			}
		}
	}
	out.text = b.String()
	return out
}

const mapMore = "more signatures:\n"

const formSignature = "signature"

// excerptSpanClip renders the declaration's first line as a signature span.
func excerptSpanClip(f snapshot.File, src []byte, d *goanalyzer.Decl, clip int) (string, Span) {
	text, start, end := render.Excerpt(src, d.StartLine, d.StartLine, clip)
	base := d.Start
	if d.DocStart > 0 && d.DocLine > 0 {
		base = d.DocStart
	}
	return text, Span{
		Path: f.Path, Blob: f.SHA256, Start: start, End: end, StartLine: d.StartLine, EndLine: d.StartLine,
		Symbol: d.ID, RelStart: start - base, Content: sha(src[start:end]), Form: formSignature,
	}
}

// configLabel names the configurations of a partially configured file
// ("none" when the file is in no requested configuration).
func configLabel(cfgs []string) string {
	if len(cfgs) == 0 {
		return "none"
	}
	return strings.Join(cfgs, ",")
}
