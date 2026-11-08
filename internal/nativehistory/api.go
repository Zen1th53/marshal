package nativehistory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/project"
)

type Provider string

const (
	Codex       Provider = "codex"
	Claude      Provider = "claude"
	OpenCode    Provider = "opencode"
	Antigravity Provider = "antigravity"
)

type Option func(*Capture)

func WithBinary(binary string) Option              { return func(c *Capture) { c.binary = binary } }
func WithAuthorize(check func(string) bool) Option { return func(c *Capture) { c.authorized = check } }
func WithTimeout(timeout time.Duration) Option     { return func(c *Capture) { c.runTimeout = timeout } }
func WithClaude(enabled bool) Option               { return func(c *Capture) { c.claude = enabled } }
func WithChatFile(check func(string) bool) Option  { return func(c *Capture) { c.chatFile = check } }
func WithOpenCodeRun(run func(...string) ([]byte, error)) Option {
	return func(c *Capture) {
		// A nil runner must leave openCodeRun nil too. Wrapping nil in the
		// context-adapter closure would make the field non-nil, so sync() would
		// take the OpenCode branch for a codex/claude watch and call the nil
		// run. The legacy single-argument signature stays the public surface;
		// the runner ignores its context, which is already bounded by runTimeout.
		if run == nil {
			c.openCodeRun = nil
			return
		}
		c.openCodeRun = func(_ context.Context, args ...string) ([]byte, error) {
			return run(args...)
		}
	}
}
func WithOpenCodeDB(path string) Option { return func(c *Capture) { c.openCodeDB = path } }
func WithAntigravitySummaries(path string) Option {
	return func(c *Capture) { c.antigravitySummaries = path }
}
func WithSeen(seen map[string]string) Option {
	return func(c *Capture) {
		if seen != nil {
			c.seen = seen
		}
	}
}

// Resume carries where earlier syncs stopped reading each Claude history
// file from one Capture to the next. A caller that builds a Capture per
// sync passes the same Resume every time; without it each new Capture reads
// every changed history from its first byte again.
type Resume struct{ claude map[string]claudeResume }

// NewResume returns an empty Resume.
func NewResume() *Resume { return &Resume{claude: map[string]claudeResume{}} }

// WithResume shares r with the Capture, which reads and updates it.
func WithResume(r *Resume) Option {
	return func(c *Capture) {
		if r == nil {
			return
		}
		if r.claude == nil {
			r.claude = map[string]claudeResume{}
		}
		c.claudeResume = r.claude
	}
}

func WithIndexState(saved, dirty bool) Option {
	return func(c *Capture) { c.indexSaved, c.indexDirty = saved, dirty }
}
func WithConsume(consume func(importer.SessionTranscript) error) Option {
	return func(c *Capture) { c.consume = consume }
}
func WithObserveSession(observe func(importer.SessionTranscript) error) Option {
	return func(c *Capture) { c.observeSession = observe }
}
func WithCaptureTools(enabled bool) Option { return func(c *Capture) { c.captureTools = enabled } }
func WithIndex(path string) Option         { return func(c *Capture) { c.indexPath = path } }

func New(provider Provider, root string, opts ...Option) (*Capture, error) {
	var c *Capture
	var err error
	switch provider {
	case Codex, Claude:
		dir, e := HistoryDir(provider, root)
		if e != nil {
			return nil, e
		}
		c = newNativeHistoryWatch(dir, root)
		c.claude = provider == Claude
	case OpenCode:
		binary, e := project.FindBinary("opencode")
		if e != nil {
			return nil, e
		}
		c = newOpenCodeHistoryWatch(binary, root)
	case Antigravity:
		c, err = newAntigravityHistoryWatch(root)
	default:
		return nil, fmt.Errorf("unsupported native history provider %q", provider)
	}
	if err != nil {
		return nil, err
	}
	for _, option := range opts {
		option(c)
	}
	if provider == OpenCode {
		c.configureOpenCodeRunner(c.binary)
	}
	if c.consume == nil {
		c.consume = func(importer.SessionTranscript) error { return nil }
	}
	return c, nil
}

func NewAt(provider Provider, dir, root string, opts ...Option) (*Capture, error) {
	if provider != Codex && provider != Claude && provider != OpenCode && provider != Antigravity {
		return nil, fmt.Errorf("unsupported native history provider %q", provider)
	}
	c := newNativeHistoryWatch(dir, root)
	c.claude = provider == Claude
	c.antigravity = provider == Antigravity
	for _, option := range opts {
		option(c)
	}
	if c.consume == nil {
		c.consume = func(importer.SessionTranscript) error { return nil }
	}
	return c, nil
}

func (c *Capture) Baseline(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.loadIndex(); err != nil {
		return err
	}
	if c.openCodeRun != nil {
		return c.primeOpenCode(ctx)
	}
	if c.antigravity {
		return c.primeAntigravity(ctx)
	}
	if c.authorized != nil && !c.authorized(c.dir) {
		return nil
	}
	walkErr := filepath.WalkDir(c.dir, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		c.seen[path] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		c.indexDirty = true
		return nil
	})
	return errorsJoin(walkErr, c.saveIndexIfChanged())
}

func (c *Capture) Sync(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.sync(ctx)
}

func (c *Capture) SyncFile(path string) error {
	return c.syncFile(context.Background(), path)
}
func (c *Capture) WorkspaceMatches(uris []string) bool { return c.antigravityWorkspaceMatches(uris) }

type OpenCodeLiveSession struct {
	SessionID string
	Updated   int64
	Document  []byte
}

var ErrOpenCodeSchemaMoved = errOpenCodeSchemaMoved

func OpenCodeDBPath() (string, error) { return openCodeDBPath() }

func ReadOpenCodeLive(dbPath, root string) ([]OpenCodeLiveSession, error) {
	sessions, err := readOpenCodeLive(context.Background(), dbPath, root)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	out := make([]OpenCodeLiveSession, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, OpenCodeLiveSession{SessionID: session.id, Updated: session.updated, Document: session.document})
	}
	return out, nil
}

func SplitWorkspaceURIs(value string) []string { return splitWorkspaceURIs(value) }

func (c *Capture) SaveIndex() error             { return c.saveIndex() }
func (c *Capture) SeenEmpty() bool              { return len(c.seen) == 0 }
func (c *Capture) LoadIndex() error             { return c.loadIndex() }
func (c *Capture) IndexState() (bool, bool)     { return c.indexSaved, c.indexDirty }
func (c *Capture) SeenIndex() map[string]string { return c.seen }

func HistoryDir(provider Provider, root string) (string, error) {
	if provider == OpenCode {
		return openCodeDBPath()
	}
	if provider == Antigravity {
		home, err := antigravityHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "conversations"), nil
	}
	homeEnv, homeDir, historyDir := "CODEX_HOME", ".codex", "sessions"
	if provider == Claude {
		homeEnv, homeDir, historyDir = "CLAUDE_CONFIG_DIR", ".claude", "projects"
	}
	if provider != Codex && provider != Claude {
		return "", fmt.Errorf("provider %q has no JSONL history directory", provider)
	}
	home := os.Getenv(homeEnv)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, homeDir)
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(root, home)
	}
	if provider == Claude {
		slug := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(filepath.Clean(root), "-")
		return filepath.Join(home, historyDir, slug), nil
	}
	return filepath.Join(home, historyDir), nil
}

func FindBinary(provider Provider) (string, error) {
	name := string(provider)
	if provider == Antigravity {
		name = "agy"
	}
	return project.FindBinary(name)
}

func errorsJoin(errs ...error) error {
	var nonnil []error
	for _, err := range errs {
		if err != nil {
			nonnil = append(nonnil, err)
		}
	}
	return errors.Join(nonnil...)
}
