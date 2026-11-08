//go:build !governed && linux

package netpolicy

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAGYNativeUserMetadataRPCUsesOnlyValidatedBoundPOST(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, method string
		duplicateAuth, pass      bool
	}{
		{"native-project", "/v1internal:fetchUserInfo", `{"project":"synthetic-native-project"}`, "POST", false, true},
		{"escaped-route", "/v1internal:%66etchUserInfo", `{"project":"synthetic-native-project"}`, "POST", false, false},
		{"query", "/v1internal:fetchUserInfo?other=1", `{"project":"synthetic-native-project"}`, "POST", false, false},
		{"empty-query", "/v1internal:fetchUserInfo?", `{"project":"synthetic-native-project"}`, "POST", false, false},
		{"get", "/v1internal:fetchUserInfo", `{"project":"synthetic-native-project"}`, "GET", false, false},
		{"duplicate-auth", "/v1internal:fetchUserInfo", `{"project":"synthetic-native-project"}`, "POST", true, false},
		{"missing-project", "/v1internal:fetchUserInfo", `{}`, "POST", false, false},
		{"extra-field", "/v1internal:fetchUserInfo", `{"project":"synthetic-native-project","other":true}`, "POST", false, false},
		{"duplicate-project", "/v1internal:fetchUserInfo", `{"project":"synthetic-native-project","project":"other"}`, "POST", false, false},
		{"case-project", "/v1internal:fetchUserInfo", `{"Project":"synthetic-native-project"}`, "POST", false, false},
		{"nested-project", "/v1internal:fetchUserInfo", `{"project":{"id":"synthetic-native-project"}}`, "POST", false, false},
		{"empty-project", "/v1internal:fetchUserInfo", `{"project":""}`, "POST", false, false},
		{"whitespace-project", "/v1internal:fetchUserInfo", `{"project":" synthetic-native-project "}`, "POST", false, false},
		{"trailing-json", "/v1internal:fetchUserInfo", `{"project":"synthetic-native-project"}{}`, "POST", false, false},
		{"onboard", "/v1internal:onboardUser", `{"project":"synthetic-native-project"}`, "POST", false, false},
		{"optional-quota-remains-denied", "/v1internal:retrieveUserQuotaSummary", `{"project":"synthetic-native-project"}`, "POST", false, false},
		{"optional-admin-remains-denied", "/v1internal:fetchAdminControls", `{"project":"synthetic-native-project"}`, "POST", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := NewCredentialBroker("agy", "gemini-3.8-flash-high", "host-only")
			if e != nil {
				t.Fatal(e)
			}
			b.secret = ""
			b.nativeSource = &agyWireFixtureSource{expires: time.Now().Add(time.Hour)}
			var calls atomic.Int64
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-host-access" {
					t.Error("host native substitution missing")
				}
				fmt.Fprint(w, `{"userSettings":{"telemetryEnabled":false},"userTags":["token_usage"],"regionCode":"US"}`)
			}))
			client, _ := brokerPipeClient(t, b, upstream, nil)
			req, _ := http.NewRequest(tc.method, "https://127.0.0.1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+b.placeholder)
			if tc.duplicateAuth {
				req.Header.Add("Authorization", "Bearer other")
			}
			resp, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if tc.pass {
				if resp.StatusCode != 200 || calls.Load() != 1 || !strings.Contains(string(data), `"token_usage"`) {
					t.Fatalf("native metadata exchange refused: status=%d calls=%d", resp.StatusCode, calls.Load())
				}
			} else if resp.StatusCode < 400 || calls.Load() != 0 {
				t.Fatal("unbound or malformed native RPC reached provider")
			}
		})
	}
}

func TestAGYNativeUserMetadataResponseRefusesCredentialsBeforeWorkerBytes(t *testing.T) {
	for _, raw := range []string{`{"access_token":"unexpected"}`, `{"nested":{"refreshToken":"unexpected"}}`, `{"nested":{"ID_TOKEN":"unexpected"}}`, `{"nativeMetadata":{"note":"synthetic-host-access"}}`, `{"nativeMetadata":{"note":"synthetic-host-refresh"}}`, `{"nativeMetadata":{"note":"synthetic-host-id"}}`, `{"nativeMetadata":{"note":"Bearer ya29.synthetic-sign-in-secret"}}`, `{"ok":true,"ok":false}`, `[]`, `{"ok":true} {}`, `{"regionCode":null}`, `{"userSettings":{"telemetryEnabled":null}}`, `{"userTags":null}`, `{"userTags":[123]}`, `{"email":"person@example.invalid"}`, `{"UserSettings":{}}`} {
		t.Run(fmt.Sprintf("case-%d", len(raw)), func(t *testing.T) {
			b, e := NewCredentialBroker("agy", "gemini-3.8-flash-high", "host-only")
			if e != nil {
				t.Fatal(e)
			}
			b.secret = ""
			b.nativeSource = &agyWireFixtureSource{expires: time.Now().Add(time.Hour)}
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, raw) }))
			client, _ := brokerPipeClient(t, b, upstream, nil)
			req, _ := http.NewRequest("POST", "https://127.0.0.1/v1internal:fetchUserInfo", strings.NewReader(`{"project":"synthetic-native-project"}`))
			req.Header.Set("Authorization", "Bearer "+b.placeholder)
			resp, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 400 || strings.Contains(string(data), raw) {
				t.Fatal("uninspected native metadata exposed to worker")
			}
		})
	}
}
