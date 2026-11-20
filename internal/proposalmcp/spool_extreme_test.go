//go:build !governed && unix

package proposalmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Codex runs Marshal's MCP child in the chat's sandbox, which refuses
// connect(2) on Unix sockets: "proposalmcp: dial: dial unix
// /var/tmp/mx/proposal-*/broker.sock: connect: operation not permitted". A
// refused dial must reach the broker through its spool, with the same frame.
func TestCallUsesSpoolWhenTheSocketIsRefused(t *testing.T) {
	var got atomic.Value
	cfg, cleanup, err := ListenWith(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		got.Store(method + " " + string(raw))
		return json.RawMessage(`{"ok":true}`), nil
	}, Options{SpoolParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if cfg.Spool == "" {
		t.Fatal("a spool was asked for and none is configured")
	}
	// A socket whose directory may not be searched refuses the dial with
	// EACCES, as a sandbox refuses it with EPERM.
	dir := filepath.Dir(cfg.Endpoint)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Call(ctx, cfg, "submit", json.RawMessage(`{"b":1, "a":2}`))
	if err != nil {
		t.Fatalf("spooled call failed: %v", err)
	}
	if string(res) != `{"ok":true}` {
		t.Fatalf("result = %s", res)
	}
	if v, _ := got.Load().(string); v != `submit {"b":1, "a":2}` {
		t.Fatalf("handler saw %q: the spool must carry the exact argument bytes", v)
	}
	left, _ := os.ReadDir(cfg.Spool)
	if len(left) != 0 {
		t.Fatalf("spool not emptied after a call: %v", left)
	}
}

// Without a spool a refused dial stays an error, and a dial to a broker that
// is gone is never retried through the spool.
func TestCallWithoutSpoolKeepsTheDialError(t *testing.T) {
	cfg, cleanup, err := Listen(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if cfg.Spool != "" {
		t.Fatalf("Listen made a spool: %q", cfg.Spool)
	}
	dir := filepath.Dir(cfg.Endpoint)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if _, err := Call(context.Background(), cfg, "submit", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "dial") {
		t.Fatalf("err = %v, want the dial error", err)
	}
}

// The spool checks what the socket checks: a wrong capability is refused
// and the handler never runs; a symlinked request is never read.
func TestSpoolRefusesWrongCapabilityAndSymlinks(t *testing.T) {
	var calls atomic.Int32
	cfg, cleanup, err := ListenWith(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{}`), nil
	}, Options{SpoolParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	frame, _ := json.Marshal(requestEnvelope{Namespace: cfg.Namespace, Capability: strings.Repeat("0", 64), Method: "submit", RawArgs: `{}`})
	resp, err := callSpool(context.Background(), cfg.Spool, frame)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeResponse(resp); err == nil || err.Error() != "unauthorized" {
		t.Fatalf("wrong capability answered %s (%v)", resp, err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	good, _ := json.Marshal(requestEnvelope{Namespace: cfg.Namespace, Capability: cfg.Capability, Method: "submit", RawArgs: `{}`})
	if err := os.WriteFile(secret, good, 0o600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("ab", 16)
	if err := os.Symlink(secret, filepath.Join(cfg.Spool, id+".req")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * spoolPoll)
	if n := calls.Load(); n != 0 {
		t.Fatalf("handler ran %d times for refused requests", n)
	}
	if _, err := os.Stat(filepath.Join(cfg.Spool, id+".resp")); err == nil {
		t.Fatal("a symlinked request was answered")
	}
}

// Cleanup removes the spool with the socket directory.
func TestCleanupRemovesSpool(t *testing.T) {
	cfg, cleanup, err := ListenWith(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}, Options{SpoolParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	cleanup()
	if _, err := os.Stat(cfg.Spool); !os.IsNotExist(err) {
		t.Fatalf("spool left after cleanup: %v", err)
	}
}
