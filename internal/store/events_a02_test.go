package store

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func TestEventStorePersistsSequenceAndResumesAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := st.Append(ctx, events.Event{ID: "evt-1", Type: events.EventTypeTaskCreated, At: time.Now().UTC(), Data: map[string]any{"task_id": "task-1"}})
	if err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	second, err := st.Append(ctx, events.Event{ID: "evt-2", Type: events.EventTypeTaskCompleted, At: time.Now().UTC(), Data: map[string]any{"task_id": "task-1"}})
	if err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d, %d; want 1, 2", first.Sequence, second.Sequence)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Since(ctx, first.Sequence)
	if err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != second.ID || got[0].Sequence != second.Sequence {
		t.Fatalf("Since() = %+v, want second event", got)
	}
}

func TestEventStoreRejectsSensitiveFieldBeforePersistence(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = st.Append(ctx, events.Event{ID: "evt-secret", Type: events.EventTypeTaskCreated, At: time.Now().UTC(), Data: map[string]any{"token": "MARSHAL_TEST_SECRET_T43_A02"}})
	if !errors.Is(err, events.ErrEventSecretField) {
		t.Fatalf("Append() error = %v, want ErrEventSecretField", err)
	}
}

func TestEventStoreConcurrentAppendsHaveUniqueMonotonicSequences(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sequences := make([]events.Sequence, 100)
	errs := make(chan error, 100)
	var wg sync.WaitGroup
	for i := range sequences {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stored, appendErr := st.Append(ctx, events.Event{ID: "concurrent-" + string(rune('A'+i)), Type: events.EventTypeTaskCreated, At: time.Now().UTC()})
			if appendErr != nil {
				errs <- appendErr
				return
			}
			sequences[i] = stored.Sequence
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	for i, sequence := range sequences {
		if sequence != events.Sequence(i+1) {
			t.Fatalf("sorted sequence[%d] = %d, want %d", i, sequence, i+1)
		}
	}
}
