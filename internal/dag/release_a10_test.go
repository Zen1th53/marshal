package dag

import (
	"os"
	"strings"
	"testing"
)

func TestT29A10OperatorDocsNameStableErrorsAndEvents(t *testing.T) {
	body, err := os.ReadFile("../../docs/dynamic-dag.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"DAG_CYCLE",
		"DAG_NODE_NOT_FOUND",
		"DAG_DUPLICATE_EDGE",
		"DAG_INVALID_CONDITION",
		"dag.node.added",
		"dag.edge.added",
		"dag.node.ready",
		"dag.node.blocked",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("operator docs missing %q", required)
		}
	}
}
