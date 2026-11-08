package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
)

// hostWatchActor names the host watcher wherever it asks for something. It
// is never the owner: a request it posts is decided by the owner, and the
// decision's audit names both.
const hostWatchActor = "host-watch"

// laneCapRequest is a lane-cap change the host watcher asks the owner for.
type laneCapRequest struct {
	RunID    string
	From, To int // project global-cap values; 0 is the edition default
	Reason   string
	Restore  bool
}

// laneCapSink is where the thermal guard reads the cap and posts its
// requests. The workspace implements it with the inbox and the
// /marshal settings path; tests use a fake.
type laneCapSink interface {
	// Dispatching names the dispatching run and its effective global lane
	// cap, or says why there is nothing to lower.
	Dispatching() (runID string, effective int, why string)
	// ProjectCap is the project's global-cap setting; 0 is the default.
	ProjectCap() (int, error)
	// Request posts a change for the owner to accept and returns its key.
	Request(laneCapRequest) (string, error)
	// Withdraw takes back a request the owner has not decided yet. It
	// reports whether the request was still undecided.
	Withdraw(key string) bool
}

// hostGuardView is the guard as the Host view shows it.
type hostGuardView struct {
	Active  bool
	Cap     int // effective cap of the dispatching run when the guard last looked; 0 unknown
	Lowered int // cap asked for while active
	Pending bool
}

// hostLaneGuard halves the lane cap while the thermal rule is active and
// restores it on release. It never writes a setting itself: it asks the
// owner through the sink, and only an accepted request changes anything.
type hostLaneGuard struct {
	sink laneCapSink

	mu         sync.Mutex
	active     bool
	cap        int
	to         int
	prev       int // project setting before the lowering
	key        string
	restoreKey string
}

func (g *hostLaneGuard) view() hostGuardView {
	g.mu.Lock()
	defer g.mu.Unlock()
	return hostGuardView{Active: g.active, Cap: g.cap, Lowered: g.to, Pending: g.key != ""}
}

// trigger runs on the thermal rule's rising edge and says what it did.
func (g *hostLaneGuard) trigger(avg float64) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active = true
	if g.restoreKey != "" {
		g.sink.Withdraw(g.restoreKey)
		g.restoreKey = ""
	}
	runID, cur, why := g.sink.Dispatching()
	if why != "" {
		g.key, g.to = "", 0
		return why + "; lane cap unchanged"
	}
	g.cap = cur
	to := max(2, cur/2)
	if to >= cur {
		g.key, g.to = "", 0
		return fmt.Sprintf("lane cap already %d; nothing to lower", cur)
	}
	prev, err := g.sink.ProjectCap()
	if err != nil {
		g.key, g.to = "", 0
		return "could not read the lane cap: " + oneLine(err.Error())
	}
	reason := fmt.Sprintf("CPU package 30 s avg %.1f °C > 90; halve lanes %d → %d. Host watch asks to restore when < 85 °C.", avg, cur, to)
	key, err := g.sink.Request(laneCapRequest{RunID: runID, From: prev, To: to, Reason: reason})
	if err != nil {
		g.key, g.to = "", 0
		return "could not ask the owner: " + oneLine(err.Error())
	}
	g.key, g.to, g.prev = key, to, prev
	return fmt.Sprintf("asked owner: lane cap %d → %d (inbox)", cur, to)
}

// release runs on the thermal rule's falling edge and says what it did.
func (g *hostLaneGuard) release() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active {
		return ""
	}
	g.active = false
	to, prev, key := g.to, g.prev, g.key
	g.key, g.to = "", 0
	if to == 0 {
		return "nothing to restore"
	}
	if key != "" && g.sink.Withdraw(key) {
		return fmt.Sprintf("lowering to %d withdrawn before a decision", to)
	}
	cur, err := g.sink.ProjectCap()
	if err != nil {
		return "could not read the lane cap: " + oneLine(err.Error())
	}
	if cur != to {
		return "lowering was not applied; nothing to restore"
	}
	restoreKey, err := g.sink.Request(laneCapRequest{From: to, To: prev, Restore: true, Reason: fmt.Sprintf("CPU package back below 85 °C; restore the lane cap host watch lowered to %d.", to)})
	if err != nil {
		return "could not ask the owner to restore: " + oneLine(err.Error())
	}
	g.restoreKey = restoreKey
	return fmt.Sprintf("asked owner: restore lane cap %d → %s (inbox)", to, capText(prev))
}

func capText(n int) string {
	if n == 0 {
		return "default"
	}
	return strconv.Itoa(n)
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

// workspaceLaneCapSink posts the guard's requests to the Board inbox as
// /marshal settings global-cap commands requested by host-watch. Accepting
// one is an owner permission decision, audited as PERMISSION_DECIDED with
// requester host-watch, and then runs the ordinary settings command.
type workspaceLaneCapSink struct{ w *Workspace }

func (s workspaceLaneCapSink) Dispatching() (string, int, string) {
	p := s.w.marshalPanel()
	if p == nil {
		return "", 0, "no Marshal run is dispatching"
	}
	// The run snapshot's state is the one the Board shows.
	state := p.run.State
	if state == "" {
		state = p.State
	}
	if state != marshal.Dispatching {
		return "", 0, "no Marshal run is dispatching"
	}
	n, ok := effectiveGlobalCap(p.run.Settings)
	if !ok {
		return "", 0, "this edition has no global lane cap"
	}
	return p.RunID, n, ""
}

func (s workspaceLaneCapSink) ProjectCap() (int, error) {
	if s.w.runtime == nil {
		return 0, errors.New("no runtime attached")
	}
	service := s.w.runtime.Marshal()
	if service == nil {
		return 0, errors.New("no project is open")
	}
	record, err := service.Store.GetMarshalSettings(context.Background(), service.ProjectID)
	if err != nil {
		return 0, err
	}
	return projectGlobalCap(record.Value), nil
}

func (s workspaceLaneCapSink) Request(r laneCapRequest) (string, error) {
	scope := "this project, next Marshal run"
	if r.RunID != "" {
		scope += "; run " + r.RunID + " keeps its approved cap"
	}
	req := permission.Request{Kind: "marshal-command", Object: fmt.Sprintf("/marshal settings global-cap %d", r.To), Scope: scope, Who: hostWatchActor, Reason: r.Reason}
	key := req.Key()
	w := s.w
	w.permissions.mu.Lock()
	if w.permissions.hostCapLive == nil {
		w.permissions.hostCapLive = map[string]bool{}
	}
	w.permissions.hostCapLive[key] = true
	w.permissions.mu.Unlock()
	if err := w.queuePermissionChecked(req); err != nil {
		w.permissions.mu.Lock()
		delete(w.permissions.hostCapLive, key)
		w.permissions.mu.Unlock()
		return "", err
	}
	return key, nil
}

func (s workspaceLaneCapSink) Withdraw(key string) bool {
	w := s.w
	w.permissions.mu.Lock()
	undecided := w.permissions.outstanding[key]
	delete(w.permissions.hostCapLive, key)
	inbox := w.permissions.inbox[:0:0]
	for _, req := range w.permissions.inbox {
		if req.Key() == key {
			delete(w.permissions.outstanding, key)
			continue
		}
		inbox = append(inbox, req)
	}
	w.permissions.inbox = inbox
	w.permissions.mu.Unlock()
	w.requestRepaint()
	return undecided
}

// takeHostCapRequest admits an accepted host-watch request once, and only
// while the guard still stands behind it.
func (w *Workspace) takeHostCapRequest(req permission.Request) error {
	parts := strings.Fields(req.Object)
	if len(parts) != 4 || parts[0] != "/marshal" || parts[1] != "settings" || parts[2] != "global-cap" {
		return errors.New("invalid host-watch request")
	}
	if n, err := strconv.Atoi(parts[3]); err != nil || n < 0 || strconv.Itoa(n) != parts[3] {
		return errors.New("invalid host-watch request")
	}
	// A Marshal decision binds its launch and occurrence onto the request;
	// the guard posted it without them, so match on the posted identity.
	posted := req
	posted.SourceLaunch, posted.SourceOccurrence = "", ""
	key := posted.Key()
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	if !w.permissions.hostCapLive[key] {
		return errors.New("host-watch request expired")
	}
	delete(w.permissions.hostCapLive, key)
	return nil
}
