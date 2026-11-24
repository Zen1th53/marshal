package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func limitGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// A text diff over the hand-in cap is handed in by its verified base and
// result commits, with every changed path enumerated: scope is still checked
// against the whole commit, and the review cannot cite a diff it never saw.
func TestOversizedDiffKeepsFullScopeValidation(t *testing.T) {
	wt := t.TempDir()
	limitGit(t, wt, "init", "-q")
	limitGit(t, wt, "config", "user.name", "Test Author")
	limitGit(t, wt, "config", "user.email", "author@example.invalid")
	if err := os.WriteFile(filepath.Join(wt, "README"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	limitGit(t, wt, "add", "README")
	limitGit(t, wt, "commit", "-q", "-m", "base")
	task := marshal.Task{PlanTaskID: "T1", Worker: "worker-1", BaseCommit: limitGit(t, wt, "rev-parse", "HEAD"), Files: []string{"big.txt"}}
	g := driver.Governed{Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "big.txt"), []byte(strings.Repeat("a line of text\n", (17<<20)/15)), 0o644); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "outside.txt"), []byte("out of scope\n"), 0o644)
	}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	h, err := g.Launch(ctx, driver.Request{Task: task, Worktree: wt, Brief: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	handIn, err := g.Wait(ctx, h)
	if err != nil {
		t.Fatalf("an oversized diff stopped the hand-in: %v", err)
	}
	if handIn.Diff != "" || len(handIn.Limits) != 1 {
		t.Fatalf("diff %d bytes, limits %+v", len(handIn.Diff), handIn.Limits)
	}
	limit := handIn.Limits[0]
	if limit.Kind != "git_diff" || !limit.Limited || limit.ActualBytes <= limit.CapBytes || limit.EvidenceRef != "result:"+handIn.ResultCommit ||
		!strings.Contains(limit.Action, task.BaseCommit+" "+handIn.ResultCommit+" -- <path>") || strings.Contains(limit.Action, wt) {
		t.Fatalf("limit = %+v", limit)
	}
	// Scope covers the full commit: the out-of-scope file is still found.
	if err := marshal.ValidateHandIn(task, handIn); err == nil || !strings.Contains(err.Error(), "out of scope file: outside.txt") {
		t.Fatalf("scope check on an oversized hand-in: %v", err)
	}
	task.Files = append(task.Files, "outside.txt")
	if err := marshal.ValidateHandIn(task, handIn); err != nil {
		t.Fatalf("in-scope oversized hand-in refused: %v", err)
	}
	// The review rests on the commit and its files, never on a partial diff.
	if _, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"diff"}}, handIn); err == nil {
		t.Fatal("review cited a diff that was not handed in")
	}
	if refs, err := resolveReviewReferences(marshal.Review{EvidenceRefs: []string{"result:" + handIn.ResultCommit, "file:big.txt"}}, handIn); err != nil || len(refs) != 2 {
		t.Fatalf("commit and file evidence: %v %v", refs, err)
	}
}

// A final check whose required input cannot fit pauses for a split of the
// plan, not a plain retry, and the run notice carries the same limit.
func TestIrreducibleVerifierContextSuggestsSplit(t *testing.T) {
	s := approvedNoticeRun(t)
	rec, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	run := rec.Value
	run.State = marshal.Verifying
	if err := s.save(t.Context(), "run", run, rec.Revision); err != nil {
		t.Fatal(err)
	}
	cause := irreducibleVerifierInput(t)
	limit, ok := model.LimitOf(cause)
	if !ok || limit.Kind == "" || limit.ActualBytes <= limit.CapBytes || !strings.Contains(limit.Action, "Split") {
		t.Fatalf("verifier input refusal carries no split: %v %+v", cause, limit)
	}
	if err := s.pauseVerificationFailure(t.Context(), "run", fmt.Errorf("integrated result is not verified: %w", cause)); err == nil {
		t.Fatal("pause returned no error")
	}
	stored, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	p := stored.Pause
	if stored.State != marshal.AwaitingUser || p == nil || p.Code != marshal.CodeLimitExceeded || p.Action != marshal.PauseUsePlan || p.Limit == nil || *p.Limit != limit {
		t.Fatalf("pause: %s %+v", stored.State, p)
	}
	if len(p.Resolutions) != 1 || p.Resolutions[0] != limit.Action || strings.Contains(p.Resolutions[0], marshal.VerificationRetry) {
		t.Fatalf("next step: %q", p.Resolutions)
	}
	if !strings.Contains(p.Reason, limit.Subject) || !strings.Contains(p.Reason, fmt.Sprint(limit.CapBytes)) {
		t.Fatalf("reason does not name the offending input: %q", p.Reason)
	}
	notices := runNotices(t, s)
	if len(notices) != 1 || notices[0].EventID != p.NoticeID || notices[0].Status != marshal.CodeLimitExceeded {
		t.Fatalf("notices %+v", notices)
	}
	var payload RunNotice
	if err := json.Unmarshal([]byte(notices[0].PayloadJSON), &payload); err != nil || payload.Limit == nil || *payload.Limit != limit || payload.Next != limit.Action {
		t.Fatalf("notice payload %+v %v", payload, err)
	}
}

// A pack file at its cap is read; one byte over, the pack is refused whole
// with the file named, both sizes and a decomposition.
func TestPackFileLimitAtCap(t *testing.T) {
	dir := t.TempDir()
	for _, size := range []int{marshalPackFileLimit, marshalPackFileLimit + 1} {
		if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte(strings.Repeat("x", size)), 0o644); err != nil {
			t.Fatal(err)
		}
		text, err := readPackFile(dir, "note.md")
		if size == marshalPackFileLimit {
			if err != nil || len(text) != size {
				t.Fatalf("file at cap: %d bytes, %v", len(text), err)
			}
			continue
		}
		limit, ok := model.LimitOf(err)
		if !ok || limit.Kind != "pack_file" || !strings.Contains(limit.Subject, "note.md") || limit.ActualBytes != int64(size) || limit.CapBytes != marshalPackFileLimit || !strings.Contains(limit.Action, "split") {
			t.Fatalf("file over cap: %v %+v", err, limit)
		}
	}
}

// An encoded model request at its cap passes; one byte over refuses with a
// typed provider_envelope limit, and a stage failure turns it into a targeted
// split pause and run notice rather than a plain retry.
func TestProviderEnvelopeLimitAtCap(t *testing.T) {
	m := &MarshalCLI{Provider: "codex"}
	schema := `{"type":"object","properties":{"ok":{"type":"boolean"}}}`
	baseSize, err := marshalEnvelopeSize(m.Provider, "", schema)
	if err != nil {
		t.Fatal(err)
	}
	atCap := strings.Repeat("a", marshalModelEnvelopeBytes-baseSize)
	if got, err := marshalEnvelopeSize(m.Provider, atCap, schema); err != nil || got != marshalModelEnvelopeBytes {
		t.Fatalf("at-cap fixture size = %d (want %d), err=%v", got, marshalModelEnvelopeBytes, err)
	}
	if err := m.validateRequiredMarshalModelEnvelope(atCap, schema); err != nil {
		t.Fatalf("request at exact cap refused: %v", err)
	}

	overCap := atCap + "b"
	err = m.validateRequiredMarshalModelEnvelope(overCap, schema)
	limit, ok := model.LimitOf(err)
	if !errors.Is(err, projectcontext.ErrBudget) || !ok || limit.Kind != "provider_envelope" || !limit.Limited || limit.ActualBytes != marshalModelEnvelopeBytes+1 || limit.CapBytes != marshalModelEnvelopeBytes || !strings.Contains(limit.Action, "Split") {
		t.Fatalf("request 1 byte over cap: err=%v limit=%+v", err, limit)
	}

	// Stage failure with a typed envelope limit pauses for a split and notifies once.
	s := approvedNoticeRun(t)
	if pauseErr := s.pauseStageFailure(t.Context(), "run", "review", err); !errors.Is(pauseErr, projectcontext.ErrBudget) {
		t.Fatalf("pauseStageFailure lost cause: %v", pauseErr)
	}
	stored, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	p := stored.Pause
	if stored.State != marshal.AwaitingUser || p == nil || p.Code != marshal.CodeLimitExceeded || p.Action != marshal.PauseUsePlan || p.Limit == nil || *p.Limit != limit {
		t.Fatalf("stage pause: state=%s pause=%+v", stored.State, p)
	}
	if len(p.Resolutions) != 1 || p.Resolutions[0] != limit.Action {
		t.Fatalf("stage pause resolutions = %q, want %q", p.Resolutions, limit.Action)
	}
	notices := runNotices(t, s)
	if len(notices) != 1 || notices[0].EventID != p.NoticeID || notices[0].Status != marshal.CodeLimitExceeded {
		t.Fatalf("stage pause notices = %+v", notices)
	}
	var payload RunNotice
	if err := json.Unmarshal([]byte(notices[0].PayloadJSON), &payload); err != nil || payload.Limit == nil || *payload.Limit != limit {
		t.Fatalf("notice payload = %+v err=%v", payload, err)
	}
}

// Review prompts keep optional hand-in material inline at the cap; over the
// cap they compact optional sections in order (WorkerReported, RuntimeObserved
// output, then inline Diff without partial truncation) with visible notes
// naming what was omitted and how to fetch it, while never cutting required
// task criteria, checks, commits or touched files.
func TestMarshalReviewBudgetCompactsOptionalSections(t *testing.T) {
	m := &MarshalCLI{Provider: "codex"}
	baseTask := marshal.Task{
		PlanTaskID:          "T1",
		Title:               "Keep review evidence intact",
		Instructions:        "REQUIRED-INSTRUCTION-MARKER",
		Criteria:            []string{"REQUIRED-CRITERION-MARKER"},
		Files:               []string{"pkg/a.go"},
		Checks:              []marshal.Check{{Command: "go test ./pkg", Criteria: []string{"REQUIRED-CRITERION-MARKER"}}},
		ArchitectureContext: "REQUIRED-ARCH-SLICE",
	}
	baseHandIn := marshal.HandIn{
		BaseCommit:   "1111111111111111111111111111111111111111",
		ResultCommit: "2222222222222222222222222222222222222222",
		TreeDigest:   "tree-digest",
		FilesTouched: []string{"pkg/a.go"},
		CheckResults: []marshal.CheckResult{{Command: "go test ./pkg", Criteria: baseTask.Criteria, Passed: true}},
	}
	measure := func(prompt string) int {
		structured := marshalStructuredResultPrompt(m.Provider, marshalRoleTurnMarker+prompt, marshalReviewSchema)
		n, err := marshalEnvelopeSize(m.Provider, structured, marshalReviewSchema)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Exact boundary: optional Diff fits at exactly marshalModelEnvelopeBytes.
	atCapHandIn := baseHandIn
	atCapHandIn.Diff = "d"
	baseMeasured := measure(m.marshalReviewPrompt(baseTask, atCapHandIn, marshal.ControlFree))
	atCapHandIn.Diff = strings.Repeat("d", 1+marshalModelEnvelopeBytes-baseMeasured)
	atCapPrompt := m.marshalReviewPrompt(baseTask, atCapHandIn, marshal.ControlFree)
	if got := measure(atCapPrompt); got != marshalModelEnvelopeBytes {
		t.Fatalf("at-cap review envelope = %d, want %d", got, marshalModelEnvelopeBytes)
	}
	if strings.Contains(atCapPrompt, "OMITTED OPTIONAL") {
		t.Fatal("review prompt at exact cap omitted optional material")
	}

	// 1. Oversized advisory WorkerReported is omitted first while Diff stays inline.
	h1 := baseHandIn
	h1.Diff = "INLINE-DIFF-KEPT"
	h1.WorkerReported = []marshal.CommandRecord{{Command: "worker", Output: strings.Repeat("w", marshalModelEnvelopeBytes)}}
	p1 := m.marshalReviewPrompt(baseTask, h1, marshal.ControlFree)
	if got := measure(p1); got > marshalModelEnvelopeBytes {
		t.Fatalf("review after dropping WorkerReported = %d > %d", got, marshalModelEnvelopeBytes)
	}
	for _, want := range []string{"WorkerReported", "stored hand-in artifact", "INLINE-DIFF-KEPT", "REQUIRED-CRITERION-MARKER", "REQUIRED-ARCH-SLICE"} {
		if !strings.Contains(p1, want) {
			t.Fatalf("compacted review (step 1) missing %q", want)
		}
	}

	// 2. Oversized RuntimeObserved output is compacted while keeping command/exit code and UTF-8 validity.
	h2 := baseHandIn
	h2.RuntimeObserved = []marshal.CommandRecord{{Command: "go test ./pkg", ExitCode: 0, Output: strings.Repeat("é", marshalModelEnvelopeBytes/2)}}
	p2 := m.marshalReviewPrompt(baseTask, h2, marshal.ControlFree)
	if got := measure(p2); got > marshalModelEnvelopeBytes {
		t.Fatalf("review after compacting RuntimeObserved = %d > %d", got, marshalModelEnvelopeBytes)
	}
	if !utf8.ValidString(p2) {
		t.Fatal("compacted review prompt produced invalid UTF-8")
	}
	for _, want := range []string{"RuntimeObserved", "stored hand-in artifact", "go test ./pkg", "REQUIRED-CRITERION-MARKER"} {
		if !strings.Contains(p2, want) {
			t.Fatalf("compacted review (step 2) missing %q", want)
		}
	}

	// 3. Oversized inline Diff is omitted whole (never partially truncated) with git diff pointer.
	h3 := baseHandIn
	h3.Diff = "DIFF-PREFIX-" + strings.Repeat("x", marshalModelEnvelopeBytes)
	p3 := m.marshalReviewPrompt(baseTask, h3, marshal.ControlStrict)
	if got := measure(p3); got > marshalModelEnvelopeBytes {
		t.Fatalf("review after omitting Diff = %d > %d", got, marshalModelEnvelopeBytes)
	}
	if strings.Contains(p3, "DIFF-PREFIX-") {
		t.Fatal("oversized Diff was partially truncated instead of omitted whole")
	}
	for _, want := range []string{"Omitted inline Diff", "git diff " + baseHandIn.BaseCommit + " " + baseHandIn.ResultCommit + " -- <path>", "result:" + baseHandIn.ResultCommit, "REQUIRED-INSTRUCTION-MARKER", "REQUIRED-CRITERION-MARKER"} {
		if !strings.Contains(p3, want) {
			t.Fatalf("compacted review (step 3) missing %q", want)
		}
	}

	// 4. Irreducible required task fields are never cut; envelope validation refuses with typed limit.
	bigTask := baseTask
	bigTask.Criteria = []string{strings.Repeat("c", marshalModelEnvelopeBytes)}
	p4 := m.marshalReviewPrompt(bigTask, baseHandIn, marshal.ControlFree)
	if !strings.Contains(p4, bigTask.Criteria[0]) {
		t.Fatal("required criterion was cut from review prompt")
	}
	err := m.validateRequiredMarshalModelEnvelope(marshalStructuredResultPrompt(m.Provider, marshalRoleTurnMarker+p4, marshalReviewSchema), marshalReviewSchema)
	if limit, ok := model.LimitOf(err); !ok || limit.Kind != "provider_envelope" {
		t.Fatalf("irreducible review prompt was not refused with provider_envelope limit: %v %+v", err, limit)
	}
}

// Worker briefs drop or compact optional context (memory, plan index,
// requirements, older return reasons) with visible fetch notes while keeping
// mandatory instructions, note, architecture and newest return intact; an
// irreducible required brief refuses with a typed worker_brief limit.
func TestWorkerBriefCompactsOptionalSections(t *testing.T) {
	builder := func(task marshal.Task, bc BriefContext) string {
		var b strings.Builder
		b.WriteString("TASK:" + task.PlanTaskID + "\n")
		b.WriteString("INSTRUCTIONS:" + task.Instructions + "\n")
		b.WriteString("NOTE:" + bc.Note + "\n")
		b.WriteString("ARCH:" + bc.ArchitectureContext + "\n")
		for _, r := range bc.Returned {
			b.WriteString("RETURN:" + r + "\n")
		}
		if bc.Requirements != "" {
			b.WriteString("REQ:" + bc.Requirements + "\n")
		}
		if bc.Index != "" {
			b.WriteString("INDEX:" + bc.Index + "\n")
		}
		if bc.MemoryContext != "" {
			b.WriteString("MEM:" + bc.MemoryContext + "\n")
		}
		for _, rec := range bc.Memory {
			b.WriteString("MEMREC:" + rec.Body + "\n")
		}
		return b.String()
	}
	task := marshal.Task{PlanTaskID: "T2", Instructions: "MANDATORY-INSTRUCTIONS"}
	bc := BriefContext{
		Note:                "MANDATORY-NOTE",
		ArchitectureContext: "MANDATORY-ARCH",
		Returned:            []string{strings.Repeat("o", 65<<10), strings.Repeat("p", 65<<10), "NEWEST-RETURN-REASON"},
		Requirements:        strings.Repeat("r", 10<<10),
		Index:               strings.Repeat("i", 10<<10),
		MemoryContext:       strings.Repeat("m", 10<<10),
		Memory:              []model.MemoryRecordV2{{Body: strings.Repeat("k", 10<<10)}},
	}
	got, err := fitWorkerBrief(task, bc, builder, marshalWorkerBriefBytes)
	if err != nil {
		t.Fatalf("brief with optional bloat refused: %v", err)
	}
	if len(got) > marshalWorkerBriefBytes {
		t.Fatalf("compacted worker brief = %d > %d", len(got), marshalWorkerBriefBytes)
	}
	for _, want := range []string{"MANDATORY-INSTRUCTIONS", "MANDATORY-NOTE", "MANDATORY-ARCH", "NEWEST-RETURN-REASON", "memory_recall", "plan pack index", "plan pack requirements", "older return reasons omitted", "durable review history"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compacted worker brief missing %q", want)
		}
	}

	// Exact boundary vs +1 byte for irreducible required instructions.
	minBC := BriefContext{Note: "N"}
	baseLen := len(builder(marshal.Task{PlanTaskID: "T2"}, minBC))
	atCapTask := marshal.Task{PlanTaskID: "T2", Instructions: strings.Repeat("x", marshalWorkerBriefBytes-baseLen)}
	atCapBrief, err := fitWorkerBrief(atCapTask, minBC, builder, marshalWorkerBriefBytes)
	if err != nil || len(atCapBrief) != marshalWorkerBriefBytes {
		t.Fatalf("worker brief at exact cap: len=%d err=%v", len(atCapBrief), err)
	}
	overTask := atCapTask
	overTask.Instructions += "y"
	_, err = fitWorkerBrief(overTask, minBC, builder, marshalWorkerBriefBytes)
	if limit, ok := model.LimitOf(err); !errors.Is(err, projectcontext.ErrBudget) || !ok || limit.Kind != "worker_brief" || limit.ActualBytes != marshalWorkerBriefBytes+1 || limit.CapBytes != marshalWorkerBriefBytes {
		t.Fatalf("irreducible worker brief 1 byte over cap: err=%v limit=%+v", err, limit)
	}

	// Optional worker knowledge over cap moves to MCP recall note without overflowing.
	baseBrief := strings.Repeat("b", marshalWorkerBriefBytes-len(marshalKnowledgeRecallNote)-1)
	withRecall := appendOptionalWorkerKnowledge(baseBrief, strings.Repeat("k", 4096), marshalWorkerBriefBytes)
	if len(withRecall) > marshalWorkerBriefBytes || !strings.Contains(withRecall, marshalKnowledgeRecallNote) {
		t.Fatalf("oversized worker knowledge not moved to recall note: len=%d", len(withRecall))
	}

	// Governed brief note at exact cap passes; 1 byte over refuses with typed worker_brief limit.
	headerLen := len(governedNoteHeader("T2")) + 1
	exactNote := strings.Repeat("n", governedBriefBytes-headerLen)
	if b, err := renderGovernedBrief("T2", exactNote, nil); err != nil || len(b) != governedBriefBytes {
		t.Fatalf("governed note at exact cap: len=%d err=%v", len(b), err)
	}
	if _, err := renderGovernedBrief("T2", exactNote+"x", nil); !errors.Is(err, errGovernedBriefTooLarge) {
		t.Fatalf("governed note over cap: %v", err)
	} else if limit, ok := model.LimitOf(err); !ok || limit.Kind != "worker_brief" || limit.ActualBytes != governedBriefBytes+1 || limit.CapBytes != governedBriefBytes {
		t.Fatalf("governed note over cap limit = %+v (ok=%v)", limit, ok)
	}
	// Governed brief with a single return reason larger than remaining space still keeps a valid-UTF-8 pointer instead of dropping silently.
	longReturn := strings.Repeat("é", 2000)
	shortNote := strings.Repeat("n", governedBriefBytes-headerLen-300)
	gb, err := renderGovernedBrief("T2", shortNote, []string{"old-reason", longReturn})
	if err != nil || len(gb) > governedBriefBytes || !utf8.ValidString(gb) || !strings.Contains(gb, "durable review history") {
		t.Fatalf("governed brief tight return: len=%d valid=%v err=%v text=%q", len(gb), utf8.ValidString(gb), err, gb[len(shortNote):])
	}
}

// Observed project knowledge stays inline up to the 16 KiB reserve boundary;
// one byte past the reserve it moves to scoped MCP recall while keeping the
// required prompt intact.
func TestMarshalKnowledgeReserveAtCap(t *testing.T) {
	schema := `{"type":"object","properties":{"ok":{"type":"boolean"}}}`
	basePrompt := "Return ok."
	m := &MarshalCLI{Provider: "codex"}
	base := marshalRoleTurnMarker + basePrompt + "\n"
	probeStructured := marshalStructuredResultPrompt(m.Provider, base, schema)
	baseWire, err := marshalEnvelopeSize(m.Provider, probeStructured, schema)
	if err != nil {
		t.Fatal(err)
	}
	maxKnowledge := marshalModelEnvelopeBytes - marshalKnowledgePacketReserve - baseWire
	if maxKnowledge <= 0 {
		t.Fatalf("unexpected non-positive maxKnowledge: %d", maxKnowledge)
	}
	atReserve := strings.Repeat("k", maxKnowledge)
	if !m.marshalKnowledgeFits(base+atReserve, schema) {
		t.Fatal("knowledge exactly at the 16 KiB reserve boundary did not fit")
	}
	if m.marshalKnowledgeFits(base+atReserve+"z", schema) {
		t.Fatal("knowledge 1 byte past the 16 KiB reserve boundary still fit inline")
	}

	// Real turn boundary: at reserve stays inline, +1 byte moves to MCP recall.
	for _, tc := range []struct {
		name      string
		knowledge string
		inline    bool
	}{
		{name: "at-reserve", knowledge: "OBSERVED-KNOWLEDGE-" + strings.Repeat("k", maxKnowledge-len("OBSERVED-KNOWLEDGE-")), inline: true},
		{name: "over-reserve", knowledge: "OBSERVED-KNOWLEDGE-" + strings.Repeat("k", maxKnowledge-len("OBSERVED-KNOWLEDGE-")+1), inline: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outside := t.TempDir()
			capture := filepath.Join(outside, "prompt")
			binary := filepath.Join(outside, "codex-stub")
			script := fmt.Sprintf("#!/bin/sh\ncat > %q\nprintf '%%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"s1\"}'\nprintf '%%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{\\\"ok\\\":true}\"}}'\n", capture)
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			cli := &MarshalCLI{
				Provider: "codex",
				Binary:   binary,
				ProjectContext: func(context.Context, string, string) (string, error) {
					return tc.knowledge, nil
				},
			}
			ctx := requiredMarshalTestContext(t, cli)
			var out struct {
				OK bool `json:"ok"`
			}
			if err := cli.turn(ctx, basePrompt, schema, &out); err != nil || !out.OK {
				t.Fatalf("turn failed: %v", err)
			}
			promptBytes, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			gotPrompt := string(promptBytes)
			if !strings.Contains(gotPrompt, basePrompt) {
				t.Fatal("required prompt missing")
			}
			if strings.Contains(gotPrompt, "OBSERVED-KNOWLEDGE-") != tc.inline {
				t.Fatalf("inline=%v, want %v", !tc.inline, tc.inline)
			}
			if strings.Contains(gotPrompt, marshalKnowledgeRecallNote) == tc.inline {
				t.Fatalf("recall note presence mismatch for inline=%v", tc.inline)
			}
		})
	}
}

// Amendment packets drop optional plan summary titles with a visible note
// before refusing; a minimal packet at exact cap passes, and one byte over
// refuses with a typed amendment limit naming the offending field while
// leaving the approved plan untouched.
func TestAmendmentPacketLimitAtCap(t *testing.T) {
	// 1. Optional plan summary titles drop first with a visible notice.
	summaryRun := marshal.Run{
		PlanID:      "PLAN-amend",
		PlanVersion: 2,
		Tasks: []marshal.Task{
			{PlanTaskID: "t1", Title: strings.Repeat("a", 5000), Criteria: []string{"keep criterion"}, Files: []string{"a.go"}},
			{PlanTaskID: "t2", Title: strings.Repeat("b", 5000)},
			{PlanTaskID: "t3", Title: strings.Repeat("c", 5000)},
		},
	}
	data, target, err := marshalAmendPacket(summaryRun, "t1 add b.go to scope")
	if err != nil || target != 0 || len(data) > marshalAmendPacketBytes {
		t.Fatalf("amendment with optional title bloat: target=%d len=%d err=%v", target, len(data), err)
	}
	var decoded struct {
		Target  marshalProposedTask `json:"target_task"`
		Omitted string              `json:"omitted"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Omitted == "" || !strings.Contains(decoded.Omitted, "plan_summary") || !strings.Contains(decoded.Omitted, "PLAN-amend v2") {
		t.Fatalf("amendment omission note missing or incomplete: %q", decoded.Omitted)
	}
	if len(decoded.Target.Criteria) != 1 || decoded.Target.Criteria[0] != "keep criterion" {
		t.Fatalf("required target criteria altered: %+v", decoded.Target.Criteria)
	}

	// 2. Exact cap passes; 1 byte over refuses with typed amendment limit and leaves run intact.
	baseRun := marshal.Run{
		PlanID:      "PLAN-cap",
		PlanVersion: 1,
		Tasks:       []marshal.Task{{PlanTaskID: "t1", Criteria: []string{"x"}}},
	}
	baseData, _, err := marshalAmendPacket(baseRun, "t1 update")
	if err != nil {
		t.Fatal(err)
	}
	pad := marshalAmendPacketBytes - len(baseData)
	atCapRun := marshal.Run{
		PlanID:      "PLAN-cap",
		PlanVersion: 1,
		Tasks:       []marshal.Task{{PlanTaskID: "t1", Criteria: []string{"x" + strings.Repeat("c", pad)}}},
	}
	atData, _, err := marshalAmendPacket(atCapRun, "t1 update")
	if err != nil || len(atData) != marshalAmendPacketBytes {
		t.Fatalf("amendment at exact cap: len=%d (want %d), err=%v", len(atData), marshalAmendPacketBytes, err)
	}

	overRun := marshal.Run{
		PlanID:      "PLAN-cap",
		PlanVersion: 1,
		Tasks:       []marshal.Task{{PlanTaskID: "t1", Criteria: []string{"x" + strings.Repeat("c", pad+1)}}},
	}
	_, _, err = marshalAmendPacket(overRun, "t1 update")
	limit, ok := model.LimitOf(err)
	if !errors.Is(err, projectcontext.ErrBudget) || !ok || limit.Kind != "amendment" || !limit.Limited ||
		limit.ActualBytes != marshalAmendPacketBytes+1 || limit.CapBytes != marshalAmendPacketBytes ||
		!strings.Contains(limit.Subject, "target task criteria") || !strings.Contains(limit.Subject, "t1") ||
		!strings.Contains(limit.Action, "current plan stays as approved") {
		t.Fatalf("amendment 1 byte over cap: err=%v limit=%+v", err, limit)
	}
}
