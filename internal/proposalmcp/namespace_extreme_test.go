//go:build !governed && unix

package proposalmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNamespaceRejectsLegacyAndForeignWireBeforeHandler(t *testing.T) {
	var called atomic.Int32
	cfg, closeServer, err := Listen(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		called.Add(1)
		return json.RawMessage(`{}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeServer()
	for _, namespace := range []string{"", ".marshal-extreme", ".marshal/../.marshal-extreme"} {
		conn, err := net.Dial("unix", cfg.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		request := map[string]any{"capability": cfg.Capability, "method": "submit", "args": map[string]any{}}
		if namespace != "" {
			request["state_namespace"] = namespace
		}
		if err := json.NewEncoder(conn).Encode(request); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		var response responseEnvelope
		err = json.NewDecoder(conn).Decode(&response)
		conn.Close()
		if err != nil || response.Error == "" {
			t.Fatal("foreign/legacy wire was not refused")
		}
	}
	if called.Load() != 0 {
		t.Fatal("namespace failure reached authority handler")
	}
	if _, err := Call(context.Background(), cfg, "submit", json.RawMessage(`{}`)); err != nil {
		t.Fatal("valid bound request failed")
	}
	if called.Load() != 1 {
		t.Fatal("valid request was not dispatched exactly once")
	}
}

// The legacy parent's real DecodeStrict contract has no namespace field. A new
// client cannot silently reuse its capability by relabelling an old config.
func TestRelabelledLegacyEndpointRejectsNewWire(t *testing.T) {
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "old.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	result := make(chan bool, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			result <- false
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			result <- false
			return
		}
		var legacy struct {
			Capability string          `json:"capability"`
			Method     string          `json:"method"`
			Args       json.RawMessage `json:"args"`
			RawArgs    string          `json:"raw_args,omitempty"`
		}
		denied := DecodeStrict(line, &legacy) != nil
		result <- denied
		_ = json.NewEncoder(conn).Encode(responseEnvelope{Error: "invalid request"})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	relabelled := Config{Namespace: ".marshal", Endpoint: ln.Addr().String(), Capability: "synthetic-legacy-capability"}
	if _, err := Call(ctx, relabelled, "memory_remember", json.RawMessage(`{}`)); err == nil {
		t.Fatal("old endpoint accepted new bound request")
	}
	select {
	case denied := <-result:
		if !denied {
			t.Fatal("legacy strict decoder did not refuse added namespace")
		}
	case <-ctx.Done():
		t.Fatal("legacy peer did not complete")
	}
}

func TestNamespaceConfigCannotBeAdoptedImplicitly(t *testing.T) {
	for _, namespace := range []string{"", ".marshal-extreme"} {
		cfg := Config{Namespace: namespace, Endpoint: "/synthetic.sock", Capability: "synthetic-capability"}
		if _, cleanup, err := SaveConfig(cfg); err == nil {
			cleanup()
			t.Fatal("legacy config was adopted")
		}
		if _, err := Call(context.Background(), cfg, "submit", json.RawMessage(`{}`)); err == nil {
			t.Fatal("legacy config was dispatched")
		}
	}
}
