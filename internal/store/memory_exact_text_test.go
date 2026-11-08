package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestRequiredMemoryTextSurvivesStoreAndCompletePacket(t *testing.T) {
	ctx := t.Context()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const projectID = "PROJ-memory-exact-text"
	if err := st.InitProject(ctx, model.Project{ID: projectID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := model.MemoryRecordV2{ID: "MEM-exact-text", ProjectID: projectID, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Title: "Asl sarlavha ⚡", Body: "Asl xotira: Oʻzbekcha ⚡ <exact>\n", Scope: "project", ScopeID: projectID, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	malformed := string([]byte{'a', 0xff, 'b'})
	for _, field := range []string{"title", "body"} {
		for _, value := range []string{malformed, "text\x00hidden"} {
			bad := rec
			if field == "title" {
				bad.Title = value
			} else {
				bad.Body = value
			}
			if err := st.WriteMemoryV2(ctx, bad); !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("invalid %s write accepted: %v", field, err)
			}
			if _, err := st.GetMemoryV2(ctx, projectID, rec.ID); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("invalid %s write left a row: %v", field, err)
			}
		}
	}
	if err := st.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	before, err := st.GetMemoryV2(ctx, projectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"title", "body"} {
		for _, value := range []string{malformed, "text\x00hidden"} {
			_, err := st.UpdateMemory(ctx, projectID, rec.ID, before.Revision, func(current *model.MemoryRecordV2) error {
				if field == "title" {
					current.Title = value
				} else {
					current.Body = value
				}
				return nil
			})
			if !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("invalid %s update accepted: %v", field, err)
			}
			stored, err := st.GetMemoryV2(ctx, projectID, rec.ID)
			if err != nil || stored.Revision != before.Revision || stored.ContentDigest != before.ContentDigest || stored.Title != before.Title || stored.Body != before.Body {
				t.Fatalf("refused %s update changed canonical memory: %+v %v", field, stored, err)
			}
		}
	}
	stored, err := st.GetMemoryV2(ctx, projectID, rec.ID)
	if err != nil || stored.Title != rec.Title || stored.Body != rec.Body || stored.ContentDigest != rec.CanonicalDigest() {
		t.Fatalf("stored memory changed exact text: %+v %v", stored, err)
	}
	binding := projectcontext.ProjectBinding{CanonicalID: projectID, StorageID: projectID}
	contract := projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: binding, ID: "ARCH-memory-exact-text", Revision: 1, Purpose: "Preserve exact approved memory", Components: []projectcontext.Component{{ID: "core", Responsibility: "Remember accepted text", Paths: []string{"README.md"}}}, Invariants: []string{"Do not alter memory bytes"}, Required: []projectcontext.MemoryRef{{ID: stored.ID, ProjectID: stored.ProjectID, Scope: stored.Scope, ScopeID: stored.ScopeID, Revision: stored.Revision, ContentDigest: stored.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(stored), Class: projectcontext.Policy, Applies: projectcontext.Applicability{ProjectWide: true}, AcceptanceOccurrence: "operator-accepted", Reason: "Preserve exact wording"}}}
	digest, err := contract.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	manifest := projectcontext.Manifest{Contract: contract, Goal: projectcontext.Goal{ID: "GOAL-exact-text", Revision: 1, Outcome: "Preserve memory"}, Binding: projectcontext.Binding{Project: binding, Contract: projectcontext.ContractRef{ID: contract.ID, Revision: contract.Revision, Digest: digest}}, Records: []model.MemoryRecordV2{stored}}
	packet, err := projectcontext.Render(manifest, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Memories []struct{ Title, Body string } `json:"memories"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(packet.Text, "=== MARSHAL REQUIRED PROJECT CONTEXT ===\n")), &rendered); err != nil || len(rendered.Memories) != 1 || rendered.Memories[0].Title != rec.Title || rendered.Memories[0].Body != rec.Body {
		t.Fatalf("saved approved memory changed in packet: %+v %v", rendered, err)
	}
	if _, err := projectcontext.Render(manifest, packet.Bytes-1); !errors.Is(err, projectcontext.ErrBudget) {
		t.Fatalf("whole saved memory was truncated to fit: %v", err)
	}
}
