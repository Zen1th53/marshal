package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/hostgit"
)

var ErrStatePreparation = errors.New("project state is not ready")

func FindBinary(name string) (string, error) {
	// A binary is looked up by bare name only. A name carrying a directory
	// part would let the caller pick the file instead of the search path.
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("%q is not a binary name", name)
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if os.Getenv("MARSHAL_PROVIDER_PATH_ONLY") == "1" {
		return "", fmt.Errorf("%s binary missing from PATH", name)
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{
			filepath.Join(home, ".local", "bin"),
			"/usr/local/bin",
			"/usr/bin",
		} {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
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
	if err := statepath.ValidateRoot(start); err != nil {
		return Layout{}, err
	}
	root, err := hostgit.Root(start)
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
	if err := statepath.Prepare(root, repairWorktrees); err != nil {
		return Layout{}, fmt.Errorf("%w: %w", ErrStatePreparation, err)
	}
	if err := statepath.Validate(root); err != nil {
		return Layout{}, fmt.Errorf("%w: %w", ErrStatePreparation, err)
	}
	runtimeDir := statepath.Path(root)
	return Layout{
		Root:       root,
		Branch:     branch,
		HEAD:       head,
		RuntimeDir: runtimeDir,
		Database:   filepath.Join(runtimeDir, "state.db"),
		Socket:     SocketPath(root),
		PID:        filepath.Join(runtimeDir, "pid"),
		Artifacts:  filepath.Join(runtimeDir, "artifacts"),
		Worktrees:  filepath.Join(runtimeDir, "worktrees"),
		Logs:       filepath.Join(runtimeDir, "logs"),
	}, nil
}

func (l Layout) Ensure() error {
	if err := statepath.Prepare(l.Root, repairWorktrees); err != nil {
		return err
	}
	if l.RuntimeDir != statepath.Path(l.Root) || l.Database != statepath.Path(l.Root, "state.db") || l.Socket != SocketPath(l.Root) || l.PID != statepath.Path(l.Root, "pid") || l.Artifacts != statepath.Path(l.Root, "artifacts") || l.Worktrees != statepath.Path(l.Root, "worktrees") || l.Logs != statepath.Path(l.Root, "logs") {
		return fmt.Errorf("layout belongs to a different state namespace")
	}
	if err := statepath.Validate(l.Root); err != nil {
		return err
	}
	if filepath.Dir(l.Socket) != l.RuntimeDir {
		if err := ensureShortSocketDir(filepath.Dir(l.Socket)); err != nil {
			return err
		}
	}
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
	cmd, err := hostgit.Command(context.Background(), dir, args...)
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return string(bytes.TrimSpace(output)), nil
}

// maxSocketPath is a conservative bound below the 108-byte sun_path limit
// (107 usable bytes on Linux, 103 on the BSDs).
const maxSocketPath = 100

// SocketPath returns the runtime socket for a project root. It lives in the
// state directory unless that path would exceed the AF_UNIX limit; then a
// short per-user, per-project path is used. Server and clients both derive it
// from the root, so discovery needs no extra state.
func SocketPath(root string) string {
	inState := statepath.Path(root, "runtime.sock")
	if len(inState) <= maxSocketPath {
		return inState
	}
	sum := sha256.Sum256([]byte(statepath.Path(root)))
	name := hex.EncodeToString(sum[:8]) + ".sock"
	uid := os.Geteuid()
	var candidates []string
	if x := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(x) {
		candidates = append(candidates, filepath.Join(x, "marshal"))
	}
	candidates = append(candidates,
		filepath.Join(os.TempDir(), fmt.Sprintf("marshal-%d", uid)),
		filepath.Join("/tmp", fmt.Sprintf("marshal-%d", uid)))
	for _, dir := range candidates {
		if p := filepath.Join(dir, name); len(p) <= maxSocketPath {
			return p
		}
	}
	return inState
}
