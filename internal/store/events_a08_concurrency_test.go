package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
)

func TestT43A08ConcurrentExactRetryAcrossStoresConvergesToOneSequence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events-a08.db")
	stores := make([]*Store, 4)
	for i := range stores {
		st, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = st
		t.Cleanup(func() { _ = st.Close() })
	}
	if err := stores[0].Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	input := a02Event("EVENT-A08-SAME", "REQ-A08-SAME")
	out := make(chan events.Event, 32)
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		st := stores[i%len(stores)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := st.Append(ctx, input)
			if err != nil {
				errs <- err
				return
			}
			out <- got
		}()
	}
	wg.Wait()
	close(out)
	close(errs)
	for err := range errs {
		t.Fatalf("exact retry failed: %v", err)
	}
	var seq events.Sequence
	count := 0
	for got := range out {
		count++
		if seq == 0 {
			seq = got.Sequence
		}
		if got.Sequence != seq {
			t.Fatalf("sequence=%d want=%d", got.Sequence, seq)
		}
	}
	if count != 32 {
		t.Fatalf("count=%d", count)
	}
	var rows int
	if err := stores[0].db.QueryRowContext(ctx, "SELECT count(*) FROM structured_events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows=%d want=1", rows)
	}
}

func TestT43A08MismatchedIdempotencyReplayConflictsWithoutSecondRow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	first := a02Event("EVENT-A08-A", "REQ-A08-CONFLICT")
	if _, err := st.Append(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := a02Event("EVENT-A08-B", "REQ-A08-CONFLICT")
	if _, err := st.Append(ctx, second); !errors.Is(err, events.ErrSequenceConflict) {
		t.Fatalf("error=%v want=%v", err, events.ErrSequenceConflict)
	}
	var rows int
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM structured_events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows=%d", rows)
	}
}

func TestT43A08ReopenSinceReconstructsMissedLiveEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events-resume-a08.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := first.Append(ctx, a02Event("EVENT-A08-R"+string(rune('0'+i)), "REQ-A08-R"+string(rune('0'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	_ = first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.Since(ctx, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Sequence != 3 || got[2].Sequence != 5 {
		t.Fatalf("resume=%+v", got)
	}
}

func TestT43A08ConcurrentMismatchedIdempotencyAcrossStoresHasOneWinnerOneConflict(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events-a08-conflict.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, path)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	inputs := []events.Event{
		a02Event("EVENT-A08-C1", "REQ-A08-COLLIDE"),
		a02Event("EVENT-A08-C2", "REQ-A08-COLLIDE"),
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, st := range []*Store{a, b} {
		i, st := i, st
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := st.Append(ctx, inputs[i])
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, events.ErrSequenceConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent mismatch error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var rows int
	if err := a.db.QueryRowContext(ctx, "SELECT count(*) FROM structured_events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows=%d want=1", rows)
	}
}
