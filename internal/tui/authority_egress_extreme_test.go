//go:build !governed

package tui

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestExtremeProviderEgressQueueBypassesJudgeWithEvidence(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-provider-egress", false)
	socket := filepath.Join(t.TempDir(), "proxy.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	appendEvent := func(id string, kind events.EventType, data map[string]any) {
		t.Helper()
		data["scope_socket"], data["provider"] = socket, "codex"
		if _, err := runtime.Store().Append(t.Context(), events.Event{ID: id, Type: kind, Subject: "registered-worker", RunID: "RUN-provider", TaskID: "TASK-provider", At: time.Now().UTC(), Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("provider-scope", events.EventTypeNetworkEgressRequested, map[string]any{"source": "run scope", "allowed_endpoints": []string{}})
	req := permission.Request{Kind: "network", RunID: "RUN-provider", TaskID: "TASK-provider", Object: "ab.chatgpt.com:443", Who: "untrusted description"}
	appendEvent("provider-notification", events.EventTypeNetworkEgressNotification, map[string]any{"endpoint": req.Object, "requestable": true})
	if !w.routeBuildPermission(t.Context(), req) || runtime.EgressRequestPending(req.RunID, req.Object) || len(w.permissions.build.pending) != 0 {
		t.Fatal("provider egress waited for a judge instead of applying the canonical grant")
	}
	history, err := runtime.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range history {
		if event.Type == "PERMISSION_DECIDED" && event.Data["object"] == req.Object {
			found = true
			if event.Data["allow"] != true || event.Data["source"] != "marshal" || event.Data["provider"] != "codex" || !strings.Contains(event.Data["decision_reason"].(string), "Registered provider CLI") {
				t.Fatalf("missing automatic permission evidence: %+v", event)
			}
		}
	}
	if !found {
		t.Fatal("automatic permission decision was not recorded")
	}
	// Any other destination the live run was refused also applies
	// automatically: no judge turn, no operator popup, recorded evidence.
	decided := func(object string, allow bool) bool {
		t.Helper()
		history, err := runtime.Store().ListEvents(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range history {
			if event.Type == "PERMISSION_DECIDED" && event.Data["object"] == object && event.Data["allow"] == allow && (!allow || event.Data["decided_by"] == "marshal-runtime" && event.Data["source"] == "marshal") {
				return true
			}
		}
		return false
	}
	for _, host := range []string{"unrelated.example:443", "chatgpt.com.evil.example:443"} {
		appendEvent("notification-"+host, events.EventTypeNetworkEgressNotification, map[string]any{"endpoint": host, "requestable": true})
		req.Object, req.Who, req.Reason = host, "codex", "required provider vendor domain"
		if !w.routeBuildPermission(t.Context(), req) || runtime.EgressRequestPending(req.RunID, req.Object) || len(w.permissions.build.pending) != 0 || !w.permissions.queue.Empty() {
			t.Fatal("open egress waited for a decision:", host)
		}
		if !decided(host, true) {
			t.Fatal("open egress grant not recorded:", host)
		}
	}
	// Request text cannot invent a live run; the runtime denies it without
	// asking anyone.
	req.Object, req.RunID = "other.example:443", "RUN-unregistered"
	if !w.routeBuildPermission(t.Context(), req) || len(w.permissions.build.pending) != 0 || !decided(req.Object, false) {
		t.Fatal("request text supplied a run without a live refusal")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	req.Object, req.RunID = "late.example:443", "RUN-provider"
	appendEvent("notification-late", events.EventTypeNetworkEgressNotification, map[string]any{"endpoint": req.Object, "requestable": true})
	if !w.routeBuildPermission(t.Context(), req) || !decided(req.Object, false) {
		t.Fatal("ended run received an automatic grant")
	}
}
