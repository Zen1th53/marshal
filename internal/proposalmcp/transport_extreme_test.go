//go:build !governed && unix

package proposalmcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// rawRequest dials the endpoint, writes a hand-crafted frame verbatim, and
// returns the response line. It is the adversarial entry point the client Call
// cannot exercise, because Call always encodes a clean envelope.
func rawRequest(t *testing.T, endpoint, frame string) string {
	t.Helper()
	conn, err := net.Dial("unix", endpoint)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(frame)); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := io.ReadAll(conn)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func hasError(t *testing.T, resp string) bool {
	t.Helper()
	var re responseEnvelope
	if err := json.Unmarshal([]byte(resp), &re); err != nil {
		t.Fatalf("bad response %q: %v", resp, err)
	}
	return re.Error != ""
}

func TestTransportRoundTripPreservesExactArgs(t *testing.T) {
	var gotMethod string
	var gotArgs json.RawMessage
	var gotCapabilityInHandler bool
	handler := func(_ context.Context, method string, args json.RawMessage) (json.RawMessage, error) {
		gotMethod = method
		gotArgs = args
		if strings.Contains(string(args), "capability") {
			gotCapabilityInHandler = true
		}
		return json.RawMessage(`{"state":"admitted","occurrence_id":"occ-1"}`), nil
	}
	cfg, cleanup, err := Listen(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if cfg.Endpoint == "" || cfg.Capability == "" {
		t.Fatalf("incomplete config: %+v", cfg)
	}
	if len(cfg.Capability) != 64 {
		t.Fatalf("capability is not 256-bit hex (64 chars): got %d", len(cfg.Capability))
	}

	// Directory is 0700 and the socket is 0600.
	info, err := os.Stat(cfg.Endpoint)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(parentDir(cfg.Endpoint))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}

	args := json.RawMessage("{\n \"submission_id\": \"s1\", \"proposal\": {\"action\": \"setting\", \"key\": \"control\", \"value\": \"strict\"}\n}")
	result, err := Call(context.Background(), cfg, "submit", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotMethod != "submit" {
		t.Fatalf("handler method = %q, want submit", gotMethod)
	}
	if string(gotArgs) != string(args) {
		t.Fatalf("handler args not exact bytes:\n got %s\nwant %s", gotArgs, args)
	}
	if gotCapabilityInHandler {
		t.Fatal("capability leaked into handler args")
	}
	if string(result) != `{"state":"admitted","occurrence_id":"occ-1"}` {
		t.Fatalf("result = %s", result)
	}
}

func TestWrongCapabilityDeniedAndHandlerNotCalled(t *testing.T) {
	called := 0
	handler := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		called++
		return nil, nil
	}
	cfg, cleanup, err := Listen(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	for name, badCap := range map[string]string{
		"wrong": strings.Repeat("0", 64),
		"short": "deadbeef",
		"empty": "",
		"long":  strings.Repeat("a", 128),
	} {
		bad := cfg
		bad.Capability = badCap
		if _, err := Call(context.Background(), bad, "submit", json.RawMessage(`{}`)); err == nil {
			t.Fatalf("%s capability: expected error", name)
		}
	}
	if called != 0 {
		t.Fatalf("handler invoked %d times with a wrong capability", called)
	}
}

func TestTransportRejectsMalformedFrames(t *testing.T) {
	called := 0
	handler := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		called++
		return json.RawMessage(`{"ok":true}`), nil
	}
	cfg, cleanup, err := Listen(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	good := `{"state_namespace":".marshal","capability":%q,"method":"submit","args":{"a":1}}` + "\n"
	frames := map[string]string{
		"duplicate-envelope-key": `{"state_namespace":".marshal","capability":%q,"capability":%q,"method":"submit","args":{}}` + "\n",
		"duplicate-nested-key":   `{"state_namespace":".marshal","capability":%q,"method":"submit","args":{"a":1,"a":2}}` + "\n",
		"unknown-field":          `{"state_namespace":".marshal","capability":%q,"method":"submit","args":{},"extra":1}` + "\n",
		"trailing-data":          `{"state_namespace":".marshal","capability":%q,"method":"submit","args":{}} {"junk":1}` + "\n",
		"empty-method":           `{"state_namespace":".marshal","capability":%q,"method":"","args":{}}` + "\n",
	}
	for name, tmpl := range frames {
		frame := tmpl
		if strings.Count(frame, "%q") == 1 {
			frame = sprintfCap(frame, cfg.Capability)
		} else {
			frame = sprintfCaps(frame, cfg.Capability)
		}
		resp := rawRequest(t, cfg.Endpoint, frame)
		if !hasError(t, resp) {
			t.Fatalf("%s: expected error, got %q", name, resp)
		}
	}
	// A valid frame still works.
	resp := rawRequest(t, cfg.Endpoint, sprintfCap(good, cfg.Capability))
	if hasError(t, resp) {
		t.Fatalf("valid frame rejected: %q", resp)
	}
	if called != 1 {
		t.Fatalf("handler called %d times, want 1", called)
	}
}

func TestTransportRejectsOversizedFrame(t *testing.T) {
	called := 0
	handler := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		called++
		return json.RawMessage(`{}`), nil
	}
	cfg, cleanup, err := Listen(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	big := `{"capability":` + `"` + cfg.Capability + `"` + `,"method":"submit","args":` + strings.Repeat("x", maxRequestBytes) + `}` + "\n"
	conn, err := net.Dial("unix", cfg.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(big)); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := io.ReadAll(conn)
	// The server may reject either by returning an error frame or by resetting
	// the connection while unread request bytes remain. Both are denial.
	if err == nil && !hasError(t, strings.TrimSpace(string(data))) {
		t.Fatalf("oversized frame admitted: %q", data)
	}
	if called != 0 {
		t.Fatalf("handler invoked for oversized frame")
	}
}

func TestCleanupClosesActiveConnectionsAndRemovesDir(t *testing.T) {
	entered := make(chan struct{})
	handler := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cfg, cleanup, err := Listen(handler)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Call(context.Background(), cfg, "submit", json.RawMessage(`{}`))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never entered")
	}

	cleanup()
	cleanup() // idempotent

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("in-flight call returned nil after cleanup")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight call did not terminate after cleanup")
	}

	if _, err := os.Stat(cfg.Endpoint); !os.IsNotExist(err) {
		t.Fatalf("socket directory not removed after cleanup: %v", err)
	}
}

func TestCallCancellation(t *testing.T) {
	entered := make(chan struct{})
	handler := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cfg, cleanup, err := Listen(handler)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Call(ctx, cfg, "submit", json.RawMessage(`{}`))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never entered")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled call returned nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled call did not terminate")
	}
}

func TestDecodeStrictRejectsDuplicateKeysRecursively(t *testing.T) {
	cases := []string{
		`{"a":1,"a":2}`,
		`{"a":{"b":1,"b":2}}`,
		`{"a":[{"b":1,"b":2}]}`,
		`{"a":1,"b":{"c":2,"c":3}}`,
		`{"capability":"x","method":"y","args":{"z":1,"z":2}}`,
	}
	for _, c := range cases {
		var dst map[string]any
		if err := DecodeStrict([]byte(c), &dst); err == nil {
			t.Errorf("expected duplicate-key rejection for %s", c)
		}
	}
}

func TestDecodeStrictRejectsUnknownFieldsAndTrailing(t *testing.T) {
	type sample struct {
		A string `json:"a"`
	}
	for _, c := range []string{
		`{"a":"x","b":"y"}`,
		`{"a":"x"} {"b":"y"}`,
		`{"a":"x"}}`,
		`{"a":"x"},`,
	} {
		var dst sample
		if err := DecodeStrict([]byte(c), &dst); err == nil {
			t.Errorf("expected rejection for %q", c)
		}
	}
	var dst sample
	if err := DecodeStrict([]byte(`{"a":"x"}`), &dst); err != nil {
		t.Fatalf("valid object rejected: %v", err)
	}
	if dst.A != "x" {
		t.Fatalf("decoded value = %q", dst.A)
	}
}

func TestSaveConfigLoadConfigRoundTrip(t *testing.T) {
	cfg := Config{Namespace: ".marshal", Endpoint: "/tmp/somewhere/broker.sock", Capability: strings.Repeat("f", 64)}
	path, cleanup, err := SaveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(parentDir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != cfg {
		t.Fatalf("loaded config = %+v, want %+v", loaded, cfg)
	}

	// A config file with an unknown field or duplicate key is rejected.
	badPath := path + ".bad"
	if err := os.WriteFile(badPath, []byte(`{"endpoint":"/x","capability":"`+strings.Repeat("f", 64)+`","extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(badPath)
	if _, err := LoadConfig(badPath); err == nil {
		t.Fatal("config with unknown field accepted")
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config directory not removed after cleanup: %v", err)
	}
}

func parentDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return path
}

func sprintfCap(tmpl, cap string) string {
	return strings.Replace(tmpl, "%q", `"`+cap+`"`, 1)
}

func sprintfCaps(tmpl, cap string) string {
	return strings.ReplaceAll(tmpl, "%q", `"`+cap+`"`)
}
