package store_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func originalRequestStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}
func originalRequestGoal(id, text string) model.GoalContract {
	return model.GoalContract{ID: "GOAL-" + id, SessionID: "SESSION-" + id, Revision: 1, DesiredOutcome: "A distinct interpretation", OriginalRequest: text, Risk: model.R1, AuthoritySource: "operator"}
}

func TestSaveGoalOriginalRequestRejectsInvalidTextBeforeAnyCanonicalWrite(t *testing.T) {
	s := originalRequestStore(t, filepath.Join(t.TempDir(), "state.db"))
	for i, text := range []string{string([]byte{'a', 0xff, 'b'}), "original\x00request"} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			g := originalRequestGoal("invalid-"+strconv.Itoa(i), text)
			err := s.SaveGoalContract(t.Context(), g, 0)
			if !errors.Is(err, model.ErrGoalInvalid) {
				t.Error("invalid original request passed persist-time validation", err)
			}
			if _, err = s.GetGoalContract(t.Context(), g.ID, 1); !errors.Is(err, model.ErrGoalNotFound) {
				t.Error("invalid original request persisted a canonical revision", err)
			}
			if _, err = s.GetActiveGoalContract(t.Context(), g.SessionID); !errors.Is(err, model.ErrGoalNotFound) {
				t.Error("invalid original request advanced canonical active pointer", err)
			}
		})
	}
}

func TestSaveGoalOriginalRequestRoundTripsExactBytesAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(t.Context()); err != nil {
		s.Close()
		t.Fatal(err)
	}
	originals := []string{"", " \tFoydalanuvchining asl soʻzlari ⚡\nKeep <exact> \"quotes\" and \\slashes.\n "}
	for i, text := range originals {
		if err = s.SaveGoalContract(t.Context(), originalRequestGoal("roundtrip-"+strconv.Itoa(i), text), 0); err != nil {
			s.Close()
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for i, text := range originals {
		g := originalRequestGoal("roundtrip-"+strconv.Itoa(i), text)
		current, err := reopened.GetActiveGoalContract(t.Context(), g.SessionID)
		if err != nil || !bytes.Equal([]byte(current.OriginalRequest), []byte(text)) || current.DesiredOutcome != g.DesiredOutcome || current.Revision != 1 {
			t.Fatal("original request altered after canonical persistence/reopen", err)
		}
	}
}

func TestSaveGoalOriginalRequestInvalidRevisionPreservesCurrentGoal(t *testing.T) {
	s := originalRequestStore(t, filepath.Join(t.TempDir(), "state.db"))
	g := originalRequestGoal("current", "Exact current words ⚡\n ")
	if err := s.SaveGoalContract(t.Context(), g, 0); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetActiveGoalContract(t.Context(), g.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{string([]byte{'a', 0xff, 'b'}), "revision\x00request"} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			invalid := current
			invalid.Revision = 2
			invalid.OriginalRequest = text
			if err = s.SaveGoalContract(t.Context(), invalid, 1); !errors.Is(err, model.ErrGoalInvalid) {
				t.Error("invalid revision passed persist-time validation", err)
			}
			after, e := s.GetActiveGoalContract(t.Context(), g.SessionID)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := json.Marshal(after)
			if e != nil || !bytes.Equal(raw, before) {
				t.Error("invalid original changed current canonical goal", e)
			}
			if _, e = s.GetGoalContract(t.Context(), g.ID, 2); !errors.Is(e, model.ErrGoalNotFound) {
				t.Error("invalid revision persisted canonical history", e)
			}
		})
	}
}
