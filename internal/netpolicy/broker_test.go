package netpolicy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBrokerIsolationAndHostScope(t *testing.T) {
	const secret = "sk-test-real-credential-0123456789abcdefgh"
	seen := make(chan string, 8)
	upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		io.WriteString(w, r.Header.Get("Authorization"))
	}))
	outsider := newPipeTLSProvider(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		io.WriteString(w, "ok")
	}))
	b, err := NewCredentialBroker("codex", "", secret)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://127.0.0.1:443")
	b.hosts = map[string]bool{u.Hostname(): true}
	b.upstreamRoots = x509.NewCertPool()
	b.upstreamRoots.AddCert(upstream.cert)
	home := t.TempDir()
	env, err := b.Prepare(home, "/home/marshal", b.caPEM)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range env {
		if strings.Contains(v, secret) {
			t.Fatal("credential in worker env")
		}
	}
	err = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte("PRIVATE KEY")) {
			t.Fatal("private material in scratch HOME")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := os.ReadFile(filepath.Join(home, b.scratchName, "codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(auth, []byte(b.placeholder)) {
		t.Fatal("missing CLI placeholder")
	}
	a, err := NewRunAllowlist([]string{u.Host, "127.0.0.2:443"})
	if err != nil {
		t.Fatal(err)
	}
	ingress := newPipeListener("proxy:80")
	p, err := NewEgressProxy(ProxyConfig{Evaluator: a, Broker: b, Listener: ingress})
	if err != nil {
		t.Fatal(err)
	}
	p.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		switch address {
		case "127.0.0.1:443":
			return upstream.listener.dial(ctx, network, address)
		case "127.0.0.2:443":
			return outsider.listener.dial(ctx, network, address)
		}
		return nil, errors.New("test destination refused")
	}
	p.Start()
	defer p.Close()
	proxyURL, _ := url.Parse(p.URL())
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b.caPEM) {
		t.Fatal("bad CA")
	}
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DialContext: ingress.dial, TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "https://127.0.0.1/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer "+b.placeholder)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "Bearer "+secret {
		t.Fatal("upstream did not receive real key")
	}
	if bytes.Contains(body, []byte(secret)) || strings.Contains(resp.Header.Get("X-Echo"), secret) {
		t.Fatal("response leaked key")
	}
	// An allowed host outside the profile keeps its TLS tunnel and placeholders.
	outsideRoots := x509.NewCertPool()
	outsideRoots.AddCert(outsider.cert)
	tr2 := &http.Transport{Proxy: http.ProxyURL(proxyURL), DialContext: ingress.dial, TLSClientConfig: &tls.Config{RootCAs: outsideRoots}}
	defer tr2.CloseIdleConnections()
	req, _ = http.NewRequest("GET", "https://127.0.0.2", nil)
	req.Header.Set("Authorization", "Bearer "+b.placeholder)
	resp, err = (&http.Client{Transport: tr2, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := <-seen; got != "Bearer "+b.placeholder {
		t.Fatal("key escaped provider host scope")
	}
	// A second run's CA cannot authenticate this run.
	other, err := NewCredentialBroker("codex", "", secret)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := b.certificate("api.openai.com")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	otherRoots := x509.NewCertPool()
	otherRoots.AppendCertsFromPEM(other.caPEM)
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: otherRoots, DNSName: "api.openai.com"}); err == nil {
		t.Fatal("other run CA trusted")
	}
	block, _ := pem.Decode(b.caPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("bad public certificate")
	}
}

func TestBrokerFailClosed(t *testing.T) {
	if _, err := NewCredentialBroker("codex", "", ""); err == nil {
		t.Fatal("missing credential accepted")
	}
	if _, err := NewCredentialBroker("unknown", "", "secret"); err == nil {
		t.Fatal("unknown profile accepted")
	}
	b, err := NewCredentialBroker("claude", "", "secret")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err = os.WriteFile(file, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Prepare(file, "/home/marshal", b.caPEM); err == nil {
		t.Fatal("setup failure accepted")
	}
}

func TestSubscriptionRefreshRefusedWithoutVerifiedNativeLock(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			dir, file, raw := ".codex", "auth.json", `{"auth_mode":"chatgpt","tokens":{"access_token":"real-access","refresh_token":"real-refresh","id_token":"real-id"}}`
			if provider == "claude" {
				dir, file, raw = ".claude", ".credentials.json", `{"claudeAiOauth":{"accessToken":"real-access","refreshToken":"real-refresh"}}`
			}
			path := filepath.Join(home, dir, file)
			os.MkdirAll(filepath.Dir(path), 0700)
			os.WriteFile(path, []byte(raw), 0600)
			if _, err := LoadCredentialBroker(provider, ""); err == nil || !strings.Contains(err.Error(), "refresh lock") {
				t.Fatalf("unsafe OAuth admitted: %v", err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != raw {
				t.Fatal("host credentials changed on refusal")
			}
		})
	}
}

// Local fake HTTP/TLS servers with no external socket authority. This also
// exercises the real CONNECT parser and certificate verification in restricted
// build environments.
type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

type pipeListener struct {
	address  pipeAddr
	incoming chan net.Conn
	done     chan struct{}
	once     sync.Once
}

func newPipeListener(address string) *pipeListener {
	return &pipeListener{address: pipeAddr(address), incoming: make(chan net.Conn), done: make(chan struct{})}
}
func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return l.address }
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.incoming <- server:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-l.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}

type pipeTLSProvider struct {
	listener *pipeListener
	cert     *x509.Certificate
}

func newPipeTLSProvider(t *testing.T, host string, handler http.Handler) *pipeTLSProvider {
	t.Helper()
	authority, err := NewCredentialBroker("codex", "", "fake-upstream-key")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := authority.certificate(host)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	listener := newPipeListener(net.JoinHostPort(host, "443"))
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}))
	}()
	t.Cleanup(func() { server.Close() })
	return &pipeTLSProvider{listener: listener, cert: leaf}
}

func brokerPipeClient(t *testing.T, b *CredentialBroker, upstream *pipeTLSProvider, authorize func(context.Context) bool) (*http.Client, *EgressProxy) {
	t.Helper()
	b.hosts = map[string]bool{"127.0.0.1": true}
	b.upstreamRoots = x509.NewCertPool()
	b.upstreamRoots.AddCert(upstream.cert)
	ingress := newPipeListener("proxy:80")
	allowlist, err := NewRunAllowlist([]string{"127.0.0.1:443"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewEgressProxy(ProxyConfig{Broker: b, Evaluator: allowlist, Listener: ingress, CredentialAllowed: authorize})
	if err != nil {
		t.Fatal(err)
	}
	p.dialContext = upstream.listener.dial
	p.Start()
	t.Cleanup(func() { p.Close() })
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(b.caPEM)
	proxyURL, _ := url.Parse(p.URL())
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DialContext: ingress.dial, TLSClientConfig: &tls.Config{RootCAs: roots}}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}, p
}

func TestBrokerAPIProfiles(t *testing.T) {
	for _, profile := range []struct{ provider, model string }{{"codex", ""}, {"claude", ""}, {"gemini", ""}, {"opencode", "openai/gpt-test"}, {"opencode", "anthropic/claude-test"}, {"opencode", "google/gemini-test"}, {"opencode", "deepseek/test"}} {
		t.Run(profile.provider+profile.model, func(t *testing.T) {
			const secret = "real-provider-key-123456789"
			b, err := NewCredentialBroker(profile.provider, profile.model, secret)
			if err != nil {
				t.Fatal(err)
			}
			seen := make(chan bool, 1)
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expected := secret
				if b.header == "Authorization" {
					expected = "Bearer " + secret
				}
				seen <- r.Header.Get(b.header) == expected || (b.header == "X-Goog-Api-Key" && r.URL.Query().Get("key") == secret)
				io.WriteString(w, "ok")
			}))
			client, _ := brokerPipeClient(t, b, upstream, nil)
			req, _ := http.NewRequest("POST", "https://127.0.0.1/v1/messages", strings.NewReader(`{"message":"hello"}`))
			expected := b.placeholder
			if b.header == "Authorization" {
				expected = "Bearer " + expected
			}
			req.Header.Set(b.header, expected)
			if b.header == "X-Goog-Api-Key" {
				req.Header.Del(b.header)
				req.URL.RawQuery = "key=" + b.placeholder
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 || !<-seen {
				t.Fatal("profile substitution failed")
			}
			home := t.TempDir()
			env, err := b.Prepare(home, "/home/marshal", b.caPEM)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(env, "\n"), b.envKey+"="+b.placeholder) {
				t.Fatal("placeholder env missing")
			}
		})
	}
}

func TestBrokerRefusals(t *testing.T) {
	for _, kind := range []string{"inner-host", "body-token", "revoked", "bad-tls", "encoding", "denied-host", "plaintext"} {
		t.Run(kind, func(t *testing.T) {
			b, err := NewCredentialBroker("claude", "", "real-opaque-key")
			if err != nil {
				t.Fatal(err)
			}
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind != "encoding" {
					t.Error("refused request reached provider")
				}
				w.Header().Set("Content-Encoding", "gzip")
				io.WriteString(w, "real-opaque-key")
			}))
			client, _ := brokerPipeClient(t, b, upstream, func(context.Context) bool { return kind != "revoked" })
			if kind == "bad-tls" {
				b.upstreamRoots = x509.NewCertPool()
			}
			body := "hello"
			if kind == "body-token" {
				body = b.placeholder
			}
			req, _ := http.NewRequest("POST", "https://127.0.0.1/v1/messages", strings.NewReader(body))
			req.Header.Set("X-Api-Key", b.placeholder)
			if kind == "inner-host" {
				req.Host = "evil.example"
			}
			if kind == "denied-host" {
				req.URL.Host = "127.0.0.2"
				req.Host = "127.0.0.2"
			}
			if kind == "plaintext" {
				req.URL.Scheme = "http"
				req.URL.Host = "127.0.0.1:443"
				req.Host = "127.0.0.1:443"
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Fatal("unsafe exchange accepted")
			}
			data, _ := io.ReadAll(resp.Body)
			if bytes.Contains(data, []byte(b.secret)) {
				t.Fatal("refusal leaked secret")
			}
		})
	}
}

func TestBrokerHostFileIsNotCopied(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	hostConfig := filepath.Join(home, "native")
	t.Setenv("CODEX_HOME", hostConfig)
	if err := os.Mkdir(hostConfig, 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"auth_mode":"apikey","OPENAI_API_KEY":"real-host-key","other_token":"must-not-copy","settings":"native-only"}`
	if err := os.WriteFile(filepath.Join(hostConfig, "auth.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadCredentialBroker("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	if _, err = b.Prepare(scratch, "/home/marshal", b.caPEM); err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(filepath.Join(scratch, b.scratchName, "codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"real-host-key", "must-not-copy", "native-only"} {
		if bytes.Contains(exported, []byte(value)) {
			t.Fatal("native auth file contents copied")
		}
	}
	original, err := os.ReadFile(filepath.Join(hostConfig, "auth.json"))
	if err != nil || string(original) != raw {
		t.Fatal("native file modified")
	}
	if _, err = b.Prepare(t.TempDir(), "/home/marshal", nil); err == nil {
		t.Fatal("missing system roots accepted")
	}
}
