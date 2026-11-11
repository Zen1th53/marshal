package tui

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/doctor"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestLaunchScanRunsPartsInParallel(t *testing.T) {
	orig := launchScanParts
	defer func() { launchScanParts = orig }()

	sleepDur := 200 * time.Millisecond
	launchScanParts.git = func(ctx context.Context, root string) (scanGit, error) {
		time.Sleep(sleepDur)
		return scanGit{Branch: "main", Changed: 1}, nil
	}
	launchScanParts.files = func(ctx context.Context, root string) (scanFiles, error) {
		time.Sleep(sleepDur)
		return scanFiles{Language: "Go", Files: 10}, nil
	}
	launchScanParts.worktrees = func(ctx context.Context, root string) (int, error) {
		time.Sleep(sleepDur)
		return 1, nil
	}
	launchScanParts.unmerged = func(ctx context.Context, root string) ([]string, error) {
		time.Sleep(sleepDur)
		return []string{"feat"}, nil
	}
	launchScanParts.run = func(ctx context.Context, w *Workspace, root string) (*scanRun, error) {
		time.Sleep(sleepDur)
		return nil, nil
	}
	launchScanParts.runs = func(ctx context.Context, root string) (int, error) {
		time.Sleep(sleepDur)
		return 1, nil
	}
	launchScanParts.limits = func(ctx context.Context, w *Workspace) ([]scanLimit, error) {
		time.Sleep(sleepDur)
		return []scanLimit{{Provider: "codex", LeftPercent: 50, Known: true}}, nil
	}
	launchScanParts.knowledge = func(ctx context.Context, w *Workspace, root string) (string, error) {
		time.Sleep(sleepDur)
		return "pref", nil
	}

	w := &Workspace{}
	start := time.Now()
	scan := w.runLaunchScan(t.Context(), "/test/root")
	elapsed := time.Since(start)

	if elapsed >= 600*time.Millisecond {
		t.Fatalf("scan took %v, want < 600ms", elapsed)
	}
	if scan.Branch != "main" || scan.Language != "Go" || scan.Knowledge != "pref" {
		t.Fatalf("scan results mismatch: %+v", scan)
	}
}

func TestLaunchScanTimeoutIsUnknown(t *testing.T) {
	origParts := launchScanParts
	origTimeout := launchScanPartTimeout
	origDoctor := launchDoctor
	defer func() {
		launchScanParts = origParts
		launchScanPartTimeout = origTimeout
		launchDoctor = origDoctor
	}()

	launchDoctor = func(context.Context, string, doctor.Options) doctor.Report {
		return passReport()
	}
	launchScanPartTimeout = 50 * time.Millisecond

	// Hang git, files, worktrees, unmerged, run, runs, limits
	hang := func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}

	launchScanParts.git = func(ctx context.Context, root string) (scanGit, error) {
		hang(ctx)
		return scanGit{}, ctx.Err()
	}
	launchScanParts.files = func(ctx context.Context, root string) (scanFiles, error) {
		hang(ctx)
		return scanFiles{}, ctx.Err()
	}
	launchScanParts.worktrees = func(ctx context.Context, root string) (int, error) {
		hang(ctx)
		return 0, ctx.Err()
	}
	launchScanParts.unmerged = func(ctx context.Context, root string) ([]string, error) {
		hang(ctx)
		return nil, ctx.Err()
	}
	launchScanParts.run = func(ctx context.Context, w *Workspace, root string) (*scanRun, error) {
		hang(ctx)
		return nil, ctx.Err()
	}
	launchScanParts.runs = func(ctx context.Context, root string) (int, error) {
		hang(ctx)
		return 0, ctx.Err()
	}
	launchScanParts.limits = func(ctx context.Context, w *Workspace) ([]scanLimit, error) {
		hang(ctx)
		return nil, ctx.Err()
	}
	launchScanParts.knowledge = func(ctx context.Context, w *Workspace, root string) (string, error) {
		hang(ctx)
		return "", ctx.Err()
	}

	root := newLaunchRepo(t)
	w := &Workspace{workDir: root}
	w.runLaunchCheck(t.Context())

	if w.launchBlocked() {
		t.Fatalf("launch is blocked, want ready: blockers=%v", w.launchBlockers())
	}
	w.launch.mu.Lock()
	phase := w.launch.phase
	scan := w.launch.scan
	w.launch.mu.Unlock()

	if phase != launchReady {
		t.Fatalf("launch phase %d, want ready", phase)
	}

	block := scan.briefingBlock(nil)
	for _, want := range []string{
		"- project: unknown",
		"branch: unknown",
		"uncommitted changes: unknown",
		"open worktrees: unknown",
		"- unmerged branches: unknown",
		"- active run: unknown",
		"- earlier runs: unknown",
		"- limits: unknown",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
}

func TestProjectScanBlockFormat(t *testing.T) {
	scan := projectScan{
		Language:    "Go",
		Files:       412,
		Branch:      "main",
		Changed:     3,
		Worktrees:   2,
		Unmerged:    []string{"feature/login", "fix/typo"},
		ActiveRun:   nil,
		EarlierRuns: 2,
		Sessions: []scanSessions{
			{Provider: "claude", Loaded: 14, Found: 14},
			{Provider: "codex", Loaded: 0, Found: 0},
		},
		Limits: []scanLimit{
			{Provider: "codex", LeftPercent: 42, Known: true},
			{Provider: "claude", LeftPercent: 94, Known: true},
			{Provider: "agy", Known: false},
			{Provider: "opencode", Known: false},
		},
	}

	want := `PROJECT SCAN (data, read only, taken at launch):
- doctor: passed
- project: Go, 412 files
- branch: main; uncommitted changes: 3 files; open worktrees: 2
- unmerged branches: feature/login, fix/typo
- active run: none
- earlier runs: 2 (requirements under .marshal/marshal/runs/*/plan/)
- earlier sessions: claude 14 loaded, codex 0
- limits: codex 42% · claude 94% · agy unavailable · opencode unavailable`

	got := scan.briefingBlock(nil)
	if got != want {
		t.Fatalf("briefingBlock mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestProjectScanBlockNoIDs(t *testing.T) {
	scan := projectScan{
		Language:  "Go",
		Files:     10,
		Branch:    "main",
		Changed:   0,
		Worktrees: 0,
		ActiveRun: &scanRun{
			Title: "Refactor database queries",
			Done:  2,
			Total: 5,
			State: marshal.Dispatching,
		},
	}
	block := scan.briefingBlock(nil)
	if strings.Contains(block, "RUN-") {
		t.Fatalf("block contains RUN-: %s", block)
	}
	if strings.Contains(block, "PLAN-") {
		t.Fatalf("block contains PLAN-: %s", block)
	}
	hexMatch := regexp.MustCompile(`\b[0-9a-fA-F]{7,}\b`)
	if hexMatch.MatchString(block) {
		t.Fatalf("block contains hex run ID: %s", block)
	}
	wantLine := `- active run: "Refactor database queries", 2 of 5 tasks done, state dispatching`
	if !strings.Contains(block, wantLine) {
		t.Fatalf("block missing active run line:\n%s", block)
	}
}

func TestContinuityBriefingCarriesScanOnce(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{workDir: root}
	w.launch.scan = projectScan{
		Language:      "Go",
		Files:         100,
		KnowledgeRoot: root,
	}

	// 1. Root briefing has PROJECT SCAN exactly once
	brief := w.marshalContinuityBriefing(root, "Marshal protocol.")
	count := strings.Count(brief, "PROJECT SCAN (data, read only, taken at launch):")
	if count != 1 {
		t.Fatalf("expected PROJECT SCAN exactly once, got %d in:\n%s", count, brief)
	}

	// Verify before PROJECT KNOWLEDGE
	scanIdx := strings.Index(brief, "PROJECT SCAN")
	knowIdx := strings.Index(brief, "PROJECT KNOWLEDGE")
	if knowIdx != -1 && scanIdx > knowIdx {
		t.Fatalf("PROJECT SCAN must appear before PROJECT KNOWLEDGE (scan=%d, know=%d)", scanIdx, knowIdx)
	}

	// 2. Already-briefed string does not duplicate
	briefAgain := w.marshalContinuityBriefing(root, brief)
	countAgain := strings.Count(briefAgain, "PROJECT SCAN (data, read only, taken at launch):")
	if countAgain != 1 {
		t.Fatalf("expected PROJECT SCAN still exactly once after re-brief, got %d", countAgain)
	}

	// 3. Worktree briefing does not carry PROJECT SCAN
	wtRoot := filepath.Join(root, ".marshal", "worktrees", "wt-1")
	wtBrief := w.marshalContinuityBriefing(wtRoot, "Worker protocol.")
	if strings.Contains(wtBrief, "PROJECT SCAN (data, read only, taken at launch):") {
		t.Fatalf("worktree briefing must not carry PROJECT SCAN:\n%s", wtBrief)
	}
}

func TestProjectKnowledgePrefetchReused(t *testing.T) {
	ws, rt := realControlWorkspace(t, "knowledge-prefetch", false)
	root := rt.ProjectRoot()

	calls := 0
	origFetch := projectKnowledgeFetch
	projectKnowledgeFetch = func(ctx context.Context, r *app.Runtime, root string) (string, error) {
		calls++
		return origFetch(ctx, r, root)
	}
	defer func() { projectKnowledgeFetch = origFetch }()

	// Prefetch knowledge via launch scan
	ws.runLaunchScan(t.Context(), root)
	if calls != 1 {
		t.Fatalf("expected 1 fetch during launch scan, got %d", calls)
	}

	// Chat start briefing calls marshalContinuityBriefing
	brief := ws.marshalContinuityBriefing(root, "Marshal protocol.")
	if calls != 1 {
		t.Fatalf("expected no additional fetch during briefing, got %d calls", calls)
	}
	if !strings.Contains(brief, "PROJECT SCAN (data, read only, taken at launch):") {
		t.Fatalf("expected briefing to contain PROJECT SCAN, got:\n%s", brief)
	}
}
