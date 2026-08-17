package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
)

func a02Event(id, key string) events.Event {
	return events.Event{
		ID:             events.EventID(id),
		Type:           events.Type("events.appended"),
		Subject:        events.SubjectID("system"),
		Data:           map[string]string{"result": "stored"},
		IdempotencyKey: events.IdempotencyKey(key),
	}
}

func TestT43A02MigrationCreatesOrderedEventStore(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version=%d want=%d", got, LatestSchemaVersion)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='structured_events'"); got != 1 {
		t.Fatalf("structured_events table count=%d", got)
	}
	for _, index := range []string{"structured_events_by_sequence", "structured_events_by_task", "structured_events_by_evidence"} {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", index); got != 1 {
			t.Fatalf("index %s count=%d", index, got)
		}
	}
}

func TestT43A02AppendAssignsMonotonicSequenceAndSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	one, err := first.Append(ctx, a02Event("EVENT-1", "REQ-1"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := first.Append(ctx, a02Event("EVENT-2", "REQ-2"))
	if err != nil {
		t.Fatal(err)
	}
	if one.Sequence != 1 || two.Sequence != 2 || one.At.IsZero() || two.At.IsZero() || one.At.Location() != time.UTC || two.At.Location() != time.UTC {
		t.Fatalf("sequence/time one=%+v two=%+v", one, two)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.Since(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Sequence != 1 || got[1].Sequence != 2 {
		t.Fatalf("Since=%+v", got)
	}
}

func TestT43A02IdempotentReplayAndMismatchedReplayConflict(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	original := a02Event("EVENT-1", "REQ-1")
	first, err := st.Append(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := st.Append(ctx, original)
	if err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	if replay.Sequence != first.Sequence || !replay.At.Equal(first.At) {
		t.Fatalf("replay changed canonical event first=%+v replay=%+v", first, replay)
	}
	conflict := original
	conflict.Data = map[string]string{"result": "different"}
	if _, err := st.Append(ctx, conflict); !errors.Is(err, events.ErrSequenceConflict) {
		t.Fatalf("mismatched replay error=%v want=%v", err, events.ErrSequenceConflict)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM structured_events"); got != 1 {
		t.Fatalf("event rows=%d", got)
	}
}

func TestT43A02SinceReturnsDetachedData(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, a02Event("EVENT-1", "REQ-1")); err != nil {
		t.Fatal(err)
	}
	first, err := st.Since(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Data["result"] = "mutated"
	second, err := st.Since(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Data["result"] != "stored" {
		t.Fatalf("durable event aliased caller data: %+v", second[0].Data)
	}
}

func TestT43A02AppendRejectsConfiguredSecretLiteral(t *testing.T) {
	ctx := context.Background()
	const marker = "MARSHAL_TEST_SECRET_T43_A02_71c9"
	path := filepath.Join(t.TempDir(), "events-secret.db")
	st, err := OpenWithSanitizer(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{LiteralSecrets: []string{marker}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	event := events.Event{
		ID: "EVENT-SECRET-1", Type: "events.appended", Subject: "system",
		Data: map[string]string{"detail": marker}, IdempotencyKey: "REQ-SECRET-1",
	}
	_, err = st.Append(ctx, event)
	if !errors.Is(err, events.ErrSecretField) {
		t.Fatalf("Append() error=%v want=%v", err, events.ErrSecretField)
	}
	if err != nil && bytes.Contains([]byte(err.Error()), []byte(marker)) {
		t.Fatal("public event error leaked configured secret literal")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM structured_events"); got != 0 {
		t.Fatalf("secret event rows=%d want=0", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(marker)) {
		t.Fatal("configured secret literal persisted in SQLite")
	}
}

func TestT43A02AppendRejectsForeignTaskRunAndEvidenceReferences(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cases := []events.Event{
		{ID: "EVENT-FOREIGN-TASK", Type: "events.appended", Subject: "system", TaskID: "TASK-MISSING", IdempotencyKey: "REQ-FOREIGN-TASK"},
		{ID: "EVENT-FOREIGN-RUN", Type: "events.appended", Subject: "system", RunID: "RUN-MISSING", IdempotencyKey: "REQ-FOREIGN-RUN"},
		{ID: "EVENT-FOREIGN-EVIDENCE", Type: "events.appended", Subject: "system", EvidenceID: "EVIDENCE-MISSING", IdempotencyKey: "REQ-FOREIGN-EVIDENCE"},
	}
	for _, event := range cases {
		if _, err := st.Append(ctx, event); !errors.Is(err, events.ErrStoreFailed) {
			t.Fatalf("Append(%s) error=%v want=%v", event.ID, err, events.ErrStoreFailed)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM structured_events"); got != 0 {
		t.Fatalf("foreign reference event rows=%d want=0", got)
	}
}

type a02FlakyBus struct {
	calls int
}

func (b *a02FlakyBus) Publish(context.Context, events.Event) error {
	b.calls++
	if b.calls == 1 {
		return errors.New("MARSHAL_TEST_SECRET_T43_A02_LIVE_DELIVERY_39af")
	}
	return nil
}

func (b *a02FlakyBus) Subscribe(context.Context, events.Sequence) (<-chan events.Event, func(), error) {
	return nil, func() {}, nil
}

func TestT43A02LiveDeliveryFailureReconcilesFromDurableIdempotentRetry(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	bus := &a02FlakyBus{}
	engine, err := newAuthorizedEventEngineForStoreTests(st, bus)
	if err != nil {
		t.Fatal(err)
	}
	input := a02Event("EVENT-RETRY-1", "REQ-RETRY-1")
	first, err := engine.Append(ctx, input)
	if err == nil {
		t.Fatal("first append unexpectedly reported live delivery success")
	}
	if strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T43_A02_LIVE_DELIVERY_39af") {
		t.Fatal("live delivery backend error leaked secret marker")
	}
	if first.Sequence == 0 || first.At.IsZero() {
		t.Fatalf("durable result missing after live delivery failure: %+v", first)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM structured_events"); got != 1 {
		t.Fatalf("event rows after delivery failure=%d want=1", got)
	}
	second, err := engine.Append(ctx, input)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if second.Sequence != first.Sequence || !second.At.Equal(first.At) {
		t.Fatalf("retry changed durable identity first=%+v second=%+v", first, second)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM structured_events"); got != 1 {
		t.Fatalf("event rows after retry=%d want=1", got)
	}
	if bus.calls != 2 {
		t.Fatalf("live publish calls=%d want=2", bus.calls)
	}
}

func TestT43A02HundredConcurrentAppendsHaveUniqueMonotonicSequence(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const total = 100
	var wg sync.WaitGroup
	errCh := make(chan error, total)
	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.Append(ctx, a02Event(fmt.Sprintf("EVENT-CONCURRENT-%03d", i), fmt.Sprintf("REQ-CONCURRENT-%03d", i)))
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}
	got, err := st.Since(ctx, 0, total)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Fatalf("events=%d want=%d", len(got), total)
	}
	for i, event := range got {
		want := events.Sequence(i + 1)
		if event.Sequence != want {
			t.Fatalf("event[%d].Sequence=%d want=%d", i, event.Sequence, want)
		}
	}
}
