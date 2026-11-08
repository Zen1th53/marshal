package app

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturereview"
)

func TestMarshalArchitectureAcceptanceRequiresRecordedExactInstruction(t *testing.T) {
	rt := browserContextRuntime(t)
	file := browserContextState(t, rt, false)
	review, err := architecturereview.New(architecturereview.Config{Project: rt.ProjectIdentity(), ArchitectureID: "architecture-context", StateDir: filepath.Dir(file)})
	if err != nil {
		t.Fatal(err)
	}
	state, err := review.State()
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		ID       string `json:"architecture_id"`
		Revision int64  `json:"revision"`
		Digest   string `json:"digest"`
	}
	if err := json.Unmarshal(state, &selected); err != nil {
		t.Fatal(err)
	}
	accept := func(ref string, revision int64, digest string) error {
		return rt.acceptArchitectureByMarshal(t.Context(), review, "marshal-session", ref, "proposal-1", selected.ID, revision, digest)
	}
	if err := accept("invented", selected.Revision, selected.Digest); err == nil {
		t.Fatal("unrecorded instruction accepted")
	}
	_, err = rt.RecordArchitectureOperatorInstruction(t.Context(), "marshal-session", "operator-1", "accept architecture")
	if !architectureCommandEdition {
		if err == nil {
			t.Fatal("Community recorded a model approval instruction")
		}
		if err := accept("operator-1", selected.Revision, selected.Digest); err == nil {
			t.Fatal("Community accepted model approval")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := accept("operator-1", selected.Revision+1, selected.Digest); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err := accept("operator-1", selected.Revision, "forged"); err == nil {
		t.Fatal("forged digest accepted")
	}
	if err := accept("operator-1", selected.Revision, selected.Digest); err != nil {
		t.Fatal(err)
	}
	if err := rt.RequireArchitectureAcceptance(t.Context()); err != nil {
		t.Fatal(err)
	}
	accepted, err := rt.ArchitectureAcceptance(t.Context())
	if err != nil || !accepted.ModelSelfApproval || accepted.OperatorInstructionRef != "operator-1" {
		t.Fatalf("runtime acceptance lookup: %+v %v", accepted, err)
	}
	if !rt.store.ArchitectureFactAvailable(t.Context(), rt.ProjectIdentity(), json.RawMessage(`{"id":"runtime","name":"Runtime","responsibility":"Own state","technology":"Go","files":[],"evidence":[]}`)) {
		t.Fatal("accepted native architecture fact unavailable")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Approval struct {
			Model bool   `json:"model_self_approval"`
			Ref   string `json:"operator_instruction_ref"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Approval.Model || saved.Approval.Ref != "operator-1" {
		t.Fatalf("missing model receipt: %s", raw)
	}
	if err := os.WriteFile(filepath.Join(rt.ProjectRoot(), "new-source.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rt.RequireArchitectureAcceptance(t.Context()); err == nil {
		t.Fatal("source change retained usable acceptance")
	}
}

func TestMarshalArchitectureAcceptanceRefusesForgedAndForeignInstructions(t *testing.T) {
	if !architectureCommandEdition {
		return
	}
	for _, mutation := range []string{"session", "foreign project", "instruction text", "revision changed", "forged approval JSON", "receipt deleted"} {
		t.Run(mutation, func(t *testing.T) {
			rt := browserContextRuntime(t)
			file := browserContextState(t, rt, false)
			review, err := architecturereview.New(architecturereview.Config{Project: rt.ProjectIdentity(), ArchitectureID: "architecture-context", StateDir: filepath.Dir(file)})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var state map[string]any
			if err := json.Unmarshal(raw, &state); err != nil {
				t.Fatal(err)
			}
			digest := state["digest"].(string)
			text := "accept architecture"
			if mutation == "instruction text" {
				text = "do not accept architecture"
			}
			if _, err := rt.RecordArchitectureOperatorInstruction(t.Context(), "session-1", "ref-1", text); err != nil && mutation != "instruction text" {
				t.Fatal(err)
			}
			session := "session-1"
			if mutation == "session" {
				session = "session-2"
			}
			if mutation == "foreign project" {
				other := browserContextRuntime(t)
				if err := other.acceptArchitectureByMarshal(t.Context(), review, session, "ref-1", "proposal-1", "architecture-context", 1, digest); err == nil {
					t.Fatal("cross-project instruction accepted")
				}
				return
			}
			if mutation == "revision changed" {
				state["revision"] = 2
				raw, err = json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "forged approval JSON" {
				if err := review.AcceptByMarshal(t.Context(), rt.ProjectIdentity(), "architecture-context", 1, digest, "ref-1", "invented-receipt", func(json.RawMessage) error { return nil }); err != nil {
					t.Fatal(err)
				}
				if err := rt.RequireArchitectureAcceptance(t.Context()); err == nil {
					t.Fatal("forged JSON bypassed SQLite receipt")
				}
				return
			}
			if mutation == "receipt deleted" {
				if err := rt.acceptArchitectureByMarshal(t.Context(), review, session, "ref-1", "proposal-1", "architecture-context", 1, digest); err != nil {
					t.Fatal(err)
				}
				// Immutable rows cannot be deleted, including through the receiver's database.
				db, err := sql.Open("sqlite", filepath.Join(rt.layout.RuntimeDir, "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.ExecContext(t.Context(), "DELETE FROM architecture_marshal_receipts"); err == nil {
					t.Fatal("receipt deletion allowed")
				}
				return
			}
			if err := rt.acceptArchitectureByMarshal(t.Context(), review, session, "ref-1", "proposal-1", "architecture-context", 1, digest); err == nil {
				t.Fatal("invalid operator instruction or stale review accepted")
			}
		})
	}
}

func TestMarshalArchitectureAcceptanceWithoutBrowserServer(t *testing.T) {
	if !architectureCommandEdition {
		return
	}
	rt := browserContextRuntime(t)
	file := browserContextState(t, rt, false)
	if _, err := rt.RecordArchitectureOperatorInstruction(t.Context(), "chat", "operator-ref", "accept architecture"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		ID       string `json:"architecture_id"`
		Revision int64  `json:"revision"`
		Digest   string `json:"digest"`
	}
	if err := json.Unmarshal(raw, &selected); err != nil {
		t.Fatal(err)
	}
	if err := rt.AcceptArchitectureByMarshal(t.Context(), "chat", "operator-ref", "proposal-ref", selected.ID, selected.Revision, selected.Digest); err != nil {
		t.Fatal(err)
	}
	if err := rt.RequireArchitectureAcceptance(t.Context()); err != nil {
		t.Fatal(err)
	}
}
