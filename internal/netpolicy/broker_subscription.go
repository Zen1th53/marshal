package netpolicy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Shared across runs: only the native CLI writes auth.json. MARSHAL's lock
// coalesces its own refreshes; it never attempts to lock or mutate native storage.
var codexRefresh = struct {
	sync.Mutex
	states map[string]refreshState
}{states: make(map[string]refreshState)}

type refreshState struct {
	generation uint64
	err        error
}

type subscriptionSource struct {
	path, cli, home string
	claude          bool
}
type subscriptionCredential struct {
	access, account string
	expires         time.Time
	secrets         []string
}

func loadCodexSubscription(path string) (*CredentialBroker, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ErrCredentialBroker
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, ErrCredentialBroker
	}
	source := &subscriptionSource{path: path, home: home}
	if _, err := source.read(); err != nil {
		return nil, err
	}
	cli, err := exec.LookPath("codex")
	if err != nil {
		return nil, errors.New("credential broker: Codex subscription refused: host CLI unavailable")
	}
	source.cli, err = filepath.Abs(cli)
	if err != nil {
		return nil, ErrCredentialBroker
	}
	b, err := NewCredentialBroker("codex", "", "subscription-host-only")
	if err != nil {
		return nil, err
	}
	b.secret = ""
	b.subscription = source
	b.placeholder = placeholderJWT(b.scratchName, time.Now().Add(24*time.Hour))
	b.hosts = map[string]bool{"chatgpt.com": true, "auth.openai.com": true}
	return b, nil
}

func placeholderJWT(account string, expires time.Time) string {
	claims, _ := json.Marshal(map[string]any{"exp": expires.Unix(), "email": "worker@invalid", "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account, "chatgpt_plan_type": "plus"}})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".marshal-placeholder"
}

func (b *CredentialBroker) subscriptionAuth() map[string]any {
	return map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "last_refresh": time.Now().UTC().Format(time.RFC3339), "tokens": map[string]any{"access_token": b.placeholder, "refresh_token": b.scratchName, "id_token": placeholderJWT(b.scratchName, time.Now().Add(24*time.Hour)), "account_id": b.scratchName}}
}

func (s *subscriptionSource) read() (subscriptionCredential, error) {
	if s.claude {
		return s.readClaude()
	}
	// No credential material is retained across requests, including rotated refresh
	// tokens. Unknown formats or incomplete sign-ins never fall back to API mode.
	raw, err := os.ReadFile(s.path)
	if err != nil || len(raw) > 1<<20 {
		return subscriptionCredential{}, ErrCredentialBroker
	}
	var auth struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			ID      string `json:"id_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(raw, &auth) != nil {
		return subscriptionCredential{}, ErrCredentialBroker
	}
	t := auth.Tokens
	parts := strings.Split(t.Access, ".")
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if len(parts) == 3 {
		data, _ := base64.RawURLEncoding.DecodeString(parts[1])
		_ = json.Unmarshal(data, &claims)
	}
	if auth.AuthMode != "chatgpt" || claims.Exp == 0 || t.Account == "" || t.Refresh == "" || t.ID == "" || strings.ContainsAny(t.Access+t.Account, "\r\n\x00") {
		return subscriptionCredential{}, errors.New("credential broker: Codex subscription refused: native refresh lock delegation requires complete managed sign-in metadata")
	}
	return subscriptionCredential{access: t.Access, account: t.Account, expires: time.Unix(claims.Exp, 0), secrets: []string{t.Access, t.Refresh, t.ID, t.Account}}, nil
}

func (s *subscriptionSource) generation() uint64 {
	codexRefresh.Lock()
	defer codexRefresh.Unlock()
	return codexRefresh.states[s.path].generation
}

func (s *subscriptionSource) refresh(ctx context.Context, generation uint64, previous string) error {
	codexRefresh.Lock()
	defer codexRefresh.Unlock()
	state := codexRefresh.states[s.path]
	if state.generation != generation {
		return state.err
	}
	current, err := s.read()
	if err != nil {
		return err
	}
	// A native CLI may already have refreshed while the request was in flight.
	if current.access != previous && time.Until(current.expires) > 2*time.Minute {
		return nil
	}
	err = s.runRefresh(ctx)
	codexRefresh.states[s.path] = refreshState{generation: state.generation + 1, err: err}
	return err
}

func (s *subscriptionSource) runRefresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.cli, "app-server", "--stdio", "-c", `cli_auth_credentials_store="file"`, "-c", "analytics.enabled=false")
	// Constructed exclusively from host state, never the worker envelope. Output
	// can include auth diagnostics: discard stderr and parse only fixed RPC IDs.
	cmd.Env = []string{"HOME=" + s.home, "CODEX_HOME=" + filepath.Dir(s.path), "PATH=" + os.Getenv("PATH")}
	cmd.Dir = filepath.Dir(s.path)
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return ErrCredentialBroker
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return ErrCredentialBroker
	}
	if cmd.Start() != nil {
		return ErrCredentialBroker
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if _, err = io.WriteString(input, "{\"id\":1,\"method\":\"initialize\",\"params\":{\"clientInfo\":{\"name\":\"marshal\",\"version\":\"0.0.5\"},\"capabilities\":null}}\n"); err != nil {
		return ErrCredentialBroker
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	initialized := false
	for scanner.Scan() {
		var reply struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &reply) != nil {
			return ErrCredentialBroker
		}
		if len(reply.Error) != 0 && string(reply.Error) != "null" {
			return ErrCredentialBroker
		}
		if reply.ID == 1 && !initialized {
			if len(reply.Result) == 0 {
				return ErrCredentialBroker
			}
			initialized = true
			if _, err = io.WriteString(input, "{\"method\":\"initialized\"}\n{\"id\":2,\"method\":\"account/read\",\"params\":{\"refreshToken\":true}}\n"); err != nil {
				return ErrCredentialBroker
			}
		} else if reply.ID == 2 && initialized {
			var result struct {
				Account struct {
					Type string `json:"type"`
				} `json:"account"`
			}
			if json.Unmarshal(reply.Result, &result) != nil || result.Account.Type != "chatgpt" {
				return ErrCredentialBroker
			}
			return nil
		}
	}
	return ErrCredentialBroker
}

// Subscription reports whether this run uses managed subscription sign-in.
func (b *CredentialBroker) Subscription() bool { return b != nil && b.subscription != nil }

// Claude refresh belongs entirely to the operator's native host session. There
// is no host command invocation, refresh-token exchange or storage mutation here.
func loadClaudeSubscription(path string) (*CredentialBroker, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrCredentialBroker
	}
	source := &subscriptionSource{path: path, claude: true}
	if _, err = source.read(); err != nil {
		return nil, err
	}
	b, err := NewCredentialBroker("claude", "", "subscription-host-only")
	if err != nil {
		return nil, err
	}
	b.secret = ""
	b.subscription = source
	b.hosts = map[string]bool{"api.anthropic.com": true}
	return b, nil
}

func (s *subscriptionSource) readClaude() (subscriptionCredential, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil || len(raw) > 1<<20 {
		return subscriptionCredential{}, ErrCredentialBroker
	}
	var auth struct {
		OAuth struct {
			Access  string `json:"accessToken"`
			Refresh string `json:"refreshToken"`
			Expires int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(raw, &auth) != nil {
		return subscriptionCredential{}, ErrCredentialBroker
	}
	t := auth.OAuth
	if t.Access == "" || t.Expires <= 0 || strings.ContainsAny(t.Access, "\r\n\x00") {
		return subscriptionCredential{}, errors.New("credential broker: Claude subscription refused: incomplete sign-in metadata; no host refresh lock or refresh command available")
	}
	return subscriptionCredential{access: t.Access, expires: time.UnixMilli(t.Expires), secrets: []string{t.Access, t.Refresh}}, nil
}

func (b *CredentialBroker) claudeAuth() map[string]any {
	return map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": b.placeholder, "refreshToken": b.scratchName,
		"expiresAt":        time.Now().Add(24 * time.Hour).UnixMilli(),
		"scopes":           []string{"user:inference", "user:profile"},
		"subscriptionType": "max", "rateLimitTier": "default_claude_max_5x",
	}}
}

const ClaudeSignInExpiredMessage = "Claude sign-in expired on this machine. Run claude once on the host to refresh it, then retry."

// One operator notification attempt per run, even for overlapping failures.
// Notification errors never authorize a provider request or start a retry loop.
func (p *EgressProxy) alertClaude(ctx context.Context, host string, port int) {
	b := p.broker
	if b.subscription == nil || !b.subscription.claude {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.expiryAlerted {
		b.expiryAlerted = true
		_ = p.recordDecision(ctx, host, port, nil, Decision{Reason: ReasonClaudeSignInExpired})
	}
}

// Recheck Claude exactly once. A native host refresh must have replaced the
// rejected access token with a fresh one before the request can be retried.
func (s *subscriptionSource) renew(ctx context.Context, generation uint64, previous string) (subscriptionCredential, error) {
	if s.claude {
		current, err := s.read()
		if err != nil || current.access == previous || time.Until(current.expires) <= 2*time.Minute {
			return subscriptionCredential{}, ErrCredentialBroker
		}
		return current, nil
	}
	if err := s.refresh(ctx, generation, previous); err != nil {
		return subscriptionCredential{}, err
	}
	return s.read()
}

func (b *CredentialBroker) refusesAuthHost(host string) bool {
	return b != nil && b.subscription != nil && b.subscription.claude && normalizeHost(host) == "platform.claude.com"
}
