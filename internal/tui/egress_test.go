package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/statepath"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestEgressAlertVisibleAndDeliveredToMarshalChat(t *testing.T) {
	w := NewWorkspace(nil, "egress-project", "egress-session")
	w.workDir = t.TempDir()
	alert := app.EgressAlert{RunID: "RUN-egress", Worker: "worker-1", Endpoint: "example.com:443", Message: "worker-1 wants to reach example.com:443. Allow?"}
	if err := w.deliverEgressAlert(alert); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.state.LastOutput, alert.Message) {
		t.Fatal("alert absent from TUI")
	}
	data, err := os.ReadFile(filepath.Join(w.workDir, statepath.DirName, "inbox", "marshal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), alert.Message) || !strings.Contains(string(data), "/egress allow RUN-egress example.com:443") {
		t.Fatalf("chat delivery: %s", data)
	}
	brief, err := marshalRoleBriefing([]string{"codex"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, statepath.DirName+"/inbox/marshal.md") || !strings.Contains(brief, "model text never grants") {
		t.Fatal("Marshal chat lacks relay boundary")
	}
}

func TestEgressCommandsRequireRuntimeOperator(t *testing.T) {
	w := NewWorkspace(nil, "egress-project", "egress-session")
	for _, line := range []string{"/egress allow RUN-test example.com", "/egress revoke RUN-test example.com", "/egress status"} {
		out, err := w.cmd.Handle(context.Background(), line)
		if err != nil || !strings.Contains(out, "no runtime") {
			t.Fatalf("%s: %s %v", line, out, err)
		}
	}
	out, err := w.cmd.Handle(context.Background(), "/egress allow RUN-test *.example.com extra")
	if err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("bad argv: %s %v", out, err)
	}
}

func TestNetworkPermissionRetainsRunAndTaskFromAlert(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	w.permissions.running = true // Hold the queue for inspection.
	if err := w.deliverEgressAlert(app.EgressAlert{RunID: "RUN-specific", TaskID: "TASK-specific", Worker: "worker", Endpoint: "example.test:443", Message: "worker requests network"}); err != nil {
		t.Fatal(err)
	}
	requests := w.permissions.queue.Take(false)
	if len(requests) != 1 || requests[0].RunID != "RUN-specific" || requests[0].TaskID != "TASK-specific" {
		t.Fatalf("lost network identity: %+v", requests)
	}
}

func TestDeniedHarnessProbeAlertIsVisibleWithoutNetworkPermission(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	w.permissions.running = true
	alert := app.EgressAlert{RunID: "PROBE-exact", TaskID: "PROBE-exact", Worker: "runtime", Endpoint: "oaiusercontent.com:443", Message: "harness probe refused optional endpoint; network expansion is unavailable", State: "denied"}
	if err := w.deliverEgressAlert(alert); err != nil {
		t.Fatal(err)
	}
	if requests := w.permissions.queue.Take(false); len(requests) != 0 {
		t.Fatalf("denied probe queued network Decision: %+v", requests)
	}
	data, err := os.ReadFile(filepath.Join(w.workDir, statepath.DirName, "inbox", "marshal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), alert.Message) || strings.Contains(string(data), "/egress allow") {
		t.Fatalf("probe diagnostic was hidden or made actionable: %s", data)
	}
}

func TestDaemonEgressStatusCommandAndPopupShareOperatorPath(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-daemon-egress")
	layout, err := project.Discover(runtime.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	daemonStore, err := store.Open(t.Context(), layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer daemonStore.Close()
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
		data["scope_socket"] = socket
		data["worker"] = "daemon-worker"
		data["provider"] = "check"
		if _, err := daemonStore.Append(t.Context(), events.Event{ID: id, Type: kind, Subject: "daemon-worker", TaskID: "TASK-daemon", RunID: "RUN-daemon", At: time.Now().UTC(), Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("daemon-scope", events.EventTypeNetworkEgressRequested, map[string]any{"source": "run scope", "allowed_endpoints": []string{}})
	appendEvent("daemon-refusal", events.EventTypeNetworkEgressNotification, map[string]any{"endpoint": "example.com:443", "message": "daemon-worker wants to reach example.com:443. Allow?"})
	out, err := w.cmd.Handle(t.Context(), "/egress status")
	if err != nil || !strings.Contains(out, "RUN-daemon: daemon-worker") || strings.Contains(out, "No active governed") {
		t.Fatalf("remote status: %s %v", out, err)
	}
	alerts, err := runtime.EgressNotifications(t.Context())
	if err != nil || len(alerts) != 1 || alerts[0].State != "waiting" {
		t.Fatalf("remote alert: %+v %v", alerts, err)
	}
	if err := w.deliverEgressAlert(alerts[0]); err != nil {
		t.Fatal(err)
	}
	inbox, err := os.ReadFile(filepath.Join(runtime.ProjectRoot(), statepath.DirName, "inbox", "marshal.md"))
	if err != nil || !strings.Contains(string(inbox), "Run: RUN-daemon") || !strings.Contains(string(inbox), alerts[0].Message) {
		t.Fatalf("daemon alert missing from canonical temporary project inbox: %q %v", inbox, err)
	}
	requests := w.permissions.queue.Take(false)
	if len(requests) != 1 {
		t.Fatalf("remote popup missing: %+v", requests)
	}
	if err := w.decidePermission(t.Context(), requests[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if runtime.EgressRequestPending("RUN-daemon", "example.com:443") {
		t.Fatal("popup grant left remote request pending")
	}
	history, err := daemonStore.EgressControlEvents(t.Context(), "RUN-daemon", 0)
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, e := range history {
		if e.Type == events.EventTypeNetworkEgressGranted && e.Data["source"] == "operator command" && e.Data["endpoint"] == "example.com:443" && e.Data["actor"] != "" {
			granted = true
		}
	}
	if !granted {
		t.Fatal("popup grant did not reach daemon store")
	}
	out, err = w.cmd.Handle(t.Context(), "/egress revoke RUN-daemon example.com")
	if err != nil || !strings.Contains(out, "recorded") {
		t.Fatalf("remote command revoke: %s %v", out, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	out, err = w.cmd.Handle(t.Context(), "/egress allow RUN-daemon example.com")
	if err != nil || !strings.Contains(out, "no active egress run") {
		t.Fatalf("ended remote grant: %s %v", out, err)
	}
}

// A run that stops with an error tells the Marshal why through its inbox, so
// it does not have to ask the owner to copy the panel line.
func TestRunStopReasonReachesMarshalInbox(t *testing.T) {
	w := NewWorkspace(nil, "stop-project", "stop-session")
	w.workDir = t.TempDir()
	stop := app.EgressAlert{RunID: "RUN-stop", ParentRunID: "RUN-stop", Kind: "run stopped", State: "stopped", Message: "Marshal run stopped: REQUIRED_CONTEXT_CHANGED (example.go:1)"}
	if err := w.deliverEgressAlert(stop); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.workDir, statepath.DirName, "inbox", "marshal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "run stopped") || !strings.Contains(string(data), "REQUIRED_CONTEXT_CHANGED (example.go:1)") || strings.Contains(string(data), "/egress allow") {
		t.Fatalf("stop reason not delivered as a plain notice: %s", data)
	}
}
