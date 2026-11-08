package store_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

var storedVisibleGoalFields = []string{"id", "original", "outcome", "artifact", "constraint-id", "constraint-text", "constraint-source", "constraint-scope", "do-not-do", "criterion", "scope", "critical-claim"}

func storedVisibleGoal(id string) model.GoalContract {
	g := originalRequestGoal(id, "Exact original")
	g.Confirmation = model.ConfirmationPending
	g.ExpectedArtifact = "artifact"
	g.Constraints = []model.Constraint{{ID: "constraint", Text: "Preserve work", Source: "operator", Scope: "project", IsHard: true}}
	g.DoNotDo = []string{"No unapproved edits"}
	g.SuccessCriteria = []string{"Exact result"}
	g.Scope = []string{"README.md"}
	g.RequiredCriticalClaims = []string{"critical"}
	return g
}
func corruptStoredVisibleGoal(g *model.GoalContract, field, text string) {
	switch field {
	case "id":
		g.ID = text
	case "original":
		g.OriginalRequest = text
	case "outcome":
		g.DesiredOutcome = text
	case "artifact":
		g.ExpectedArtifact = text
	case "constraint-id":
		g.Constraints[0].ID = text
	case "constraint-text":
		g.Constraints[0].Text = text
	case "constraint-source":
		g.Constraints[0].Source = text
	case "constraint-scope":
		g.Constraints[0].Scope = text
	case "do-not-do":
		g.DoNotDo[0] = text
	case "criterion":
		g.SuccessCriteria[0] = text
	case "scope":
		g.Scope[0] = text
	case "critical-claim":
		g.RequiredCriticalClaims[0] = text
	}
}
func TestSaveVisibleGoalTextRefusesMalformedCreateAndRevision(t *testing.T) {
	s := originalRequestStore(t, filepath.Join(t.TempDir(), "state.db"))
	for i, field := range storedVisibleGoalFields {
		for j, bad := range []string{string([]byte{'a', 0xff, 'b'}), "a\x00b"} {
			t.Run(field+"/"+strconv.Itoa(j), func(t *testing.T) {
				id := strconv.Itoa(i) + "-" + strconv.Itoa(j)
				g := storedVisibleGoal("create-" + id)
				corruptStoredVisibleGoal(&g, field, bad)
				if err := s.SaveGoalContract(t.Context(), g, 0); !errors.Is(err, model.ErrGoalInvalid) {
					t.Error("malformed visible goal text created canonical state", err)
				}
				if _, err := s.GetActiveGoalContract(t.Context(), g.SessionID); !errors.Is(err, model.ErrGoalNotFound) {
					t.Error("refused create left active goal", err)
				}
				current := storedVisibleGoal("current-" + id)
				if err := s.SaveGoalContract(t.Context(), current, 0); err != nil {
					t.Fatal(err)
				}
				before, err := s.GetActiveGoalContract(t.Context(), current.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := json.Marshal(before)
				if err != nil {
					t.Fatal(err)
				}
				invalid := storedVisibleGoal("current-" + id)
				invalid.Revision = 2
				corruptStoredVisibleGoal(&invalid, field, bad)
				if err = s.SaveGoalContract(t.Context(), invalid, 1); !errors.Is(err, model.ErrGoalInvalid) {
					t.Error("malformed visible goal revision accepted", err)
				}
				after, err := s.GetActiveGoalContract(t.Context(), current.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(after)
				if err != nil || string(raw) != string(snapshot) {
					t.Error("invalid visible text changed exact current goal", err)
				}
				if _, err = s.GetGoalContract(t.Context(), current.ID, 2); !errors.Is(err, model.ErrGoalNotFound) {
					t.Error("invalid visible text persisted revision", err)
				}
			})
		}
	}
}
func TestSaveVisibleGoalTextPreservesValidUnicodeAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(t.Context()); err != nil {
		s.Close()
		t.Fatal(err)
	}
	g := storedVisibleGoal("valid-unicode")
	for _, field := range storedVisibleGoalFields {
		corruptStoredVisibleGoal(&g, field, " \tExact 文字 ⚡ <bytes> \"quotes\"\n ")
	}
	if err = s.SaveGoalContract(t.Context(), g, 0); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetActiveGoalContract(t.Context(), g.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	// Save owns clocks and derived understanding, not the user's text.
	g.CreatedAt, g.UpdatedAt, g.UnderstandingState = got.CreatedAt, got.UpdatedAt, got.UnderstandingState
	if !reflect.DeepEqual(g, got) {
		t.Fatal("valid model-visible goal text altered by canonical JSON persistence/reopen")
	}
}
