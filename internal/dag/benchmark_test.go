package dag

import (
	"context"
	"fmt"
	"testing"
)

func benchmarkDAGEngine(b *testing.B, nodes int) (*Engine, *a05Backend) {
	b.Helper()
	backend := newA05Backend()
	for i := 0; i < nodes; i++ {
		id := TaskID(fmt.Sprintf("TASK-%04d", i))
		backend.nodes[id] = Node{TaskID: id, Kind: NodeKindTask, Status: StatusPending}
	}
	for i := 0; i < nodes-1; i++ {
		from := TaskID(fmt.Sprintf("TASK-%04d", i))
		to := TaskID(fmt.Sprintf("TASK-%04d", i+1))
		backend.edges = append(backend.edges, Edge{From: from, To: to, Condition: ConditionCompleted})
	}
	engine, err := NewEngine(backend)
	if err != nil {
		b.Fatal(err)
	}
	return engine, backend
}

func BenchmarkT29A09Ready100(b *testing.B) {
	engine, _ := benchmarkDAGEngine(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Ready(ctx, "TASK-0099"); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkT29A09Topological100(b *testing.B) {
	engine, _ := benchmarkDAGEngine(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Topological(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkT29A09ReverseLookup100(b *testing.B) {
	_, backend := benchmarkDAGEngine(b, 100)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := backend.DAGEdgesTo(ctx, "TASK-0099"); err != nil {
			b.Fatal(err)
		}
	}
}
