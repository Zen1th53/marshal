//go:build !governed

package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

func persistedReviewRecord(t *testing.T, w *Workspace, id, source string) model.MemoryRecordV2 {
	t.Helper()
	now := time.Now().UTC()
	r := model.MemoryRecordV2{ID: id, ProjectID: w.projectID, Revision: 1, Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Confidence: model.ConfidenceInferred, Title: "Retained policy awaiting explicit review", Body: "Preserve every accepted project decision and its original evidence.", Scope: string(model.ScopeProject), ScopeID: w.projectID, Source: model.MemorySource{Kind: source, Reference: "explicit isolated fixture"}, ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
	r.ContentDigest = r.CanonicalDigest()
	return r
}

func TestPrivateMemoryReviewMergesLiveAndPersistedWithoutApproval(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-memory-mixed-pages", false)
	var ids []string
	for i := 0; i < 13; i++ {
		r := persistedReviewRecord(t, w, fmt.Sprintf("MEM-persisted-%03d", i), "external")
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	for i := 0; i < 15; i++ {
		if _, _, err := runtime.ProposeContinuation(t.Context(), importer.SessionTranscript{SessionID: fmt.Sprintf("prior-host-%d", i), Provider: "claude", CWD: runtime.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: fmt.Sprintf("Retained untrusted fixture handoff %d", i)}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range runtime.ContinuationCandidates() {
		ids = append(ids, r.ID)
	}
	var text string
	for i := 1; i <= 3; i++ {
		out, err := w.cmd.Handle(t.Context(), fmt.Sprintf("/memory review %d", i))
		if err != nil {
			t.Fatal(err)
		}
		text += strings.Split(out, "\nNext:")[0]
	}
	for _, id := range ids {
		if strings.Count(text, id) != 1 {
			t.Fatal("mixed review omitted or duplicated candidate", id)
		}
	}
	rows, err := w.store.ListMemoryV2(t.Context(), store.MemoryQueryFilter{ProjectID: w.projectID})
	if err != nil || len(rows) != 28 || len(runtime.ContinuationCandidates()) != 28 {
		t.Fatal("review silently accepted or lost live retention", len(rows), err)
	}
	if !strings.Contains(w.cmd.helpText(), "promote <id>") {
		t.Fatal("supported promotion absent from help")
	}
	if out, _, ok := w.completer.Complete("/memory prom", len("/memory prom"), false); !ok || strings.TrimSpace(out) != "/memory promote" {
		t.Fatal("supported promotion absent from completion", out)
	}
	var live string
	command := "/memory review live"
	for page := 0; page < 2; page++ {
		out, err := w.cmd.Handle(t.Context(), command)
		if err != nil {
			t.Fatal(err)
		}
		live += strings.Split(out, "\nNext:")[0]
		if page == 0 {
			parts := strings.Split(out, "\nNext: ")
			if len(parts) != 2 || !strings.HasPrefix(parts[1], "/memory review live after ") {
				t.Fatal("live list lacks keyset continuation", out)
			}
			command = parts[1]
		}
	}
	var liveID string
	for _, rec := range runtime.ContinuationCandidates() {
		if strings.HasPrefix(rec.ID, "MEM-persisted-") {
			continue
		}
		liveID = rec.ID
		if strings.Count(live, rec.ID) != 1 {
			t.Fatal("live selector omitted or duplicated retention", rec.ID)
		}
	}
	if strings.Contains(live, "MEM-persisted-") {
		t.Fatal("live selector mixed persisted sources")
	}
	if _, err := w.cmd.Handle(t.Context(), "/memory promote "+liveID+" Treat live listing as review"); err == nil {
		t.Fatal("live metadata granted promotion")
	}
}

func TestPrivateMemoryReadCacheBoundsAndEvictsCapabilities(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-memory-cache-bound", false)
	for i := 0; i < 33; i++ {
		r := persistedReviewRecord(t, w, fmt.Sprintf("MEM-cached-%02d", i), "runtime_service")
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		if _, err := w.cmd.Handle(t.Context(), "/memory read "+r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.cmd.memoryReads.reads) != 32 {
		t.Fatal("read metadata cache unbounded")
	}
	if _, err := w.cmd.Handle(t.Context(), "/memory promote MEM-cached-00 Reviewed old source"); err == nil {
		t.Fatal("evicted handle still promoted")
	}
	for _, line := range []string{"/memory review 0", "/memory review many", "/memory read", "/memory read MEM-cached-32 -1", "/memory promote MEM-cached-32"} {
		out, err := w.cmd.Handle(t.Context(), line)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Fatal("invalid command failed to explain supported flow", line, out, err)
		}
	}
}

func TestPrivateMemoryReviewKeysetCommandsVisitEveryRetainedRowOnce(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-memory-keyset", false)
	for i := 0; i < 25; i++ {
		r := persistedReviewRecord(t, w, fmt.Sprintf("MEM-keyset-%03d", i), "external")
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	command := "/memory review"
	var joined string
	for page := 0; page < 3; page++ {
		out, err := w.cmd.Handle(t.Context(), command)
		if err != nil {
			t.Fatal(err)
		}
		joined += strings.Split(out, "\nNext:")[0]
		if page < 2 {
			parts := strings.Split(out, "\nNext: ")
			if len(parts) != 2 || !strings.HasPrefix(parts[1], "/memory review after MEM-keyset-") {
				t.Fatal("review lacks bounded keyset continuation", out)
			}
			command = parts[1]
		}
	}
	for i := 0; i < 25; i++ {
		if strings.Count(joined, fmt.Sprintf("MEM-keyset-%03d", i)) != 1 {
			t.Fatal("keyset omitted or duplicated retained row", i)
		}
	}
}

func TestPrivateMemoryConcurrentCloseCannotRetainOrMintRead(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-memory-close-read", false)
	r := persistedReviewRecord(t, w, "MEM-close-race", "runtime_service")
	if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var jobs sync.WaitGroup
	for i := 0; i < 8; i++ {
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			<-start
			for j := 0; j < 4; j++ {
				_, _, _ = w.cmd.handleOperatorMemoryReview(t.Context(), []string{"read", r.ID})
			}
		}()
	}
	close(start)
	w.Close()
	jobs.Wait()
	if _, _, err := w.cmd.handleOperatorMemoryReview(t.Context(), []string{"read", r.ID}); err == nil {
		t.Fatal("closed workspace minted a read capability")
	}
	w.cmd.memoryReads.mu.Lock()
	retained := len(w.cmd.memoryReads.reads)
	w.cmd.memoryReads.mu.Unlock()
	if retained != 0 {
		t.Fatal("concurrent read survived close", retained)
	}
}

func TestPrivateMemoryReadRequiresContiguousCompleteUnicodeBody(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-memory-full-body", false)
	r := persistedReviewRecord(t, w, "MEM-long-reviewed-source", "runtime_service")
	r.Body = strings.Repeat("é", app.OperatorMemoryPageBytes+9)
	r.ContentDigest = r.CanonicalDigest()
	if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	control := w.controlSource().Authority.(*runtimeControlAuthority).localControl
	ctx := control.Context(t.Context())
	token, err := runtime.BeginOperatorMemoryRead(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := runtime.ReadOperatorMemoryPage(ctx, token, 0, func(s string) string { return s })
	if err != nil || page.FullRead || !utf8.ValidString(page.Body) || len(page.Body) > app.OperatorMemoryPageBytes {
		t.Fatal("first Unicode page was incomplete or malformed", err)
	}
	if _, err = runtime.CommandPromoteReviewedMemory(ctx, token, "Review incomplete body"); err == nil {
		t.Fatal("partial read promoted")
	}
	if _, err = runtime.ReadOperatorMemoryPage(ctx, token, page.NextOffset+2, func(s string) string { return s }); err == nil {
		t.Fatal("cursor skipped retained bytes")
	}
	body := page.Body
	offset := page.NextOffset
	for !page.FullRead {
		page, err = runtime.ReadOperatorMemoryPage(ctx, token, offset, func(s string) string { return s })
		if err != nil {
			t.Fatal(err)
		}
		body += page.Body
		offset = page.NextOffset
	}
	if body != r.Body {
		t.Fatal("contiguous read lost source bytes")
	}
	if _, err = runtime.ReadOperatorMemoryPage(ctx, token, offset, func(s string) string { return s }); err == nil {
		t.Fatal("complete page replayed")
	}
	if _, err = runtime.CommandPromoteReviewedMemory(ctx, token, " "); err == nil {
		t.Fatal("blank rationale accepted")
	}
	result, err := runtime.CommandPromoteReviewedMemory(ctx, token, "Reviewed every byte of the retained source")
	if err != nil || !result.Persisted || !result.Indexed {
		t.Fatal("actual full read never promoted", result, err)
	}
	if _, err = runtime.CommandPromoteReviewedMemory(ctx, token, "Reviewed every byte of the retained source"); err == nil {
		t.Fatal("successful promotion replayed")
	}
}

func TestPrivateMemoryEmptyBodyCannotCreateReview(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-memory-empty", false)
	r := persistedReviewRecord(t, w, "MEM-empty-source", "runtime_service")
	r.Body = ""
	r.ContentDigest = r.CanonicalDigest()
	if err := w.store.WriteMemoryV2(t.Context(), r); err == nil {
		t.Fatal("empty body became canonical memory")
	}
	ctx := w.controlSource().Authority.(*runtimeControlAuthority).localControl.Context(t.Context())
	if _, err := runtime.BeginOperatorMemoryRead(ctx, r.ID); err == nil {
		t.Fatal("invalid empty memory minted review")
	}
}

func TestPrivateMemoryPromotionRefusesChangedFullStateAndExistingEvidenceRules(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-memory-refusals", false)
	for _, fault := range []string{"timestamp", "body", "acl", "no-evidence", "missing-evidence", "durable-agent"} {
		t.Run(fault, func(t *testing.T) {
			r := persistedReviewRecord(t, w, "MEM-refusal-"+fault, "runtime_service")
			if fault == "no-evidence" || fault == "missing-evidence" {
				r.Source.Kind = "external"
			}
			if fault == "missing-evidence" {
				r.EvidenceIDs = []string{"E-not-stored"}
			}
			if fault == "durable-agent" {
				r.Lifecycle = model.MemoryDurable
			}
			if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			if _, err := w.cmd.Handle(t.Context(), "/memory read "+r.ID); err != nil {
				t.Fatal(err)
			}
			wantRevision := r.Revision
			if fault == "timestamp" || fault == "body" || fault == "acl" {
				_, err := w.store.UpdateMemory(t.Context(), r.ProjectID, r.ID, r.Revision, func(changed *model.MemoryRecordV2) error {
					switch fault {
					case "timestamp":
						changed.UpdatedAt = changed.UpdatedAt.Add(time.Second)
					case "body":
						changed.Body += " Changed source"
					case "acl":
						changed.ACLScope = "SESSION-foreign"
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				wantRevision++
			}
			if _, err := w.cmd.Handle(t.Context(), "/memory promote "+r.ID+" Reviewed retained source"); err == nil {
				t.Fatal("invalid review or evidence promoted")
			}
			current, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.ID)
			if err != nil || current.Revision != wantRevision || current.Authority != model.AuthorityAgent {
				t.Fatal("refusal changed canonical authority", err)
			}
		})
	}
}

func TestPrivateMemoryReviewPagesRetainedRowsWithoutForeignDisclosure(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-memory-pages", false)
	for i := 0; i < 25; i++ {
		r := persistedReviewRecord(t, w, fmt.Sprintf("MEM-page-%03d", i), "external")
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	private := persistedReviewRecord(t, w, "MEM-PRIVATE-WITHHELD", "external")
	private.ACLScope = "SESSION-foreign"
	foreign := persistedReviewRecord(t, w, "MEM-FOREIGN-WITHHELD", "external")
	foreign.ScopeID = "PROJECT-foreign"
	for _, r := range []model.MemoryRecordV2{private, foreign} {
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	var text string
	for page := 1; page <= 3; page++ {
		out, err := w.cmd.Handle(t.Context(), fmt.Sprintf("/memory review %d", page))
		if err != nil {
			t.Fatal(err)
		}
		text += strings.Split(out, "\nNext:")[0]
	}
	for i := 0; i < 25; i++ {
		if strings.Count(text, fmt.Sprintf("MEM-page-%03d", i)) != 1 {
			t.Fatal("retained row omitted or duplicated", i)
		}
	}
	for _, id := range []string{private.ID, foreign.ID} {
		if strings.Contains(text, id) {
			t.Fatal("review disclosed foreign metadata")
		}
		if out, err := w.cmd.Handle(t.Context(), "/memory read "+id); err == nil || out != "" {
			t.Fatal("read disclosed foreign body", out, err)
		}
	}
	if out, err := w.cmd.Handle(t.Context(), "/memory review 4"); err != nil || !strings.Contains(out, "No unreviewed") {
		t.Fatal("empty persisted page incorrect", out, err)
	}
}

func TestPrivateMemoryReadCacheClearsAtRuntimeAttachAndWorkspaceClose(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-memory-cache-clear", false)
	r := persistedReviewRecord(t, w, "MEM-before-switch", "runtime_service")
	if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := w.cmd.Handle(t.Context(), "/memory read "+r.ID); err != nil {
		t.Fatal(err)
	}
	w.AttachRuntime(runtime, projectid.ID(runtime.ProjectIdentity()))
	if _, err := w.cmd.Handle(t.Context(), "/memory promote "+r.ID+" Reviewed before switch"); err == nil {
		t.Fatal("runtime attach retained promotion handle")
	}
	if _, err := w.cmd.Handle(t.Context(), "/memory read "+r.ID); err != nil {
		t.Fatal(err)
	}
	token := w.cmd.memoryReads.reads[r.ID]
	ctx := w.controlSource().Authority.(*runtimeControlAuthority).localControl.Context(t.Context())
	w.Close()
	if _, err := runtime.CommandPromoteReviewedMemory(ctx, token, "Reviewed before close"); err == nil {
		t.Fatal("workspace close retained opaque promotion handle")
	}
}

func TestPrivateMemoryReadChecksWholeBodyBeforePageDelivery(t *testing.T) {
	for _, hidden := range []string{"boundary-fixture-private-secret", "MARSHAL PROTOCOL"} {
		t.Run(hidden, func(t *testing.T) {
			w, _ := realControlWorkspace(t, "SESSION-memory-boundary", false)
			r := persistedReviewRecord(t, w, "MEM-source-piece", "runtime_service")
			r.Body = strings.Repeat("x", app.OperatorMemoryPageBytes-7) + hidden + " tail"
			r.ContentDigest = r.CanonicalDigest()
			if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			w.mu.Lock()
			if hidden != "MARSHAL PROTOCOL" {
				w.state.KnownSecrets = []string{hidden}
			}
			w.mu.Unlock()
			text, err := w.cmd.Handle(t.Context(), "/memory read "+r.ID)
			if err != nil || !strings.Contains(text, "withheld") || strings.Contains(text, hidden[:7]) || strings.Contains(text, "Read complete") {
				t.Fatal("whole-source boundary guard failed before first delivery", text, err)
			}
			if _, err = w.cmd.Handle(t.Context(), "/memory promote "+r.ID+" Explicit complete review"); err == nil {
				t.Fatal("withheld body granted promotion")
			}
			current, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.ID)
			if err != nil || current.Revision != r.Revision || current.Body != r.Body {
				t.Fatal("withholding changed retained source", err)
			}
		})
	}
}

func TestPrivateMemoryContractInventoryExposesOnlyCanonicalBootstrapPins(t *testing.T) {
	t.Run("bootstrap", func(t *testing.T) {
		w, _, cfg := liveMCPFixture(t)
		r := persistedReviewRecord(t, w, "MEM-not-accepted-inventory", "external")
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
		var got store.ProjectContractInventoryPage
		if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &got); err != nil {
			t.Fatal(err)
		}
		if got.Project != w.runtime.ProjectContractBinding() || got.CurrentVersion != 0 || got.CurrentContract != nil || len(got.Entries) != 0 {
			t.Fatal("bootstrap requires internal Go or candidate became accepted", got)
		}
	})
	t.Run("accepted", func(t *testing.T) {
		w, _, cfg, _, _ := nativePlanningFixture(t)
		child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
		var got store.ProjectContractInventoryPage
		if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &got); err != nil {
			t.Fatal(err)
		}
		accepted, err := w.store.ReadAcceptedProjectContract(t.Context(), w.runtime.ProjectContractBinding())
		if err != nil || got.Project != w.runtime.ProjectContractBinding() || got.CurrentVersion != accepted.Version || got.CurrentContract == nil || got.CurrentContract.ID != accepted.Contract.ID || got.CurrentContract.Revision != accepted.Contract.Revision || got.CurrentContract.Digest != accepted.Digest {
			t.Fatal("inventory omitted canonical CAS/ref", got, err)
		}
	})
}

func TestPrivateMemoryReadFinalPageCannotReplay(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-memory-page-replay", false)
	r := persistedReviewRecord(t, w, "MEM-final-page", "runtime_service")
	if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := w.cmd.Handle(t.Context(), "/memory read "+r.ID); err != nil {
		t.Fatal(err)
	}
	if text, err := w.cmd.Handle(t.Context(), "/memory read "+r.ID+" "+strconv.Itoa(len(r.Body))); err == nil {
		t.Fatal("completed cursor replayed", text)
	}
}

func TestPrivateMemoryReviewRetainedUntrustedRowsSurviveReopen(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-persistent-memory-review", false)
	for _, id := range []string{"MEM-retained-candidate", "MEM-retained-import"} {
		r := persistedReviewRecord(t, w, id, "external")
		if id == "MEM-retained-import" {
			r.Lifecycle = model.MemoryDurable
		}
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	root, identity := runtime.ProjectRoot(), runtime.ProjectIdentity()
	w.Close()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	next := NewWorkspace(reopened.Store(), reopened.ProjectID(), "SESSION-persistent-memory-reopened")
	next.AttachRuntime(reopened, projectid.ID(identity))
	t.Cleanup(next.Close)
	text, err := next.cmd.Handle(t.Context(), "/memory review")
	if err != nil || !strings.Contains(text, "MEM-retained-candidate") || !strings.Contains(text, "MEM-retained-import") {
		t.Fatal("persisted unreviewed records disappeared after reopen", text, err)
	}
}

func TestPrivateMemoryActualReadThenExplicitPromotionPreservesSource(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-memory-read-promote", false)
	record := persistedReviewRecord(t, w, "MEM-reviewed-owner-policy", "runtime_service")
	if err := w.store.WriteMemoryV2(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	text, err := w.cmd.Handle(t.Context(), "/memory read "+record.ID)
	if err != nil || !strings.Contains(text, record.Body) || !strings.Contains(text, "Read complete") {
		t.Fatal("operator never received the complete exact source body", text, err)
	}
	text, err = w.cmd.Handle(t.Context(), "/memory promote "+record.ID+" Reviewed explicit owner preservation policy")
	if err != nil || !strings.Contains(text, "Promoted") {
		t.Fatal("supported command never persisted reviewed promotion", text, err)
	}
	current, err := w.store.GetMemoryV2(t.Context(), w.projectID, record.ID)
	if err != nil || current.Lifecycle != model.MemoryDurable || current.Authority != model.AuthorityOperator || current.Revision != record.Revision+1 || current.Body != record.Body || current.Source != record.Source {
		t.Fatal("promotion changed provenance or failed canonical persistence", current, err)
	}
	if _, err = w.store.ReadAcceptedProjectContract(t.Context(), w.runtime.ProjectContractBinding()); err == nil {
		t.Fatal("candidate promotion manufactured accepted architecture")
	}
}

func TestPrivateMemoryHistoricalSessionImportIsInspectableAfterRetentionAndReopen(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-memory-historical", false)
	records, _, err := runtime.ProposeContinuation(t.Context(), importer.SessionTranscript{SessionID: "historical-import-without-canonical-session", Provider: "claude", CWD: runtime.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "Retained historical session source with explicit provenance."}}})
	if err != nil || len(records) != 1 {
		t.Fatal("actual import fixture missing", err)
	}
	rec := records[0]
	// Legacy retained session-scoped DATA differs from the ordinary retention
	// Decision below, which intentionally normalizes an import to project scope.
	historical := rec
	historical.ID = "MEM-legacy-historical-session"
	historical.Lifecycle = model.MemoryDurable
	if err = w.store.WriteMemoryV2(t.Context(), historical); err != nil {
		t.Fatal(err)
	}
	if _, err = w.cmd.Handle(t.Context(), "/memory allow "+rec.ID); err != nil {
		t.Fatal("existing explicit retention Decision unavailable", err)
	}
	root, identity := runtime.ProjectRoot(), runtime.ProjectIdentity()
	w.Close()
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	next := NewWorkspace(reopened.Store(), reopened.ProjectID(), "SESSION-memory-historical-reopened")
	next.AttachRuntime(reopened, projectid.ID(identity))
	t.Cleanup(next.Close)
	text, err := next.cmd.Handle(t.Context(), "/memory review")
	if err != nil || !strings.Contains(text, rec.ID) || !strings.Contains(text, historical.ID) || !strings.Contains(text, "Durable agent imports") {
		t.Fatal("historical retained session hidden after reopen", text, err)
	}
	text, err = next.cmd.Handle(t.Context(), "/memory read "+rec.ID)
	if err != nil || !strings.Contains(text, rec.Body) || !strings.Contains(text, "Read complete") {
		t.Fatal("historical exact source not inspectable", text, err)
	}
	if _, err = next.cmd.Handle(t.Context(), "/memory promote "+rec.ID+" Treat historical model note as accepted policy"); err == nil {
		t.Fatal("historical read bypassed imported durable-agent refusal")
	}
	text, err = next.cmd.Handle(t.Context(), "/memory read "+historical.ID)
	if err != nil || !strings.Contains(text, historical.Body) || !strings.Contains(text, "Read complete") {
		t.Fatal("legacy session-scoped source hidden", text, err)
	}
	control := next.controlSource().Authority.(*runtimeControlAuthority).localControl
	ctx := control.Context(t.Context())
	owner, _ := auth.LocalFromContext(ctx)
	if _, err = next.store.ReadProjectContractOwnerMemory(ctx, next.projectID, owner.ID(), historical.ID); err == nil {
		t.Fatal("owner review widened accepted essential reader")
	}
	current, err := next.store.GetMemoryV2(ctx, next.projectID, rec.ID)
	if err != nil || current.Body != rec.Body || current.Source != rec.Source || current.Authority != model.AuthorityAgent {
		t.Fatal("historical discovery rewrote original source", err)
	}
}
