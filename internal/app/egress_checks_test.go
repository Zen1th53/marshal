package app

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
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
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "task", Worker: "worker", Mode: marshal.Governed, Branch: statepath.RefName + "/plan/task", Checks: []marshal.Check{{Command: "touch " + marker}}}}}
	dir := filepath.Join(s.Worktrees, "TASK-plan-integration")
	head := marshalGit(t, repo, "rev-parse", "HEAD")
	run.BaseCommit = head
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
	if statepath.PublicEngine {
		if err == nil || !strings.Contains(err.Error(), "missing or malformed") {
			t.Fatalf("private missing runner proof admitted: %v", err)
		}
		return
	}
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
	if openEgressEdition {
		t.Skip("the private edition grants proxied destinations at first connection; TestEditionEgressUndecidedDestination covers it")
	}
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
	endpoint := net.JoinHostPort(host, port)
	// Refused proxy requests must be refused by the proxy (403), not by a
	// timeout. The IPv6 probe and pause model curl's own AF_INET6 probe socket
	// (refused with evidence) running ahead of its proxy request on a slow
	// runner: an extra refusal must not be taken for the proxy refusal.
	command := fmt.Sprintf(`python3 -c 'import socket
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
try: s.connect(("127.0.0.1",%[1]s));raise RuntimeError("direct reached")
except PermissionError: pass' || exit 10
python3 -c 'import socket
try: socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
except PermissionError: pass'
sleep .3
code=$(curl --silent --output /dev/null --write-out '%%{http_code}' --max-time 10 --noproxy '' --proxy http://127.0.0.1:18080 %[2]s)
[ "$code" = 403 ] || { echo "ungranted request: $code"; exit 11; }
while [ ! -f grant ]; do sleep .02; done
curl --silent --show-error --fail --max-time 10 --noproxy '' --proxy http://127.0.0.1:18080 %[2]s || exit 12
while [ ! -f revoke ]; do sleep .02; done
code=$(curl --silent --output /dev/null --write-out '%%{http_code}' --max-time 10 --noproxy '' --proxy http://127.0.0.1:18080 %[2]s)
[ "$code" = 403 ] || { echo "revoked request: $code"; exit 13; }
`, port, server.URL)
	done := make(chan marshal.CommandRecord, 1)
	go func() { done <- r.runGovernedCheck(ctx, "RUN-plan", "TASK-check", "checker", dir, command) }()
	var rows []EgressStatus
	var alerts []EgressAlert
	for {
		rows = r.EgressStatus()
		alerts, err = r.EgressNotifications(t.Context())
		// Grant only after the proxy itself refused this endpoint. Counting
		// alerts is not enough: other refused syscalls (curl's IPv6 probe)
		// also notify, and a grant issued before the proxy evaluates the
		// first request rightly lets that request through.
		if err == nil && len(rows) == 1 && rows[0].ParentRunID == "RUN-plan" && rows[0].Provider == "check" && len(rows[0].Allowed) == 0 && egressRefusedBy(t, r, rows[0].RunID, endpoint, "socket supervisor") && egressRefusedBy(t, r, rows[0].RunID, endpoint, "proxy attempt") {
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
		// The scope ends with the check, so a check that already exited
		// explains the refusal better than the refusal does.
		select {
		case result := <-done:
			t.Fatalf("check exited before revocation: %+v (%v)", result, err)
		default:
		}
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

// egressRefusedBy reports whether the run's evidence holds a refused attempt
// on endpoint from source ("socket supervisor" or "proxy attempt");
// egressAttempt also matches allowed ones.
func egressRefusedBy(t *testing.T, r *Runtime, runID, endpoint, source string) bool {
	t.Helper()
	return egressAttempt(t, r, runID, endpoint, source, false)
}

func egressAttempt(t *testing.T, r *Runtime, runID, endpoint, source string, allowed bool) bool {
	t.Helper()
	history, err := r.store.Since(t.Context(), 0)
	if err != nil {
		return false
	}
	for _, event := range history {
		if event.RunID == runID && event.Type == events.EventTypeNetworkEgressAttempt && event.Data["source"] == source && event.Data["endpoint"] == endpoint && event.Data["allowed"] == allowed {
			return true
		}
	}
	return false
}

// Every check gets its own network namespace, so 127.0.0.1:18080 inside it is
// a bridge to that run's own proxy socket. A grant held by a concurrent run
// must not admit another run's request to the same destination.
func TestGovernedCheckGrantDoesNotReachConcurrentRun(t *testing.T) {
	if openEgressEdition {
		t.Skip("the private edition grants proxied destinations at first connection")
	}
	r := openNetpolRuntime(t)
	if !r.egressEnforcementAvailable() {
		t.Skip("required namespaces/bridge unavailable")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl unavailable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("approved")) }))
	defer server.Close()
	endpoint := strings.TrimPrefix(server.URL, "http://")
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	proxied := fmt.Sprintf(`curl --silent --output /dev/null --write-out '%%{http_code}' --max-time 10 --noproxy '' --proxy http://127.0.0.1:18080 %s`, server.URL)
	granted, other := t.TempDir(), t.TempDir()
	grantedDone, otherDone := make(chan marshal.CommandRecord, 1), make(chan marshal.CommandRecord, 1)
	go func() {
		grantedDone <- r.runGovernedCheck(ctx, "RUN-granted", "TASK-granted", "checker", granted, `while [ ! -f grant ]; do sleep .02; done
code=$(`+proxied+`); [ "$code" = 200 ] || { echo "granted request: $code"; exit 12; }
while [ ! -f finish ]; do sleep .02; done`)
	}()
	go func() {
		otherDone <- r.runGovernedCheck(ctx, "RUN-other", "TASK-other", "checker", other, `while [ ! -f probe ]; do sleep .02; done
code=$(`+proxied+`); [ "$code" = 403 ] || { echo "ungranted request: $code"; exit 11; }`)
	}()
	runs := map[string]string{}
	for len(runs) < 2 {
		for _, row := range r.EgressStatus() {
			runs[row.ParentRunID] = row.RunID
		}
		select {
		case result := <-grantedDone:
			t.Fatalf("granted check exited early: %+v", result)
		case result := <-otherDone:
			t.Fatalf("other check exited early: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := r.CommandEgress(control.Context(t.Context()), runs["RUN-granted"], "allow", endpoint); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(granted, "grant"), nil, 0600)
	// The check's worktree is read-only to it; its allowed proxy attempt is
	// the signal that the grant is live and in use.
	for !egressAttempt(t, r, runs["RUN-granted"], endpoint, "proxy attempt", true) {
		select {
		case result := <-grantedDone:
			t.Fatalf("granted check: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// The grant is live in the first run while the second run asks.
	os.WriteFile(filepath.Join(other, "probe"), nil, 0600)
	select {
	case result := <-otherDone:
		if result.ExitCode != 0 {
			t.Fatalf("concurrent run's grant reached another run: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !egressRefusedBy(t, r, runs["RUN-other"], endpoint, "proxy attempt") {
		t.Fatal("other run's proxy did not record the refusal")
	}
	os.WriteFile(filepath.Join(granted, "finish"), nil, 0600)
	select {
	case result := <-grantedDone:
		if result.ExitCode != 0 {
			t.Fatalf("granted check %+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
