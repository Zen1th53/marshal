package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func TestEgressControlEventsOrderedFilteredAndIncremental(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var grant events.Event
	for _, input := range []struct {
		id, run string
		kind    events.EventType
	}{
		{"scope", "RUN-a", events.EventTypeNetworkEgressRequested},
		{"attempt", "RUN-a", events.EventTypeNetworkEgressAttempt},
		{"grant", "RUN-a", events.EventTypeNetworkEgressGranted},
		{"other", "RUN-b", events.EventTypeNetworkEgressNotification},
		{"revoke", "RUN-a", events.EventTypeNetworkEgressRevoked},
	} {
		stored, err := s.Append(t.Context(), events.Event{ID: input.id, RunID: input.run, Type: input.kind, At: time.Now().UTC(), Data: map[string]any{"scope_socket": "private-proxy", "endpoint": "example.com:443"}})
		if err != nil {
			t.Fatal(err)
		}
		if input.id == "grant" {
			grant = stored
		}
	}
	selected, err := s.EgressControlEvents(t.Context(), "RUN-a", 0)
	if err != nil || len(selected) != 3 {
		t.Fatalf("filtered history: %+v %v", selected, err)
	}
	for i, id := range []string{"scope", "grant", "revoke"} {
		if selected[i].ID != id || selected[i].Data["scope_socket"] != "private-proxy" || selected[i].At.IsZero() {
			t.Fatalf("projection metadata/order: %+v", selected[i])
		}
	}
	incremental, err := s.EgressControlEvents(t.Context(), "RUN-a", grant.Sequence)
	if err != nil || len(incremental) != 1 || incremental[0].ID != "revoke" {
		t.Fatalf("incremental cursor: %+v %v", incremental, err)
	}
	all, err := s.EgressControlEvents(t.Context(), "", 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("all scopes: %+v %v", all, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EgressControlEvents(t.Context(), "RUN-a", 0); err == nil {
		t.Fatal("closed store read did not fail")
	}
}
