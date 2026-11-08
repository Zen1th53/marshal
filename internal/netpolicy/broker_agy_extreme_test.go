//go:build !governed && linux

package netpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func agyFixtureRaw(expiry time.Time) []byte {
	raw, _ := json.Marshal(map[string]any{"token": map[string]any{"access_token": "synthetic-host-access", "refresh_token": "synthetic-host-refresh", "token_type": "Bearer", "expiry": expiry.UTC().Format(time.RFC3339Nano)}, "auth_method": "consumer", "id_token": "synthetic-host-id"})
	return raw
}
func TestAGYOAuthDecoderRejectsAmbiguousOrWrongSignIn(t *testing.T) {
	good := agyFixtureRaw(time.Now().Add(time.Hour))
	got, err := parseAGYOAuthCredential(good)
	if err != nil || got.access != "synthetic-host-access" || got.refresh != "synthetic-host-refresh" || got.id != "synthetic-host-id" || time.Until(got.expires) < 50*time.Minute {
		t.Fatal("wrapped native OAuth fixture not accepted")
	}
	for _, bad := range [][]byte{[]byte(`{"access_token":"flat-unsupported"}`), []byte(`{"token":null}`), []byte(`{"token":{},"token":{}}`), append(good, []byte(`{}`)...), []byte(`{"token":{"access_token":"x","refresh_token":"y","token_type":"Bearer","expiry":"invalid"},"auth_method":"consumer","id_token":"z"}`)} {
		if _, err := parseAGYOAuthCredential(bad); err == nil {
			t.Fatal("ambiguous or incomplete native sign-in accepted")
		}
	}
}
func TestAGYNativeSecretReaderRefusesLockedDuplicateAndWrongAttributes(t *testing.T) {
	good := agySecretItem{Path: "/synthetic/item", Attributes: map[string]string{"service": "gemini", "username": "antigravity", "xdg:schema": "org.freedesktop.Secret.Generic"}}
	for _, kind := range []string{"missing", "locked", "duplicate", "wrong-service", "expired", "valid"} {
		t.Run(kind, func(t *testing.T) {
			store := &agyFixtureStore{items: []agySecretItem{good}, raw: agyFixtureRaw(time.Now().Add(time.Hour))}
			switch kind {
			case "missing":
				store.items = nil
			case "locked":
				store.items[0].Locked = true
			case "duplicate":
				store.items = append(store.items, good)
			case "wrong-service":
				store.items[0].Attributes = map[string]string{"service": "other", "username": "antigravity", "xdg:schema": "org.freedesktop.Secret.Generic"}
			case "expired":
				store.raw = agyFixtureRaw(time.Now().Add(-time.Hour))
			}
			_, err := readAGYNativeCredential(t.Context(), store)
			if (kind == "valid") != (err == nil) {
				t.Fatalf("%s admission mismatch", kind)
			}
			if kind != "valid" && kind != "expired" && store.reads != 0 {
				t.Fatal("secret read before exact unlocked unique item established")
			}
		})
	}
}

type agyFixtureStore struct {
	items []agySecretItem
	raw   []byte
	reads int
}

func (s *agyFixtureStore) Search(context.Context, map[string]string) ([]agySecretItem, error) {
	return s.items, nil
}
func (s *agyFixtureStore) Secret(context.Context, agySecretItem) ([]byte, error) {
	s.reads++
	return append([]byte(nil), s.raw...), nil
}

func TestAGYOAuthDecoderRejectsCaseVariantFieldAliases(t *testing.T) {
	good := string(agyFixtureRaw(time.Now().Add(time.Hour)))
	for _, bad := range []string{strings.Replace(good, `"token":`, `"Token":`, 1), strings.Replace(good, `"access_token":`, `"ACCESS_TOKEN":`, 1), strings.Replace(good, `"auth_method":`, `"AUTH_METHOD":`, 1)} {
		if _, err := parseAGYOAuthCredential([]byte(bad)); err == nil {
			t.Fatal("case-insensitive native JSON alias accepted")
		}
	}
}

type agyWireFixtureSource struct {
	nativeHost string
	expires    time.Time
	reads      int
	fail       bool
}

func (s *agyWireFixtureSource) read(ctx context.Context, b *CredentialBroker) (nativeOAuthCredential, error) {
	s.reads++
	if s.fail {
		return nativeOAuthCredential{}, ErrCredentialBroker
	}
	return nativeOAuthCredential{access: "synthetic-host-access", expires: s.expires, replacements: map[string]string{"synthetic-host-access": b.placeholder, "synthetic-host-refresh": b.refreshPlaceholder, "synthetic-host-id": b.idPlaceholder}}, nil
}
func (*agyWireFixtureSource) prepare(home string, b *CredentialBroker) error {
	return (&agyNativeSource{}).prepare(home, b)
}

// Test-only fixed authority translation keeps all TLS/DNS sockets in-memory;
// the production route policy still sees the exact native host being tested.
func (s *agyWireFixtureSource) nativeRequest(req *http.Request) *http.Request {
	copy := new(http.Request)
	*copy = *req
	copy.Host = s.nativeHost
	if copy.Host == "" {
		copy.Host = "daily-cloudcode-pa.googleapis.com"
	}
	return copy
}
func (s *agyWireFixtureSource) requestAllowed(req *http.Request, body []byte) bool {
	return (&agyNativeSource{}).requestAllowed(s.nativeRequest(req), body)
}
func (s *agyWireFixtureSource) responseForWorker(req *http.Request, resp *http.Response, body []byte, secrets []string) ([]byte, bool) {
	return (&agyNativeSource{}).responseForWorker(s.nativeRequest(req), resp, body, secrets)
}
func (*agyWireFixtureSource) refusesAuthHost(host string) bool {
	return (&agyNativeSource{}).refusesAuthHost(host)
}
func TestAGYNativeBrokerSubstitutionNeverExportsHostSignIn(t *testing.T) {
	b, err := NewCredentialBroker("agy", "gemini-3.8-flash-high", "native-host-only")
	if err != nil {
		t.Fatal(err)
	}
	b.secret = ""
	source := &agyWireFixtureSource{expires: time.Now().Add(time.Hour)}
	b.nativeSource = source
	b.idPlaceholder = agySyntheticID(b.scratchName, time.Now().Add(time.Hour))
	scratch := t.TempDir()
	env, err := b.Prepare(scratch, "/home/marshal", b.caPEM)
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{"synthetic-host-access", "synthetic-host-refresh", "synthetic-host-id"}
	assertNoSubscriptionSecrets(t, strings.Join(env, "\n"), secrets)
	raw, err := os.ReadFile(filepath.Join(scratch, agyOAuthWorkerRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	assertNoSubscriptionSecrets(t, string(raw), secrets)
	got, err := parseAGYOAuthCredential(raw)
	if err != nil || got.access != b.placeholder || got.id != b.idPlaceholder {
		t.Fatal("only valid synthetic wrapper expected")
	}
	var calls atomic.Int64
	upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-host-access" || r.Header.Get("Chatgpt-Account-Id") != "" || r.Header.Get("X-Api-Key") != "" {
			t.Error("native-only bearer substitution expected")
		}
		w.Header().Set("X-Echo", "synthetic-host-access")
		fmt.Fprint(w, "synthetic-host-access synthetic-host-refresh synthetic-host-id")
	}))
	client, _ := brokerPipeClient(t, b, upstream, nil)
	req, _ := http.NewRequest("POST", "https://127.0.0.1/v1internal:loadCodeAssist", strings.NewReader(`{"metadata":{"ideType":"ANTIGRAVITY"}}`))
	req.Header.Set("Authorization", "Bearer "+b.placeholder)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || calls.Load() != 1 || source.reads != 1 {
		t.Fatal("single native exchange expected")
	}
	assertNoSubscriptionSecrets(t, string(data)+fmt.Sprint(resp.Header), secrets)
	if b.subscription != nil || !b.refusesAuthHost("oauth2.googleapis.com") || b.handles("oauth2.googleapis.com") {
		t.Fatal("AGY accidentally entered another provider's refresh or auth scope")
	}
}
func TestAGYNativeBrokerRefusesUnboundExpiredRefreshAndBodyTokens(t *testing.T) {
	for _, kind := range []string{"header", "expired", "refresh-path", "body-access", "body-refresh", "onboard", "loader-error", "401"} {
		t.Run(kind, func(t *testing.T) {
			b, err := NewCredentialBroker("agy", "gemini-3.8-flash-high", "native-host-only")
			if err != nil {
				t.Fatal(err)
			}
			b.secret = ""
			source := &agyWireFixtureSource{expires: time.Now().Add(time.Hour)}
			b.nativeSource = source
			if kind == "expired" {
				source.expires = time.Now().Add(-time.Hour)
			}
			if kind == "loader-error" {
				source.fail = true
			}
			var calls atomic.Int64
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(401) }))
			client, _ := brokerPipeClient(t, b, upstream, nil)
			path := "/v1internal:loadCodeAssist"
			body := `{"metadata":{}}`
			header := "Bearer " + b.placeholder
			switch kind {
			case "header":
				header = "Bearer arbitrary"
			case "refresh-path":
				path = "/oauth/token"
			case "body-access":
				body = b.placeholder
			case "body-refresh":
				body = b.refreshPlaceholder
			case "onboard":
				path = "/v1internal:onboardUser"
			}
			req, _ := http.NewRequest("POST", "https://127.0.0.1"+path, strings.NewReader(body))
			req.Header.Set("Authorization", header)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if kind == "401" {
				if calls.Load() != 1 || resp.StatusCode != 401 || source.reads != 1 {
					t.Fatal("native 401 must not trigger refresh/retry")
				}
			} else if calls.Load() != 0 || resp.StatusCode < 400 {
				t.Fatal("invalid native request reached provider")
			}
		})
	}
}

func TestAGYOAuthDecoderRejectsMalformedRequiredNativeMetadata(t *testing.T) {
	good := string(agyFixtureRaw(time.Now().Add(time.Hour)))
	for _, bad := range []string{strings.Replace(good, `"Bearer"`, `"bearer"`, 1), strings.Replace(good, `"consumer"`, `"enterprise"`, 1), strings.Replace(good, `"expiry":`, `"unknown_expiry":`, 1)} {
		if _, err := parseAGYOAuthCredential([]byte(bad)); err == nil {
			t.Fatal("malformed native metadata accepted")
		}
	}
}

func agyFileFixtureRaw(expiry time.Time) []byte {
	raw, _ := json.Marshal(map[string]any{"token": map[string]any{"access_token": "synthetic-file-access", "refresh_token": "synthetic-file-refresh", "token_type": "Bearer", "expiry": expiry.UTC().Format(time.RFC3339Nano), "expires_in": 3599}, "auth_method": "consumer", "id_token_email": "owner@example.invalid"})
	return raw
}

func TestAGYOAuthDecoderAcceptsOnlyExactFileLayout(t *testing.T) {
	got, err := parseAGYOAuthCredential(agyFileFixtureRaw(time.Now().Add(time.Hour)))
	if err != nil || got.access != "synthetic-file-access" || got.refresh != "synthetic-file-refresh" || got.id != "" {
		t.Fatalf("file layout not accepted: %v", err)
	}
	for _, bad := range []string{
		// expires_in belongs only to the file layout.
		`{"token":{"access_token":"a","refresh_token":"r","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","expires_in":1},"auth_method":"consumer","id_token":"i"}`,
		// Both identity fields at once is neither layout.
		`{"token":{"access_token":"a","refresh_token":"r","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z"},"auth_method":"consumer","id_token":"i","id_token_email":"e"}`,
		`{"token":{"access_token":"a","refresh_token":"r","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","expires_in":1},"auth_method":"consumer","id_token_email":""}`,
	} {
		if _, err := parseAGYOAuthCredential([]byte(bad)); err == nil {
			t.Fatalf("inexact layout accepted: %s", bad)
		}
	}
}

func TestAGYFileCredentialRequiresPrivateRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "antigravity-oauth-token")
	if err := os.WriteFile(path, agyFileFixtureRaw(time.Now().Add(time.Hour)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAGYFileCredential(path); err != nil {
		t.Fatalf("private sign-in file refused: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAGYFileCredential(path); err == nil {
		t.Fatal("group/world-readable sign-in file accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readAGYFileCredential(link); err == nil {
		t.Fatal("symlinked sign-in file accepted")
	}
	expired := filepath.Join(dir, "expired")
	if err := os.WriteFile(expired, agyFileFixtureRaw(time.Now().Add(-time.Hour)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAGYFileCredential(expired); err == nil {
		t.Fatal("expired sign-in file accepted")
	}
	if _, err := readAGYFileCredential(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file not reported as absent: %v", err)
	}
}
