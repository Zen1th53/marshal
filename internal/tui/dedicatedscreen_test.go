package tui

import (
	"strings"
	"testing"
)

// This is the semantic renderer oracle. A registry entry alone is not
// coverage: every bound read-only leaf must render its own frozen subject, and
// a grouped snapshot that cannot answer it remains explicit debt.
func TestEveryDedicatedLeafRendersItsExactSubject(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatal(err)
	}
	var structuralDebt []string
	for _, node := range integrationNodes(ia) {
		if node == nil || len(node.Children) != 0 || node.Binding == BindingGap || node.Type.IsAction() || node.Type == NodeCrossLink || sectionTitleOf(node) == "Control" {
			continue
		}
		content, ok := zeroSnapshotScreen(node)
		if !ok {
			continue // renderer coverage is enforced by the acceptance inventory
		}
		if len(content.Fields) == 0 || content.Fields[0].Label != node.Title {
			t.Errorf("%s first field = %#v, want exact subject %q", node.SpecID, content.Fields, node.Title)
			continue
		}
		if strings.Contains(content.Fields[0].Value.Reason, "does not expose a distinct typed value") {
			structuralDebt = append(structuralDebt, node.SpecID+" "+node.Title)
		}
	}
	if len(structuralDebt) != 0 {
		t.Fatalf("%d bound leaves still lack a dedicated typed value:\n%s", len(structuralDebt), strings.Join(structuralDebt, "\n"))
	}
}

func zeroSnapshotScreen(node *Node) (ScreenContent, bool) {
	switch sectionTitleOf(node) {
	case "Home", "Status":
		_, ok := screenRenderers[node.SpecID]
		return RenderNode(node, Snapshot{}), ok
	case "Work":
		return RenderWorkNode(node, WorkSnapshot{})
	case "Verify":
		return RenderVerifyNode(node, VerifySnapshot{})
	case "Memory":
		return RenderMemoryNode(node, MemorySnapshot{})
	case "Models":
		return RenderModelsNode(node, ModelsSnapshot{})
	case "Security":
		return RenderSecurityNode(node, SecuritySnapshot{})
	case "System":
		return RenderSystemNode(node, SystemSnapshot{})
	default:
		return ScreenContent{}, false
	}
}
