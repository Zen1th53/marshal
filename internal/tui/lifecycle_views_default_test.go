//go:build governed

package tui

import "testing"

func TestCommunityLifecycleGapsKeepOriginalBehavior(t *testing.T) {
	w := &Workspace{}
	source := &StatusSource{}
	w.attachLifecycleSource(source)
	if source.lifecycle != nil {
		t.Fatal("Community attached a private retained-run reader")
	}
	ia, _ := FrozenIA()
	for _, id := range []string{"CTUI-0092", "CTUI-0100"} {
		node, _ := ia.Node(id)
		content := RenderNode(node, Snapshot{})
		if !content.ReadOnly || !content.HasNotice || len(content.Fields) != 0 || node.Binding != BindingGap {
			t.Fatalf("Community gap behavior changed: %+v", content)
		}
	}
}
