package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

type projectScan struct {
	Language      string
	Files         int
	Branch        string
	Changed       int
	Worktrees     int
	Unmerged      []string
	ActiveRun     *scanRun
	EarlierRuns   int
	Sessions      []scanSessions
	Limits        []scanLimit
	Knowledge     string
	KnowledgeErr  error
	KnowledgeRoot string
}

type scanRun struct {
	Title string
	Done  int
	Total int
	State marshal.RunState
}

type scanSessions struct {
	Provider string
	Loaded   int
	Found    int
}

type scanLimit struct {
	Provider    string
	LeftPercent int
	Known       bool
}

type scanGit struct {
	Branch  string
	Changed int
}

type scanFiles struct {
	Language string
	Files    int
}

var languageByExt = map[string]string{
	".go":    "Go",
	".ts":    "TypeScript",
	".tsx":   "TypeScript",
	".js":    "JavaScript",
	".jsx":   "JavaScript",
	".py":    "Python",
	".rs":    "Rust",
	".java":  "Java",
	".kt":    "Kotlin",
	".rb":    "Ruby",
	".php":   "PHP",
	".cs":    "C#",
	".c":     "C",
	".h":     "C",
	".cc":    "C++",
	".cpp":   "C++",
	".hpp":   "C++",
	".swift": "Swift",
}

var launchScanPartTimeout = 5 * time.Second

// scanTitleMaxRunes bounds the active run's title in the briefing; the title
// is the first line of the owner's request and can be a whole paragraph.
const scanTitleMaxRunes = 80

var launchScanParts = struct {
	git       func(ctx context.Context, root string) (scanGit, error)
	files     func(ctx context.Context, root string) (scanFiles, error)
	worktrees func(ctx context.Context, root string) (int, error)
	unmerged  func(ctx context.Context, root string) ([]string, error)
	run       func(ctx context.Context, w *Workspace, root string) (*scanRun, error)
	runs      func(ctx context.Context, root string) (int, error)
	limits    func(ctx context.Context, w *Workspace) ([]scanLimit, error)
	knowledge func(ctx context.Context, w *Workspace, root string) (string, error)
}{
	git:       defaultScanGit,
	files:     defaultScanFiles,
	worktrees: defaultScanWorktrees,
	unmerged:  defaultScanUnmerged,
	run:       defaultScanRun,
	runs:      defaultScanRuns,
	limits:    defaultScanLimits,
	knowledge: defaultScanKnowledge,
}

// scanGitOutput runs one read-only Git query for the launch scan. Optional
// locks are off, so git status never refreshes or rewrites the index.
func scanGitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd, err := hostgit.Command(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Output()
}

func defaultScanGit(ctx context.Context, root string) (scanGit, error) {
	outBranch, err := scanGitOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return scanGit{}, err
	}
	branch := strings.TrimSpace(string(outBranch))

	outStatus, err := scanGitOutput(ctx, root, "status", "--porcelain")
	if err != nil {
		return scanGit{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(outStatus)), "\n")
	changed := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			changed++
		}
	}
	return scanGit{Branch: branch, Changed: changed}, nil
}

func defaultScanFiles(ctx context.Context, root string) (scanFiles, error) {
	out, err := scanGitOutput(ctx, root, "ls-files", "-z")
	if err != nil {
		return scanFiles{}, err
	}
	counts := make(map[string]int)
	total := 0
	for _, line := range strings.Split(string(out), "\x00") {
		if line == "" {
			continue
		}
		total++
		if lang, ok := languageByExt[strings.ToLower(filepath.Ext(line))]; ok {
			counts[lang]++
		}
	}
	return scanFiles{Language: dominantLanguage(counts), Files: total}, nil
}

// dominantLanguage is the mapped language with the most files, ties broken
// by name so the result is stable. Files with other extensions (docs,
// configuration) do not outvote the code; with no mapped file it is mixed.
func dominantLanguage(counts map[string]int) string {
	best, bestCount := "mixed", 0
	for lang, count := range counts {
		if count > bestCount || (count == bestCount && lang < best) {
			best, bestCount = lang, count
		}
	}
	return best
}

func defaultScanWorktrees(ctx context.Context, root string) (int, error) {
	out, err := scanGitOutput(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(out), "\n")
	worktrees := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "worktree ") {
			worktrees++
		}
	}
	return max(0, worktrees-1), nil
}

func defaultScanUnmerged(ctx context.Context, root string) ([]string, error) {
	out, err := scanGitOutput(ctx, root, "for-each-ref", "--format=%(refname:short)", "refs/heads", "--no-merged", "HEAD")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var unmerged []string
	for _, l := range lines {
		b := strings.TrimSpace(l)
		if b == "" || isMarshalBranch(b) {
			continue
		}
		unmerged = append(unmerged, b)
	}
	return unmerged, nil
}

func isMarshalBranch(name string) bool {
	if strings.HasPrefix(name, "marshal/") ||
		strings.HasPrefix(name, "marshal-extreme/") ||
		strings.HasPrefix(name, "limited-") {
		return true
	}
	if statepath.RefName != "" && strings.HasPrefix(name, statepath.RefName+"/") {
		return true
	}
	if statepath.LegacyRefName != "" && strings.HasPrefix(name, statepath.LegacyRefName+"/") {
		return true
	}
	return false
}

func defaultScanRun(ctx context.Context, w *Workspace, root string) (*scanRun, error) {
	if w == nil {
		return nil, nil
	}
	st := w.store
	if st == nil && w.runtime != nil {
		st = w.runtime.Store()
	}
	if st == nil {
		return nil, nil
	}
	projectID := w.projectID
	if projectID == "" && w.runtime != nil {
		projectID = w.runtime.ProjectID()
	}
	if projectID == "" {
		return nil, nil
	}

	_, record, err := st.LatestMarshalRun(ctx, projectID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	r := record.Value
	if r.State == "" || r.State == marshal.Closed {
		return nil, nil
	}

	total := len(r.Tasks)
	done := 0
	for _, t := range r.Tasks {
		if t.State == marshal.Merged {
			done++
		}
	}

	return &scanRun{
		Title: extractPlanTitle(r),
		Done:  done,
		Total: total,
		State: r.State,
	}, nil
}

// extractPlanTitle is the run's title for the briefing, at most
// scanTitleMaxRunes long.
func extractPlanTitle(r marshal.Run) string {
	title := planTitleLine(r)
	if runes := []rune(title); len(runes) > scanTitleMaxRunes {
		title = strings.TrimSpace(string(runes[:scanTitleMaxRunes])) + "…"
	}
	return title
}

func planTitleLine(r marshal.Run) string {
	if r.GoalBinding != "" {
		line := strings.TrimSpace(strings.Split(r.GoalBinding, "\n")[0])
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		line = strings.Trim(line, `"'`)
		if line != "" {
			return line
		}
	}
	if r.Pack != nil && r.Pack.Requirements != "" {
		for _, l := range strings.Split(r.Pack.Requirements, "\n") {
			l = strings.TrimSpace(l)
			l = strings.TrimPrefix(l, "#")
			l = strings.TrimSpace(l)
			l = strings.Trim(l, `"'`)
			if l != "" {
				return l
			}
		}
	}
	if len(r.Tasks) > 0 && r.Tasks[0].Title != "" {
		return r.Tasks[0].Title
	}
	return "plan"
}

func defaultScanRuns(ctx context.Context, root string) (int, error) {
	pattern := filepath.Join(root, statepath.DirName, "marshal", "runs", "*", "plan")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() {
			count++
		}
	}
	return count, nil
}

func defaultScanLimits(ctx context.Context, w *Workspace) ([]scanLimit, error) {
	if w == nil {
		return nil, nil
	}
	quotas := w.readUsage(time.Now())
	limits := make([]scanLimit, 0, len(quotas))
	for _, q := range quotas {
		if !q.Known {
			limits = append(limits, scanLimit{Provider: q.Provider, Known: false})
			continue
		}
		win, ok := q.Binding()
		if !ok || !win.Known() {
			limits = append(limits, scanLimit{Provider: q.Provider, Known: false})
			continue
		}
		limits = append(limits, scanLimit{
			Provider:    q.Provider,
			LeftPercent: usageLeft(win),
			Known:       true,
		})
	}
	return limits, nil
}

func defaultScanKnowledge(ctx context.Context, w *Workspace, root string) (string, error) {
	if w == nil || w.runtime == nil {
		return "", nil
	}
	owner := w.runtime.ProjectRoot()
	if root != owner {
		rel, err := filepath.Rel(filepath.Join(owner, statepath.DirName, "worktrees"), root)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", nil
		}
	}
	return projectKnowledgeFetch(ctx, w.runtime, root)
}

func (w *Workspace) runLaunchScan(ctx context.Context, root string) projectScan {
	var scan projectScan
	scan.KnowledgeRoot = root
	parts := launchScanParts

	var wg sync.WaitGroup
	var mu sync.Mutex

	timeout := launchScanPartTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// runPart runs one part in its own goroutine with its own timeout. The
	// part hands back a function that applies its result; it is applied only
	// when the part finished in time, so a part still running after its
	// timeout never touches the scan. A failed or timed-out part is unknown.
	runPart := func(action func(context.Context) (func(), error), onUnknown func(error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			partCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			type outcome struct {
				apply func()
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				apply, err := action(partCtx)
				done <- outcome{apply, err}
			}()
			var got outcome
			select {
			case got = <-done:
			case <-partCtx.Done():
				got.err = partCtx.Err()
			}
			mu.Lock()
			defer mu.Unlock()
			if got.err != nil || got.apply == nil {
				onUnknown(got.err)
				return
			}
			got.apply()
		}()
	}

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.git(c, root)
		return func() { scan.Branch, scan.Changed = res.Branch, res.Changed }, err
	}, func(error) { scan.Branch, scan.Changed = "unknown", -1 })

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.files(c, root)
		return func() { scan.Language, scan.Files = res.Language, res.Files }, err
	}, func(error) { scan.Language, scan.Files = "unknown", -1 })

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.worktrees(c, root)
		return func() { scan.Worktrees = res }, err
	}, func(error) { scan.Worktrees = -1 })

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.unmerged(c, root)
		return func() { scan.Unmerged = res }, err
	}, func(error) { scan.Unmerged = []string{"unknown"} })

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.run(c, w, root)
		return func() { scan.ActiveRun = res }, err
	}, func(error) { scan.ActiveRun = &scanRun{State: "unknown"} })

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.runs(c, root)
		return func() { scan.EarlierRuns = res }, err
	}, func(error) { scan.EarlierRuns = -1 })

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.limits(c, w)
		return func() { scan.Limits = res }, err
	}, func(error) { scan.Limits = nil })

	runPart(func(c context.Context) (func(), error) {
		res, err := parts.knowledge(c, w, root)
		return func() { scan.Knowledge, scan.KnowledgeErr = res, nil }, err
	}, func(err error) {
		if err == nil {
			err = context.DeadlineExceeded
		}
		scan.Knowledge, scan.KnowledgeErr = "", err
	})

	wg.Wait()
	if w != nil {
		w.launch.mu.Lock()
		w.launch.scan = scan
		w.launch.mu.Unlock()
	}
	return scan
}

func (s projectScan) briefingBlock(doctorWarnings []string) string {
	lines := []string{"PROJECT SCAN (data, read only, taken at launch):"}

	// doctor:
	if len(doctorWarnings) == 0 {
		lines = append(lines, "- doctor: passed")
	} else {
		lines = append(lines, "- doctor: warnings: "+strings.Join(doctorWarnings, "; "))
	}

	// project:
	if s.Language == "unknown" || s.Files < 0 {
		lines = append(lines, "- project: unknown")
	} else if s.Language != "" || s.Files > 0 {
		lines = append(lines, fmt.Sprintf("- project: %s, %d files", s.Language, s.Files))
	}

	// branch:
	var branchParts []string
	if s.Branch == "unknown" {
		branchParts = append(branchParts, "branch: unknown")
	} else if s.Branch != "" {
		branchParts = append(branchParts, "branch: "+s.Branch)
	}
	if s.Changed < 0 {
		branchParts = append(branchParts, "uncommitted changes: unknown")
	} else if s.Branch != "unknown" || s.Changed >= 0 {
		branchParts = append(branchParts, fmt.Sprintf("uncommitted changes: %d files", s.Changed))
	}
	if s.Worktrees < 0 {
		branchParts = append(branchParts, "open worktrees: unknown")
	} else {
		branchParts = append(branchParts, fmt.Sprintf("open worktrees: %d", s.Worktrees))
	}
	if len(branchParts) > 0 {
		lines = append(lines, "- "+strings.Join(branchParts, "; "))
	}

	// unmerged branches:
	if len(s.Unmerged) == 1 && s.Unmerged[0] == "unknown" {
		lines = append(lines, "- unmerged branches: unknown")
	} else if len(s.Unmerged) > 0 {
		if len(s.Unmerged) <= 10 {
			lines = append(lines, "- unmerged branches: "+strings.Join(s.Unmerged, ", "))
		} else {
			first10 := strings.Join(s.Unmerged[:10], ", ")
			more := len(s.Unmerged) - 10
			lines = append(lines, fmt.Sprintf("- unmerged branches: %s, and %d more", first10, more))
		}
	}

	// active run:
	if s.ActiveRun == nil {
		lines = append(lines, "- active run: none")
	} else if s.ActiveRun.State == "unknown" {
		lines = append(lines, "- active run: unknown")
	} else {
		lines = append(lines, fmt.Sprintf("- active run: %q, %d of %d tasks done, state %s", s.ActiveRun.Title, s.ActiveRun.Done, s.ActiveRun.Total, s.ActiveRun.State))
	}

	// earlier runs:
	if s.EarlierRuns < 0 {
		lines = append(lines, "- earlier runs: unknown")
	} else if s.EarlierRuns > 0 {
		lines = append(lines, fmt.Sprintf("- earlier runs: %d (requirements under .marshal/marshal/runs/*/plan/)", s.EarlierRuns))
	}

	// earlier sessions:
	if len(s.Sessions) > 0 {
		var sessParts []string
		for _, sess := range s.Sessions {
			if sess.Loaded > 0 {
				sessParts = append(sessParts, fmt.Sprintf("%s %d loaded", sess.Provider, sess.Loaded))
			} else {
				sessParts = append(sessParts, fmt.Sprintf("%s 0", sess.Provider))
			}
		}
		lines = append(lines, "- earlier sessions: "+strings.Join(sessParts, ", "))
	}

	// limits:
	if s.Limits == nil {
		lines = append(lines, "- limits: unknown")
	} else if len(s.Limits) > 0 {
		var limParts []string
		for _, lim := range s.Limits {
			if lim.Known {
				limParts = append(limParts, fmt.Sprintf("%s %d%%", lim.Provider, lim.LeftPercent))
			} else {
				limParts = append(limParts, fmt.Sprintf("%s unavailable", lim.Provider))
			}
		}
		lines = append(lines, "- limits: "+strings.Join(limParts, " · "))
	}

	return strings.Join(lines, "\n")
}

func (w *Workspace) launchScanBlock(root string) string {
	w.launch.mu.Lock()
	scan := w.launch.scan
	warnings := append([]string(nil), w.launch.warnings...)
	w.launch.mu.Unlock()

	if scan.KnowledgeRoot == "" || scan.KnowledgeRoot != root {
		return ""
	}
	block := scan.briefingBlock(warnings)
	if block == "" {
		return ""
	}
	return "\n" + block + "\n"
}
