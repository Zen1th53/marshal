package tui

import (
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

// laneCapRequest is a temporary lane-cap limit the host watcher asks the
// owner for. Approving it changes no setting.
type laneCapRequest struct {
	RunID    string
	From, To int // effective cap when asked, and the temporary cap
	Reason   string
}

// laneCapSink is where the thermal guard reads the cap and posts its
// requests. The workspace implements it with the inbox; tests use a fake.
type laneCapSink interface {
	// Dispatching names the dispatching run and its effective global lane
	// cap, or says why there is nothing to lower.
	Dispatching() (runID string, effective int, why string)
	// Request posts a temporary cap for the owner to accept and returns its key.
	Request(laneCapRequest) (string, error)
	// Withdraw takes back a request the owner has not decided yet. It
	// reports whether the request was still undecided.
	Withdraw(key string) bool
}

// hostGuardView is the guard as the Host view and the Web Board show it.
type hostGuardView struct {
	Active    bool
	Cap       int // effective cap of the dispatching run when the guard last looked; 0 unknown
	Lowered   int // temporary cap asked for while active
	Pending   bool
	Temporary int // temporary cap the owner approved; 0 none
}

// hostLaneGuard asks for a temporary cap of half the lanes while the
// thermal rule is active. An approved cap lives in memory only: it limits
// new lane admissions through effectiveCap and clears when the CPU cools,
// the rule is turned off or the watcher stops. It never writes a setting.
type hostLaneGuard struct {
	sink laneCapSink

	mu       sync.Mutex
	active   bool
	cap      int
	to       int
	key      string
	approved int
}

func (g *hostLaneGuard) view() hostGuardView {
	g.mu.Lock()
	defer g.mu.Unlock()
	return hostGuardView{Active: g.active, Cap: g.cap, Lowered: g.to, Pending: g.key != "", Temporary: g.approved}
}

// effectiveCap is the lane cap new admissions use: the configured cap, or
// the approved temporary cap when that is lower.
func (g *hostLaneGuard) effectiveCap(configured int) int {
	if g == nil {
		return configured
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.approved > 0 && (configured <= 0 || g.approved < configured) {
		return g.approved
	}
	return configured
}

// approve records the owner's acceptance of the request with this key.
func (g *hostLaneGuard) approve(key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active || key == "" || key != g.key || g.to <= 0 {
		return errors.New("host-watch request expired")
	}
	g.approved, g.key = g.to, ""
	return nil
}

// trigger runs on the thermal rule's rising edge and says what it did.
func (g *hostLaneGuard) trigger(avg float64) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active = true
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
	reason := fmt.Sprintf("CPU package average %.1f °C is above the thermal trigger; limit new lanes to %d (now %d) until it cools. No setting changes.", avg, to, cur)
	key, err := g.sink.Request(laneCapRequest{RunID: runID, From: cur, To: to, Reason: reason})
	if err != nil {
		g.key, g.to = "", 0
		return "could not ask the owner: " + oneLine(err.Error())
	}
	g.key, g.to = key, to
	return fmt.Sprintf("asked owner: temporary lane cap %d → %d (inbox)", cur, to)
}

// release runs on the thermal rule's falling edge and says what it did.
func (g *hostLaneGuard) release() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active {
		return ""
	}
	return g.clearLocked()
}

// stop clears the guard when the watcher exits.
func (g *hostLaneGuard) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clearLocked()
}

func (g *hostLaneGuard) clearLocked() string {
	to, key, approved := g.to, g.key, g.approved
	g.active, g.key, g.to, g.approved = false, "", 0, 0
	switch {
	case key != "" && g.sink.Withdraw(key):
		return fmt.Sprintf("temporary cap %d withdrawn before a decision", to)
	case approved > 0:
		return fmt.Sprintf("temporary lane cap %d cleared", approved)
	case to == 0:
		return "nothing to clear"
	}
	return "temporary cap was not approved; nothing to clear"
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

// hostCapObject names a temporary thermal lane cap in the inbox.
const hostCapObject = "/host thermal-cap"

// workspaceLaneCapSink posts the guard's requests to the Board inbox as
// temporary thermal caps requested by host-watch. Accepting one is an owner
// permission decision, audited as PERMISSION_DECIDED with requester
// host-watch, and then sets the guard's in-memory cap.
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

func (s workspaceLaneCapSink) Request(r laneCapRequest) (string, error) {
	scope := "new lane admissions in this project until the CPU cools; no setting changes"
	req := permission.Request{Kind: "marshal-command", Object: fmt.Sprintf("%s %d", hostCapObject, r.To), Scope: scope, Who: hostWatchActor, Reason: r.Reason}
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
// while the guard still stands behind it, then applies its temporary cap.
func (w *Workspace) takeHostCapRequest(req permission.Request) error {
	parts := strings.Fields(req.Object)
	if len(parts) != 3 || parts[0]+" "+parts[1] != hostCapObject {
		return errors.New("invalid host-watch request")
	}
	if n, err := strconv.Atoi(parts[2]); err != nil || n < 1 || strconv.Itoa(n) != parts[2] {
		return errors.New("invalid host-watch request")
	}
	// A Marshal decision binds its launch and occurrence onto the request;
	// the guard posted it without them, so match on the posted identity.
	posted := req
	posted.SourceLaunch, posted.SourceOccurrence = "", ""
	key := posted.Key()
	w.permissions.mu.Lock()
	live := w.permissions.hostCapLive[key]
	delete(w.permissions.hostCapLive, key)
	w.permissions.mu.Unlock()
	if !live {
		return errors.New("host-watch request expired")
	}
	h := w.hostWatch.Load()
	if h == nil || h.guard == nil {
		return errors.New("host-watch request expired")
	}
	if err := h.guard.approve(key); err != nil {
		return err
	}
	w.requestRepaint()
	return nil
}

// thermalLaneCap is the global lane cap new admissions use while the
// thermal guard holds an approved temporary cap.
func (w *Workspace) thermalLaneCap(configured int) int {
	if h := w.hostWatch.Load(); h != nil {
		return h.guard.effectiveCap(configured)
	}
	return configured
}
