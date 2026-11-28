package chattransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordedTransport struct {
	Transport
	events chan Event
	sends  []Turn
	err    error
}

func newRecorded() *recordedTransport             { return &recordedTransport{events: make(chan Event, 16)} }
func (r *recordedTransport) Events() <-chan Event { return r.events }
func (r *recordedTransport) Send(_ context.Context, t Turn) error {
	r.sends = append(r.sends, t)
	return r.err
}
func (r *recordedTransport) Close() error { return nil }
func durableFixture(t *testing.T, r *recordedTransport, path string) *Durable {
	t.Helper()
	d, err := Persisted(r, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func nextDurable(t *testing.T, d *Durable) Event {
	t.Helper()
	select {
	case e := <-d.Events():
		return e
	case <-time.After(time.Second):
		t.Fatal("missing event")
		return Event{}
	}
}
func TestDurableRetryAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	r := newRecorded()
	d := durableFixture(t, r, path)
	turn := NewTurn("owner text")
	if err := d.Prepare(turn); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("intent not saved before dispatch", err)
	}
	for i := 0; i < 3; i++ {
		if err := d.Send(t.Context(), turn); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.sends) != 1 {
		t.Fatalf("duplicate sends: %d", len(r.sends))
	}
	r.events <- Event{Kind: EventText, Text: "reply"}
	if e := nextDurable(t, d); e.TurnID != turn.ID {
		t.Fatalf("uncorrelated reply: %+v", e)
	}
	d.Close()
	restarted := newRecorded()
	recovered := durableFixture(t, restarted, path)
	if recovered.Pending() == nil || recovered.Pending().Text != turn.Text {
		t.Fatal("lost pending text")
	}
	if err := recovered.Send(t.Context(), turn); err == nil {
		t.Fatal("uncertain send replayed")
	}
	if len(restarted.sends) != 0 {
		t.Fatal("sent after recovery")
	}
	// Startup events must not complete an interrupted turn.
	restarted.events <- Event{Kind: EventResult}
	nextDurable(t, recovered)
	if recovered.Pending() == nil {
		t.Fatal("startup cleared interrupted turn")
	}
	if err := recovered.Resolve(turn.ID); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Send(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	if len(restarted.sends) != 0 {
		t.Fatal("acknowledged message replayed")
	}
}
func TestDurableCompletionDeduplicatesAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.json")
	r := newRecorded()
	d := durableFixture(t, r, path)
	turn := NewTurn("hello")
	if err := d.Send(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	r.events <- Event{Kind: EventResult, TurnID: "another-turn"}
	nextDurable(t, d)
	if d.Pending() == nil {
		t.Fatal("foreign result completed turn")
	}
	r.events <- Event{Kind: EventResult}
	e := nextDurable(t, d)
	if e.TurnID != turn.ID || d.Pending() != nil {
		t.Fatal("completion lost correlation")
	}
	d.Close()
	r2 := newRecorded()
	d2 := durableFixture(t, r2, path)
	if err := d2.Send(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	if len(r2.sends) != 0 {
		t.Fatal("completed retry sent twice")
	}
}
func TestDurableFailedSendRetained(t *testing.T) {
	r := newRecorded()
	r.err = errors.New("provider rejected input")
	d := durableFixture(t, r, filepath.Join(t.TempDir(), "outbox.json"))
	turn := NewTurn("hello")
	if err := d.Send(t.Context(), turn); err == nil || !strings.Contains(err.Error(), r.err.Error()) {
		t.Fatal(err)
	}
	if err := d.Send(t.Context(), turn); err == nil {
		t.Fatal("uncertain failure retried")
	}
	if len(r.sends) != 1 || d.Pending() == nil {
		t.Fatal("lost or duplicated failed send")
	}
}
func TestVersionedFixtures(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + provider + "-v1.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			var parser claudeParser
			var events []Event
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if provider == "claude" {
					events = append(events, parser.parse([]byte(line))...)
				} else {
					events = append(events, parseCodexV1([]byte(line))...)
				}
			}
			if len(events) < 3 || events[0].Kind != EventSession || events[len(events)-1].Kind != EventResult {
				t.Fatalf("fixture: %+v", events)
			}
			found := false
			for _, e := range events {
				if e.Kind == EventText && e.Text == "Hello" {
					found = true
				}
			}
			if !found {
				t.Fatal("missing fixture reply")
			}
		})
	}
}
func TestCodexRuntimeQualificationAndResume(t *testing.T) {
	refused := NewCodex(newFakeRunner("no JSON mode"))
	if err := refused.Start(t.Context(), Session{Binary: "codex"}); !errors.Is(err, ErrUnqualified) {
		t.Fatal(err)
	}
	r := newFakeRunner("--json --config --sandbox")
	c := NewCodex(r)
	defer c.Close()
	if err := c.Start(t.Context(), Session{Binary: "codex", Args: []string{"--sandbox", "read-only"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(t.Context(), Turn{ID: "turn-1", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	p := <-r.started
	p.readTurn(t)
	p.emit(t, `{"type":"thread.started","thread_id":"thread-1"}`, `{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"hi"}}`, `{"type":"turn.completed"}`)
	p.exit()
	for {
		select {
		case e := <-c.Events():
			if e.TurnID != "turn-1" {
				t.Fatal("missing ID")
			}
			if e.Kind == EventText && e.MessageID != "turn-1:m" {
				t.Fatalf("unscoped reply ID: %+v", e)
			}
			if e.Kind == EventResult {
				goto complete
			}
		case <-time.After(time.Second):
			t.Fatal("missing result")
		}
	}
complete:
	if err := c.Send(t.Context(), Turn{ID: "turn-2", Text: "again"}); err != nil {
		t.Fatal(err)
	}
	p2 := <-r.started
	if got := strings.Join(p2.args, " "); got != "exec --sandbox read-only resume --json thread-1 -" {
		t.Fatal(got)
	}
	p2.readTurn(t)
	p2.emit(t, `{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"second reply"}}`, `{"type":"turn.completed"}`)
	p2.exit()
	select {
	case e := <-c.Events():
		if e.Kind != EventText || e.MessageID != "turn-2:m" || e.TurnID != "turn-2" {
			t.Fatalf("reused reply identity: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("missing second reply")
	}
}

func TestProviderRejectionIncludesDiagnostics(t *testing.T) {
	r := ExecRunner{}
	p, err := r.Start(t.Context(), "/bin/sh", "", []string{"-c", "printf 'invalid option: provider rejected input' >&2; exit 2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Wait()
	if err == nil || !strings.Contains(err.Error(), "provider rejected input") {
		t.Fatalf("missing rejection reason: %v", err)
	}
}

func TestClaudeWireAndReplyTurnID(t *testing.T) {
	c, _, p := startClaude(t, "")
	turn := NewTurn("hello")
	if err := c.Send(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	wire := p.readTurn(t)
	if !strings.Contains(wire, `"uuid":"`+turn.ID+`"`) {
		t.Fatal("missing wire ID", wire)
	}
	p.emit(t, `{"type":"assistant","message":{"id":"reply","content":[{"type":"text","text":"hello"}]}}`, `{"type":"result","subtype":"success"}`)
	for i := 0; i < 2; i++ {
		select {
		case e := <-c.Events():
			if e.TurnID != turn.ID {
				t.Fatalf("reply ID: %+v", e)
			}
		case <-time.After(time.Second):
			t.Fatal("missing correlated event")
		}
	}
}
func TestOutboxRejectsMalformedOrUnversionedState(t *testing.T) {
	for _, data := range []string{`{`, `null`, `{}`, `{"Version":2}`, `{"Version":1,"Pending":{"Text":"lost ID"}}`} {
		path := filepath.Join(t.TempDir(), "outbox.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if d, err := Persisted(nil, path); err == nil {
			d.Close()
			t.Fatalf("accepted invalid state: %s", data)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != data {
			t.Fatal("invalid intent overwritten")
		}
	}
}

func TestClaudeExitDuringPendingTurnIsFailure(t *testing.T) {
	c, _, p := startClaude(t, "")
	turn := NewTurn("hello")
	if err := c.Send(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	p.readTurn(t)
	p.exit()
	event := next(t, c.Events())
	if event.Kind != EventExit || !event.IsError || event.TurnID != turn.ID {
		t.Fatalf("incomplete turn did not fail: %+v", event)
	}
}
