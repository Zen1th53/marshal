package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestGovernedIntegrationUsesRunBoundCheckRunner(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	marker := filepath.Join(t.TempDir(), "host-check-ran")
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "task", Worker: "worker", Mode: marshal.Governed, Branch: "marshal/plan/task", Checks: []marshal.Check{{Command: "touch " + marker}}}}}
	dir := filepath.Join(s.Worktrees, "TASK-plan-integration")
	head := marshalGit(t, repo, "rev-parse", "HEAD")
	marshalGit(t, repo, "worktree", "add", "--detach", dir, head)
	called := 0
	s.GovernedCheck = func(_ context.Context, parent, task, worker, checkout, command string) marshal.CommandRecord {
		called++
		if parent != "plan" || task != "task" || worker != "worker" || checkout == dir || marshalGit(t, checkout, "rev-parse", "HEAD") != head {
			t.Fatalf("unbound check %s %s %s %s", parent, task, worker, checkout)
		}
		return marshal.CommandRecord{Command: command, ExitCode: 1, Output: "unapproved connection refused"}
	}
	session, _, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || session.RequiredChecks["task#0"] != verification.StatusFail {
		t.Fatalf("check bypassed runner: %d %+v", called, session)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("check executed on host")
	}
	s.GovernedCheck = nil
	session, _, err = s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	if session.RequiredChecks["task#0"] != verification.StatusFail {
		t.Fatal("missing sandbox did not fail closed")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("missing sandbox fell back to host")
	}
}

func TestGovernedCheckRefusalEvidenceAndOperatorGrantRevoke(t *testing.T) {
	r := openNetpolRuntime(t)
	if !r.egressEnforcementAvailable() {
		t.Skip("required namespaces/bridge unavailable")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl unavailable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("approved")) }))
	defer server.Close()
	dir := t.TempDir()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := fmt.Sprintf(`python3 -c 'import socket
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
try: s.connect(("127.0.0.1",%s));raise RuntimeError("direct reached")
except PermissionError: pass' || exit 10
if curl --silent --show-error --fail --max-time 2 --noproxy '' --proxy http://127.0.0.1:18080 %s; then exit 11; fi
while [ ! -f grant ]; do sleep .02; done
curl --silent --show-error --fail --max-time 2 --noproxy '' --proxy http://127.0.0.1:18080 %s || exit 12
while [ ! -f revoke ]; do sleep .02; done
if curl --silent --show-error --fail --max-time 2 --noproxy '' --proxy http://127.0.0.1:18080 %s; then exit 13; fi
`, port, server.URL, server.URL, server.URL)
	done := make(chan marshal.CommandRecord, 1)
	go func() { done <- r.runGovernedCheck(ctx, "RUN-plan", "TASK-check", "checker", dir, command) }()
	var rows []EgressStatus
	var alerts []EgressAlert
	for {
		rows = r.EgressStatus()
		alerts, err = r.EgressNotifications(t.Context())
		if err == nil && len(rows) == 1 && rows[0].ParentRunID == "RUN-plan" && rows[0].Provider == "check" && len(rows[0].Allowed) == 0 && len(alerts) >= 2 {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("check exited before refusal: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if len(rows) != 1 || rows[0].ParentRunID != "RUN-plan" || rows[0].Provider != "check" || len(rows[0].Allowed) != 0 {
		t.Fatalf("check scope: %+v", rows)
	}
	if err != nil || len(alerts) < 2 {
		t.Fatalf("direct and proxy refusals missing: %+v %v", alerts, err)
	}
	endpoint := net.JoinHostPort(host, port)
	if err := r.CommandEgress(control.Context(t.Context()), rows[0].RunID, "allow", endpoint); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "grant"), nil, 0600)
	for {
		var found bool
		history, err := r.store.Since(t.Context(), 0)
		if err == nil {
			for _, event := range history {
				if event.RunID == rows[0].RunID {
					if event.Type == events.EventTypeNetworkEgressAllowed {
						found = true
						break
					}
					if event.Type == events.EventTypeNetworkEgressAttempt {
						if allowed, ok := event.Data["allowed"].(bool); ok && allowed {
							found = true
							break
						}
					}
				}
			}
		}
		if found {
			time.Sleep(50 * time.Millisecond)
			break
		}
		select {
		case result := <-done:
			t.Fatalf("check exited before allowed request: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := r.CommandEgress(control.Context(t.Context()), rows[0].RunID, "revoke", endpoint); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "revoke"), nil, 0600)
	select {
	case result := <-done:
		if result.ExitCode != 0 || !strings.Contains(result.Output, "approved") {
			t.Fatalf("check %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if len(r.EgressStatus()) != 0 {
		t.Fatal("check scope survived completion")
	}
	alerts, err = r.EgressNotifications(t.Context())
	if err != nil || len(alerts) < 3 {
		t.Fatal("revocation refusal did not persist")
	}
}
