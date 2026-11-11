package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
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

func defaultScanGit(ctx context.Context, root string) (scanGit, error) {
	cmdBranch, err := hostgit.Command(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return scanGit{}, err
	}
	outBranch, err := cmdBranch.Output()
	if err != nil {
		return scanGit{}, err
	}
	branch := strings.TrimSpace(string(outBranch))

	cmdStatus, err := hostgit.Command(ctx, root, "status", "--porcelain")
	if err != nil {
		return scanGit{}, err
	}
	outStatus, err := cmdStatus.Output()
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
	cmd, err := hostgit.Command(ctx, root, "ls-files")
	if err != nil {
		return scanFiles{}, err
	}
	out, err := cmd.Output()
	if err != nil {
		return scanFiles{}, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return scanFiles{Language: "mixed", Files: 0}, nil
	}
	lines := strings.Split(raw, "\n")
	counts := make(map[string]int)
	total := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total++
		ext := strings.ToLower(filepath.Ext(line))
		lang, ok := languageByExt[ext]
		if !ok {
			lang = "mixed"
		}
		counts[lang]++
	}
	if total == 0 {
		return scanFiles{Language: "mixed", Files: 0}, nil
	}

	bestLang := "mixed"
	bestCount := 0
	for lang, count := range counts {
		if count > bestCount {
			bestCount = count
			bestLang = lang
		}
	}
	return scanFiles{Language: bestLang, Files: total}, nil
}

func defaultScanWorktrees(ctx context.Context, root string) (int, error) {
	cmd, err := hostgit.Command(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return 0, err
	}
	out, err := cmd.Output()
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
	cmd, err := hostgit.Command(ctx, root, "for-each-ref", "--format=%(refname:short)", "refs/heads", "--no-merged", "HEAD")
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
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

	title := extractPlanTitle(r)
	return &scanRun{
		Title: title,
		Done:  done,
		Total: total,
		State: r.State,
	}, nil
}

func extractPlanTitle(r marshal.Run) string {
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
			LeftPercent: int(math.Floor(win.LeftPercent)),
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

	var wg sync.WaitGroup
	var mu sync.Mutex

	timeout := launchScanPartTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	runPart := func(action func(context.Context) error, onResult func(), onUnknown func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			partCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- action(partCtx)
			}()
			select {
			case err := <-done:
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					onUnknown()
				} else {
					onResult()
				}
			case <-partCtx.Done():
				mu.Lock()
				defer mu.Unlock()
				onUnknown()
			}
		}()
	}

	// 1. git
	var gitRes scanGit
	runPart(
		func(c context.Context) error {
			var err error
			scanGitFn := launchScanParts.git
			gitRes, err = scanGitFn(c, root)
			return err
		},
		func() {
			scan.Branch = gitRes.Branch
			scan.Changed = gitRes.Changed
		},
		func() {
			scan.Branch = "unknown"
			scan.Changed = -1
		},
	)

	// 2. files
	var filesRes scanFiles
	runPart(
		func(c context.Context) error {
			var err error
			filesRes, err = launchScanParts.files(c, root)
			return err
		},
		func() {
			scan.Language = filesRes.Language
			scan.Files = filesRes.Files
		},
		func() {
			scan.Language = "unknown"
			scan.Files = -1
		},
	)

	// 3. worktrees
	var wtRes int
	runPart(
		func(c context.Context) error {
			var err error
			wtRes, err = launchScanParts.worktrees(c, root)
			return err
		},
		func() {
			scan.Worktrees = wtRes
		},
		func() {
			scan.Worktrees = -1
		},
	)

	// 4. unmerged
	var unmergedRes []string
	runPart(
		func(c context.Context) error {
			var err error
			unmergedRes, err = launchScanParts.unmerged(c, root)
			return err
		},
		func() {
			scan.Unmerged = unmergedRes
		},
		func() {
			scan.Unmerged = []string{"unknown"}
		},
	)

	// 5. run
	var runRes *scanRun
	runPart(
		func(c context.Context) error {
			var err error
			runRes, err = launchScanParts.run(c, w, root)
			return err
		},
		func() {
			scan.ActiveRun = runRes
		},
		func() {
			scan.ActiveRun = &scanRun{State: "unknown"}
		},
	)

	// 6. runs
	var runsRes int
	runPart(
		func(c context.Context) error {
			var err error
			runsRes, err = launchScanParts.runs(c, root)
			return err
		},
		func() {
			scan.EarlierRuns = runsRes
		},
		func() {
			scan.EarlierRuns = -1
		},
	)

	// 7. limits
	var limitsRes []scanLimit
	runPart(
		func(c context.Context) error {
			var err error
			limitsRes, err = launchScanParts.limits(c, w)
			return err
		},
		func() {
			scan.Limits = limitsRes
		},
		func() {
			scan.Limits = nil
		},
	)

	// 8. knowledge
	var knowRes string
	var knowErr error
	runPart(
		func(c context.Context) error {
			knowRes, knowErr = launchScanParts.knowledge(c, w, root)
			return knowErr
		},
		func() {
			scan.Knowledge = knowRes
			scan.KnowledgeErr = nil
		},
		func() {
			scan.Knowledge = ""
			if knowErr != nil {
				scan.KnowledgeErr = knowErr
			} else {
				scan.KnowledgeErr = context.DeadlineExceeded
			}
		},
	)

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
