package netpolicy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrokerRefreshClassification(t *testing.T) {
	for _, tc := range []struct{ path, body, rule string }{
		{"/v1/messages", `{"messages":[{"content":"refresh_token refreshToken &grant_type=refresh_token /home/marshal/.marshal-broker-example"}]}`, ""},
		{"/v1/messages?refresh_token=mentioned", `{"metadata":{"grant_type":"refresh_token"}}`, ""},
		{"/v1/messages", `{"refresh_token":"placeholder","refreshToken":"placeholder"}`, ""},
		{"/v1/messages", `{"grant_type":"refresh_token"}`, "refresh-grant-json"},
		{"/v1/messages", `grant_type=refresh_token&refresh_token=placeholder`, "refresh-grant-form"},
		{"/oauth/token", `{}`, "oauth-token-path"},
		{"/v1/oauth/token", `{}`, "oauth-token-path"},
		{"/api/oauth/claude_cli/roles", `{}`, ""},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			b, _, _ := claudeFixture(t, time.Now().Add(time.Hour))
			provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("inference-ok")) }))
			client, p := brokerPipeClient(t, b, provider, nil)
			matched := ""
			p.attempt = func(_ context.Context, _ string, _ int, d Decision) error {
				if d.Reason == ReasonBrokerRefreshDenied {
					matched = string(d.RuleID)
				}
				return nil
			}
			body := strings.ReplaceAll(tc.body, ".marshal-broker-example", b.scratchName)
			req, _ := http.NewRequest("POST", "https://127.0.0.1"+tc.path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+b.placeholder)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := 200
			if tc.rule != "" {
				want = 403
			}
			if resp.StatusCode != want || matched != tc.rule {
				t.Fatalf("status=%d rule=%q body=%s; want %d %q", resp.StatusCode, matched, data, want, tc.rule)
			}
		})
	}
}

func TestBrokerDistinctPlaceholderRoundTrip(t *testing.T) {
	b, _, _, _ := subscriptionFixture(t)
	auth := b.subscriptionAuth()["tokens"].(map[string]any)
	scratch := t.TempDir()
	if _, err := b.Prepare(scratch, "/home/marshal", b.caPEM); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(scratch, b.scratchName, "codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var signIn struct {
		Tokens map[string]string `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &signIn); err != nil {
		t.Fatal(err)
	}
	for field, want := range auth {
		if signIn.Tokens[field] != want {
			t.Fatalf("scratch %s mismatch", field)
		}
	}
	seen := map[string]bool{}
	for field, value := range auth {
		s := value.(string)
		if s == "" || seen[s] {
			t.Fatalf("non-distinct placeholder for %s", field)
		}
		seen[s] = true
	}
	credential, err := b.subscription.read()
	if err != nil {
		t.Fatal(err)
	}
	provider := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Chatgpt-Account-Id") != credential.account || r.Header.Get("Authorization") != "Bearer "+credential.access {
			t.Error("request substitution mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"access_token": credential.secrets[0], "refresh_token": credential.secrets[1], "id_token": credential.secrets[2], "account_id": credential.secrets[3]})
	}))
	client, _ := brokerPipeClient(t, b, provider, nil)
	for i := 0; i < 2; i++ {
		status, data := subscriptionRequest(t, client, b, "/responses", nil)
		if status != 200 {
			t.Fatal(status)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatal(err)
		}
		for field, want := range auth {
			if got[field] != want {
				t.Fatalf("%s placeholder mismatch", field)
			}
		}
		assertNoSubscriptionSecrets(t, data, credential.secrets)
	}
}
