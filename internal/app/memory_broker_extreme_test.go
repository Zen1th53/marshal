//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"testing"
	"time"
)

func TestMCPMemoryIndexFailureRetainsCanonicalAndFreshRuntimeRepairs(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := t.Context()
	if err := r.store.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.store.InitMCPMemory(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{ID: "MEM-index-failure", ProjectID: r.ProjectID(), Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Confidence: model.ConfidenceInferred, Title: "projection repair", Body: "Rebuild private knowledge projection", Scope: "project", ScopeID: r.ProjectID(), ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "live_mcp", Reference: "owner"}}
	raw, _ := json.Marshal(map[string]string{"action": "memory", "id": rec.ID})
	c, _, err := r.store.AdmitMCPMemory(ctx, r.ProjectID(), store.MCPProposal{ID: "mcp:index-failure", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "index-failure", Payload: string(raw)}, store.MCPMemoryCandidate{ImportKey: "index-failure", PayloadDigest: "digest", Record: rec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.RegisterMCPMemoryCandidate(ctx, c.Occurrence); err != nil {
		t.Fatal(err)
	}
	req := permission.Request{Kind: "memory", Object: rec.ID, ProposalID: c.Occurrence, SourceLaunch: "launch"}
	if err = r.CommandPermission(ctx, req, true, "unauthenticated caller"); err == nil {
		t.Fatal("memory permission bypassed local authentication")
	}
	audit := model.Event{ID: "EVENT-index-failure", ProjectID: r.ProjectID(), Type: "PERMISSION_DECIDED", Timestamp: time.Now().UTC(), Data: map[string]any{"allow": true, "source": "marshal"}}
	// Fault injection only replaces the derived-index step, after the checked
	// commit seam used by authenticated CommandPermission; it cannot write SQL.
	handled, err := r.commitBrokerMemoryPermissionWithIndex(ctx, req, true, rec, audit, func(context.Context, model.MemoryRecordV2) error { return errors.New("injected index unavailable") })
	if !handled || err == nil {
		t.Fatalf("index failure missing: %v %v", handled, err)
	}
	saved, err := r.store.MCPMemory(ctx, r.ProjectID(), c.Occurrence)
	if err != nil || saved.State != "committed" {
		t.Fatalf("false failure receipt: %+v %v", saved, err)
	}
	if _, err = r.store.GetMemoryV2(ctx, r.ProjectID(), rec.ID); err != nil {
		t.Fatal("canonical memory lost")
	}
	fresh, err := Open(ctx, r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.RepairMCPMemoryIndex(ctx, c.Occurrence); err != nil {
		t.Fatal(err)
	}
	saved, err = fresh.store.MCPMemory(ctx, fresh.ProjectID(), c.Occurrence)
	if err != nil || saved.State != "applied" {
		t.Fatal(saved, err)
	}
	records, err := fresh.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: fresh.ProjectID()})
	if err != nil || len(records) != 1 {
		t.Fatalf("repair reinserted record %d %v", len(records), err)
	}
}

func TestMCPMemoryManagedIDNeverFallsThroughMissingOriginalOccurrence(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := t.Context()
	if err := r.store.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.store.InitMCPMemory(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{ID: "MEM-managed-sink", ProjectID: r.ProjectID(), Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Confidence: model.ConfidenceInferred, Title: "Immutable source", Body: "Historical checked source data.", Scope: "project", ScopeID: r.ProjectID(), ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "live_mcp", Reference: "owner"}}
	raw, _ := json.Marshal(map[string]string{"action": "memory", "id": rec.ID})
	c, _, err := r.store.AdmitMCPMemory(ctx, r.ProjectID(), store.MCPProposal{ID: "mcp:original-sink", Launch: "original-launch", Owner: "owner", Provider: "codex", Submission: "original-sink", Payload: string(raw)}, store.MCPMemoryCandidate{ImportKey: "original-sink", PayloadDigest: "digest", Record: rec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.RegisterMCPMemoryCandidate(ctx, c.Occurrence); err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []permission.Request{
		{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent", SourceLaunch: "original-launch", ProposalID: "mcp:generic-alias"},
		{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent", SourceLaunch: "replacement-launch", ProposalID: c.Occurrence},
		{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent"},
	} {
		if err := r.CommandPermission(control.Context(ctx), req, true, "authenticated fixture decision"); err == nil {
			t.Fatal("managed ID fell through legacy writer")
		}
	}
	// Classification comes from the canonical journal even if caller-owned
	// metadata claims this known ID is an ordinary imported record.
	forged := rec
	forged.Source = model.MemorySource{Kind: "ordinary", Reference: "caller"}
	forged.ExtMeta = map[string]any{"launch_id": "caller"}
	handled, err := r.commitBrokerMemoryPermissionWithIndex(ctx, permission.Request{Kind: "memory", Object: rec.ID, SourceLaunch: "original-launch", ProposalID: "mcp:alias"}, true, forged, model.Event{}, func(context.Context, model.MemoryRecordV2) error {
		t.Fatal("index called before canonical grant")
		return nil
	})
	if !handled || err == nil {
		t.Fatal("caller metadata hid canonical broker origin")
	}
	if _, err = r.store.GetMemoryV2(ctx, r.ProjectID(), rec.ID); err == nil {
		t.Fatal("managed record persisted outside original receipt")
	}
	original, err := r.store.MCPMemory(ctx, r.ProjectID(), c.Occurrence)
	if err != nil || original.State != "admitted" {
		t.Fatal(original, err)
	}
}
