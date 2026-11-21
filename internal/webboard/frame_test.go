package webboard

import (
	"encoding/json"
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
	if strings.Count(s, "null") != 2 {
		t.Fatalf("a list encodes as null: %s", s)
	}
	for _, want := range []string{`"tasks":[]`, `"workers":[]`, `"activity":[]`, `"requests":[]`, `"keys":[]`, `"items":[]`, `"lines":[]`, `"stages":[]`, `"checks":[]`, `"defaults":[]`, `"now":""`, `"decision":null`, `"paused":null`} {
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
}

// Every vm field is either sent (a wire field of the same name) or listed in
// excluded: a field added to the view model must be decided here.
func TestFrameMapsEveryField(t *testing.T) {
	pairs := map[reflect.Type]reflect.Type{
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
		reflect.TypeOf(vm.Key{}): reflect.TypeOf(Key{}), reflect.TypeOf(vm.LimitRow{}): reflect.TypeOf(LimitRow{}),
		reflect.TypeOf(vm.Dispatch{}): reflect.TypeOf(Dispatch{}), reflect.TypeOf(vm.System{}): reflect.TypeOf(System{}),
		reflect.TypeOf(vm.MarshalSays{}): reflect.TypeOf(MarshalSays{}),
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

// The Marshal's last turn reaches the page scrubbed and capped, and says
// whether it ends with a question.
func TestFrameMarshalSays(t *testing.T) {
	f := FromBoard(fixtureRunning())
	if m := f.MarshalSays; !m.Question || m.Truncated || m.Answered || m.At != "2026-11-20T19:27:00Z" || !strings.HasPrefix(m.Text, "T02 came back") {
		t.Fatalf("running: %+v", m)
	}
	if m := FromBoard(vm.Board{}).MarshalSays; m.Text != "" || m.Question || m.At != "" {
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
	if !m.Question {
		t.Fatal("the question at the end of a long message is lost by the cap")
	}
	if FromBoard(vm.Board{MarshalSays: vm.MarshalSays{Text: "Is it? No, it is done."}}).MarshalSays.Question {
		t.Fatal("a question mark mid-text is not a question to the owner")
	}
}

// Every usage window of a provider reaches the browser with its reset and
// reading times, "not reported" as -1; the dispatch state and the host
// figures go too.
func TestFrameCarriesLimitsDispatchAndSystem(t *testing.T) {
	f := FromBoard(fixtureRunning())
	claude := f.Workers[0]
	if len(claude.Limits) != 3 || claude.Dispatch.State != "dispatching" || claude.Dispatch.Reason == "" {
		t.Fatalf("claude worker: %+v", claude)
	}
	wk := claude.Limits[1]
	if wk.Family != "all models" || wk.Window != "weekly" || wk.PercentLeft != 71 || wk.ResetsAt != "2026-11-24T09:00:00Z" ||
		wk.ObservedAt != "2026-11-20T19:29:00Z" || wk.Source != "claude /usage" {
		t.Fatalf("limit row: %+v", wk)
	}
	if agy := f.Workers[1].Limits[1]; agy.PercentLeft != -1 || agy.ResetsAt != "" {
		t.Fatalf("an unreported window: %+v", agy)
	}
	if s := f.System; s.CPUPct != 64 || s.TempC != 83 || !s.ThermalGuard || s.ThermalCap != 6 || s.UpdateAvailable != "v0.3.9" || s.MCPFailed {
		t.Fatalf("system: %+v", s)
	}
	body, err := Encode(fixtureRunning())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"limits":[{"family":"all models","window":"5h","percentLeft":58`, `"dispatch":{"state":"spare"`, `"system":{"cpuPct":64`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("frame lacks %s", want)
		}
	}
	// A worker with no reported windows sends an empty list, never null.
	if f := FromBoard(fixtureReady()); f.Workers[1].Limits == nil || len(f.Workers[1].Limits) != 0 {
		t.Fatalf("ready agy limits: %#v", f.Workers[1].Limits)
	}
}
