package store

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/dag"
)

func TestA02DAGNodePersistenceContract(t *testing.T) {
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDAGNode(context.Background(), dag.Node{TaskID: "TASK-A", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
}
