package project

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

func FindBinary(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		switch name {
		case "codex":
			pattern := filepath.Join(home, ".codex", "packages", "standalone", "releases", "*", "bin", "codex")
			if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
				sort.Slice(matches, func(i, j int) bool {
					dirI := filepath.Base(filepath.Dir(filepath.Dir(matches[i])))
					dirJ := filepath.Base(filepath.Dir(filepath.Dir(matches[j])))
					return compareSemver(dirI, dirJ) < 0
				})
				return matches[len(matches)-1], nil
			}
		case "gemini":
			pattern := filepath.Join(home, ".gemini", "antigravity-ide", "bin", "gemini")
			if info, err := os.Stat(pattern); err == nil && !info.IsDir() {
				return pattern, nil
			}
		}
	}
	return "", fmt.Errorf("%s binary missing", name)
}

func compareSemver(v1, v2 string) int {
	p1 := parseSemver(v1)
	p2 := parseSemver(v2)
	maxLen := len(p1)
	if len(p2) > maxLen {
		maxLen = len(p2)
	}
	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(p1) {
			n1 = p1[i]
		}
		if i < len(p2) {
			n2 = p2[i]
		}
		if n1 != n2 {
			if n1 < n2 {
				return -1
			}
			return 1
		}
	}
	if v1 < v2 {
		return -1
	} else if v1 > v2 {
		return 1
	}
	return 0
}

func parseSemver(v string) []int {
	var nums []int
	var cur int
	var inNum bool
	for i := 0; i < len(v); i++ {
		ch := v[i]
		if ch >= '0' && ch <= '9' {
			cur = cur*10 + int(ch-'0')
			inNum = true
		} else {
			if inNum {
				nums = append(nums, cur)
				cur = 0
				inNum = false
			}
			if ch == '-' || ch == '+' {
				break
			}
		}
	}
	if inNum {
		nums = append(nums, cur)
	}
	return nums
}

type Layout struct {
	Root       string
	Branch     string
	HEAD       string
	RuntimeDir string
	Database   string
	Socket     string
	PID        string
	Artifacts  string
	Worktrees  string
	Logs       string
}

func Discover(start string) (Layout, error) {
	root, err := gitOutput(start, "rev-parse", "--show-toplevel")
	if err != nil {
		return Layout{}, fmt.Errorf("discover repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve repository root: %w", err)
	}
	branch, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return Layout{}, fmt.Errorf("discover branch: %w", err)
	}
	head, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return Layout{}, fmt.Errorf("discover HEAD: %w", err)
	}
	runtimeDir := filepath.Join(root, ".slaves")
	return Layout{
		Root:       root,
		Branch:     branch,
		HEAD:       head,
		RuntimeDir: runtimeDir,
		Database:   filepath.Join(runtimeDir, "state.db"),
		Socket:     filepath.Join(runtimeDir, "runtime.sock"),
		PID:        filepath.Join(runtimeDir, "pid"),
		Artifacts:  filepath.Join(runtimeDir, "artifacts"),
		Worktrees:  filepath.Join(runtimeDir, "worktrees"),
		Logs:       filepath.Join(runtimeDir, "logs"),
	}, nil
}

func (l Layout) Ensure() error {
	for _, path := range []string{l.RuntimeDir, l.Artifacts, l.Worktrees, l.Logs} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create runtime directory %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("secure runtime directory %s: %w", path, err)
		}
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return string(bytes.TrimSpace(output)), nil
}
