// Package architectureworkspace projects accepted contracts without persistence.
package architectureworkspace

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/archconform"
	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

type Finding struct {
	Title    string `json:"title"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence"`
	Source   string `json:"source"`
	Status   string `json:"status"`
	Part     string `json:"part,omitempty"`
	Edge     string `json:"edge,omitempty"`
	Area     string `json:"area,omitempty"`
}
type Snapshot struct {
	Title    string                              `json:"title"`
	Notice   string                              `json:"notice"`
	Layouts  map[string]architecturecodec.Layout `json:"layouts"`
	Findings map[string][]Finding                `json:"findings"`
	Areas    map[string][]Finding                `json:"areas"`
}

// Project uses contract parents as areas because contracts have no area type.
// Unknown locations remain unplaced; geometry never supplies membership.
func Project(c projectcontext.Contract, observations []Finding) (Snapshot, error) {
	out := Snapshot{Title: c.ID, Findings: map[string][]Finding{}, Areas: map[string][]Finding{}}
	parents := map[string]string{}
	areaIDs := map[string]bool{}
	edges := map[string]projectcontext.Connection{}
	components := []any{}
	connections := []any{}
	for _, v := range c.Components {
		if v.Removed {
			continue
		}
		parents[v.ID] = v.Parent
		if v.Parent != "" {
			areaIDs[v.Parent] = true
		}
		component := map[string]any{"id": v.ID, "name": v.ID, "responsibility": v.Responsibility, "technology": "", "files": v.Paths, "parent": v.Parent}
		if v.Parent == "" {
			delete(component, "parent")
		}
		components = append(components, component)
	}
	for _, e := range projectcontext.ContractConnections(c) {
		if _, ok := parents[e.From]; !ok {
			continue
		}
		if _, ok := parents[e.To]; !ok {
			continue
		}
		edges[e.ID()] = projectcontext.Connection{From: e.From, To: e.To, Carries: e.Carries}
		connections = append(connections, map[string]any{"id": e.ID(), "source": e.From, "target": e.To, "label": e.Carries, "protocol": ""})
	}
	raw, err := json.Marshal(map[string]any{"title": c.ID, "purpose": c.Purpose, "components": components, "connections": connections, "constraints": []string{}})
	if err != nil {
		return out, err
	}
	out.Layouts, err = architecturecodec.GenericLayouts(raw)
	if err != nil {
		return out, err
	}
	owner := archconform.NewMap(c)
	seen := map[string]bool{}
	for _, f := range observations {
		if f.Severity == "" {
			f.Severity = "error"
		}
		if f.Status == "" {
			f.Status = "open"
		}
		file, _, _ := strings.Cut(f.Evidence, ":")
		target := owner.Owner(file)
		if target == "" {
			if _, ok := parents[f.Part]; ok {
				target = f.Part
			}
		}
		areas := map[string]bool{}
		if e, ok := edges[f.Edge]; ok {
			target = f.Edge
			for _, id := range []string{e.From, e.To} {
				rollup(parents, parents[id], areas)
			}
		} else if target != "" {
			rollup(parents, parents[target], areas)
		} else if areaIDs[f.Area] {
			areas[f.Area] = true
			rollup(parents, parents[f.Area], areas)
		}
		keyBytes, _ := json.Marshal([]any{f.Title, f.Evidence, f.Source, f.Severity, f.Status, target, areas})
		key := string(keyBytes)
		if seen[key] {
			continue
		}
		seen[key] = true
		if target != "" {
			out.Findings[target] = append(out.Findings[target], f)
		} else if len(areas) == 0 {
			out.Findings[""] = append(out.Findings[""], f)
		}
		for area := range areas {
			out.Areas[area] = append(out.Areas[area], f)
		}
	}
	return out, nil
}
func rollup(parents map[string]string, id string, seen map[string]bool) {
	for id != "" && !seen[id] {
		seen[id] = true
		id = parents[id]
	}
}
func Conformance(findings []archconform.Finding) []Finding {
	out := []Finding{}
	for _, f := range findings {
		out = append(out, Finding{Title: f.String(), Evidence: f.File, Part: f.Part, Source: "conformance", Severity: "error", Status: "open"})
	}
	return out
}

// Reviewer retains legacy file:part mismatch evidence without inferring topology.
func Reviewer(mismatches []string) []Finding {
	out := []Finding{}
	for _, text := range mismatches {
		fields := strings.SplitN(text, ":", 4)
		f := Finding{Title: text, Source: "reviewer", Severity: "warning", Status: "open"}
		if len(fields) > 1 {
			f.Evidence = strings.TrimSpace(fields[0])
			f.Part = strings.TrimSpace(fields[1])
			if line, err := strconv.Atoi(f.Part); err == nil && line > 0 {
				f.Evidence += ":" + f.Part
				f.Part = ""
				if len(fields) > 2 {
					f.Part = strings.TrimSpace(fields[2])
				}
			}
		}
		out = append(out, f)
	}
	return out
}
