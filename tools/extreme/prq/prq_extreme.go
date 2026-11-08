//go:build !governed

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PRItem struct {
	Number   int    `json:"number"`
	Worktree string `json:"worktree"`
}

type Config struct {
	MainBranch        string
	Remote            string
	StatusFile        string
	PollInterval      int
	MaxPollAttempts   int
	InitialBackoffMs  int
	MaxBackoffMs      int
	MaxNetworkRetries int
}

type CheckItem struct {
	TypeName   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type PRViewResponse struct {
	HeadRefOID        string      `json:"headRefOid"`
	Mergeable         string      `json:"mergeable"`
	StatusCheckRollup []CheckItem `json:"statusCheckRollup"`
}

type StatusEvent struct {
	Timestamp string   `json:"timestamp"`
	PR        int      `json:"pr"`
	Worktree  string   `json:"worktree,omitempty"`
	Step      string   `json:"step"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	Error     string   `json:"error,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	Attempt   int      `json:"attempt,omitempty"`
}

type StatusLogger struct {
	filePath string
	mu       sync.Mutex
}

func NewStatusLogger(filePath string) *StatusLogger {
	return &StatusLogger{filePath: filePath}
}

func (l *StatusLogger) Log(ev StatusEvent) {
	if ev.Timestamp == "" {
		ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	line := string(data)

	// JSON status line to stdout
	fmt.Println(line)

	// Append JSON status line to status file
	if l.filePath != "" {
		l.mu.Lock()
		defer l.mu.Unlock()
		if err := os.MkdirAll(filepath.Dir(l.filePath), 0755); err == nil {
			f, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				f.WriteString(line + "\n")
				f.Close()
			}
		}
	}
}

func runInWorktree(worktree string, name string, args ...string) (string, error) {
	return runInWorktreeWithEnv(worktree, nil, name, args...)
}

func runInWorktreeWithEnv(worktree string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = worktree
	env := os.Environ()
	if len(extraEnv) > 0 {
		env = append(env, extraEnv...)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func getUnmergedFiles(worktree string) ([]string, error) {
	out, err := runInWorktree(worktree, "git", "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var res []string
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			res = append(res, filepath.ToSlash(l))
		}
	}
	return res, nil
}

func isReleaseNotesFile(path string) bool {
	clean := filepath.Clean(filepath.ToSlash(path))
	matched, _ := filepath.Match("release/RELEASE_NOTES_*.md", clean)
	return matched
}

func isAllowedConflict(path string) bool {
	if path == "distribution/PACK-MANIFEST.json" {
		return true
	}
	return isReleaseNotesFile(path)
}

func ResolveNotesConflict(content string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var out []string
	var ours []string
	var theirs []string
	inConflict := false
	inTheirs := false
	inBase := false

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "<<<<<<<") {
			inConflict = true
			inTheirs = false
			inBase = false
			ours = nil
			theirs = nil
			continue
		}
		if inConflict && strings.HasPrefix(line, "|||||||") {
			inBase = true
			inTheirs = false
			continue
		}
		if inConflict && strings.HasPrefix(line, "=======") {
			inTheirs = true
			inBase = false
			continue
		}
		if inConflict && strings.HasPrefix(line, ">>>>>>>") {
			inConflict = false
			inTheirs = false
			inBase = false
			// Keep both sides, main (theirs) first!
			out = append(out, theirs...)
			out = append(out, ours...)
			ours = nil
			theirs = nil
			continue
		}
		if inConflict {
			if inTheirs {
				theirs = append(theirs, line)
			} else if inBase {
				// skip base lines in 3-way diff
			} else {
				ours = append(ours, line)
			}
		} else {
			out = append(out, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if inConflict {
		return "", fmt.Errorf("unclosed conflict markers in release notes")
	}

	res := strings.Join(out, "\n")
	if strings.HasSuffix(content, "\n") && !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return res, nil
}

func evaluateChecks(checks []CheckItem) (allPass bool, hasPending bool, hasFailure bool, failureDetail string) {
	if len(checks) == 0 {
		return false, true, false, ""
	}
	for _, c := range checks {
		status := strings.ToUpper(c.Status)
		conclusion := strings.ToUpper(c.Conclusion)
		state := strings.ToUpper(c.State)
		name := c.Name
		if name == "" {
			name = c.Context
		}

		// Check failures
		if conclusion == "FAILURE" || conclusion == "TIMED_OUT" || conclusion == "CANCELLED" || conclusion == "ACTION_REQUIRED" || conclusion == "STARTUP_FAILURE" || state == "FAILURE" || state == "ERROR" {
			return false, false, true, fmt.Sprintf("%s (%s/%s)", name, status, conclusion)
		}

		// Check pending
		if state != "SUCCESS" && !(status == "COMPLETED" && (conclusion == "SUCCESS" || conclusion == "NEUTRAL" || conclusion == "SKIPPED")) {
			hasPending = true
		}
	}
	if hasPending {
		return false, true, false, ""
	}
	return true, false, false, ""
}

func ProcessPR(cfg Config, pr PRItem, logger *StatusLogger) (resultErr error) {
	defer func() {
		if resultErr != nil {
			logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "processing", Status: "failed", Error: resultErr.Error()})
		}
	}()
	logger.Log(StatusEvent{
		PR:       pr.Number,
		Worktree: pr.Worktree,
		Step:     "start",
		Status:   "processing",
		Message:  fmt.Sprintf("Processing PR %d in %s", pr.Number, pr.Worktree),
	})

	remote := cfg.Remote
	if remote == "" {
		remote = "origin"
	}
	mainBranch := cfg.MainBranch
	if mainBranch == "" {
		mainBranch = "main"
	}
	targetRef := fmt.Sprintf("%s/%s", remote, mainBranch)

	// Refuse to mix operator edits into automated commits or merges.
	initialStatus, err := runInWorktree(pr.Worktree, "git", "status", "--porcelain")
	if err != nil || strings.TrimSpace(initialStatus) != "" {
		return fmt.Errorf("worktree must be clean before processing: %v: %s", err, initialStatus)
	}

	branch, err := runInWorktree(pr.Worktree, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) == mainBranch {
		return fmt.Errorf("require a PR branch distinct from %s: %v", mainBranch, err)
	}
	if _, err := runInWorktree(pr.Worktree, "git", "rev-parse", "--verify", "MERGE_HEAD"); err == nil {
		return errors.New("worktree already has an unfinished merge")
	}
	// Abort only a merge started by this operation, preserving the clean input.
	defer func() {
		if resultErr != nil {
			if _, err := runInWorktree(pr.Worktree, "git", "rev-parse", "--verify", "MERGE_HEAD"); err == nil {
				if out, err := runInWorktree(pr.Worktree, "git", "merge", "--abort"); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("abort failed: %w: %s", err, out))
				}
			}
		}
	}()

	// 1. Fetch
	logger.Log(StatusEvent{
		PR:       pr.Number,
		Worktree: pr.Worktree,
		Step:     "fetch",
		Status:   "running",
		Message:  fmt.Sprintf("Fetching %s %s", remote, mainBranch),
	})
	if out, err := runInWorktree(pr.Worktree, "git", "fetch", remote, mainBranch); err != nil {
		errStr := fmt.Sprintf("git fetch failed: %v: %s", err, out)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "fetch", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}

	// 2. Merge origin/main into the branch (no rebase, no force-push)
	logger.Log(StatusEvent{
		PR:       pr.Number,
		Worktree: pr.Worktree,
		Step:     "merge",
		Status:   "running",
		Message:  fmt.Sprintf("Merging %s into branch", targetRef),
	})
	_, mergeErr := runInWorktree(pr.Worktree, "git", "merge", "--no-edit", targetRef)
	if mergeErr != nil {
		// Conflict check
		conflicts, err := getUnmergedFiles(pr.Worktree)
		if err != nil {
			runInWorktree(pr.Worktree, "git", "merge", "--abort")
			errStr := fmt.Sprintf("failed to get unmerged files: %v", err)
			logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "merge", Status: "failed", Error: errStr})
			return errors.New(errStr)
		}

		if len(conflicts) == 0 {
			return fmt.Errorf("git merge failed without resolvable conflicts: %w", mergeErr)
		}

		var unsupported []string
		for _, c := range conflicts {
			if !isAllowedConflict(c) {
				unsupported = append(unsupported, c)
			}
		}

		if len(unsupported) > 0 {
			runInWorktree(pr.Worktree, "git", "merge", "--abort")
			errStr := fmt.Sprintf("unsupported conflict in: %s", strings.Join(unsupported, ", "))
			logger.Log(StatusEvent{
				PR:        pr.Number,
				Worktree:  pr.Worktree,
				Step:      "conflict",
				Status:    "stopped",
				Message:   fmt.Sprintf("conflict in %s stops PR", strings.Join(unsupported, ", ")),
				Conflicts: unsupported,
				Error:     errStr,
			})
			return errors.New(errStr)
		}

		// Auto-resolve allowed conflicts:
		// First, resolve release notes conflicts
		for _, c := range conflicts {
			if isReleaseNotesFile(c) {
				logger.Log(StatusEvent{
					PR:       pr.Number,
					Worktree: pr.Worktree,
					Step:     "resolve",
					Status:   "running",
					Message:  fmt.Sprintf("Auto-resolving %s (keeping both sides, main first)", c),
				})
				fullPath := filepath.Join(pr.Worktree, c)
				contentBytes, err := os.ReadFile(fullPath)
				if err != nil {
					runInWorktree(pr.Worktree, "git", "merge", "--abort")
					errStr := fmt.Sprintf("read conflicted notes failed: %v", err)
					logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
					return errors.New(errStr)
				}
				resolved, err := ResolveNotesConflict(string(contentBytes))
				if err != nil {
					runInWorktree(pr.Worktree, "git", "merge", "--abort")
					errStr := fmt.Sprintf("resolve notes conflict failed: %v", err)
					logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
					return errors.New(errStr)
				}
				if err := os.WriteFile(fullPath, []byte(resolved), 0644); err != nil {
					runInWorktree(pr.Worktree, "git", "merge", "--abort")
					errStr := fmt.Sprintf("write resolved notes failed: %v", err)
					logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
					return errors.New(errStr)
				}
				if out, err := runInWorktree(pr.Worktree, "git", "add", c); err != nil {
					runInWorktree(pr.Worktree, "git", "merge", "--abort")
					errStr := fmt.Sprintf("git add notes failed: %v: %s", err, out)
					logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
					return errors.New(errStr)
				}
			}
		}

		// Second, auto-resolve distribution/PACK-MANIFEST.json if conflicted
		hasManifestConflict := false
		for _, c := range conflicts {
			if c == "distribution/PACK-MANIFEST.json" {
				hasManifestConflict = true
				break
			}
		}

		if hasManifestConflict {
			logger.Log(StatusEvent{
				PR:       pr.Number,
				Worktree: pr.Worktree,
				Step:     "resolve",
				Status:   "running",
				Message:  "Auto-resolving distribution/PACK-MANIFEST.json (taking main's and regenerating)",
			})
			// Take main's
			if out, err := runInWorktree(pr.Worktree, "git", "checkout", targetRef, "--", "distribution/PACK-MANIFEST.json"); err != nil {
				runInWorktree(pr.Worktree, "git", "merge", "--abort")
				errStr := fmt.Sprintf("checkout main manifest failed: %v: %s", err, out)
				logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
				return errors.New(errStr)
			}
			// Regenerate with python3 tools/release_verify.py --generate . distribution/PACK-MANIFEST.json
			if out, err := runInWorktree(pr.Worktree, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json"); err != nil {
				runInWorktree(pr.Worktree, "git", "merge", "--abort")
				errStr := fmt.Sprintf("regenerate manifest during merge failed: %v: %s", err, out)
				logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
				return errors.New(errStr)
			}
			if out, err := runInWorktree(pr.Worktree, "git", "add", "distribution/PACK-MANIFEST.json"); err != nil {
				runInWorktree(pr.Worktree, "git", "merge", "--abort")
				errStr := fmt.Sprintf("git add manifest failed: %v: %s", err, out)
				logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
				return errors.New(errStr)
			}
		} else {
			// If release notes were modified, regenerate manifest with updated notes hash
			if out, err := runInWorktree(pr.Worktree, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json"); err != nil {
				runInWorktree(pr.Worktree, "git", "merge", "--abort")
				errStr := fmt.Sprintf("regenerate manifest failed: %v: %s", err, out)
				logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
				return errors.New(errStr)
			}
			if out, err := runInWorktree(pr.Worktree, "git", "add", "distribution/PACK-MANIFEST.json"); err != nil {
				runInWorktree(pr.Worktree, "git", "merge", "--abort")
				errStr := fmt.Sprintf("git add manifest failed: %v: %s", err, out)
				logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "resolve", Status: "failed", Error: errStr})
				return errors.New(errStr)
			}
		}

		// Commit the merge
		commitMsg := fmt.Sprintf("Merge %s into PR #%d", targetRef, pr.Number)
		if out, err := runInWorktreeWithEnv(pr.Worktree, nil, "git", "commit", "-m", commitMsg); err != nil {
			errStr := fmt.Sprintf("commit merge failed: %v: %s", err, out)
			logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "merge", Status: "failed", Error: errStr})
			return errors.New(errStr)
		}
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "merge", Status: "resolved", Message: "Auto-resolved conflicts and committed merge"})
	} else {
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "merge", Status: "clean", Message: "Clean merge of main"})
	}

	// 4. Regenerate manifest after committing, require git status clean and verification PASS,
	//    go build ./... and go test ./tools/effect-inventory
	logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "verify", Status: "running", Message: "Regenerating manifest and validating status/checks"})

	if out, err := runInWorktree(pr.Worktree, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json"); err != nil {
		errStr := fmt.Sprintf("regenerate manifest after committing failed: %v: %s", err, out)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "verify", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}

	manifestDiff, err := runInWorktree(pr.Worktree, "git", "diff", "--", "distribution/PACK-MANIFEST.json")
	if err != nil {
		return err
	}
	if strings.TrimSpace(manifestDiff) != "" {
		if _, err := runInWorktree(pr.Worktree, "git", "add", "distribution/PACK-MANIFEST.json"); err != nil {
			return err
		}
		if _, err := runInWorktree(pr.Worktree, "git", "commit", "-m", "Regenerate pack manifest"); err != nil {
			return err
		}
	}

	statusOut, err := runInWorktree(pr.Worktree, "git", "status", "--porcelain")
	if err != nil {
		errStr := fmt.Sprintf("git status failed: %v: %s", err, statusOut)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "verify", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}
	if strings.TrimSpace(statusOut) != "" {
		errStr := fmt.Sprintf("worktree not clean after manifest generation: %s", statusOut)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "verify", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}

	verifyOut, err := runInWorktree(pr.Worktree, "python3", "tools/release_verify.py", ".", "distribution/PACK-MANIFEST.json")
	if err != nil || !strings.Contains(verifyOut, `"status": "PASS"`) {
		errStr := fmt.Sprintf("release verification failed: %v: %s", err, verifyOut)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "verify", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}

	goEnv := []string{
		"GOCACHE=/tmp/marshal-ex5-gocache",
	}
	if customCache := os.Getenv("GOCACHE"); customCache != "" {
		goEnv = []string{"GOCACHE=" + customCache}
	}

	if out, err := runInWorktreeWithEnv(pr.Worktree, goEnv, "go", "build", "./..."); err != nil {
		errStr := fmt.Sprintf("go build ./... failed: %v: %s", err, out)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "build", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}

	if out, err := runInWorktreeWithEnv(pr.Worktree, goEnv, "go", "test", "./tools/effect-inventory"); err != nil {
		errStr := fmt.Sprintf("go test ./tools/effect-inventory failed: %v: %s", err, out)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "test", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}
	logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "verify", Status: "ok", Message: "Clean git status, verification PASS, build and test succeeded"})

	// 5. Push (no force-push)
	logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "push", Status: "running", Message: "Pushing to remote"})
	if out, err := runInWorktree(pr.Worktree, "git", "push", remote, "HEAD"); err != nil {
		errStr := fmt.Sprintf("git push failed: %v: %s", err, out)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "push", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}
	logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "push", Status: "ok", Message: "Pushed to remote"})

	// 6. Poll gh with backoff on network errors until MERGEABLE and all checks pass
	logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "poll", Status: "running", Message: "Polling gh for mergeability and checks"})

	pollInterval := time.Duration(cfg.PollInterval) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	currentBackoff := time.Duration(cfg.InitialBackoffMs) * time.Millisecond
	if currentBackoff <= 0 {
		currentBackoff = 1 * time.Second
	}
	maxBackoff := time.Duration(cfg.MaxBackoffMs) * time.Millisecond
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	maxRetries := cfg.MaxNetworkRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	maxAttempts := cfg.MaxPollAttempts
	if maxAttempts <= 0 {
		maxAttempts = 720
	}

	head, err := runInWorktree(pr.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)

	networkErrorsCount := 0
	pollAttempt := 0
	readyToMerge := false

	for {
		pollAttempt++
		if pollAttempt > maxAttempts {
			errStr := fmt.Sprintf("exceeded maximum poll attempts (%d)", maxAttempts)
			logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "poll", Status: "timeout", Error: errStr})
			return errors.New(errStr)
		}

		prViewOut, err := runInWorktree(pr.Worktree, "gh", "pr", "view", strconv.Itoa(pr.Number), "--repo", "Zen1th53/marshal", "--json", "headRefOid,mergeable,statusCheckRollup")
		if err != nil {
			networkErrorsCount++
			if networkErrorsCount > maxRetries {
				errStr := fmt.Sprintf("exceeded max network retries (%d): %v: %s", maxRetries, err, prViewOut)
				logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "poll", Status: "failed", Error: errStr})
				return errors.New(errStr)
			}
			logger.Log(StatusEvent{
				PR:       pr.Number,
				Worktree: pr.Worktree,
				Step:     "poll",
				Status:   "retry",
				Message:  fmt.Sprintf("Network error on gh pr view (retry %d/%d), backing off %v", networkErrorsCount, maxRetries, currentBackoff),
				Attempt:  networkErrorsCount,
				Error:    fmt.Sprintf("%v: %s", err, prViewOut),
			})
			time.Sleep(currentBackoff)
			currentBackoff *= 2
			if currentBackoff > maxBackoff {
				currentBackoff = maxBackoff
			}
			continue
		}

		networkErrorsCount = 0
		currentBackoff = time.Duration(cfg.InitialBackoffMs) * time.Millisecond
		if currentBackoff <= 0 {
			currentBackoff = 1 * time.Second
		}

		var resp PRViewResponse
		if err := json.Unmarshal([]byte(prViewOut), &resp); err != nil {
			errStr := fmt.Sprintf("invalid json from gh pr view: %v", err)
			logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "poll", Status: "failed", Error: errStr})
			return errors.New(errStr)
		}

		if resp.HeadRefOID != head {
			logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "poll", Status: "waiting", Message: "Waiting for GitHub to observe verified commit", Attempt: pollAttempt})
			time.Sleep(pollInterval)
			continue
		}
		mergeable := strings.ToUpper(resp.Mergeable)
		if mergeable == "CONFLICTING" {
			errStr := "PR is CONFLICTING on GitHub"
			logger.Log(StatusEvent{
				PR:       pr.Number,
				Worktree: pr.Worktree,
				Step:     "poll",
				Status:   "conflicting",
				Message:  errStr,
				Error:    errStr,
			})
			return errors.New(errStr)
		}

		if mergeable != "MERGEABLE" {
			logger.Log(StatusEvent{
				PR:       pr.Number,
				Worktree: pr.Worktree,
				Step:     "poll",
				Status:   "waiting",
				Message:  fmt.Sprintf("Mergeable status is %s; waiting", mergeable),
				Attempt:  pollAttempt,
			})
			time.Sleep(pollInterval)
			continue
		}

		allPass, hasPending, hasFailure, failureDetail := evaluateChecks(resp.StatusCheckRollup)
		if hasFailure {
			errStr := fmt.Sprintf("checks failed: %s", failureDetail)
			logger.Log(StatusEvent{
				PR:       pr.Number,
				Worktree: pr.Worktree,
				Step:     "poll",
				Status:   "checks_failed",
				Message:  errStr,
				Error:    errStr,
			})
			return errors.New(errStr)
		}

		if hasPending {
			logger.Log(StatusEvent{
				PR:       pr.Number,
				Worktree: pr.Worktree,
				Step:     "poll",
				Status:   "waiting",
				Message:  "Checks pending; waiting",
				Attempt:  pollAttempt,
			})
			time.Sleep(pollInterval)
			continue
		}

		if allPass {
			logger.Log(StatusEvent{
				PR:       pr.Number,
				Worktree: pr.Worktree,
				Step:     "poll",
				Status:   "checks_passed",
				Message:  "PR is MERGEABLE and all checks pass",
				Attempt:  pollAttempt,
			})
			readyToMerge = true
			break
		}
	}

	if !readyToMerge {
		return errors.New("polling finished without mergeable status")
	}

	// Bind the GitHub merge to the exact local commit that passed verification.
	// 7. gh pr merge --merge
	logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "merge_pr", Status: "running", Message: "Merging PR via gh"})
	if out, err := runInWorktree(pr.Worktree, "gh", "pr", "merge", strconv.Itoa(pr.Number), "--repo", "Zen1th53/marshal", "--merge", "--match-head-commit", strings.TrimSpace(head)); err != nil {
		errStr := fmt.Sprintf("gh pr merge failed: %v: %s", err, out)
		logger.Log(StatusEvent{PR: pr.Number, Worktree: pr.Worktree, Step: "merge_pr", Status: "failed", Error: errStr})
		return errors.New(errStr)
	}

	logger.Log(StatusEvent{
		PR:       pr.Number,
		Worktree: pr.Worktree,
		Step:     "merge_pr",
		Status:   "merged",
		Message:  fmt.Sprintf("PR %d successfully merged", pr.Number),
	})

	return nil
}

func RunQueue(cfg Config, prs []PRItem) error {
	logger := NewStatusLogger(cfg.StatusFile)
	var stoppedPRs []int
	for _, pr := range prs {
		if err := ProcessPR(cfg, pr, logger); err != nil {
			stoppedPRs = append(stoppedPRs, pr.Number)
		}
	}
	if len(stoppedPRs) > 0 {
		return fmt.Errorf("queue completed with stopped/failed PRs: %v", stoppedPRs)
	}
	return nil
}
