package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestControlMemoryRejectsForeignOrDetachedAuthority(t *testing.T) {
	ctx := context.Background()
	ws, runtime := realControlWorkspace(t, "SESSION-memory-authority")
	other, foreign := realControlWorkspace(t, "SESSION-memory-foreign")
	original := ws.controlSource().Authority.(*runtimeControlAuthority)
	foreignControl := other.controlSource().Authority.(*runtimeControlAuthority).localControl
	before, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"foreign-binding", "absent-binding", "foreign-runtime", "detached-control", "foreign-control"} {
		t.Run(scenario, func(t *testing.T) {
			// Copy only the scalar handles, not the authority's mutex.
			a := &runtimeControlAuthority{runtime: runtime, store: runtime.Store(), projectID: original.projectID, sessionID: original.sessionID, localControl: original.localControl}
			invalidBinding := true
			switch scenario {
			case "foreign-binding":
				a.projectID = projectid.ID(foreign.ProjectIdentity())
			case "absent-binding":
				a.projectID = ""
			case "foreign-runtime":
				a.runtime = foreign
			case "detached-control":
				a.localControl = nil
				invalidBinding = false
			case "foreign-control":
				a.localControl = foreignControl
				invalidBinding = false
			}
			if _, err := a.Remember(ctx, "refused source", "Never retain through foreign authority."); err == nil {
				t.Fatal("unauthorized write succeeded")
			}
			if invalidBinding {
				if _, err := a.MemoryStatus(ctx); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("status: %v", err)
				}
				if _, err := a.MemoryRecord(ctx, "MEM-unknown"); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("get: %v", err)
				}
				if _, err := a.RecallRecent(ctx, 1); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("recent: %v", err)
				}
				if err := a.RebuildMemoryProjections(ctx); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("rebuild: %v", err)
				}
				if err := a.InvalidateMemory(ctx, "MEM-unknown", runtime.ProjectID()); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("invalidate: %v", err)
				}
				if _, err := a.CaptureOutcome(ctx, "TASK-unknown", "done", ""); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("outcome: %v", err)
				}
			}
		})
	}
	after, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused requests changed memory: %v", err)
	}
}

func TestControlMemoryPreservesACLRevocationAndSecretRefusal(t *testing.T) {
	ctx := context.Background()
	ws, runtime := realControlWorkspace(t, "SESSION-memory-acl")
	a := ws.controlSource().Authority.(*runtimeControlAuthority)
	rec, err := a.Remember(ctx, "visible study", "Bounded reviewed source facts.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.MemoryRecord(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	// Model an existing canonical ACL revocation in the owned fixture database.
	// This leaves the production read filters unchanged.
	_, err = runtime.Store().UpdateMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, func(row *model.MemoryRecordV2) error { row.ACLScope = "SESSION-different-owner"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.MemoryRecord(ctx, rec.ID); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("revoked ACL disclosed exact record: %v", err)
	}
	if rows, err := a.RecallRecent(ctx, 1); err != nil || len(rows) != 0 {
		t.Fatalf("revoked ACL disclosed recent record: %+v %v", rows, err)
	}
	before, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil {
		t.Fatal(err)
	}
	body := "Untrusted source contains " + "sk-proj-" + "abc1234567890def1234567890"
	_, err = a.Remember(ctx, "refused secret source", body)
	if security.RedactsSecrets {
		// Extreme keeps the memory with the secret removed.
		if err != nil {
			t.Fatalf("Marshal refused a redactable source: %v", err)
		}
		after, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
		if err != nil || len(after) != len(before)+1 {
			t.Fatalf("redacted memory not kept: %v", err)
		}
		for _, rec := range after {
			if strings.Contains(rec.Body, "abc1234567890def1234567890") {
				t.Fatal("secret persisted")
			}
		}
		return
	}
	if err == nil {
		t.Fatal("central memory firewall accepted credential-shaped source")
	}
	after, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused source changed retained memory: %v", err)
	}
}
