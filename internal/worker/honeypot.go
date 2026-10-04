package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var ErrHoneypot = errors.New("honeypot token detected; task stopped and must not be merged")

// Honeypot owns synthetic, unissued credentials and evidence outside the project.
// Its HOME is private scratch storage, mounted only into the worker sandbox.
type Honeypot struct {
	Home                 string
	Env                  []string
	worktree, base, root string
	tokens               []string
	mu                   sync.Mutex
	hit                  bool
	tails                map[string][]byte
	matches              []byte
}

func NewHoneypot(worktree string) (*Honeypot, error) {
	root, err := os.MkdirTemp("/tmp", "marshal-honeypot-")
	if err != nil {
		return nil, err
	}
	h := &Honeypot{root: root, Home: filepath.Join(root, "home"), worktree: worktree}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(root)
		}
	}()
	head, err := h.git(context.Background(), "rev-parse", "HEAD")
	if err == nil {
		h.base = strings.TrimSpace(string(head))
	}
	for _, prefix := range []string{"ghp_", "sk-proj-", "AKIA0", ""} {
		size := 20
		if prefix == "ghp_" {
			size = 18
		}
		if prefix == "AKIA0" {
			size = 10
		}
		random := make([]byte, size)
		if _, err := rand.Read(random); err != nil {
			return nil, err
		}
		value := prefix + hex.EncodeToString(random)
		if prefix == "AKIA0" {
			// A reserved zero keeps this synthetic access key unusable.
			value = prefix + base32.StdEncoding.EncodeToString(random)[:15]
		}
		h.tokens = append(h.tokens, value)
	}
	for i, key := range []string{"GITHUB_TOKEN", "OPENAI_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		h.Env = append(h.Env, key+"="+h.tokens[i])
	}
	files := map[string]string{
		".aws/credentials":     "[default]\naws_access_key_id = " + h.tokens[2] + "\naws_secret_access_key = " + h.tokens[3] + "\n",
		".config/gh/hosts.yml": "github.com:\n    oauth_token: " + h.tokens[0] + "\n    git_protocol: https\n",
		".env":                 strings.Join(h.Env, "\n") + "\n",
	}
	for name, content := range files {
		path := filepath.Join(h.Home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			return nil, err
		}
	}
	ok = true
	return h, nil
}

func (h *Honeypot) Contains(data []byte) bool {
	for _, token := range h.tokens {
		if bytes.Contains(data, []byte(token)) {
			return true
		}
	}
	return false
}

func (h *Honeypot) gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "diff.external=", "-c", "core.pager=cat", "-C", h.worktree}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_PAGER=cat")
	return cmd
}

func (h *Honeypot) git(ctx context.Context, args ...string) ([]byte, error) {
	cmd := h.gitCommand(ctx, args...)
	out := &limitedBuffer{limit: 16 << 20}
	cmd.Stdout = out
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("honeypot cannot inspect hand-in: %w", err)
	}
	if out.Truncated() {
		return nil, fmt.Errorf("honeypot: git evidence exceeds scan limit")
	}
	return out.Bytes(), nil
}

// Observe scans the entire stream, even bytes past the output capture limit.
// A rolling tail finds a token split across writes without retaining clean text.
func (h *Honeypot) Observe(stream string, data []byte) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.matches) > 0 {
		return true
	}
	if h.tails == nil {
		h.tails = make(map[string][]byte)
	}
	value := append(h.tails[stream], data...)
	found := h.Contains(value)
	if found {
		for _, token := range h.tokens {
			if bytes.Contains(value, []byte(token)) {
				h.matches = append(h.matches, []byte(token+"\n")...)
			}
		}
	}
	keep := 0
	for _, token := range h.tokens {
		if len(token)-1 > keep {
			keep = len(token) - 1
		}
	}
	if len(value) > keep {
		value = value[len(value)-keep:]
	}
	h.tails[stream] = append([]byte(nil), value...)
	return found
}

// Check runs before delivery. A sticky hit cannot be erased by a later clean run.
func (h *Honeypot) Check(ctx context.Context, stdout, stderr []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hit {
		return fmt.Errorf("%w; evidence: %s", ErrHoneypot, filepath.Join(h.root, "evidence.json"))
	}
	evidence := map[string]any{"stdout": string(stdout), "stderr": string(stderr), "stream_matches": string(h.matches)}
	if len(h.matches) > 0 || h.Contains(stdout) || h.Contains(stderr) {
		return h.reject(evidence)
	}
	if h.base == "" {
		return fmt.Errorf("honeypot: hand-in requires an initial git commit")
	}
	messages, err := h.git(ctx, "log", "--format=%B", h.base+"..HEAD")
	if err != nil {
		return err
	}
	evidence["commit_messages"] = string(messages)
	if h.Contains(messages) {
		return h.reject(evidence)
	}
	diff, err := h.git(ctx, "diff", "--no-ext-diff", "--no-textconv", h.base)
	if err != nil {
		return err
	}
	evidence["diff"] = string(diff)
	if h.Contains(diff) {
		return h.reject(evidence)
	}
	// A file removed by a later commit still travels in the handed-in history.
	objects, err := h.git(ctx, "rev-list", "--objects", "--no-object-names", h.base+"..HEAD")
	if err != nil {
		return err
	}
	for _, oid := range strings.Fields(string(objects)) {
		kind, err := h.git(ctx, "cat-file", "-t", oid)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(kind)) != "blob" {
			continue
		}
		command := h.gitCommand(ctx, "cat-file", "blob", oid)
		output, err := command.StdoutPipe()
		if err != nil {
			return err
		}
		if err := command.Start(); err != nil {
			return err
		}
		match, scanErr := h.scanFile(output)
		_ = output.Close()
		waitErr := command.Wait()
		if match != nil {
			evidence["blob:"+oid] = match
			return h.reject(evidence)
		}
		if scanErr != nil || waitErr != nil {
			return errors.Join(scanErr, waitErr)
		}
	}
	// Inspect raw file bytes as well: binary diffs do not expose literal tokens.
	names, err := h.git(ctx, "diff", "--name-only", "-z", h.base)
	if err != nil {
		return err
	}
	untracked, err := h.git(ctx, "ls-files", "-z", "--others", "--exclude-standard")
	names = append(names, untracked...)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(h.worktree)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range strings.Split(string(names), "\x00") {
		if name == "" {
			continue
		}
		if !filepath.IsLocal(name) {
			return fmt.Errorf("honeypot: invalid repository path")
		}
		if h.Contains([]byte(name)) {
			evidence["file_path:"+name] = []byte(name)
			return h.reject(evidence)
		}
		path := filepath.Join(h.worktree, name)
		// Never follow a worker-controlled symlink, including parent directories.
		safe := true
		for p := path; p != filepath.Clean(h.worktree); p = filepath.Dir(p) {
			info, err := os.Lstat(p)
			if os.IsNotExist(err) {
				safe = false
				break
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				safe = false
				break
			}
		}
		if !safe {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		match, scanErr := h.scanFile(file)
		_ = file.Close()
		if scanErr != nil {
			return scanErr
		}
		if match != nil {
			evidence["file:"+name] = match
			return h.reject(evidence)
		}
	}
	return nil
}

func (h *Honeypot) scanFile(file io.Reader) ([]byte, error) {
	buffer := make([]byte, 32<<10)
	var tail []byte
	for {
		n, err := file.Read(buffer)
		value := append(tail, buffer[:n]...)
		if h.Contains(value) {
			return value, nil
		}
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		keep := 0
		for _, token := range h.tokens {
			if len(token)-1 > keep {
				keep = len(token) - 1
			}
		}
		if len(value) > keep {
			value = value[len(value)-keep:]
		}
		tail = append([]byte(nil), value...)
	}
}

func (h *Honeypot) reject(evidence map[string]any) error {
	h.hit = true
	data, err := json.Marshal(evidence)
	if err == nil {
		err = os.WriteFile(filepath.Join(h.root, "evidence.json"), data, 0600)
	}
	if err != nil {
		return errors.Join(ErrHoneypot, fmt.Errorf("retain evidence: %w", err))
	}
	return fmt.Errorf("%w; evidence: %s", ErrHoneypot, filepath.Join(h.root, "evidence.json"))
}

func (h *Honeypot) Redact(data []byte) []byte {
	for _, token := range h.tokens {
		data = bytes.ReplaceAll(data, []byte(token), []byte("[honeypot]"))
	}
	return data
}

// Close removes clean scratch storage; hit evidence remains for the operator.
func (h *Honeypot) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hit {
		return nil
	}
	return os.RemoveAll(h.root)
}
