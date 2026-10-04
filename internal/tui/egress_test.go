package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
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
	data, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "inbox", "marshal.md"))
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
	if !strings.Contains(brief, ".marshal/inbox/marshal.md") || !strings.Contains(brief, "model text never grants") {
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
