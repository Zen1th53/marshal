package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

	// Every part hangs past its timeout. Half of them watch ctx, half ignore
	// it entirely and only return long after, so the timeout itself, not the
	// part's cooperation, must make the line unknown.
	ignore := map[string]bool{"git": true, "worktrees": true, "run": true, "limits": true}
	hangFor := func(name string) func(ctx context.Context) {
		return func(ctx context.Context) {
			if ignore[name] {
				time.Sleep(2 * time.Second)
				return
			}
			<-ctx.Done()
		}
	}

	launchScanParts.git = func(ctx context.Context, root string) (scanGit, error) {
		hangFor("git")(ctx)
		return scanGit{Branch: "late"}, nil
	}
	launchScanParts.files = func(ctx context.Context, root string) (scanFiles, error) {
		hangFor("files")(ctx)
		return scanFiles{}, ctx.Err()
	}
	launchScanParts.worktrees = func(ctx context.Context, root string) (int, error) {
		hangFor("worktrees")(ctx)
		return 0, ctx.Err()
	}
	launchScanParts.unmerged = func(ctx context.Context, root string) ([]string, error) {
		hangFor("unmerged")(ctx)
		return nil, ctx.Err()
	}
	launchScanParts.run = func(ctx context.Context, w *Workspace, root string) (*scanRun, error) {
		hangFor("run")(ctx)
		return nil, ctx.Err()
	}
	launchScanParts.runs = func(ctx context.Context, root string) (int, error) {
		hangFor("runs")(ctx)
		return 0, ctx.Err()
	}
	launchScanParts.limits = func(ctx context.Context, w *Workspace) ([]scanLimit, error) {
		hangFor("limits")(ctx)
		return nil, ctx.Err()
	}
	launchScanParts.knowledge = func(ctx context.Context, w *Workspace, root string) (string, error) {
		hangFor("knowledge")(ctx)
		return "", ctx.Err()
	}

	root := newLaunchRepo(t)
	w := &Workspace{workDir: root}
	start := time.Now()
	w.runLaunchCheck(t.Context())
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("launch check took %v with hung parts, want the part timeout to bound it", elapsed)
	}

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

	want := `PROJECT SCAN (data, read only, taken at launch):
- doctor: passed
- project: unknown
- branch: unknown; uncommitted changes: unknown; open worktrees: unknown
- unmerged branches: unknown
- active run: unknown
- earlier runs: unknown
- limits: unknown`
	if got := scan.briefingBlock(nil); got != want {
		t.Fatalf("timed-out scan block:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if scan.Knowledge != "" || !errors.Is(scan.KnowledgeErr, context.DeadlineExceeded) {
		t.Fatalf("timed-out knowledge = %q, %v; want empty and deadline exceeded", scan.Knowledge, scan.KnowledgeErr)
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
	const knowledge = "PROJECT KNOWLEDGE (prefetched at launch)"
	origFetch := projectKnowledgeFetch
	projectKnowledgeFetch = func(ctx context.Context, r *app.Runtime, root string) (string, error) {
		calls++
		return knowledge, nil
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
	if n := strings.Count(brief, "PROJECT SCAN (data, read only, taken at launch):"); n != 1 {
		t.Fatalf("expected PROJECT SCAN exactly once, got %d in:\n%s", n, brief)
	}
	if !strings.HasSuffix(brief, "\n"+knowledge) {
		t.Fatalf("expected the prefetched knowledge at the end of the briefing, got:\n%s", brief)
	}
	if strings.Index(brief, "PROJECT SCAN") > strings.Index(brief, knowledge) {
		t.Fatalf("PROJECT SCAN must come before the knowledge:\n%s", brief)
	}
}

// TestProjectKnowledgeTimedOutPrefetchRefetches: a prefetch that ran out of
// its 5 s part budget is not reused; the chat start fetches again.
func TestProjectKnowledgeTimedOutPrefetchRefetches(t *testing.T) {
	ws, rt := realControlWorkspace(t, "knowledge-prefetch-timeout", false)
	root := rt.ProjectRoot()
	calls := 0
	origFetch := projectKnowledgeFetch
	projectKnowledgeFetch = func(ctx context.Context, r *app.Runtime, root string) (string, error) {
		calls++
		return "fresh knowledge", nil
	}
	defer func() { projectKnowledgeFetch = origFetch }()
	ws.launch.scan = projectScan{KnowledgeRoot: root, KnowledgeErr: context.DeadlineExceeded}

	brief := ws.marshalContinuityBriefing(root, "Marshal protocol.")
	if calls != 1 || !strings.HasSuffix(brief, "\nfresh knowledge") {
		t.Fatalf("calls=%d brief:\n%s", calls, brief)
	}
}

// TestLaunchScanDefaultPartsReadOnly runs the real Git parts on a repository
// and checks the values, that the index is left byte-for-byte unchanged, and
// that a folder without Git gives errors (unknown), never a guess.
func TestLaunchScanDefaultPartsReadOnly(t *testing.T) {
	root := newLaunchRepo(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	// More docs and configuration than code: the code still decides.
	for _, f := range []string{"main.go", "util.go", "x.py", "README.md", "a.yaml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-q", "-m", "files")
	for _, b := range []string{"feature/login", "marshal/RUN-1/task", "marshal-extreme/RUN-2/task"} {
		git("checkout", "-q", "-b", b)
		if err := os.WriteFile(filepath.Join(root, strings.ReplaceAll(b, "/", "_")), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", ".")
		git("commit", "-q", "-m", b)
		git("checkout", "-q", "main")
	}
	git("worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"), "-b", "side")
	// Modify a tracked file without changing its size, then bump its mtime,
	// so a status that refreshes the index would rewrite it.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root, "main.go"), future, future); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	g, err := defaultScanGit(ctx, root)
	if err != nil || g.Branch != "main" || g.Changed != 1 {
		t.Fatalf("git part = %+v, %v; want main, 1 changed", g, err)
	}
	f, err := defaultScanFiles(ctx, root)
	if err != nil || f.Language != "Go" || f.Files != 7 {
		t.Fatalf("files part = %+v, %v; want Go, 7 files", f, err)
	}
	wt, err := defaultScanWorktrees(ctx, root)
	if err != nil || wt != 1 {
		t.Fatalf("worktrees part = %d, %v; want 1", wt, err)
	}
	unmerged, err := defaultScanUnmerged(ctx, root)
	if err != nil || strings.Join(unmerged, ",") != "feature/login" {
		t.Fatalf("unmerged part = %v, %v; want only feature/login", unmerged, err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the scan rewrote .git/index; it must be read only")
	}

	plain := t.TempDir()
	if _, err := defaultScanGit(ctx, plain); err == nil {
		t.Fatal("git part on a folder without Git must fail (unknown)")
	}
	if _, err := defaultScanFiles(ctx, plain); err == nil {
		t.Fatal("files part on a folder without Git must fail (unknown)")
	}
}

func TestProjectScanActiveRunTitleBounded(t *testing.T) {
	run := marshal.Run{GoalBinding: strings.Repeat("word ", 100) + "\nsecond line", State: marshal.Dispatching}
	want := strings.TrimSpace(strings.Repeat("word ", 16)) + "…"
	if got := extractPlanTitle(run); got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if got := extractPlanTitle(marshal.Run{GoalBinding: "# Add login\nmore"}); got != "Add login" {
		t.Fatalf("short title = %q, want %q", got, "Add login")
	}
}

func TestProjectScanDominantLanguage(t *testing.T) {
	for _, tc := range []struct {
		counts map[string]int
		want   string
	}{
		{map[string]int{}, "mixed"},
		{map[string]int{"Go": 3}, "Go"},
		{map[string]int{"Go": 3, "Python": 5}, "Python"},
		{map[string]int{"Python": 2, "Go": 2, "Rust": 1}, "Go"},
	} {
		for i := 0; i < 20; i++ { // map order varies; the answer must not
			if got := dominantLanguage(tc.counts); got != tc.want {
				t.Fatalf("dominantLanguage(%v) = %q, want %q", tc.counts, got, tc.want)
			}
		}
	}
}
