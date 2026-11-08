//go:build !governed && linux

package netpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAGYNativeUserProfileProjectionIsMetadataOnly(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		status    int
		pass      bool
	}{
		{"native-minimal", `{"email":"synthetic-host-person@example.invalid","name":"Synthetic Host Person"}`, 200, true},
		{"documented-full", `{"id":"synthetic-host-person-id","email":"synthetic-host-person@example.invalid","name":"Synthetic Host Person","family_name":"Person","given_name":"Synthetic","gender":"unspecified","hd":"example.invalid","link":"https://example.invalid/profile","locale":"en","picture":"https://example.invalid/avatar","verified_email":true}`, 200, true},
		{"missing-name", `{"email":"synthetic-host-person@example.invalid"}`, 200, false},
		{"wrong-optional-type", `{"email":"synthetic-host-person@example.invalid","name":"Synthetic Host Person","verified_email":"true"}`, 200, false},
		{"unknown-field", `{"email":"synthetic-host-person@example.invalid","name":"Synthetic Host Person","access_token":"unexpected"}`, 200, false},
		{"duplicate", `{"email":"synthetic-host-person@example.invalid","name":"Synthetic Host Person","name":"other"}`, 200, false},
		{"actual-host-secret", `{"email":"synthetic-host-person@example.invalid","name":"synthetic-host-id"}`, 200, false},
		{"redirect", `{"email":"synthetic-host-person@example.invalid","name":"Synthetic Host Person"}`, 302, false},
		{"auth-denied", `{"error":"synthetic denied"}`, 401, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := NewCredentialBroker("agy", "gemini-3.8-flash-high", "host-only")
			if e != nil {
				t.Fatal(e)
			}
			b.secret = ""
			source := &agyWireFixtureSource{expires: time.Now().Add(time.Hour), nativeHost: "www.googleapis.com"}
			b.nativeSource = source
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-host-access" {
					t.Error("native bounded GET/substitution expected")
				}
				w.Header().Set("Set-Cookie", "host-identity=synthetic-host-person")
				w.Header().Set("X-Host-Identity", "synthetic-host-person")
				w.Header().Set("Location", "https://example.invalid/identity")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.raw)
			}))
			client, _ := brokerPipeClient(t, b, upstream, nil)
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			req, _ := http.NewRequest("GET", "https://127.0.0.1/oauth2/v2/userinfo", nil)
			req.Header.Set("Authorization", "Bearer "+b.placeholder)
			resp, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if tc.pass {
				var got map[string]any
				if json.Unmarshal(data, &got) != nil || resp.StatusCode != 200 || len(got) != 2 || got["email"] != "worker@invalid" || got["name"] != "Marshal Worker" {
					t.Fatal("native metadata was not projected to exact synthetic worker display identity")
				}
				if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("X-Host-Identity") != "" || resp.Header.Get("Location") != "" || strings.Contains(string(data), "synthetic-host-person") {
					t.Fatal("native account identity escaped projection")
				}
			} else if resp.StatusCode < 400 || strings.Contains(string(data), tc.raw) {
				t.Fatal("invalid native metadata exposed to worker")
			}
		})
	}
}
func TestAGYNativeProfileGETRefusesOtherRoutesBodiesQueriesAndRevocation(t *testing.T) {
	for _, tc := range []struct {
		name, path, method, body string
		revoked                  bool
	}{
		{"query", "/oauth2/v2/userinfo?key=other", "GET", "", false}, {"empty-query", "/oauth2/v2/userinfo?", "GET", "", false}, {"escaped-path", "/oauth2/v2/%75serinfo", "GET", "", false}, {"post", "/oauth2/v2/userinfo", "POST", "", false}, {"body", "/oauth2/v2/userinfo", "GET", "{}", false}, {"tokeninfo", "/oauth2/v2/tokeninfo", "GET", "", false}, {"other-userinfo-alias", "/userinfo/v2/me", "GET", "", false}, {"daily-rpc-on-identity-host", "/v1internal:loadCodeAssist", "POST", "{}", false}, {"revoked", "/oauth2/v2/userinfo", "GET", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := NewCredentialBroker("agy", "gemini-3.8-flash-high", "host-only")
			if e != nil {
				t.Fatal(e)
			}
			b.secret = ""
			source := &agyWireFixtureSource{expires: time.Now().Add(time.Hour), nativeHost: "www.googleapis.com"}
			b.nativeSource = source
			var calls atomic.Int64
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, `{"email":"synthetic-host-person@example.invalid","name":"Synthetic Host Person"}`)
			}))
			client, _ := brokerPipeClient(t, b, upstream, func(context.Context) bool { return !tc.revoked })
			req, _ := http.NewRequest(tc.method, "https://127.0.0.1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+b.placeholder)
			resp, e := client.Do(req)
			if e != nil {
				if tc.revoked && calls.Load() == 0 && source.reads == 0 {
					return
				}
				t.Fatal(e)
			}
			resp.Body.Close()
			if resp.StatusCode < 400 || calls.Load() != 0 || source.reads != 0 {
				t.Fatal("unbound profile route reached host credential injection")
			}
		})
	}
}
