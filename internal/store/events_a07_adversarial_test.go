package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func TestT43A07HundredConcurrentAppendsAcrossStoresHaveUniqueMonotonicSequence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events-a07.db")
	const storesN = 8
	stores := make([]*Store, 0, storesN)
	for i := 0; i < storesN; i++ {
		st, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, st)
		t.Cleanup(func() { _ = st.Close() })
	}
	if err := stores[0].Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	const total = 100
	type result struct {
		sequence events.Sequence
		err      error
	}
	out := make(chan result, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := stores[i%len(stores)].Append(ctx, a02Event(fmt.Sprintf("EVENT-A07-%03d", i), fmt.Sprintf("REQ-A07-%03d", i)))
			out <- result{sequence: got.Sequence, err: err}
		}()
	}
	wg.Wait()
	close(out)
	seqs := make([]int, 0, total)
	for item := range out {
		if item.err != nil {
			cause := item.err
			for errors.Unwrap(cause) != nil {
				cause = errors.Unwrap(cause)
			}
			t.Fatalf("concurrent multi-store Append: %v root=%v", item.err, cause)
		}
		seqs = append(seqs, int(item.sequence))
	}
	sort.Ints(seqs)
	if len(seqs) != total {
		t.Fatalf("sequences=%d want=%d", len(seqs), total)
	}
	for i, seq := range seqs {
		if seq != i+1 {
			t.Fatalf("sequence[%d]=%d want=%d", i, seq, i+1)
		}
	}
	durable, err := stores[0].Since(ctx, 0, total)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != total {
		t.Fatalf("durable=%d want=%d", len(durable), total)
	}
}

func TestT43A07ReconnectReadsExactlyMissedDurableRange(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		if _, err := st.Append(ctx, a02Event(fmt.Sprintf("EVENT-RANGE-%d", i), fmt.Sprintf("REQ-RANGE-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Since(ctx, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Sequence != 4 || got[1].Sequence != 5 || got[2].Sequence != 6 {
		t.Fatalf("range=%+v", got)
	}
}

func TestT43A07SlowSubscriberDropDoesNotChangeDurableTruth(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.NewMemoryBus(1)
	ch, unsubscribe, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	_ = ch
	engine, err := newAuthorizedEventEngineForStoreTests(st, bus)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := engine.Append(ctx, a02Event(fmt.Sprintf("EVENT-SLOW-%d", i), fmt.Sprintf("REQ-SLOW-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	durable, err := engine.Since(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != 3 {
		t.Fatalf("durable=%d want=3", len(durable))
	}
}

func TestT43A07CancelledAppendHasZeroDurableSideEffect(t *testing.T) {
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := st.Append(ctx, a02Event("EVENT-CANCEL-A07", "REQ-CANCEL-A07"))
	if !errors.Is(err, events.ErrStoreFailed) {
		t.Fatalf("Append error=%v want=%v", err, events.ErrStoreFailed)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM structured_events"); got != 0 {
		t.Fatalf("rows=%d want=0", got)
	}
}

func TestT43A07IdempotentReplayPreservesSequenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events-retry-a07.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	input := a02Event("EVENT-REOPEN-A07", "REQ-REOPEN-A07")
	one, err := first.Append(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	two, err := second.Append(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if one.Sequence != two.Sequence || !one.At.Equal(two.At) {
		t.Fatalf("first=%+v second=%+v", one, two)
	}
}

func TestT43A07SinceRejectsUnrepresentableSequenceWithoutPanic(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := st.Since(ctx, events.Sequence(^uint64(0)), 1)
	if err == nil {
		t.Fatal("Since accepted sequence outside durable SQLite range")
	}
}

func TestT43A07TimestampAssignedAtCommitBoundaryIsUTC(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.Append(ctx, a02Event("EVENT-TIME-A07", "REQ-TIME-A07"))
	if err != nil {
		t.Fatal(err)
	}
	if got.At.IsZero() || got.At.Location() != time.UTC {
		t.Fatalf("timestamp=%v", got.At)
	}
}
