package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestMemoryWorkerAudienceBeforeLimitAndAfterMarshalCache(t *testing.T) {
	ctx := context.Background()
	rt, service := openTestMemoryService(t)
	principal := testPrincipal("audience-reader")
	now := time.Now().UTC()
	record := model.MemoryRecordV2{ID: "MEM-SUMMARY", ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable, Authority: model.AuthorityVerified, Title: "architecture", Body: "Source-labelled project summary: independent governed execution.", Scope: string(model.ScopeProject), ScopeID: "PROJECT-local", Source: model.MemorySource{Kind: "runtime_service", AgentID: "claude", Reference: "architecture study"}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
	if err := rt.Store().WriteMemoryV2(ctx, record); err != nil {
		t.Fatal(err)
	}
	// More than the cold fallback LIMIT: filtering afterwards would return no
	// worker results even though a relevant project summary is available.
	for i := 0; i < 65; i++ {
		imported := record
		imported.ID = fmt.Sprintf("MEM-IMPORT-%03d", i)
		imported.Source.Kind = "external"
		imported.Body = "architecture PRIVATE EARLIER SESSION"
		imported.UpdatedAt = now.Add(time.Duration(i+1) * time.Second)
		if err := rt.Store().WriteMemoryV2(ctx, imported); err != nil {
			t.Fatal(err)
		}
	}
	request := RecallRequest{ProjectID: "PROJECT-local", Query: "architecture", MaxRecords: 1, AllowedScopeIDs: []string{"PROJECT-local"}, ExcludeSessionHistory: true}
	assertWorker := func() {
		t.Helper()
		response, err := service.Recall(ctx, principal, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Results) != 1 || response.Results[0].ID != record.ID || !strings.Contains(response.Context, record.Body) {
			t.Fatalf("relevant summary was starved: %+v", response)
		}
		if strings.Contains(response.Context, "PRIVATE EARLIER SESSION") {
			t.Fatal("worker received imported conversation")
		}
		for _, decision := range response.Receipt.Decisions {
			if strings.HasPrefix(decision.MemoryID, "MEM-IMPORT-") {
				t.Fatal("excluded history leaked through receipt")
			}
		}
	}
	assertWorker() // cold SQL fallback
	if err := service.RebuildProjections(ctx, record.ProjectID); err != nil {
		t.Fatal(err)
	}
	marshalRequest := request
	marshalRequest.ExcludeSessionHistory = false
	marshalRequest.Query = "MEM-IMPORT-000"
	marshalResponse, err := service.Recall(ctx, principal, marshalRequest)
	if err != nil || len(marshalResponse.Results) != 1 {
		t.Fatalf("Marshal history access changed: %+v %v", marshalResponse, err)
	}
	workerExact := marshalRequest
	workerExact.ExcludeSessionHistory = true
	response, err := service.Recall(ctx, principal, workerExact)
	if err != nil || len(response.Results) != 0 || strings.Contains(response.Context, "PRIVATE EARLIER SESSION") {
		t.Fatalf("cached/exact history query escaped audience gate: %+v %v", response, err)
	}
	assertWorker() // authorized lexical/index/cache path
}

type sourceChangingCandidate struct {
	change func() error
}

func (p sourceChangingCandidate) Name() string { return "source-change" }
func (p sourceChangingCandidate) QueryCandidates(context.Context, string, []string, string, int) ([]CandidateResult, error) {
	return nil, p.change()
}

func TestMemoryWorkerAudienceRechecksCanonicalSource(t *testing.T) {
	ctx := context.Background()
	rt, service := openTestMemoryService(t)
	now := time.Now().UTC()
	record := model.MemoryRecordV2{ID: "MEM-CHANGING", ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable, Authority: model.AuthorityVerified, Title: "architecture", Body: "earlier private conversation", Scope: string(model.ScopeProject), ScopeID: "PROJECT-local", Source: model.MemorySource{Kind: "runtime_service"}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
	if err := rt.Store().WriteMemoryV2(ctx, record); err != nil {
		t.Fatal(err)
	}
	service.RegisterCandidateProvider(sourceChangingCandidate{change: func() error {
		_, err := rt.Store().UpdateMemory(ctx, record.ProjectID, record.ID, record.Revision, func(current *model.MemoryRecordV2) error {
			current.Source.Kind = "external"
			return nil
		})
		if err != nil {
			t.Errorf("change canonical source: %v", err)
		}
		return err
	}})
	response, err := service.Recall(ctx, testPrincipal("reader"), RecallRequest{ProjectID: record.ProjectID, Query: "architecture", ExcludeSessionHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 0 || strings.Contains(response.Context, record.Body) {
		t.Fatalf("source changed after candidate authorization leaked: %+v", response)
	}
}
