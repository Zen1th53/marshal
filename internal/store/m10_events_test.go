package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func TestM10MarshalDecisionsReopenSequence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		run  string
		kind events.EventType
	}{
		{"run-1", events.EventTypeMarshalRecommended},
		{"run-2", events.EventTypeMarshalTaskDispatched},
		{"run-1", events.EventTypeMarshalTaskAccepted},
	} {
		_, err := s.AppendMarshalDecision(ctx, events.Event{ID: string(rune('a' + i)), RunID: item.run, Type: item.kind, At: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.MarshalDecisions(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != events.EventTypeMarshalRecommended || got[1].Type != events.EventTypeMarshalTaskAccepted || got[0].Sequence >= got[1].Sequence {
		t.Fatalf("decisions: %+v", got)
	}
}
