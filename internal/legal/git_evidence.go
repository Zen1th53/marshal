package legal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func execGit(ctx context.Context, repoDir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s failed: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func CollectSourceEvidence(ctx context.Context, repoDir string) (*SourceEvidence, error) {
	headBytes, err := execGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("get HEAD: %w", err)
	}
	headSHA := strings.TrimSpace(string(headBytes))

	treeBytes, err := execGit(ctx, repoDir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return nil, fmt.Errorf("get tree SHA: %w", err)
	}
	treeSHA := strings.TrimSpace(string(treeBytes))

	timeBytes, err := execGit(ctx, repoDir, "show", "-s", "--format=%ct", headSHA)
	if err != nil {
		return nil, fmt.Errorf("get commit time: %w", err)
	}
	timeUnix, err := strconv.ParseInt(strings.TrimSpace(string(timeBytes)), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse commit timestamp: %w", err)
	}
	commitTime := time.Unix(timeUnix, 0).UTC()

	parentsBytes, err := execGit(ctx, repoDir, "show", "-s", "--format=%P", headSHA)
	var parentSHAs []string
	if err == nil {
		pStr := strings.TrimSpace(string(parentsBytes))
		if pStr != "" {
			parentSHAs = strings.Fields(pStr)
		}
	}

	branchBytes, _ := execGit(ctx, repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	branch := strings.TrimSpace(string(branchBytes))
	if branch == "HEAD" {
		branch = ""
	}

	shallowFile := filepath.Join(repoDir, ".git", "shallow")
	_, errShallow := os.Stat(shallowFile)
	isShallow := errShallow == nil

	statusBytes, err := execGit(ctx, repoDir, "status", "--porcelain")
	workingTreeClean := true
	if err == nil && len(bytes.TrimSpace(statusBytes)) > 0 {
		workingTreeClean = false
	}

	historyComplete := !isShallow

	goModPath := "github.com/Zen1th53/marshal"
	if data, err := ReadBlob(ctx, repoDir, headSHA, "go.mod"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				goModPath = strings.TrimSpace(strings.TrimPrefix(line, "module "))
				break
			}
		}
	}

	runtimeVersion := "v0.4.0"
	if data, err := ReadBlob(ctx, repoDir, headSHA, "RUNTIME-VERSION.yaml"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "version:") {
				runtimeVersion = strings.Trim(strings.TrimPrefix(line, "version:"), " \"'")
				break
			}
		}
	}

	packVersion := "6.0.0"
	if data, err := ReadBlob(ctx, repoDir, headSHA, "PACK-VERSION.yaml"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "version:") {
				packVersion = strings.Trim(strings.TrimPrefix(line, "version:"), " \"'")
				break
			}
		}
	}

	status := StatusPass
	if isShallow {
		status = StatusReviewRequired
	}

	return &SourceEvidence{
		HeadSHA:          headSHA,
		TreeSHA:          treeSHA,
		CommitTime:       commitTime,
		ParentSHAs:       parentSHAs,
		Branch:           branch,
		IsShallow:        isShallow,
		WorkingTreeClean: workingTreeClean,
		HistoryComplete:  historyComplete,
		GoModulePath:     goModPath,
		RuntimeVersion:   runtimeVersion,
		PackVersion:      packVersion,
		Status:           status,
	}, nil
}

func ReadBlob(ctx context.Context, repoDir string, headSHA string, relPath string) ([]byte, error) {
	relPath = filepath.Clean(relPath)
	if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return nil, fmt.Errorf("invalid path traversal attempt: %s", relPath)
	}
	spec := fmt.Sprintf("%s:%s", headSHA, relPath)
	return execGit(ctx, repoDir, "show", spec)
}

func CollectCommitAncestry(ctx context.Context, repoDir string, headSHA string) ([]CommitInfo, []AuthorSummary, error) {
	format := "%H%x00%P%x00%an%x00%ae%x00%at%x00%cn%x00%ce%x00%ct%x00%s%x01"
	out, err := execGit(ctx, repoDir, "log", fmt.Sprintf("--format=%s", format), "--reverse", headSHA)
	if err != nil {
		return nil, nil, fmt.Errorf("git log failed: %w", err)
	}

	records := strings.Split(string(out), "\x01")
	var commits []CommitInfo
	authorMap := make(map[string]*AuthorSummary)

	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		parts := strings.Split(rec, "\x00")
		if len(parts) < 9 {
			continue
		}

		sha := parts[0]
		parents := strings.Fields(parts[1])
		authorName := parts[2]
		authorEmail := parts[3]
		atUnix, _ := strconv.ParseInt(parts[4], 10, 64)
		committerName := parts[5]
		committerEmail := parts[6]
		ctUnix, _ := strconv.ParseInt(parts[7], 10, 64)
		subject := parts[8]

		aTime := time.Unix(atUnix, 0).UTC()
		cTime := time.Unix(ctUnix, 0).UTC()

		c := CommitInfo{
			SHA:            sha,
			ParentSHAs:     parents,
			AuthorName:     authorName,
			AuthorEmail:    authorEmail,
			AuthorTime:     aTime,
			CommitterName:  committerName,
			CommitterEmail: committerEmail,
			CommitterTime:  cTime,
			Subject:        subject,
		}
		commits = append(commits, c)

		key := fmt.Sprintf("%s <%s>", authorName, authorEmail)
		if summary, exists := authorMap[key]; exists {
			summary.CommitCount++
			if aTime.Before(summary.FirstCommitTime) {
				summary.FirstCommitTime = aTime
			}
			if aTime.After(summary.LastCommitTime) {
				summary.LastCommitTime = aTime
			}
		} else {
			authorMap[key] = &AuthorSummary{
				Name:            authorName,
				Email:           authorEmail,
				CommitCount:     1,
				FirstCommitTime: aTime,
				LastCommitTime:  aTime,
			}
		}
	}

	var authors []AuthorSummary
	for _, summary := range authorMap {
		authors = append(authors, *summary)
	}
	sort.Slice(authors, func(i, j int) bool {
		if authors[i].CommitCount != authors[j].CommitCount {
			return authors[i].CommitCount > authors[j].CommitCount
		}
		return authors[i].Email < authors[j].Email
	})

	return commits, authors, nil
}

func HashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
