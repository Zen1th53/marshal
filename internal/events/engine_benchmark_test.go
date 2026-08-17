package events

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func BenchmarkEngineAppend(b *testing.B) {
	engine := NewEngine(&memoryEventStore{})
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Append(ctx, Event{ID: "bench-" + strconv.Itoa(i), Type: EventTypeTaskCreated, At: time.Now().UTC()}); err != nil {
			b.Fatal(err)
		}
	}
}
