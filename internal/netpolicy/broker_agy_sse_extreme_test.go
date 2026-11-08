//go:build !governed && linux

package netpolicy

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAGYNativeStreamingAdmitsOnlyLiteralSSEQuery(t *testing.T) {
	for _, tc := range []struct {
		name, path, method, host string
		want                     bool
	}{
		{"native-sse", "/v1internal:streamGenerateContent?alt=sse", "POST", "daily-cloudcode-pa.googleapis.com", true},
		{"native-port", "/v1internal:streamGenerateContent?alt=sse", "POST", "daily-cloudcode-pa.googleapis.com:443", true},
		{"existing-noquery", "/v1internal:streamGenerateContent", "POST", "daily-cloudcode-pa.googleapis.com", true},
		{"duplicate", "/v1internal:streamGenerateContent?alt=sse&alt=sse", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"encoded", "/v1internal:streamGenerateContent?alt=%73se", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"encoded-key", "/v1internal:streamGenerateContent?%61lt=sse", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"extra", "/v1internal:streamGenerateContent?alt=sse&other=true", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"reverse", "/v1internal:streamGenerateContent?sse=alt", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"case", "/v1internal:streamGenerateContent?alt=SSE", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"force-empty", "/v1internal:streamGenerateContent?", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"escaped-path", "/v1internal:%73treamGenerateContent?alt=sse", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"get", "/v1internal:streamGenerateContent?alt=sse", "GET", "daily-cloudcode-pa.googleapis.com", false},
		{"metadata-query", "/v1internal:fetchAvailableModels?alt=sse", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"nonstream-query", "/v1internal:generateContent?alt=sse", "POST", "daily-cloudcode-pa.googleapis.com", false},
		{"foreign-host", "/v1internal:streamGenerateContent?alt=sse", "POST", "www.googleapis.com", false},
		{"userinfo-query", "/oauth2/v2/userinfo?alt=sse", "GET", "www.googleapis.com", false},
		{"foreign-port", "/v1internal:streamGenerateContent?alt=sse", "POST", "daily-cloudcode-pa.googleapis.com:444", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, "https://"+tc.host+tc.path, strings.NewReader(`{"request":{}}`))
			if got := (&agyNativeSource{}).requestAllowed(req, []byte(`{"request":{}}`)); got != tc.want {
				t.Fatalf("exact native route admission got %v want %v", got, tc.want)
			}
		})
	}
	req, _ := http.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", nil)
	req.URL.ForceQuery = true
	if (&agyNativeSource{}).requestAllowed(req, nil) {
		t.Fatal("ForceQuery alias accepted")
	}
}

func TestAGYNativeStreamingPreservesSSEQueryAndHostOnlyCredentialBoundary(t *testing.T) {
	b, e := NewCredentialBroker("agy", "gemini-3.8-flash-high", "host-only")
	if e != nil {
		t.Fatal(e)
	}
	b.secret = ""
	source := &agyWireFixtureSource{expires: time.Now().Add(time.Hour)}
	b.nativeSource = source
	var calls atomic.Int64
	var allowed atomic.Bool
	allowed.Store(true)
	upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.RequestURI() != "/v1internal:streamGenerateContent?alt=sse" {
			t.Error("native literal query/framing changed")
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-host-access" {
			t.Error("host-only bearer substitution missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"reply\":\"MARSHAL_PROBE_OK\"}\n\ndata: [DONE]\n\n")
	}))
	client, _ := brokerPipeClient(t, b, upstream, func(context.Context) bool { return allowed.Load() })
	send := func(body string, duplicate bool) (int, string) {
		req, _ := http.NewRequest("POST", "https://127.0.0.1/v1internal:streamGenerateContent?alt=sse", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+b.placeholder)
		if duplicate {
			req.Header.Add("Authorization", "Bearer "+b.placeholder)
		}
		resp, e := client.Do(req)
		if e != nil {
			if !allowed.Load() {
				return http.StatusForbidden, ""
			}
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	status, data := send(`{"request":{"contents":[]}}`, false)
	if status != 200 || calls.Load() != 1 || source.reads != 1 || !strings.Contains(data, "data: [DONE]\n\n") || !strings.Contains(data, "MARSHAL_PROBE_OK") {
		t.Fatal("native SSE envelope did not cross the exact granted route")
	}
	assertNoSubscriptionSecrets(t, data, []string{"synthetic-host-access", "synthetic-host-refresh", "synthetic-host-id"})
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			var value any
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &value) != nil {
				t.Fatal("invalid native SSE JSON")
			}
			decoded, _ := json.Marshal(value)
			assertNoSubscriptionSecrets(t, string(decoded), []string{"synthetic-host-access", "synthetic-host-refresh", "synthetic-host-id"})
		}
	}
	if status, _ := send(b.placeholder, false); status < 400 || calls.Load() != 1 || source.reads != 1 {
		t.Fatal("placeholder body reached upstream")
	}
	if status, _ := send(`{}`, true); status < 400 || calls.Load() != 1 || source.reads != 1 {
		t.Fatal("duplicate bearer reached upstream")
	}
	allowed.Store(false)
	if status, _ := send(`{}`, false); status < 400 || calls.Load() != 1 || source.reads != 1 {
		t.Fatal("revoked credential reached upstream")
	}
}

func TestAGYNativeGenerationRefusesDecodedHostCredentialReflection(t *testing.T) {
	for _, tc := range []struct{ name, body, path, contentType string }{
		{"sse-id", `data: {"nested":{"value":"synthetic-host-\u0069d"}}` + "\n\ndata: [DONE]\n\n", "/v1internal:streamGenerateContent?alt=sse", "text/event-stream"},
		{"sse-access", `data: {"nested":["synthetic-host-\u0061ccess"]}` + "\n\n", "/v1internal:streamGenerateContent?alt=sse", "text/event-stream"},
		{"sse-refresh", `data: {"value":"synthetic-host-\u0072efresh"}` + "\n\n", "/v1internal:streamGenerateContent?alt=sse", "text/event-stream"},
		{"json-id", `{"value":"synthetic-host-\u0069d"}`, "/v1internal:generateContent", "application/json"},
		{"json-key", `{"synthetic-host-\u0069d":"reflected"}`, "/v1internal:generateContent", "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := NewCredentialBroker("agy", "gemini-3.8-flash-high", "host-only")
			if e != nil {
				t.Fatal(e)
			}
			b.secret = ""
			b.nativeSource = &agyWireFixtureSource{expires: time.Now().Add(time.Hour)}
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				fmt.Fprint(w, tc.body)
			}))
			client, _ := brokerPipeClient(t, b, upstream, nil)
			req, _ := http.NewRequest("POST", "https://127.0.0.1"+tc.path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+b.placeholder)
			resp, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 400 || strings.Contains(string(data), "synthetic-host-") {
				t.Fatal("decoded host credential response was not refused before worker bytes")
			}
		})
	}
}

func TestAGYNativeGenerationScansDecodedRepresentationsAndDuplicateValues(t *testing.T) {
	secret := "synthetic-host-/ +"
	for _, variant := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret))} {
		raw, _ := json.Marshal(variant)
		escaped := fmt.Sprintf(`"\u%04x%s`, variant[0], string(raw)[2:])
		body := []byte(`{"nested":[` + escaped + `],"duplicate":` + escaped + `,"duplicate":"safe"}`)
		if agyGenerationJSONHasNoSecrets(body, []string{secret}) {
			t.Fatal("decoded known credential representation escaped inspection")
		}
	}
	for _, tc := range []struct {
		path, body, contentType string
		status                  int
	}{
		{"/v1internal:generateContent", `{"usage":{"tokenCount":9},"example":{"access_token":"example-not-issued"},"reply":"MARSHAL_PROBE_OK"}`, "application/json", 200},
		{"/v1internal:streamGenerateContent", "data: {\"reply\":\n" + "data: \"MARSHAL_PROBE_OK\"}\r\n\r\ndata: [DONE]\r\n\r\n", "text/event-stream", 200},
		{"/v1internal:streamGenerateContent", `{"error":{"code":503,"message":"temporarily unavailable"}}`, "application/json", 503},
	} {
		req, _ := http.NewRequest("POST", "https://daily-cloudcode-pa.googleapis.com"+tc.path, nil)
		resp := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.contentType}}}
		if !agyGenerationResponseAllowed(req, resp, []byte(tc.body), []string{secret}) {
			t.Fatal("ordinary native JSON/SSE/error response refused")
		}
	}
}

func TestAGYNativeStreamingRefusesUnicodeEscapedEncodedCredentialOnTLS(t *testing.T) {
	for _, variant := range []string{base64.StdEncoding.EncodeToString([]byte("synthetic-host-id")), base64.RawURLEncoding.EncodeToString([]byte("synthetic-host-id")), hex.EncodeToString([]byte("synthetic-host-id"))} {
		t.Run(fmt.Sprintf("variant-length-%d", len(variant)), func(t *testing.T) {
			raw, _ := json.Marshal(variant)
			escaped := fmt.Sprintf(`"\u%04x%s`, variant[0], string(raw)[2:])
			b, e := NewCredentialBroker("agy", "gemini-3.8-flash-high", "host-only")
			if e != nil {
				t.Fatal(e)
			}
			b.secret = ""
			b.nativeSource = &agyWireFixtureSource{expires: time.Now().Add(time.Hour)}
			upstream := newPipeTLSProvider(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"nested\":["+escaped+"]}\n\ndata: [DONE]\n\n")
			}))
			client, _ := brokerPipeClient(t, b, upstream, nil)
			req, _ := http.NewRequest("POST", "https://127.0.0.1/v1internal:streamGenerateContent?alt=sse", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+b.placeholder)
			resp, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 400 || strings.Contains(string(data), "data:") {
				t.Fatal("decoded finite credential representation crossed TLS boundary")
			}
		})
	}
}
