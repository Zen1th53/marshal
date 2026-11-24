package webboard

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

func TestEncodeRunningFrame(t *testing.T) {
	b := fixtureRunning()
	b.Now = time.Date(2026, 11, 20, 19, 31, 5, 0, time.FixedZone("CET", 3600))
	body, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if f["v"] != float64(1) || f["screen"] != "running" {
		t.Fatalf("v/screen: %v %v", f["v"], f["screen"])
	}
	if f["now"] != "2026-11-20T19:31:05+01:00" {
		t.Fatalf("now is not RFC 3339 with its zone: %v", f["now"])
	}
	top := f["top"].(map[string]any)
	if top["clock"] != "2026-11-20T19:31:00Z" || top["state"] != "RUNNING" || top["stateTone"] != "info" {
		t.Fatalf("top: %v", top)
	}
	lane := f["lanes"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if lane["elapsedSec"] != float64(840) || lane["lastSignalSec"] != float64(8) || lane["selected"] != true {
		t.Fatalf("lane durations are not seconds: %v", lane)
	}
	plan := f["plan"].(map[string]any)
	if plan["etaSec"] != float64(2700) || len(plan["throughput"].([]any)) != 12 {
		t.Fatalf("plan: %v", plan)
	}
	if f["decision"] != nil || f["paused"] != nil {
		t.Fatal("a running frame carries no card")
	}
	ev := f["activity"].([]any)[2].(map[string]any)
	if ev["count"] != float64(3) || ev["last"] != "2026-11-20T19:29:00Z" {
		t.Fatalf("event: %v", ev)
	}
}

// An empty Board encodes every list as [], never null, and unknown times
// as "".
func TestEncodeEmptyBoard(t *testing.T) {
	body, err := Encode(vm.Board{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "mode") // Mode uses null for individual observations not yet collected.
	rest, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(rest), "null") != 3 { // decision, paused, waiting
		t.Fatalf("a list encodes as null: %s", s)
	}
	for _, want := range []string{`"tasks":[]`, `"workers":[]`, `"limits":[]`, `"activity":[]`, `"requests":[]`, `"keys":[]`, `"items":[]`, `"lines":[]`, `"stages":[]`, `"checks":[]`, `"defaults":[]`, `"now":""`, `"decision":null`, `"paused":null`, `"waiting":null`, `"approvals":[]`} {
		if !strings.Contains(s, want) {
			t.Errorf("empty frame lacks %s: %s", want, s)
		}
	}
}

// The architecture review link carries its private token, the owner's
// Accept: it never reaches the browser, nor do the collectors' refs.
func TestFrameCarriesNoTokenOrRefs(t *testing.T) {
	b := fixtureDecision()
	b.Activity = append(b.Activity, vm.Event{Kind: "arch", Text: "opened http://127.0.0.1:37667/?token=7964deadbeef462b"})
	b.Decision.Lines = append(b.Decision.Lines, vm.CardLine{Text: "see http://localhost:37667/?x=1&t=7964deadbeef462b now"})
	body, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, bad := range []string{"token=7", "t=7", "7964deadbeef", "secret-ref"} {
		if strings.Contains(s, bad) {
			t.Errorf("frame carries %q: %s", bad, s)
		}
	}
	if !strings.Contains(s, `"hasLink":true`) || !strings.Contains(s, `"noDisplay":true`) {
		t.Fatalf("the card does not say a link exists: %s", s)
	}
}

// excluded are the view model fields the browser never gets.
var excluded = map[string]bool{
	"Decision.Link": true, // the architecture review URL with its token
	"Decision.Refs": true, // collector ids
	"CardLine.Ref":  true,
	"Inbox.Refs":    true,
	// The terminal home's own: the page is the Web Board.
	"Board.Web": true,
}

// Every vm field is either sent (a wire field of the same name) or listed in
// excluded: a field added to the view model must be decided here.
func TestFrameMapsEveryField(t *testing.T) {
	pairs := map[reflect.Type]reflect.Type{
		reflect.TypeOf(vm.Mode{}):  reflect.TypeOf(Mode{}),
		reflect.TypeOf(vm.Board{}): reflect.TypeOf(Frame{}), reflect.TypeOf(vm.TopBar{}): reflect.TypeOf(TopBar{}),
		reflect.TypeOf(vm.NowLine{}): reflect.TypeOf(NowLine{}), reflect.TypeOf(vm.Marshal{}): reflect.TypeOf(Marshal{}),
		reflect.TypeOf(vm.Stage{}): reflect.TypeOf(Stage{}), reflect.TypeOf(vm.Plan{}): reflect.TypeOf(Plan{}),
		reflect.TypeOf(vm.Lane{}): reflect.TypeOf(Lane{}), reflect.TypeOf(vm.Lanes{}): reflect.TypeOf(Lanes{}),
		reflect.TypeOf(vm.Task{}): reflect.TypeOf(Task{}), reflect.TypeOf(vm.Worker{}): reflect.TypeOf(Worker{}),
		reflect.TypeOf(vm.Inbox{}): reflect.TypeOf(Inbox{}), reflect.TypeOf(vm.Event{}): reflect.TypeOf(Event{}),
		reflect.TypeOf(vm.Step{}): reflect.TypeOf(Step{}), reflect.TypeOf(vm.Steps{}): reflect.TypeOf(Steps{}),
		reflect.TypeOf(vm.Request{}): reflect.TypeOf(Request{}), reflect.TypeOf(vm.Chat{}): reflect.TypeOf(Chat{}),
		reflect.TypeOf(vm.Check{}): reflect.TypeOf(Check{}), reflect.TypeOf(vm.Readiness{}): reflect.TypeOf(Readiness{}),
		reflect.TypeOf(vm.Node{}): reflect.TypeOf(Node{}), reflect.TypeOf(vm.Decision{}): reflect.TypeOf(Decision{}),
		reflect.TypeOf(vm.CardLine{}): reflect.TypeOf(CardLine{}), reflect.TypeOf(vm.Link{}): reflect.TypeOf(Link{}),
		reflect.TypeOf(vm.PausedTask{}): reflect.TypeOf(PausedTask{}), reflect.TypeOf(vm.Paused{}): reflect.TypeOf(Paused{}),
		reflect.TypeOf(vm.Key{}):            reflect.TypeOf(Key{}),
		reflect.TypeOf(vm.ProviderLimits{}): reflect.TypeOf(ProviderLimits{}), reflect.TypeOf(vm.LimitRow{}): reflect.TypeOf(LimitRow{}),
		reflect.TypeOf(vm.System{}): reflect.TypeOf(System{}), reflect.TypeOf(vm.MarshalSays{}): reflect.TypeOf(MarshalSays{}),
		reflect.TypeOf(vm.Signal{}): reflect.TypeOf(Signal{}), reflect.TypeOf(vm.Waiting{}): reflect.TypeOf(Waiting{}),
		reflect.TypeOf(vm.Approval{}): reflect.TypeOf(Approval{}),
		reflect.TypeOf(vm.Choice{}):   reflect.TypeOf(Choice{}), reflect.TypeOf(vm.PlanPreview{}): reflect.TypeOf(PlanPreview{}),
		reflect.TypeOf(vm.PlanTask{}): reflect.TypeOf(PlanTask{}),
	}
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(v reflect.Type) {
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || v.PkgPath() != reflect.TypeOf(vm.Board{}).PkgPath() || seen[v] {
			return
		}
		seen[v] = true
		wire, ok := pairs[v]
		if !ok {
			t.Errorf("vm.%s has no wire type", v.Name())
			return
		}
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			name := v.Name() + "." + f.Name
			if excluded[name] {
				if _, sent := wire.FieldByName(f.Name); sent {
					t.Errorf("%s is excluded but sent", name)
				}
				continue
			}
			if _, sent := wire.FieldByName(f.Name); !sent {
				t.Errorf("%s is neither sent nor excluded", name)
			}
			walk(f.Type)
		}
	}
	walk(reflect.TypeOf(vm.Board{}))
}

// A clock tick alone is no change; a lane's second is.
func TestChangeKeyIgnoresClock(t *testing.T) {
	a, b := fixtureRunning(), fixtureRunning()
	b.Now, b.Top.Clock = b.Now.Add(time.Minute), b.Top.Clock.Add(time.Minute)
	ka, _ := changeKey(a)
	kb, _ := changeKey(b)
	if string(ka) != string(kb) {
		t.Fatal("a clock tick changed the frame key")
	}
	b.Lanes.Items[0].Elapsed += time.Second
	kb, _ = changeKey(b)
	if string(ka) == string(kb) {
		t.Fatal("a lane's elapsed second did not change the frame key")
	}
}

// The limit rows reach the browser with their dates in full (RFC 3339 with
// the zone), -1 for a limit not reported and the rows that decide the state.
func TestEncodeLimits(t *testing.T) {
	zone := time.FixedZone("CET", 3600)
	b := vm.Board{Limits: []vm.ProviderLimits{
		{Provider: "agy", Model: "gemini-4-argon-high", State: vm.LimitUsable, Rows: []vm.LimitRow{
			{Family: "Claude and GPT", Window: "weekly", PercentLeft: 0, ResetsAt: time.Date(2026, 11, 26, 9, 32, 0, 0, zone), ObservedAt: time.Date(2026, 11, 21, 19, 37, 0, 0, zone), Source: "agy /usage"},
			{Family: "Gemini 4 Argon", Window: "weekly", PercentLeft: 99, ResetsAt: time.Date(2026, 11, 24, 23, 48, 0, 0, time.UTC), Source: "agy /usage", Drives: true},
		}},
		{Provider: "claude", State: vm.LimitUsable, Rows: []vm.LimitRow{{PercentLeft: -1, Source: "not reported by the CLI"}}},
		{Provider: "opencode", State: vm.LimitNotChecked},
	}}
	body, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Limits []map[string]any `json:"limits"`
	}
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Limits) != 3 || f.Limits[0]["state"] != "usable" || f.Limits[0]["model"] != "gemini-4-argon-high" {
		t.Fatalf("limits: %v", f.Limits)
	}
	rows := f.Limits[0]["rows"].([]any)
	spent, argon := rows[0].(map[string]any), rows[1].(map[string]any)
	if spent["resetsAt"] != "2026-11-26T09:32:00+01:00" || spent["observedAt"] != "2026-11-21T19:37:00+01:00" || spent["percentLeft"] != float64(0) || spent["drives"] != false {
		t.Fatalf("spent row: %v", spent)
	}
	if argon["family"] != "Gemini 4 Argon" || argon["window"] != "weekly" || argon["resetsAt"] != "2026-11-24T23:48:00Z" || argon["drives"] != true {
		t.Fatalf("argon row: %v", argon)
	}
	claude := f.Limits[1]["rows"].([]any)[0].(map[string]any)
	if claude["percentLeft"] != float64(-1) || claude["source"] != "not reported by the CLI" || claude["resetsAt"] != "" {
		t.Fatalf("not reported row: %v", claude)
	}
	if rows, ok := f.Limits[2]["rows"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("a provider with no rows encodes rows as []: %v", f.Limits[2])
	}
}

// The Marshal's last turn reaches the page scrubbed and capped; whether it
// asks the owner something comes from the Board's From-you line only.
func TestFrameMarshalSays(t *testing.T) {
	f := FromBoard(fixtureRunning())
	if !f.NowLine.ChatAsked || f.NowLine.FromYou != vm.ChatAskedLine {
		t.Fatalf("chat asked: %+v", f.NowLine)
	}
	if m := f.MarshalSays; m.Truncated || m.Answered || m.At != "2026-11-20T19:27:00Z" || !strings.HasPrefix(m.Text, "T02 came back") {
		t.Fatalf("running: %+v", m)
	}
	if m := FromBoard(vm.Board{}).MarshalSays; m.Text != "" || m.At != "" {
		t.Fatalf("nothing said: %+v", m)
	}
	key := "sk-ant-api03-" + strings.Repeat("Zx9Qw3Er7Ty", 6)
	gh := "ghp_" + strings.Repeat("aB3dE5fG7h", 4)
	b := vm.Board{MarshalSays: vm.MarshalSays{Text: "Use " + key + " and " + gh + " to sign in. Done."}}
	body, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{key, gh, "Zx9Qw3Er7Ty", "aB3dE5fG7h"} {
		if strings.Contains(string(body), bad) {
			t.Errorf("a secret reached the frame: %q", bad)
		}
	}
	long := strings.Repeat("ünïcode words ", 200) + "\nAll good?"
	m := FromBoard(vm.Board{MarshalSays: vm.MarshalSays{Text: long}}).MarshalSays
	if !m.Truncated || len([]rune(m.Text)) != SaysMax+1 || !strings.HasSuffix(m.Text, "…") {
		t.Fatalf("cap: truncated=%v len=%d", m.Truncated, len([]rune(m.Text)))
	}
}

// Every limit row reaches the browser with its reset and reading times,
// "not reported" as -1 and the rows that drive the state; the host figures
// go too.
func TestFrameCarriesLimitsAndSystem(t *testing.T) {
	f := FromBoard(fixtureRunning())
	claude := f.Limits[0]
	if claude.Provider != "claude" || len(claude.Rows) != 3 || claude.State != "usable" || !claude.Rows[0].Drives {
		t.Fatalf("claude limits: %+v", claude)
	}
	wk := claude.Rows[1]
	if wk.Family != "" || wk.Window != "weekly" || wk.PercentLeft != 71 || wk.ResetsAt != "2026-11-24T09:00:00Z" ||
		wk.ObservedAt != "2026-11-20T19:29:00Z" || wk.Source != "claude /usage" || wk.Drives {
		t.Fatalf("limit row: %+v", wk)
	}
	if agy := f.Limits[1].Rows[1]; agy.PercentLeft != -1 || agy.ResetsAt != "" {
		t.Fatalf("an unreported window: %+v", agy)
	}
	if s := f.System; s.CPUPct != 64 || s.TempC != 83 || !s.ThermalGuard || s.ThermalCap != 6 || s.UpdateAvailable != "v0.3.9" || s.MCPFailed {
		t.Fatalf("system: %+v", s)
	}
	body, err := Encode(fixtureRunning())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"limits":[{"provider":"claude","model":"sonnet","state":"usable"`, `"window":"5h","percentLeft":58`, `"system":{"cpuPct":64`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("frame lacks %s", want)
		}
	}
	// A board with no limits sends an empty list, never null.
	if f := FromBoard(fixtureReady()); f.Limits == nil || len(f.Limits) != 0 {
		t.Fatalf("ready limits: %#v", f.Limits)
	}
}

func TestModeFrameDoesNotExposeLease(t *testing.T) {
	enabled, serial, active, cap := true, false, 2, 1
	m := vm.Mode{Edition: "standard", Entitlement: "active", RequestStatus: "unknown", Delegation: &serial, ExecutionEnabled: &enabled, EffectiveTier: "ultra", RecordedRunTier: "standard", PlanParallel: &serial, Active: &active, EffectiveCap: &cap, Reasons: []string{"approved plan requires serial work"}}
	body, err := Encode(vm.Board{Mode: m})
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(body, &frame); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(frame["mode"], &fields); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"edition", "entitlement", "requestStatus", "delegation", "executionEnabled", "effectiveTier", "recordedRunTier", "planParallel", "active", "effectiveCap", "reasons"}
	if len(fields) != len(allowed) {
		t.Fatalf("unexpected mode data: %s", frame["mode"])
	}
	for _, name := range allowed {
		if _, ok := fields[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
	if !reflect.DeepEqual(FromBoard(vm.Board{Mode: m}).Mode, Mode{
		Edition: m.Edition, Entitlement: m.Entitlement, RequestStatus: m.RequestStatus,
		Delegation: &serial, ExecutionEnabled: &enabled, EffectiveTier: "ultra", RecordedRunTier: "standard",
		PlanParallel: &serial, Active: &active, EffectiveCap: &cap, Reasons: []string{"approved plan requires serial work"},
	}) || fields["active"] != float64(2) || fields["effectiveCap"] != float64(1) || fields["planParallel"] != false || fields["executionEnabled"] != true {
		t.Fatal(fields)
	}
	unknown := FromBoard(vm.Board{}).Mode
	if unknown.Active != nil || unknown.PlanParallel != nil || unknown.EffectiveCap != nil || unknown.Reasons == nil {
		t.Fatalf("unknown observations: %+v", unknown)
	}
}

// An approval reaches the page with its times and age, and a secret in its
// reason never does; the reason is capped.
func TestApprovalFrameIncludesAgeAndRedactsReason(t *testing.T) {
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	secret := "sk-" + "proj-" + strings.Repeat("abcdef1234567890", 3)
	b := vm.Board{Now: now, Approvals: []vm.Approval{
		{ID: "ap-1", TaskID: "T02", Awaiting: "owner", CreatedAt: now.Add(-3 * time.Minute), ExpiresAt: now.Add(7 * time.Minute),
			Status: "requested", Reason: "push to origin with key " + secret + " " + strings.Repeat("x", 400),
			Command: "/approve execution:ap-1", Actions: []vm.Key{{Key: "/approve execution:ap-1", Label: "approve"}},
			Waiting: &vm.Waiting{Code: vm.WaitAuthority, Label: "approval ap-1 awaits the owner", BlockerID: "ap-1", Source: "approval", Inspect: "/approve execution:ap-1", RetryAt: now.Add(7 * time.Minute)}},
		{ID: "ap-0", TaskID: "T01", Awaiting: "owner", CreatedAt: now.Add(-20 * time.Minute), Status: "expired",
			Waiting: &vm.Waiting{Code: vm.WaitExpiry, Label: "expired: cannot be answered", Source: "approval", Inspect: "Inbox"}},
	}}
	body, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "abcdef1234567890abcdef") {
		t.Fatalf("frame carries the secret: %s", body)
	}
	var f Frame
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Approvals) != 2 {
		t.Fatalf("approvals = %+v", f.Approvals)
	}
	a := f.Approvals[0]
	if a.ID != "ap-1" || a.TaskID != "T02" || a.Awaiting != "owner" || a.Status != "requested" || a.AgeSec != 180 ||
		a.CreatedAt != "2026-03-02T11:57:00Z" || a.ExpiresAt != "2026-03-02T12:07:00Z" || len(a.Actions) != 1 {
		t.Fatalf("approval = %+v", a)
	}
	if a.Waiting == nil || a.Waiting.Code != vm.WaitAuthority || a.Waiting.BlockerID != "ap-1" || a.Waiting.RetryAt != "2026-03-02T12:07:00Z" {
		t.Fatalf("approval wait = %+v", a.Waiting)
	}
	if n := len([]rune(a.Reason)); n > ReasonMax+1 || !strings.HasPrefix(a.Reason, "push to origin") {
		t.Fatalf("reason = %q (%d runes)", a.Reason, n)
	}
	if x := f.Approvals[1]; x.Status != "expired" || len(x.Actions) != 0 || x.AgeSec != 1200 || x.ExpiresAt != "" {
		t.Fatalf("expired approval = %+v", x)
	}
	// The change key has no clock: an approval's age alone pushes no frame.
	later := b
	later.Now = now.Add(time.Minute)
	ka, _ := changeKey(b)
	kb, _ := changeKey(later)
	if string(ka) != string(kb) {
		t.Fatal("an approval's age changed the frame key")
	}
}

// A worker Marshal cannot verify is said to be unverifiable, never exited
// or dead: on the wire and as the page renders it.
func TestUnverifiableWorkerNeverRendersDead(t *testing.T) {
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	b := vm.Board{Now: now, Lanes: vm.Lanes{Items: []vm.Lane{
		{Name: "claude-1", Task: "T01", Step: "unverified", Process: "unverifiable", LastSignal: 20 * time.Second,
			Signal:  vm.Signal{At: now.Add(-20 * time.Second), State: vm.SignalUnverifiable, Label: "cannot verify: the process is in another namespace"},
			Waiting: &vm.Waiting{Code: vm.WaitSignal, Label: "cannot verify: the process is in another namespace", BlockerID: "claude-1", Source: "heartbeat", Inspect: "/marshal status"}},
		{Name: "claude-2", Task: "T02", Step: "no worker", LastSignal: 5 * time.Minute, Stale: true,
			Signal: vm.Signal{At: now.Add(-5 * time.Minute), State: vm.SignalStale, Label: "no heartbeat for 5m: cannot verify the worker, it may still run"}},
	}}}
	body, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	var f Frame
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	for _, l := range f.Lanes.Items {
		if l.Signal.State == vm.SignalExited || !strings.Contains(l.Signal.Label, "cannot verify") || l.Signal.At == "" {
			t.Errorf("lane %s signal = %+v", l.Name, l.Signal)
		}
	}
	if w := f.Lanes.Items[0].Waiting; w == nil || w.Code != vm.WaitSignal || w.Source != "heartbeat" || w.Inspect == "" {
		t.Errorf("unverifiable lane wait = %+v", w)
	}
	for _, bad := range []string{"dead", "exited"} {
		if strings.Contains(strings.ToLower(string(body)), bad) {
			t.Errorf("frame says %q: %s", bad, body)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("node not found: the page rendering check is skipped")
		return
	}
	if out, err := exec.Command(node, "testdata/liveness.cjs").CombinedOutput(); err != nil {
		t.Fatalf("liveness rendering: %v\n%s", err, out)
	}
}

// retained are the wire fields the page does not draw. Each one stays on
// the wire for readers of earlier pages and for the terminal-equivalent
// state it names, so none is removed until its replacement is on the page
// and no consumer reads it.
var retained = map[string]string{
	"SettingsState.-":    "server side only",
	"TopBar.clock":       "the page keeps its own clock",
	"Lane.stale":         "the page derives staleness from lastSignal",
	"Lane.modelWaitSec":  "waiting is told by Lane.waiting",
	"Waiting.blockerId":  "an opaque id; the text names the blocker",
	"Worker.leftPct":     "quota windows are drawn from Limits, but earlier pages read this",
	"Approval.createdAt": "the page orders by expiry",
	"Approval.command":   "the page shows the approval's reason, not its command line",
	"Decision.binding":   "sent back through the action tokens, never drawn",
	"Decision.hasLink":   "the review opens from the Architecture page",
	"Decision.noDisplay": "the review opens from the Architecture page",
	"Link.both":          "drawn as a two-way arrow only where links are listed",
}

// A field sent to the browser is drawn by the page or listed in retained
// with its reason; a retained field is still on the wire. An empty startup
// frame and a frame from before the field existed both decode and encode.
func TestWireFieldDispositionsAndConsumerCompatibility(t *testing.T) {
	js, err := os.ReadFile("page/app.js")
	if err != nil {
		t.Fatal(err)
	}
	drawn := func(name string) bool { return strings.Contains(string(js), "."+name) }
	seen := map[reflect.Type]bool{}
	onWire := map[string]bool{}
	var walk func(reflect.Type)
	walk = func(v reflect.Type) {
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || seen[v] || v.PkgPath() != reflect.TypeOf(Frame{}).PkgPath() {
			return
		}
		seen[v] = true
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" {
				continue
			}
			key := v.Name() + "." + name
			onWire[key] = true
			if why, ok := retained[key]; ok {
				if why == "" {
					t.Errorf("%s is retained without a reason", key)
				}
			} else if !drawn(name) {
				t.Errorf("%s is sent but neither drawn by the page nor listed as retained", key)
			}
			walk(f.Type)
		}
	}
	walk(reflect.TypeOf(Frame{}))
	for key := range retained {
		if !onWire[key] {
			t.Errorf("%s is listed as retained but is no longer on the wire", key)
		}
	}

	// The fields the page now draws are on an empty startup frame.
	body, err := Encode(vm.Board{})
	if err != nil {
		t.Fatal(err)
	}
	var startup map[string]any
	if err := json.Unmarshal(body, &startup); err != nil {
		t.Fatal(err)
	}
	for _, path := range [][]string{{"marshal", "contextUsed"}, {"marshal", "inputKnown"}, {"chat", "stage"}, {"workers"}, {"approvals"}, {"inbox"}, {"ready", "actions"}} {
		var at any = startup
		for _, k := range path {
			m, ok := at.(map[string]any)
			if !ok {
				t.Fatalf("startup frame has no %v", path)
			}
			if at, ok = m[k]; !ok {
				t.Fatalf("startup frame has no %v", path)
			}
		}
		if at == nil {
			t.Fatalf("startup frame carries null at %v", path)
		}
	}
	if startup["decision"] != nil || startup["paused"] != nil {
		t.Fatal("an empty Board carries a card")
	}

	// A frame from before these fields existed still decodes, and what is
	// missing reads as unknown rather than as a value.
	var old Frame
	older := `{"v":1,"screen":"home","marshal":{"provider":"codex","contextPct":40},"chat":{"bound":true},"paused":{"title":"Paused"},"decision":{"kind":"plan","title":"Plan"},"workers":[{"provider":"codex"}]}`
	if err := json.Unmarshal([]byte(older), &old); err != nil {
		t.Fatal(err)
	}
	if old.Marshal.ContextUsed != 0 || old.Marshal.InputKnown || old.Chat.Stage != "" || old.Paused.Kind != "" || len(old.Decision.Links) != 0 || old.Workers[0].LeftPct != 0 {
		t.Fatalf("an old frame decodes to values: %+v", old)
	}
}
