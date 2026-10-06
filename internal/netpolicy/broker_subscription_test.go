package netpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func codexFixture(account string, expires time.Time) []byte {
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "last_refresh": time.Now().UTC().Format(time.RFC3339), "tokens": map[string]string{"access_token": placeholderJWT(account, expires), "refresh_token": "real-refresh-" + account, "id_token": "real-id-" + account, "account_id": account}})
	return raw
}

// This fake is an actual host subprocess, with the same bounded stdio exchange
// as Codex. Only it writes auth.json; all provider calls stay on net.Pipe.
func subscriptionFixture(t *testing.T) (*CredentialBroker, string, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "host")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "auth.json")
	old, next := codexFixture("host-old", time.Now().Add(time.Hour)), codexFixture("host-new", time.Now().Add(2*time.Hour))
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "next.json"), next, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec /usr/bin/env -i HOME=\"$HOME\" CODEX_HOME=\"$CODEX_HOME\" PATH=\"$PATH\" PWD=\"$PWD\" '" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' -test.run='^TestBrokerSubscriptionHostCLIProcess$' -- \"$@\"\n"
	cli := filepath.Join(dir, "codex")
	if err = os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := NewCredentialBroker("codex", "", "unused")
	if err != nil {
		t.Fatal(err)
	}
	b.secret = ""
	b.subscription = &subscriptionSource{path: path, cli: cli, home: home}
	b.placeholder = placeholderJWT(b.scratchName, time.Now().Add(24*time.Hour))
	return b, path, old, next
}

func subscriptionRequest(t *testing.T, client *http.Client, b *CredentialBroker, path string, body io.Reader) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", "https://127.0.0.1"+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+b.placeholder)
	req.Header.Set("Chatgpt-Account-Id", b.scratchName)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if credential, err := b.subscription.read(); err == nil {
		for key, values := range resp.Header {
			assertNoSubscriptionSecrets(t, key+strings.Join(values, " "), credential.secrets)
		}
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func fixtureSecrets(t *testing.T, raw []byte) []string {
	t.Helper()
	var a struct {
		Tokens map[string]string `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	var secrets []string
	for _, v := range a.Tokens {
		secrets = append(secrets, v)
	}
	return secrets
}

func assertNoSubscriptionSecrets(t *testing.T, data string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(data, secret) {
			t.Fatal("host credential crossed worker boundary")
		}
	}
}

func TestBrokerSubscriptionFreshReadAndIsolation(t *testing.T) {
	b, path, old, next := subscriptionFixture(t)
	secrets := append(fixtureSecrets(t, old), fixtureSecrets(t, next)...)
	scratch := t.TempDir()
	env, err := b.Prepare(scratch, "/home/marshal", b.caPEM)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSubscriptionSecrets(t, strings.Join(env, "\n"), secrets)
	cleared := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") {
			if kv != "OPENAI_API_KEY=" {
				t.Fatal("subscription forced into API-key mode")
			}
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("honeypot API key was not cleared")
	}
	if err = filepath.WalkDir(scratch, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			assertNoSubscriptionSecrets(t, string(raw), secrets)
			if strings.Contains(string(raw), "PRIVATE KEY") {
				t.Fatal("CA key in scratch")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var want atomic.Value
	want.Store("Bearer " + fixtureSecretsAccess(t, old))
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want.Load().(string) {
			t.Error("did not inject latest host access token")
		}
		if r.Header.Get("Chatgpt-Account-Id") == b.scratchName {
			t.Error("account placeholder forwarded")
		}
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), b.placeholder) {
			t.Error("placeholder in upstream body")
		}
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	client, _ := brokerPipeClient(t, b, provider, nil)
	status, data := subscriptionRequest(t, client, b, "/responses", strings.NewReader("worker-input"))
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	assertNoSubscriptionSecrets(t, data, secrets)
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(old) {
		t.Fatal("broker wrote host file")
	}
	// Simulate a separate native CLI rotation between requests.
	if err = os.WriteFile(path, next, 0600); err != nil {
		t.Fatal(err)
	}
	want.Store("Bearer " + fixtureSecretsAccess(t, next))
	status, data = subscriptionRequest(t, client, b, "/responses", nil)
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	assertNoSubscriptionSecrets(t, data, secrets)
	unchanged, err = os.ReadFile(path)
	if err != nil || string(unchanged) != string(next) {
		t.Fatal("broker wrote native rotation")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(path), "calls")); !os.IsNotExist(err) {
		t.Fatal("unnecessary host refresh")
	}
}

func fixtureSecretsAccess(t *testing.T, raw []byte) string {
	t.Helper()
	var a struct {
		Tokens struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	return a.Tokens.Access
}

func TestBrokerSubscription401SingleHostRefresh(t *testing.T) {
	for _, requests := range []int{1, 8} {
		t.Run(fmt.Sprint(requests), func(t *testing.T) {
			b, path, old, next := subscriptionFixture(t)
			oldToken, newToken := fixtureSecretsAccess(t, old), fixtureSecretsAccess(t, next)
			secrets := append(fixtureSecrets(t, old), fixtureSecrets(t, next)...)
			var initial, retries atomic.Int32
			barrier := make(chan struct{})
			provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Header.Get("Authorization") {
				case "Bearer " + oldToken:
					if initial.Add(1) == int32(requests) {
						close(barrier)
					}
					select {
					case <-barrier:
					case <-time.After(3 * time.Second):
						t.Error("requests failed to overlap")
					}
					w.WriteHeader(401)
					fmt.Fprint(w, oldToken)
				case "Bearer " + newToken:
					retries.Add(1)
					fmt.Fprint(w, strings.Join(secrets, " "))
				default:
					t.Error("unexpected upstream credential")
					w.WriteHeader(403)
				}
			}))
			client, _ := brokerPipeClient(t, b, provider, nil)
			other, err := NewCredentialBroker("codex", "", "unused")
			if err != nil {
				t.Fatal(err)
			}
			other.secret, other.subscription = "", b.subscription
			other.placeholder = placeholderJWT(other.scratchName, time.Now().Add(24*time.Hour))
			otherClient, _ := brokerPipeClient(t, other, provider, nil)
			var wg sync.WaitGroup
			for i := 0; i < requests; i++ {
				wg.Add(1)
				requestClient, requestBroker := client, b
				if i%2 == 1 {
					requestClient, requestBroker = otherClient, other
				}
				go func() {
					defer wg.Done()
					status, data := subscriptionRequest(t, requestClient, requestBroker, "/responses", strings.NewReader("worker-data"))
					if status != 200 {
						t.Errorf("status %d", status)
					}
					assertNoSubscriptionSecrets(t, data, secrets)
				}()
			}
			wg.Wait()
			calls, err := os.ReadFile(filepath.Join(filepath.Dir(path), "calls"))
			if err != nil || string(calls) != "refresh\n" {
				t.Fatalf("host refresh count: %q, %v", calls, err)
			}
			if initial.Load() != int32(requests) || retries.Load() != int32(requests) {
				t.Fatal("request did not retry exactly once")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(next) {
				t.Fatal("host update was not exclusively fake CLI output")
			}
		})
	}
}

func TestBrokerSubscriptionExpiryAndRetryBound(t *testing.T) {
	for _, lifetime := range []time.Duration{-time.Hour, time.Minute} {
		t.Run(lifetime.String(), func(t *testing.T) {
			b, path, _, next := subscriptionFixture(t)
			expired := codexFixture("expired", time.Now().Add(lifetime))
			if err := os.WriteFile(path, expired, 0600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer "+fixtureSecretsAccess(t, next) {
					t.Error("expired token forwarded")
				}
				w.WriteHeader(401)
			}))
			client, _ := brokerPipeClient(t, b, provider, nil)
			status, _ := subscriptionRequest(t, client, b, "/responses", nil)
			if status != 401 || calls.Load() != 1 {
				t.Fatal("refresh repeated after proactive refresh")
			}
			count, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "calls"))
			if string(count) != "refresh\n" {
				t.Fatal("proactive refresh count")
			}
		})
	}
}

func TestBrokerSubscriptionSandboxRefreshRefusedAndLogged(t *testing.T) {
	b, path, old, _ := subscriptionFixture(t)
	var upstreamCalls atomic.Int32
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls.Add(1) }))
	client, p := brokerPipeClient(t, b, provider, nil)
	var logged atomic.Int32
	p.attempt = func(ctx context.Context, host string, port int, d Decision) error {
		encoded, err := json.Marshal(d)
		if err != nil {
			return err
		}
		assertNoSubscriptionSecrets(t, host+string(encoded), fixtureSecrets(t, old))
		if d.Reason == ReasonBrokerRefreshDenied {
			logged.Add(1)
		}
		return nil
	}
	for _, body := range []string{`{"grant_type":"refresh_token","refresh_token":"` + b.scratchName + `"}`, "grant_type=refresh_token&refresh_token=" + b.scratchName} {
		status, data := subscriptionRequest(t, client, b, "/oauth/token", strings.NewReader(body))
		if status != 403 {
			t.Fatalf("refresh status %d", status)
		}
		assertNoSubscriptionSecrets(t, data, fixtureSecrets(t, old))
	}
	if upstreamCalls.Load() != 0 || logged.Load() != 2 {
		t.Fatal("refresh forwarded or not logged")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(old) {
		t.Fatal("sandbox refresh changed host sign-in")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "calls")); !os.IsNotExist(err) {
		t.Fatal("sandbox requested host command")
	}
}

func TestBrokerSubscriptionLoadHostProfile(t *testing.T) {
	original, path, _, _ := subscriptionFixture(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", filepath.Dir(path))
	t.Setenv("HOME", filepath.Dir(path))
	t.Setenv("PATH", filepath.Dir(original.subscription.cli))
	b, err := LoadCredentialBroker("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Subscription() || b.secret != "" || !b.handles("chatgpt.com") || !b.handles("auth.openai.com") || b.handles("api.openai.com") || b.handles("evil.chatgpt.com") {
		t.Fatal("incorrect subscription host scope")
	}
	if b.subscription.cli != original.subscription.cli || b.subscription.path != path {
		t.Fatal("host refresh source mismatch")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("missing host sign-in forwarded") }))
	client, _ := brokerPipeClient(t, b, provider, nil)
	status, _ := subscriptionRequest(t, client, b, "/responses", nil)
	if status != 503 {
		t.Fatalf("missing host sign-in status %d", status)
	}
}

func TestBrokerSubscriptionHostRefreshFailureIsClosed(t *testing.T) {
	b, path, old, _ := subscriptionFixture(t)
	// The host helper is installed but exits unsuccessfully. Diagnostics can
	// contain credentials; neither errors nor bodies may return those diagnostics.
	script := "#!/bin/sh\nprintf '%s' 'real-refresh-host-old' >&2\nexit 1\n"
	if err := os.WriteFile(b.subscription.cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(401) }))
	client, _ := brokerPipeClient(t, b, provider, nil)
	status, data := subscriptionRequest(t, client, b, "/responses", nil)
	if status != 503 || calls.Load() != 1 {
		t.Fatal("failed refresh did not fail closed")
	}
	assertNoSubscriptionSecrets(t, data, fixtureSecrets(t, old))
	after, _ := os.ReadFile(path)
	if string(after) != string(old) {
		t.Fatal("broker wrote failed host refresh")
	}
}

func TestBrokerSubscriptionRepeated401RetriesOnlyOnce(t *testing.T) {
	b, path, old, next := subscriptionFixture(t)
	var calls atomic.Int32
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	client, _ := brokerPipeClient(t, b, provider, nil)
	status, data := subscriptionRequest(t, client, b, "/responses", nil)
	if status != 401 || calls.Load() != 2 {
		t.Fatal("401 retry was not bounded to one")
	}
	assertNoSubscriptionSecrets(t, data, append(fixtureSecrets(t, old), fixtureSecrets(t, next)...))
	count, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "calls"))
	if string(count) != "refresh\n" {
		t.Fatal("host refresh repeated")
	}
}

// Helper subprocess for the fake native CLI. Its OAuth exchange uses a local
// HTTP auth server over net.Pipe so tests need neither sockets nor the network.
func TestBrokerSubscriptionHostCLIProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	if strings.Join(os.Args[separator+1:], "\n") != strings.Join([]string{"app-server", "--stdio", "-c", `cli_auth_credentials_store="file"`, "-c", "analytics.enabled=false"}, "\n") {
		t.Fatal("unsafe host arguments")
	}
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if key != "HOME" && key != "CODEX_HOME" && key != "PATH" && key != "PWD" {
			t.Fatal("worker environment reached host CLI")
		}
		if strings.Contains(kv, ".marshal-broker-") || strings.Contains(kv, "marshal-placeholder") {
			t.Fatal("placeholder reached host CLI")
		}
	}
	dir := os.Getenv("CODEX_HOME")
	cwd, err := os.Getwd()
	if err != nil || cwd != dir || os.Getenv("PWD") != dir {
		t.Fatal("worker cwd reached host CLI")
	}
	decoder := json.NewDecoder(os.Stdin)
	var init struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params struct {
			ClientInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		} `json:"params"`
	}
	if decoder.Decode(&init) != nil || init.ID != 1 || init.Method != "initialize" || init.Params.ClientInfo.Name != "marshal" {
		t.Fatal("invalid host initialize")
	}
	fmt.Println(`{"id":1,"result":{}}`)
	var ready struct {
		Method string `json:"method"`
	}
	if decoder.Decode(&ready) != nil || ready.Method != "initialized" {
		t.Fatal("invalid host initialized")
	}
	var request struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params struct {
			Refresh bool `json:"refreshToken"`
		} `json:"params"`
	}
	if decoder.Decode(&request) != nil || request.ID != 2 || request.Method != "account/read" || !request.Params.Refresh {
		t.Fatal("worker data reached host RPC")
	}
	count, err := os.OpenFile(filepath.Join(dir, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = count.WriteString("refresh\n"); err != nil {
		t.Fatal(err)
	}
	count.Close()
	time.Sleep(50 * time.Millisecond)
	old, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth struct {
		Tokens struct {
			Refresh string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(old, &auth) != nil {
		t.Fatal("invalid fake native sign-in")
	}
	next, err := os.ReadFile(filepath.Join(dir, "next.json"))
	if err != nil {
		t.Fatal(err)
	}
	listener := newPipeListener("auth:80")
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var grant map[string]string
		if json.Unmarshal(data, &grant) != nil || r.Method != "POST" || r.URL.Path != "/oauth/token" || grant["grant_type"] != "refresh_token" || grant["refresh_token"] != auth.Tokens.Refresh || strings.Contains(string(data), ".marshal-broker-") {
			t.Error("fake CLI did not use host refresh token")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(next)
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	transport := &http.Transport{DialContext: listener.dial}
	client := &http.Client{Transport: transport}
	payload, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": auth.Tokens.Refresh})
	response, err := client.Post("http://auth/oauth/token", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal("fake auth refresh failed")
	}
	temp := filepath.Join(dir, "cli.tmp")
	if err = os.WriteFile(temp, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(temp, filepath.Join(dir, "auth.json")); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(os.Stderr, string(data)) // Host diagnostics must never reach the worker.
	fmt.Println(`{"id":2,"result":{"account":{"type":"chatgpt"}}}`)
	_, _ = io.Copy(io.Discard, os.Stdin)
}
