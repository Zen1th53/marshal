//go:build !governed

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type FakeGHState struct {
	HeadRefOID               string      `json:"head_ref_oid,omitempty"`
	NetworkFailuresRemaining int         `json:"network_failures_remaining"`
	Mergeable                string      `json:"mergeable"`
	Checks                   []CheckItem `json:"checks"`
}

type FakeGHAction struct {
	Action string   `json:"action"`
	Args   []string `json:"args"`
}

func setupTestEnv(t *testing.T) (tempDir, bareRepo string) {
	t.Helper()
	tempDir = t.TempDir()
	bareRepo = filepath.Join(tempDir, "origin.git")

	// 1. Initialize bare origin
	runCmd(t, tempDir, "git", "init", "--bare", bareRepo)

	// 2. Clone seed repo to initialize main branch
	seedDir := filepath.Join(tempDir, "seed")
	runCmd(t, tempDir, "git", "clone", bareRepo, seedDir)
	runCmd(t, seedDir, "git", "config", "user.name", "PRQ Test")
	runCmd(t, seedDir, "git", "config", "user.email", "prq-test@example.com")
	runCmd(t, seedDir, "git", "checkout", "-b", "main")

	// Set up required directories in seed repo
	mustMkdir(t, filepath.Join(seedDir, "distribution"))
	mustMkdir(t, filepath.Join(seedDir, "release"))
	mustMkdir(t, filepath.Join(seedDir, "tools", "effect-inventory"))

	// Create go.mod
	mustWrite(t, filepath.Join(seedDir, "go.mod"), "module github.com/Zen1th53/marshal-test\n\ngo 1.25\n")

	// Keep multiple packages, matching the real repository: go build ./...
	// must not emit a standalone executable into the checkout.
	mustWrite(t, filepath.Join(seedDir, "library.go"), "package library\n")

	// Create tools/effect-inventory files
	mustWrite(t, filepath.Join(seedDir, "tools", "effect-inventory", "main.go"), "package main\n\nfunc main() {}\n")
	mustWrite(t, filepath.Join(seedDir, "tools", "effect-inventory", "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestDummy(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(seedDir, "tools", "effect-inventory", "sites.csv"), "file,function,effect,ordinal,source_hash,scope,guard,review_rationale\n")

	// Copy release_verify.py
	verifyPyBytes, err := os.ReadFile("../../release_verify.py")
	if err != nil {
		t.Fatalf("failed to read release_verify.py: %v", err)
	}
	mustWrite(t, filepath.Join(seedDir, "tools", "release_verify.py"), string(verifyPyBytes))

	// Create release notes
	mustWrite(t, filepath.Join(seedDir, "release", "RELEASE_NOTES_0.0.8.md"), "# Release Notes 0.0.8\n\n## Changes\n")

	// Create initial PACK-MANIFEST.json
	initManifest := `{
  "manifest_version": 1,
  "pack_version": "0.0.8",
  "generated_date": "2026-10-10T00:00:00Z",
  "hash_algorithm": "sha256",
  "excluded_paths": [
    ".git",
    "distribution/PACK-MANIFEST.json"
  ],
  "files": []
}
`
	mustWrite(t, filepath.Join(seedDir, "distribution", "PACK-MANIFEST.json"), initManifest)

	// Generate real hashes into PACK-MANIFEST.json
	runCmd(t, seedDir, "git", "add", ".")
	runCmd(t, seedDir, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, seedDir, "git", "add", ".")
	runCmd(t, seedDir, "git", "commit", "-m", "Initial commit on main")
	runCmd(t, seedDir, "git", "push", "origin", "main")
	runCmd(t, bareRepo, "git", "symbolic-ref", "HEAD", "refs/heads/main")

	// 3. Setup fake gh in bin/
	binDir := filepath.Join(tempDir, "bin")
	mustMkdir(t, binDir)
	fakeGHScript := fmt.Sprintf(`#!/usr/bin/env python3
import json
import os
import sys
import subprocess

state_file = %q
actions_file = %q

with open(actions_file, "a") as f:
    f.write(json.dumps({"args": sys.argv[1:]}) + "\n")

if len(sys.argv) > 1 and sys.argv[1] == "pr":
    sub = sys.argv[2]
    if sub == "view":
        if os.path.exists(state_file):
            with open(state_file, "r") as f:
                state = json.load(f)
        else:
            state = {"network_failures_remaining": 0, "mergeable": "MERGEABLE", "checks": []}

        rem = state.get("network_failures_remaining", 0)
        if rem > 0:
            state["network_failures_remaining"] = rem - 1
            with open(state_file, "w") as f:
                json.dump(state, f)
            sys.stderr.write("fake-gh network connection reset by peer\n")
            sys.exit(1)

        mergeable = state.get("mergeable", "MERGEABLE")
        checks = state.get("checks", [])
        head = state.get("head_ref_oid") or subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
        payload = {"headRefOid": head, "mergeable": mergeable, "statusCheckRollup": checks}
        print(json.dumps(payload))
        sys.exit(0)

    elif sub == "merge":
        sys.exit(0)

sys.stderr.write(f"unknown gh command: {sys.argv}\n")
sys.exit(1)
`, filepath.Join(tempDir, "gh-state.json"), filepath.Join(tempDir, "gh-actions.json"))

	ghPath := filepath.Join(binDir, "gh")
	mustWrite(t, ghPath, fakeGHScript)
	if err := os.Chmod(ghPath, 0755); err != nil {
		t.Fatalf("chmod gh failed: %v", err)
	}

	// Prepend binDir to PATH
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("GOCACHE", "/tmp/marshal-ex5-gocache")

	return tempDir, bareRepo
}

func setFakeGHState(t *testing.T, tempDir string, state FakeGHState) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal gh state: %v", err)
	}
	mustWrite(t, filepath.Join(tempDir, "gh-state.json"), string(data))
}

func readGHActions(tempDir string) ([]FakeGHAction, error) {
	path := filepath.Join(tempDir, "gh-actions.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var actions []FakeGHAction
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var act FakeGHAction
		if err := json.Unmarshal([]byte(l), &act); err == nil {
			actions = append(actions, act)
		}
	}
	return actions, nil
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v in %s failed: %v\nOutput: %s", name, args, dir, err, string(out))
	}
	return string(out)
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s failed: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s failed: %v", path, err)
	}
}

func createPRWorktree(t *testing.T, tempDir, bareRepo, branch string) string {
	t.Helper()
	wtDir := filepath.Join(tempDir, "wt-"+branch)
	runCmd(t, tempDir, "git", "clone", "-b", "main", bareRepo, wtDir)
	runCmd(t, wtDir, "git", "config", "user.name", "PRQ Tester")
	runCmd(t, wtDir, "git", "config", "user.email", "prq-tester@example.com")
	runCmd(t, wtDir, "git", "checkout", "-b", branch)
	return wtDir
}

func TestResolveNotesConflict(t *testing.T) {
	// 2-way conflict
	input2way := `# Release Notes
<<<<<<< HEAD
- Feature from PR branch
=======
- Feature from Main branch
>>>>>>> origin/main
Trailing content
`
	resolved, err := ResolveNotesConflict(input2way)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected2way := `# Release Notes
- Feature from Main branch
- Feature from PR branch
Trailing content
`
	if resolved != expected2way {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected2way, resolved)
	}

	// 3-way diff3 conflict
	inputDiff3 := `# Release Notes
<<<<<<< HEAD
- Feature from PR
||||||| base
- Old feature
=======
- Feature from Main
>>>>>>> origin/main
Footer
`
	resolved3, err := ResolveNotesConflict(inputDiff3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedDiff3 := `# Release Notes
- Feature from Main
- Feature from PR
Footer
`
	if resolved3 != expectedDiff3 {
		t.Fatalf("expected:\n%s\ngot:\n%s", expectedDiff3, resolved3)
	}

	// Unclosed conflict marker
	badInput := `# Release Notes
<<<<<<< HEAD
- Unclosed
`
	_, err = ResolveNotesConflict(badInput)
	if err == nil {
		t.Fatalf("expected error on unclosed conflict marker")
	}
}

// 1. Clean merge
func TestPRQ_CleanMerge(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	setFakeGHState(t, tempDir, FakeGHState{
		Mergeable: "MERGEABLE",
		Checks: []CheckItem{
			{Status: "COMPLETED", Conclusion: "SUCCESS", Name: "ci"},
		},
	})

	wt := createPRWorktree(t, tempDir, bareRepo, "feature-clean")
	mustWrite(t, filepath.Join(wt, "feature.txt"), "hello feature\n")
	runCmd(t, wt, "git", "add", "feature.txt")
	runCmd(t, wt, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "commit", "-m", "feature commit")

	statusFile := filepath.Join(tempDir, "prq-status.jsonl")
	cfg := Config{
		MainBranch:        "main",
		Remote:            "origin",
		StatusFile:        statusFile,
		PollInterval:      10,
		MaxPollAttempts:   5,
		InitialBackoffMs:  10,
		MaxBackoffMs:      50,
		MaxNetworkRetries: 3,
	}

	err := RunQueue(cfg, []PRItem{{Number: 101, Worktree: wt}})
	if err != nil {
		t.Fatalf("expected RunQueue to succeed, got %v", err)
	}

	actions, _ := readGHActions(tempDir)
	merged := false
	for _, act := range actions {
		if len(act.Args) >= 2 && act.Args[0] == "pr" && act.Args[1] == "merge" {
			merged = true
			joined := strings.Join(act.Args, " ")
			if !strings.Contains(joined, "--repo Zen1th53/marshal") || !strings.Contains(joined, "--match-head-commit "+strings.TrimSpace(runCmd(t, wt, "git", "rev-parse", "HEAD"))) {
				t.Fatalf("merge not bound to verified commit: %s", joined)
			}
			break
		}
	}
	if !merged {
		t.Fatalf("expected PR 101 to be merged by gh, actions: %+v", actions)
	}
}

// 2. Manifest conflict
func TestPRQ_ManifestConflict(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	setFakeGHState(t, tempDir, FakeGHState{
		Mergeable: "MERGEABLE",
		Checks: []CheckItem{
			{Status: "COMPLETED", Conclusion: "SUCCESS", Name: "ci"},
		},
	})

	// PR worktree adds pr_item.txt
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-manifest")
	mustWrite(t, filepath.Join(wt, "pr_item.txt"), "pr item\n")
	runCmd(t, wt, "git", "add", "pr_item.txt")
	runCmd(t, wt, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "commit", "-m", "pr item commit")

	// Main adds main_item.txt and commits/pushes
	seedDir := filepath.Join(tempDir, "seed")
	mustWrite(t, filepath.Join(seedDir, "main_item.txt"), "main item\n")
	runCmd(t, seedDir, "git", "add", "main_item.txt")
	runCmd(t, seedDir, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, seedDir, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, seedDir, "git", "commit", "-m", "main item commit")
	runCmd(t, seedDir, "git", "push", "origin", "main")

	statusFile := filepath.Join(tempDir, "prq-status.jsonl")
	cfg := Config{
		MainBranch:        "main",
		Remote:            "origin",
		StatusFile:        statusFile,
		PollInterval:      10,
		MaxPollAttempts:   5,
		InitialBackoffMs:  10,
		MaxBackoffMs:      50,
		MaxNetworkRetries: 3,
	}

	err := RunQueue(cfg, []PRItem{{Number: 102, Worktree: wt}})
	if err != nil {
		t.Fatalf("expected RunQueue to succeed on manifest conflict auto-resolve, got: %v", err)
	}

	actions, _ := readGHActions(tempDir)
	merged := false
	for _, act := range actions {
		if len(act.Args) >= 2 && act.Args[0] == "pr" && act.Args[1] == "merge" {
			merged = true
			break
		}
	}
	if !merged {
		t.Fatalf("expected PR 102 to be merged after manifest auto-resolve")
	}

	// Verify both pr_item.txt and main_item.txt exist in manifest
	manifestContent, _ := os.ReadFile(filepath.Join(wt, "distribution", "PACK-MANIFEST.json"))
	if !strings.Contains(string(manifestContent), "pr_item.txt") || !strings.Contains(string(manifestContent), "main_item.txt") {
		t.Fatalf("manifest does not contain both files: %s", string(manifestContent))
	}
}

// 3. Notes conflict
func TestPRQ_NotesConflict(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	setFakeGHState(t, tempDir, FakeGHState{
		Mergeable: "MERGEABLE",
		Checks: []CheckItem{
			{Status: "COMPLETED", Conclusion: "SUCCESS", Name: "ci"},
		},
	})

	notesPath := filepath.Join("release", "RELEASE_NOTES_0.0.8.md")

	// PR worktree edits release notes
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-notes")
	mustWrite(t, filepath.Join(wt, notesPath), "# Release Notes 0.0.8\n\n## Changes\n- PR contribution line\n")
	runCmd(t, wt, "git", "add", notesPath)
	runCmd(t, wt, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "commit", "-m", "pr notes commit")

	// Main edits release notes at the same spot
	seedDir := filepath.Join(tempDir, "seed")
	mustWrite(t, filepath.Join(seedDir, notesPath), "# Release Notes 0.0.8\n\n## Changes\n- Main contribution line\n")
	runCmd(t, seedDir, "git", "add", notesPath)
	runCmd(t, seedDir, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, seedDir, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, seedDir, "git", "commit", "-m", "main notes commit")
	runCmd(t, seedDir, "git", "push", "origin", "main")

	statusFile := filepath.Join(tempDir, "prq-status.jsonl")
	cfg := Config{
		MainBranch:        "main",
		Remote:            "origin",
		StatusFile:        statusFile,
		PollInterval:      10,
		MaxPollAttempts:   5,
		InitialBackoffMs:  10,
		MaxBackoffMs:      50,
		MaxNetworkRetries: 3,
	}

	err := RunQueue(cfg, []PRItem{{Number: 103, Worktree: wt}})
	if err != nil {
		t.Fatalf("expected RunQueue to succeed with notes auto-resolve, got: %v", err)
	}

	resolvedNotes, _ := os.ReadFile(filepath.Join(wt, notesPath))
	notesStr := string(resolvedNotes)
	if strings.Contains(notesStr, "<<<<<<<") || strings.Contains(notesStr, ">>>>>>>") {
		t.Fatalf("conflict markers remain in release notes: %s", notesStr)
	}

	mainIdx := strings.Index(notesStr, "- Main contribution line")
	prIdx := strings.Index(notesStr, "- PR contribution line")
	if mainIdx == -1 || prIdx == -1 {
		t.Fatalf("both sides not found in release notes: %s", notesStr)
	}
	if mainIdx > prIdx {
		t.Fatalf("expected main to come first before PR in release notes: %s", notesStr)
	}
}

// 4. Other conflict stops
func TestPRQ_OtherConflictStops(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	setFakeGHState(t, tempDir, FakeGHState{
		Mergeable: "MERGEABLE",
		Checks:    []CheckItem{{Status: "COMPLETED", Conclusion: "SUCCESS"}},
	})

	// PR worktree creates code.txt with conflict
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-other-conflict")
	mustWrite(t, filepath.Join(wt, "code.txt"), "pr version of code\n")
	runCmd(t, wt, "git", "add", "code.txt")
	runCmd(t, wt, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "commit", "-m", "pr code commit")

	// Main creates code.txt with conflicting version
	seedDir := filepath.Join(tempDir, "seed")
	mustWrite(t, filepath.Join(seedDir, "code.txt"), "main version of code\n")
	runCmd(t, seedDir, "git", "add", "code.txt")
	runCmd(t, seedDir, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, seedDir, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, seedDir, "git", "commit", "-m", "main code commit")
	runCmd(t, seedDir, "git", "push", "origin", "main")

	statusFile := filepath.Join(tempDir, "prq-status.jsonl")
	cfg := Config{
		MainBranch:        "main",
		Remote:            "origin",
		StatusFile:        statusFile,
		PollInterval:      10,
		MaxPollAttempts:   5,
		InitialBackoffMs:  10,
		MaxBackoffMs:      50,
		MaxNetworkRetries: 3,
	}

	err := RunQueue(cfg, []PRItem{{Number: 104, Worktree: wt}})
	// The PR must stop and be reported
	if err == nil {
		t.Fatalf("expected error/stop on unsupported conflict")
	}

	actions, _ := readGHActions(tempDir)
	for _, act := range actions {
		if len(act.Args) >= 2 && act.Args[0] == "pr" && act.Args[1] == "merge" {
			t.Fatalf("gh pr merge was called but PR 104 should have stopped on conflict")
		}
	}

	// Verify status file recorded the conflict stop
	statusContent, err := os.ReadFile(statusFile)
	if err != nil {
		t.Fatalf("could not read status file: %v", err)
	}
	if !strings.Contains(string(statusContent), "conflict") || !strings.Contains(string(statusContent), "code.txt") {
		t.Fatalf("status file did not record conflict in code.txt: %s", string(statusContent))
	}
}

// 5. Network retry
func TestPRQ_NetworkRetry(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	// 2 network failures before succeeding
	setFakeGHState(t, tempDir, FakeGHState{
		NetworkFailuresRemaining: 2,
		Mergeable:                "MERGEABLE",
		Checks:                   []CheckItem{{Status: "COMPLETED", Conclusion: "SUCCESS"}},
	})

	wt := createPRWorktree(t, tempDir, bareRepo, "feature-network-retry")
	mustWrite(t, filepath.Join(wt, "retry.txt"), "retry test\n")
	runCmd(t, wt, "git", "add", "retry.txt")
	runCmd(t, wt, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "commit", "-m", "retry commit")

	statusFile := filepath.Join(tempDir, "prq-status.jsonl")
	cfg := Config{
		MainBranch:        "main",
		Remote:            "origin",
		StatusFile:        statusFile,
		PollInterval:      10,
		MaxPollAttempts:   5,
		InitialBackoffMs:  10,
		MaxBackoffMs:      50,
		MaxNetworkRetries: 5,
	}

	err := RunQueue(cfg, []PRItem{{Number: 105, Worktree: wt}})
	if err != nil {
		t.Fatalf("expected RunQueue to succeed after network retries, got: %v", err)
	}

	// Verify status file recorded retries
	statusContent, _ := os.ReadFile(statusFile)
	if !strings.Contains(string(statusContent), "retry") {
		t.Fatalf("expected retry status lines in status file: %s", string(statusContent))
	}
}

// 6. Conflicting PR reported
func TestPRQ_ConflictingPRReported(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	// GitHub reports CONFLICTING
	setFakeGHState(t, tempDir, FakeGHState{
		Mergeable: "CONFLICTING",
		Checks:    []CheckItem{},
	})

	wt := createPRWorktree(t, tempDir, bareRepo, "feature-conflicting-pr")
	mustWrite(t, filepath.Join(wt, "conflict_pr.txt"), "conflict pr test\n")
	runCmd(t, wt, "git", "add", "conflict_pr.txt")
	runCmd(t, wt, "python3", "tools/release_verify.py", "--generate", ".", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "add", "distribution/PACK-MANIFEST.json")
	runCmd(t, wt, "git", "commit", "-m", "conflict pr commit")

	statusFile := filepath.Join(tempDir, "prq-status.jsonl")
	cfg := Config{
		MainBranch:        "main",
		Remote:            "origin",
		StatusFile:        statusFile,
		PollInterval:      10,
		MaxPollAttempts:   5,
		InitialBackoffMs:  10,
		MaxBackoffMs:      50,
		MaxNetworkRetries: 3,
	}

	err := RunQueue(cfg, []PRItem{{Number: 106, Worktree: wt}})
	if err == nil {
		t.Fatalf("expected error/stop when PR is CONFLICTING on GitHub")
	}

	// Make sure gh pr merge was not called
	actions, _ := readGHActions(tempDir)
	for _, act := range actions {
		if len(act.Args) >= 2 && act.Args[0] == "pr" && act.Args[1] == "merge" {
			t.Fatalf("gh pr merge was called for CONFLICTING PR")
		}
	}

	// Verify status file recorded CONFLICTING
	statusContent, _ := os.ReadFile(statusFile)
	if !strings.Contains(string(statusContent), "CONFLICTING") && !strings.Contains(string(statusContent), "conflicting") {
		t.Fatalf("expected conflicting status in status file: %s", string(statusContent))
	}
}

func TestEvaluateChecksWaitsForPositiveEvidence(t *testing.T) {
	for _, checks := range [][]CheckItem{nil, {{Status: "WAITING"}}, {{Status: "COMPLETED"}}, {{Status: "REQUESTED"}}} {
		pass, pending, failure, _ := evaluateChecks(checks)
		if pass || !pending || failure {
			t.Fatalf("premature approval for %+v", checks)
		}
	}
	pass, pending, failure, _ := evaluateChecks([]CheckItem{{Status: "COMPLETED", Conclusion: "SUCCESS"}, {State: "SUCCESS"}})
	if !pass || pending || failure {
		t.Fatal("successful checks rejected")
	}
}

func TestPRQ_DirtyWorktreeStopsBeforeFetch(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-dirty")
	mustWrite(t, filepath.Join(wt, "operator.txt"), "keep me")
	if err := ProcessPR(Config{}, PRItem{Number: 42, Worktree: wt}, NewStatusLogger("")); err == nil {
		t.Fatal("dirty tree accepted")
	}
	if got, err := os.ReadFile(filepath.Join(wt, "operator.txt")); err != nil || string(got) != "keep me" {
		t.Fatal("operator work changed")
	}
	actions, _ := readGHActions(tempDir)
	if len(actions) != 0 {
		t.Fatal("gh called for dirty tree")
	}
}

func TestPRQ_CommitsRegeneratedManifest(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	setFakeGHState(t, tempDir, FakeGHState{Mergeable: "MERGEABLE", Checks: []CheckItem{{Status: "COMPLETED", Conclusion: "SUCCESS"}}})
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-stale-manifest")
	mustWrite(t, filepath.Join(wt, "feature.txt"), "new content")
	runCmd(t, wt, "git", "add", "feature.txt")
	runCmd(t, wt, "git", "commit", "-m", "feature without manifest")
	if err := ProcessPR(Config{}, PRItem{Number: 43, Worktree: wt}, NewStatusLogger("")); err != nil {
		t.Fatal(err)
	}
	if got := runCmd(t, wt, "git", "status", "--porcelain"); strings.TrimSpace(got) != "" {
		t.Fatalf("dirty result: %s", got)
	}
	runCmd(t, wt, "python3", "tools/release_verify.py", ".", "distribution/PACK-MANIFEST.json")
	if got := strings.TrimSpace(runCmd(t, wt, "git", "log", "-1", "--format=%an")); got != "PRQ Tester" {
		t.Fatalf("changed author: %s", got)
	}
}

func TestPRQ_EmptyChecksCannotMerge(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	setFakeGHState(t, tempDir, FakeGHState{Mergeable: "MERGEABLE"})
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-no-ci")
	err := ProcessPR(Config{PollInterval: 1, MaxPollAttempts: 2}, PRItem{Number: 44, Worktree: wt}, NewStatusLogger(""))
	if err == nil {
		t.Fatal("merged before CI registered")
	}
	actions, _ := readGHActions(tempDir)
	for _, act := range actions {
		if len(act.Args) > 1 && act.Args[1] == "merge" {
			t.Fatal("merge called without CI")
		}
	}
}

func TestPRQ_StaleGreenHeadCannotMerge(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	setFakeGHState(t, tempDir, FakeGHState{HeadRefOID: strings.Repeat("a", 40), Mergeable: "MERGEABLE", Checks: []CheckItem{{Status: "COMPLETED", Conclusion: "SUCCESS"}}})
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-stale-ci")
	if err := ProcessPR(Config{PollInterval: 1, MaxPollAttempts: 1}, PRItem{Number: 45, Worktree: wt}, NewStatusLogger("")); err == nil {
		t.Fatal("stale green commit accepted")
	}
	actions, _ := readGHActions(tempDir)
	for _, action := range actions {
		if len(action.Args) > 1 && action.Args[1] == "merge" {
			t.Fatal("merged stale CI")
		}
	}
}

func TestPRQ_StartupFailureStops(t *testing.T) {
	pass, pending, failed, _ := evaluateChecks([]CheckItem{{Status: "COMPLETED", Conclusion: "STARTUP_FAILURE"}})
	if pass || pending || !failed {
		t.Fatal("startup failure not rejected")
	}
}

func TestPRQ_MergeCommitFailureAborts(t *testing.T) {
	tempDir, bareRepo := setupTestEnv(t)
	wt := createPRWorktree(t, tempDir, bareRepo, "feature-hook-failure")
	notes := "release/RELEASE_NOTES_0.0.8.md"
	for _, item := range []struct{ dir, text string }{{wt, "feature"}, {filepath.Join(tempDir, "seed"), "main"}} {
		mustWrite(t, filepath.Join(item.dir, notes), item.text+"\n")
		runCmd(t, item.dir, "git", "add", notes)
		runCmd(t, item.dir, "git", "commit", "-m", item.text)
	}
	runCmd(t, filepath.Join(tempDir, "seed"), "git", "push", "origin", "main")
	before := strings.TrimSpace(runCmd(t, wt, "git", "rev-parse", "HEAD"))
	hook := filepath.Join(wt, ".git", "hooks", "pre-commit")
	mustWrite(t, hook, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(hook, 0755); err != nil {
		t.Fatal(err)
	}
	statusFile := filepath.Join(tempDir, "status.jsonl")
	if err := ProcessPR(Config{}, PRItem{Number: 46, Worktree: wt}, NewStatusLogger(statusFile)); err == nil {
		t.Fatal("failing hook accepted")
	}
	if out := strings.TrimSpace(runCmd(t, wt, "git", "status", "--porcelain")); out != "" {
		t.Fatalf("dirty after abort: %s", out)
	}
	if after := strings.TrimSpace(runCmd(t, wt, "git", "rev-parse", "HEAD")); after != before {
		t.Fatal("branch changed after failed merge")
	}
	if err := exec.Command("git", "-C", wt, "rev-parse", "--verify", "MERGE_HEAD").Run(); err == nil {
		t.Fatal("merge remains active")
	}
	events, err := os.ReadFile(statusFile)
	if err != nil || !strings.Contains(string(events), `"step":"processing","status":"failed"`) {
		t.Fatalf("no terminal failure: %s %v", events, err)
	}
}

func TestPRQ_MainBranchRejected(t *testing.T) {
	tempDir, _ := setupTestEnv(t)
	if err := ProcessPR(Config{}, PRItem{Number: 47, Worktree: filepath.Join(tempDir, "seed")}, NewStatusLogger("")); err == nil {
		t.Fatal("main accepted")
	}
}
