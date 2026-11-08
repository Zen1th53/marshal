package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// fakeCapSink records the guard's requests. accept makes the owner accept
// each request at once.
type fakeCapSink struct {
	why        string
	cap        int
	project    int
	accept     bool
	requests   []laneCapRequest
	pending    map[string]bool
	withdrawn  []string
	projectErr error
}

func (f *fakeCapSink) Dispatching() (string, int, string) {
	if f.why != "" {
		return "", 0, f.why
	}
	return "R-7f3a", f.cap, ""
}
func (f *fakeCapSink) ProjectCap() (int, error) { return f.project, f.projectErr }
func (f *fakeCapSink) Request(r laneCapRequest) (string, error) {
	f.requests = append(f.requests, r)
	key := "req-" + strconv.Itoa(len(f.requests))
	if f.pending == nil {
		f.pending = map[string]bool{}
	}
	if f.accept {
		f.project = r.To
	} else {
		f.pending[key] = true
	}
	return key, nil
}
func (f *fakeCapSink) Withdraw(key string) bool {
	f.withdrawn = append(f.withdrawn, key)
	was := f.pending[key]
	delete(f.pending, key)
	return was
}

// The workspace sink posts host-watch requests to the permission queue,
// never as the owner, and admits an accepted one once while it is live.
func TestWorkspaceLaneCapSink(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	defer w.Close()
	sink := workspaceLaneCapSink{w}
	if _, _, why := sink.Dispatching(); why == "" {
		t.Fatal("no run, yet the sink reports one dispatching")
	}
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "R-1", State: marshal.Dispatching, run: marshal.Run{State: marshal.Dispatching}}
	w.mu.Unlock()
	runID, n, why := sink.Dispatching()
	if c, ok := effectiveGlobalCap(marshal.Settings{}); ok {
		if why != "" || runID != "R-1" || n != c {
			t.Fatalf("Dispatching = %q %d %q", runID, n, why)
		}
	} else if why != "this edition has no global lane cap" {
		t.Fatalf("default edition why = %q", why)
	}
	key, err := sink.Request(laneCapRequest{RunID: "R-1", From: 0, To: 6, Reason: "hot"})
	if err != nil {
		t.Fatal(err)
	}
	taken := w.permissions.queue.Take(false)
	if len(taken) != 1 {
		t.Fatalf("queued %+v", taken)
	}
	req := taken[0]
	if req.Who != hostWatchActor || req.Kind != "marshal-command" || req.Object != "/marshal settings global-cap 6" || req.RunID != "" || req.Key() != key {
		t.Fatalf("request %+v", req)
	}
	if !strings.Contains(req.Scope, "next Marshal run") || !strings.Contains(req.Scope, "R-1 keeps its approved cap") {
		t.Fatalf("scope %q", req.Scope)
	}
	// The request sits in the Board inbox; withdrawing it removes it.
	w.permissions.mu.Lock()
	w.permissions.inbox = append(w.permissions.inbox, req)
	w.permissions.mu.Unlock()
	if !sink.Withdraw(key) {
		t.Fatal("undecided request not reported as withdrawn")
	}
	if len(w.boardInbox()) != 0 {
		t.Fatal("withdrawn request still in the inbox")
	}
	if err := w.takeHostCapRequest(req); err == nil {
		t.Fatal("withdrawn request admitted")
	}
	// A live request is admitted once.
	_, err = sink.Request(laneCapRequest{To: 6})
	if err != nil {
		t.Fatal(err)
	}
	req = w.permissions.queue.Take(false)[0]
	if err := w.takeHostCapRequest(req); err != nil {
		t.Fatal(err)
	}
	if err := w.takeHostCapRequest(req); err == nil {
		t.Fatal("request admitted twice")
	}
	bad := req
	bad.Object = "/marshal settings execution-rights small-tasks"
	if err := w.takeHostCapRequest(bad); err == nil {
		t.Fatal("non-cap host-watch request admitted")
	}
}

// A host-watch request answered through the Marshal decision channel carries
// the decision's launch and occurrence; it is still the live request.
func TestHostCapRequestAdmitsMarshalBoundDecision(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	defer w.Close()
	sink := workspaceLaneCapSink{w}
	if _, err := sink.Request(laneCapRequest{RunID: "R-1", To: 6, Reason: "hot"}); err != nil {
		t.Fatal(err)
	}
	req := w.permissions.queue.Take(false)[0]
	bound := req
	bound.SourceLaunch, bound.SourceOccurrence = "launch-1", "mcp:occurrence-1"
	if err := w.takeHostCapRequest(bound); err != nil {
		t.Fatalf("Marshal-bound decision refused: %v", err)
	}
	if err := w.takeHostCapRequest(bound); err == nil {
		t.Fatal("Marshal-bound decision admitted twice")
	}
	if err := w.takeHostCapRequest(req); err == nil {
		t.Fatal("request admitted again after the Marshal-bound decision")
	}
}
