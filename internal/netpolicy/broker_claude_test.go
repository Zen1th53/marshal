package netpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func claudeFixture(t *testing.T, expiry time.Time) (*CredentialBroker, string, []byte) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".credentials.json")
	raw := claudeCredential("real-claude-access-old", expiry)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadCredentialBroker("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Subscription() || !b.handles("api.anthropic.com") || b.handles("platform.claude.com") || !b.refusesAuthHost("platform.claude.com") || b.handles("example.com") {
		t.Fatal("wrong subscription host profile")
	}
	return b, path, raw
}

func claudeCredential(access string, expiry time.Time) []byte {
	raw, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": access, "refreshToken": "real-claude-refresh", "expiresAt": expiry.UnixMilli(), "scopes": []string{"user:inference"}}})
	return raw
}

func assertClaudeUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != string(want) {
		t.Fatal("broker changed host sign-in")
	}
}

func TestBrokerClaudeLatestTokenIsolation(t *testing.T) {
	b, path, original := claudeFixture(t, time.Now().Add(time.Hour))
	secrets := []string{"real-claude-access-old", "real-claude-access-new", "real-claude-refresh"}
	scratch := t.TempDir()
	env, err := b.Prepare(scratch, "/home/marshal", b.caPEM)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSubscriptionSecrets(t, strings.Join(env, "\n"), secrets)
	if !strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_API_KEY=\n") || !strings.Contains(strings.Join(env, "\n"), "CLAUDE_CONFIG_DIR=/home/marshal/") {
		t.Fatal("Claude env not prepared")
	}
	if err = filepath.WalkDir(scratch, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		assertNoSubscriptionSecrets(t, string(raw), secrets)
		if strings.Contains(string(raw), "PRIVATE KEY") {
			t.Fatal("CA private key exported")
		}
		if d.Name() == ".credentials.json" {
			var auth struct {
				OAuth struct {
					Access  string `json:"accessToken"`
					Refresh string `json:"refreshToken"`
					Expires int64  `json:"expiresAt"`
				} `json:"claudeAiOauth"`
			}
			if json.Unmarshal(raw, &auth) != nil || auth.OAuth.Access != b.placeholder || auth.OAuth.Refresh != b.scratchName || time.Until(time.UnixMilli(auth.OAuth.Expires)) < 23*time.Hour {
				t.Fatal("placeholder sign-in not fresh")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var want atomic.Value
	want.Store("real-claude-access-old")
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+want.Load().(string) || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Chatgpt-Account-Id") != "" {
			t.Error("incorrect Claude subscription injection")
		}
		body, _ := io.ReadAll(r.Body)
		assertNoSubscriptionSecrets(t, string(body), secrets)
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		fmt.Fprint(w, r.Header.Get("Authorization")+" real-claude-refresh")
	}))
	client, _ := brokerPipeClient(t, b, provider, nil)
	for i := 0; i < 2; i++ {
		if i == 1 {
			original = claudeCredential("real-claude-access-new", time.Now().Add(time.Hour))
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			want.Store("real-claude-access-new")
		}
		// Claude does not send the Codex account header.
		req, _ := http.NewRequest("POST", "https://127.0.0.1/v1/messages", strings.NewReader(`{"messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+b.placeholder)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		assertNoSubscriptionSecrets(t, string(data)+fmt.Sprint(resp.Header), secrets)
		assertClaudeUnchanged(t, path, original)
	}
}

func TestBrokerClaudeExpiryConcurrentOneAlert(t *testing.T) {
	for _, expiry := range []time.Time{time.Now().Add(-time.Hour), time.Now().Add(time.Minute), time.Now().Add(time.Hour)} {
		t.Run(fmt.Sprint(expiry.UnixNano()), func(t *testing.T) {
			b, path, original := claudeFixture(t, expiry)
			var requests, alerts atomic.Int32
			provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(401) }))
			client, proxy := brokerPipeClient(t, b, provider, nil)
			proxy.attempt = func(_ context.Context, _ string, _ int, d Decision) error {
				if d.Reason == ReasonClaudeSignInExpired {
					alerts.Add(1)
				}
				return nil
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					status, data := subscriptionRequest(t, client, b, "/v1/messages", nil)
					if status != 503 {
						t.Errorf("status %d", status)
					}
					assertNoSubscriptionSecrets(t, data, []string{"real-claude-access-old", "real-claude-refresh"})
				}()
			}
			wg.Wait()
			if alerts.Load() != 1 {
				t.Fatalf("alerts %d", alerts.Load())
			}
			if expiry.Before(time.Now().Add(2*time.Minute)) && requests.Load() != 0 {
				t.Fatal("expired token reached provider")
			}
			if requests.Load() > 8 {
				t.Fatal("retried unchanged token")
			}
			assertClaudeUnchanged(t, path, original)
		})
	}
}

func TestBrokerClaudeHostRotationRetry(t *testing.T) {
	b, path, _ := claudeFixture(t, time.Now().Add(time.Hour))
	next := claudeCredential("real-claude-access-new", time.Now().Add(time.Hour))
	nextPath := filepath.Join(filepath.Dir(path), "next.json")
	if err := os.WriteFile(nextPath, next, 0600); err != nil {
		t.Fatal(err)
	}
	// Only this fake host CLI writes the sign-in, independently of broker code.
	cli := filepath.Join(filepath.Dir(path), "fake-host-cli")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\ncp -- \"$1\" \"$2\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var requests, alerts atomic.Int32
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			if r.Header.Get("Authorization") != "Bearer real-claude-access-old" {
				t.Error("wrong first token")
			}
			if err := exec.Command(cli, nextPath, path).Run(); err != nil {
				t.Error(err)
			}
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer real-claude-access-new" {
			t.Error("wrong retry token")
		}
		fmt.Fprint(w, "real-claude-access-new")
	}))
	client, proxy := brokerPipeClient(t, b, provider, nil)
	proxy.attempt = func(_ context.Context, _ string, _ int, d Decision) error {
		if d.Reason == ReasonClaudeSignInExpired {
			alerts.Add(1)
		}
		return nil
	}
	status, data := subscriptionRequest(t, client, b, "/v1/messages", nil)
	if status != 200 || requests.Load() != 2 || alerts.Load() != 0 {
		t.Fatalf("status %d requests %d alerts %d", status, requests.Load(), alerts.Load())
	}
	assertNoSubscriptionSecrets(t, data, []string{"real-claude-access-old", "real-claude-access-new", "real-claude-refresh"})
	assertClaudeUnchanged(t, path, next)
}

func TestBrokerClaudeSandboxRefreshRefused(t *testing.T) {
	b, path, original := claudeFixture(t, time.Now().Add(time.Hour))
	var requests, denied atomic.Int32
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	client, proxy := brokerPipeClient(t, b, provider, nil)
	proxy.attempt = func(_ context.Context, _ string, _ int, d Decision) error {
		if d.Reason == ReasonBrokerRefreshDenied {
			denied.Add(1)
		}
		return nil
	}
	for _, input := range []struct{ path, body string }{{"/v1/oauth/token", "{}"}, {"/v1/messages", `{"refresh_token":"placeholder"}`}, {"/v1/messages", `{"refreshToken":"placeholder"}`}, {"/v1/messages", b.scratchName}} {
		status, data := subscriptionRequest(t, client, b, input.path, strings.NewReader(input.body))
		if status != 403 {
			t.Fatalf("status %d", status)
		}
		assertNoSubscriptionSecrets(t, data, []string{"real-claude-access-old", "real-claude-refresh"})
	}
	if requests.Load() != 0 || denied.Load() != 4 {
		t.Fatalf("requests %d denials %d", requests.Load(), denied.Load())
	}
	assertClaudeUnchanged(t, path, original)
}

func TestBrokerClaudeTokenHostRefusedBeforeDial(t *testing.T) {
	b, path, original := claudeFixture(t, time.Now().Add(time.Hour))
	var denied atomic.Int32
	allowlist, err := NewRunAllowlist([]string{"platform.claude.com:443", "platform.claude.com:80"})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewEgressProxy(ProxyConfig{Broker: b, Evaluator: allowlist, Listener: unusedListener{}, Attempt: func(_ context.Context, host string, _ int, d Decision) error {
		if host != "platform.claude.com" || d.Reason != ReasonBrokerRefreshDenied {
			t.Error("wrong refresh refusal evidence")
		}
		denied.Add(1)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxy.dialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Error("token host dialed")
		return nil, ErrCredentialBroker
	}
	for _, req := range []*http.Request{httptest.NewRequest("CONNECT", "http://platform.claude.com:443", nil), httptest.NewRequest("POST", "http://platform.claude.com/v1/oauth/token", strings.NewReader("refresh_token=placeholder"))} {
		if req.Method == "CONNECT" {
			req.Host = "platform.claude.com:443"
		}
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("status %d", w.Code)
		}
	}
	if denied.Load() != 2 {
		t.Fatal("missing refusal evidence")
	}
	assertClaudeUnchanged(t, path, original)
}

func TestBrokerClaudeSecond401FailsClosed(t *testing.T) {
	b, path, _ := claudeFixture(t, time.Now().Add(time.Hour))
	next := claudeCredential("real-claude-access-new", time.Now().Add(time.Hour))
	var requests, alerts atomic.Int32
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			if err := os.WriteFile(path, next, 0600); err != nil {
				t.Error(err)
			}
		}
		w.WriteHeader(401)
		fmt.Fprint(w, "real-claude-access-new real-claude-refresh")
	}))
	client, proxy := brokerPipeClient(t, b, provider, nil)
	proxy.attempt = func(_ context.Context, _ string, _ int, d Decision) error {
		if d.Reason == ReasonClaudeSignInExpired {
			alerts.Add(1)
		}
		return nil
	}
	status, data := subscriptionRequest(t, client, b, "/v1/messages", nil)
	if status != 503 || requests.Load() != 2 || alerts.Load() != 1 {
		t.Fatalf("status %d requests %d alerts %d", status, requests.Load(), alerts.Load())
	}
	assertNoSubscriptionSecrets(t, data, []string{"real-claude-access-new", "real-claude-refresh"})
	assertClaudeUnchanged(t, path, next)
}
