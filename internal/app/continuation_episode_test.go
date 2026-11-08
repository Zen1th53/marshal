package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
)

// One 25-minute codex session produced 121 "Imported Session" durable
// records, one per captured message, and recall for "calc" returned only
// those. A session is now one bounded, deduplicated episode candidate.
func TestLiveSessionCaptureIsOneBoundedEpisodeNotOneMemoryPerMessage(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	requests := 0
	r.SetPermissionSink(func(req permission.Request) {
		if req.Kind == "memory" {
			requests++
		}
	})
	base := time.Date(2026, 10, 29, 3, 30, 0, 0, time.UTC)
	var all []importer.Message
	for i := 0; i < 121; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msg := importer.Message{Role: role, Content: fmt.Sprintf("calc step %03d: %s", i, strings.Repeat("x", 120)), Timestamp: base.Add(time.Duration(i) * 10 * time.Second)}
		all = append(all, msg)
		// The live watcher delivers what is new since its last poll.
		if _, _, err := r.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: "codex-25min", Provider: "codex", CWD: r.ProjectRoot(), Messages: []importer.Message{msg}}); err != nil {
			t.Fatal(err)
		}
	}
	// A restarted watcher replays the whole history: nothing new is recorded.
	records, _, err := r.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: "codex-25min", Provider: "codex", CWD: r.ProjectRoot(), Messages: all})
	if err != nil || len(records) != 1 {
		t.Fatalf("replay: %v %v", records, err)
	}
	stored, err := r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID()})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || requests != 1 {
		t.Fatalf("session flooded memory: records=%d review requests=%d", len(stored), requests)
	}
	ep := stored[0]
	if ep.Lifecycle != model.MemoryCandidate || !strings.HasPrefix(ep.ID, "MEM-IMPORT-") || ep.ID != importer.SessionEpisodeID(r.ProjectID(), "codex", "codex-25min", 0) {
		t.Fatalf("episode identity: %+v", ep)
	}
	if len(ep.Body) > importer.EpisodeMaxBytes || !strings.Contains(ep.Body, "calc step 000") || !strings.Contains(ep.Body, "calc step 120") || !strings.Contains(ep.Body, "omitted") {
		t.Fatalf("episode not bounded with opening and latest turns (%d bytes)", len(ep.Body))
	}
	if episodeCount(ep.ExtMeta["episode_messages"]) != 121 || strings.Count(ep.Body, "calc step 060") > 1 {
		t.Fatalf("episode messages: %v", ep.ExtMeta["episode_messages"])
	}

	// Once the episode is decided, later turns open the next part; the
	// retained part is never edited behind the reviewer's back.
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "memory", Object: ep.ID}, true, "operator memory review"); err != nil {
		t.Fatal(err)
	}
	retained, _ := r.store.GetMemoryV2(ctx, r.ProjectID(), ep.ID)
	next, _, err := r.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: "codex-25min", Provider: "codex", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "calc done"}}})
	if err != nil || len(next) != 1 || next[0].ID != importer.SessionEpisodeID(r.ProjectID(), "codex", "codex-25min", 1) || next[0].Lifecycle != model.MemoryCandidate {
		t.Fatalf("next part: %+v %v", next, err)
	}
	after, _ := r.store.GetMemoryV2(ctx, r.ProjectID(), ep.ID)
	if after.Body != retained.Body || after.Revision != retained.Revision {
		t.Fatal("retained episode edited after its decision")
	}
}

func TestPendingEpisodeApprovalUsesUpdatedRevision(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	tr := importer.SessionTranscript{SessionID: "s", Provider: "claude", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "user", Content: "first"}}}
	first, _, err := r.ProposeContinuation(ctx, tr)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	tr.Messages = []importer.Message{{Role: "assistant", Content: "second"}}
	if _, _, err := r.ProposeContinuation(ctx, tr); err != nil {
		t.Fatal(err)
	}
	control, _ := r.OpenLocalControl(ctx)
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "memory", Object: first[0].ID}, true, "operator memory review"); err != nil {
		t.Fatalf("approval of an episode updated while pending: %v", err)
	}
	rec, _ := r.store.GetMemoryV2(ctx, r.ProjectID(), first[0].ID)
	if rec.Lifecycle != model.MemoryDurable || !strings.Contains(rec.Body, "first") || !strings.Contains(rec.Body, "second") {
		t.Fatalf("approved episode: %+v", rec)
	}
}

func TestRecallRanksCuratedKnowledgeAboveImportedHistory(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Several retained session episodes mention calc (as the 121 imports did).
	for i := 0; i < 5; i++ {
		recs, _, err := r.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: fmt.Sprintf("old-%d", i), Provider: "codex", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "user", Content: "calc calc calc please fix calc"}}})
		if err != nil || len(recs) != 1 {
			t.Fatal(recs, err)
		}
		if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "memory", Object: recs[0].ID}, true, "operator memory review"); err != nil {
			t.Fatal(err)
		}
	}
	curated := model.MemoryRecordV2{
		ID: "MEM-CURATED-CALC", ProjectID: r.ProjectID(), Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable,
		Confidence: model.ConfidenceObserved, Authority: model.AuthorityAgent, Title: "calc module convention",
		Body: "The calc module keeps parsing and evaluation separate.", Scope: string(model.ScopeProject), ScopeID: r.ProjectID(),
		Source: model.MemorySource{Kind: "operator", Reference: "test"}, ObservedAt: time.Now().UTC(), ValidFrom: time.Now().UTC(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), IngestedAt: time.Now().UTC(),
	}
	if err := r.store.WriteMemoryV2(ctx, curated); err != nil {
		t.Fatal(err)
	}
	if err := r.Memory().IndexRecord(ctx, curated); err != nil {
		t.Fatal(err)
	}
	resp, err := r.Memory().Recall(ctx, testPrincipal("marshal"), RecallRequest{ProjectID: r.ProjectID(), Query: "calc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].ID != curated.ID {
		t.Fatalf("curated knowledge did not win: %+v", resp.Results)
	}
	history := 0
	for _, item := range resp.Results {
		if strings.HasPrefix(item.ID, "MEM-IMPORT-") {
			history++
		}
	}
	if history > maxRecalledSessionHistory {
		t.Fatalf("imported history crowded recall: %d of %d", history, len(resp.Results))
	}
	capped := false
	for _, d := range resp.Receipt.Decisions {
		capped = capped || d.Reason == "session_history_capped"
	}
	if !capped {
		t.Fatal("history cap not recorded in the retrieval receipt")
	}
}

// The Marshal's own chat is captured too. Each rejection of its episode was
// itself a new turn, which opened the next part and asked again: 32 review
// requests in one e2e run. A declined session is no longer re-asked.
func TestRejectedEpisodeStopsFurtherReviewRequests(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	requests := 0
	r.SetPermissionSink(func(req permission.Request) {
		if req.Kind == "memory" {
			requests++
		}
	})
	tr := importer.SessionTranscript{SessionID: "marshal-chat", Provider: "claude", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "user", Content: "first"}}}
	first, _, err := r.ProposeContinuation(ctx, tr)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	control, _ := r.OpenLocalControl(ctx)
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "memory", Object: first[0].ID}, false, "Marshal declined its own transcript"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tr.Messages = []importer.Message{{Role: "assistant", Content: fmt.Sprintf("I declined request %d", i)}}
		next, _, err := r.ProposeContinuation(ctx, tr)
		if err != nil || len(next) != 0 {
			t.Fatalf("declined session captured again: %+v %v", next, err)
		}
	}
	if requests != 1 {
		t.Fatalf("review requests = %d, want 1", requests)
	}
}

// A real Extreme run spent 8-10 minutes per Marshal turn reviewing its own
// noninteractive reviewer, verifier and permission turns, captured from the
// provider's history in the project folder as MEM-IMPORT candidates. Those
// sessions are now declined by the runtime without a review request, while an
// interactive session is still proposed for review.
func TestMarshalRoleTurnCaptureIsDeclinedWithoutReview(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	var requests []string
	r.SetPermissionSink(func(req permission.Request) {
		if req.Kind == "memory" {
			requests = append(requests, req.Object)
		}
	})
	at := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	roleTurns := []importer.SessionTranscript{
		{SessionID: "claude-review", Provider: "claude", CWD: r.ProjectRoot(), Messages: []importer.Message{
			{Role: "user", Content: marshalRoleTurnMarker + "Review this hand-in and return a JSON verdict.", Timestamp: at},
		}},
		{SessionID: "codex-verify", Provider: "codex", CWD: r.ProjectRoot(), Messages: []importer.Message{
			// Codex records its environment context as a user message first.
			{Role: "user", Content: "<environment_context><cwd>" + r.ProjectRoot() + "</cwd></environment_context>", Timestamp: at},
			{Role: "user", Content: marshalRoleTurnMarker + "Perform independent pre-delivery verification.", Timestamp: at},
		}},
		{SessionID: "agy-review", Provider: "antigravity", CWD: r.ProjectRoot(), Messages: []importer.Message{
			{Role: "user", Content: "/plan " + marshalRoleTurnAgyPreamble + "Use only the supplied task and evidence.\n\n" + marshalRoleTurnMarker + "Review this hand-in.", Timestamp: at},
		}},
	}
	for _, tr := range roleTurns {
		records, _, err := r.ProposeContinuation(ctx, tr)
		if err != nil || len(records) != 0 {
			t.Fatalf("%s: role turn proposed: %+v %v", tr.SessionID, records, err)
		}
		// The live watcher then delivers the turn's answer on its own.
		tr.Messages = []importer.Message{{Role: "assistant", Content: `{"Verdict":"accept"}`, Timestamp: at.Add(time.Minute)}}
		records, _, err = r.ProposeContinuation(ctx, tr)
		if err != nil || len(records) != 0 {
			t.Fatalf("%s: role turn answer proposed: %+v %v", tr.SessionID, records, err)
		}
		declined, err := r.store.GetMemoryV2(ctx, r.ProjectID(), importer.SessionEpisodeID(r.ProjectID(), tr.Provider, tr.SessionID, 0))
		if err != nil || declined.Lifecycle != model.MemoryRejected || declined.ExtMeta["rejected_by"] != marshalRuntimeRoleTurnDecider || strings.Contains(declined.Body, "Review this hand-in") {
			t.Fatalf("%s: role turn not declined by the runtime: %+v %v", tr.SessionID, declined, err)
		}
	}
	if len(requests) != 0 {
		t.Fatalf("role turns asked the Marshal for review: %v", requests)
	}
	if got := r.ContinuationCandidates(); len(got) != 0 {
		t.Fatalf("role turns left candidates: %+v", got)
	}

	// An interactive session is still captured, even when a later message
	// quotes a role-turn prompt.
	chat := importer.SessionTranscript{SessionID: "marshal-chat", Provider: "codex", CWD: r.ProjectRoot(), Messages: []importer.Message{
		{Role: "user", Content: "Start.", Timestamp: at},
		{Role: "assistant", Content: "I am the Marshal for this run.", Timestamp: at},
	}}
	records, _, err := r.ProposeContinuation(ctx, chat)
	if err != nil || len(records) != 1 || records[0].Lifecycle != model.MemoryCandidate || len(requests) != 1 {
		t.Fatalf("interactive session not proposed: %+v %v requests=%v", records, err, requests)
	}
	chat.Messages = []importer.Message{{Role: "user", Content: marshalRoleTurnMarker + "pasted prompt", Timestamp: at.Add(time.Minute)}}
	records, _, err = r.ProposeContinuation(ctx, chat)
	if err != nil || len(records) != 1 || records[0].Lifecycle != model.MemoryCandidate || !strings.Contains(records[0].Body, "pasted prompt") {
		t.Fatalf("interactive session stopped capturing: %+v %v", records, err)
	}
}

// Every role turn the runtime starts opens with the marker the capture rule
// recognizes, so the two cannot drift apart.
func TestMarshalRoleTurnPromptCarriesCaptureMarker(t *testing.T) {
	root := t.TempDir()
	stdin := filepath.Join(root, "stdin.txt")
	stub := `#!/bin/sh
cat > '` + stdin + `'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-test"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}'
`
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	cli := &MarshalCLI{Provider: "codex", Dir: root}
	var output struct {
		OK bool `json:"ok"`
	}
	if err := cli.turn(requiredMarshalTestContext(t, cli), "Review this hand-in and return a JSON verdict.", marshalDraftSchema, &output); err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatal(err)
	}
	if !isMarshalRoleTurn(importer.SessionTranscript{Messages: []importer.Message{{Role: "user", Content: string(prompt)}}}) {
		t.Fatalf("role-turn prompt not recognized by capture: %.200q", prompt)
	}
}
