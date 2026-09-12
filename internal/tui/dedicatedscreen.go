package tui

import (
	"strings"
	"sync"
	"unicode"
)

var frozenTitles = struct {
	sync.Once
	byID map[string]string
}{}

func frozenTitle(specID string) string {
	frozenTitles.Do(func() {
		frozenTitles.byID = map[string]string{}
		if ia, err := FrozenIA(); err == nil {
			for _, node := range frozenNodes(ia.Root) {
				frozenTitles.byID[node.SpecID] = node.Title
			}
		}
	})
	if title := frozenTitles.byID[specID]; title != "" {
		return title
	}
	return specID
}

func frozenNodes(root *Node) []*Node {
	if root == nil {
		return nil
	}
	var out []*Node
	var walk func(*Node)
	walk = func(node *Node) {
		out = append(out, node)
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return out
}

// dedicatedLeafContent turns a shared canonical snapshot into the one subject
// named by a frozen leaf. It never manufactures a value: a field is selected
// only when its label overlaps the subject, otherwise the leaf says which
// dedicated typed read is absent. Container summaries and record-list screens
// keep their multi-field/table presentation.
func dedicatedLeafContent(n *Node, content ScreenContent, source string) ScreenContent {
	if n == nil || len(n.Children) != 0 || n.Type.IsAction() || n.Type == NodeCrossLink || n.Binding == BindingGap || content.HasNotice {
		return content
	}
	if keepsRecordSet(n.Title) {
		content.Fields = append([]Field{{Label: n.Title, Value: summarizeFields(content.Fields, source)}}, content.Fields...)
		return content
	}
	// An explicit renderer may lead with the exact frozen subject and then add
	// essential corroborating detail (for example a Cloud refusal reason).
	// Preserve those rows instead of compressing the screen back to one value.
	if len(content.Fields) > 0 && content.Fields[0].Label == n.Title {
		return content
	}

	best, bestScore := -1, 0
	want := subjectWords(n.Title)
	for i, field := range content.Fields {
		score := overlapScore(want, subjectWords(field.Label))
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best >= 0 {
		content.Fields = []Field{{Label: n.Title, Value: content.Fields[best].Value}}
		return content
	}
	content.Fields = []Field{{Label: n.Title, Value: Unknown(
		"the canonical snapshot does not expose a distinct typed value for this subject",
		source)}}
	return content
}

func keepsRecordSet(title string) bool {
	lower := strings.ToLower(title)
	for _, word := range []string{"list", "ledger", "graph", "history", "events", "inventory", "queue", "roster", "detail", "findings", "contradictions", "attempts", "journal"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func summarizeFields(fields []Field, source string) Value {
	if len(fields) == 0 {
		return Empty(source)
	}
	// Worst-wins preserves UNKNOWN/BLOCKED/NOT_RUN instead of upgrading a
	// record set because one neighbouring field happened to be known.
	rank := map[Truth]int{
		TruthKnown: 0, TruthEmpty: 1, TruthNotRun: 2, TruthUnset: 3,
		TruthUnknown: 4, TruthLoading: 5, TruthStale: 6, TruthOffline: 7,
		TruthBlocked: 8, TruthRefused: 9, TruthError: 10,
	}
	worst := fields[0].Value
	for _, field := range fields[1:] {
		if rank[field.Value.Status] > rank[worst.Status] {
			worst = field.Value
		}
	}
	if worst.Source == "" {
		worst.Source = source
	}
	return worst
}

func subjectWords(text string) map[string]struct{} {
	words := map[string]struct{}{}
	for _, raw := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		word := strings.TrimSuffix(strings.TrimSuffix(raw, "es"), "s")
		switch word {
		case "and", "or", "the", "a", "an", "current", "canonical", "state", "status", "result", "detail":
			continue
		}
		if word != "" {
			words[word] = struct{}{}
		}
	}
	return words
}

func overlapScore(a, b map[string]struct{}) int {
	score := 0
	for word := range a {
		if _, ok := b[word]; ok {
			score++
		}
	}
	return score
}
