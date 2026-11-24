package webboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// evidence is a mutable details source for the tests.
type evidence struct {
	mu     sync.Mutex
	run    string
	commit string
	msgs   []Message
	base   string
}

func (e *evidence) source() DetailsSource {
	return DetailsSource{
		Scope: func(context.Context) string { e.mu.Lock(); defer e.mu.Unlock(); return e.run },
		Details: func(context.Context) RunDetails {
			e.mu.Lock()
			defer e.mu.Unlock()
			return RunDetails{Run: e.run, Scope: e.run, MCP: "MCP failed: token sk-" + strings.Repeat("a", 40) + " refused",
				Paused: &PausedDetail{Kind: "other", Code: "x", Reason: "Stopped", Raw: "boom ghp_" + strings.Repeat("b", 36)},
				Tasks:  []TaskEvidence{{Task: "T1", Title: "one", State: "handed in", Verdict: "accept", DiffSubject: "T1@" + e.commit}}}
		},
		Diff: func(_ context.Context, subject, cursor string) (DiffPart, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if subject != "T1@"+e.commit {
				return DiffPart{}, model.ErrPolicyDenied
			}
			if cursor == "" {
				return DiffPart{Text: "diff --git a/a b/a\n+one\n", Next: "T1@" + e.commit + "#1"}, nil
			}
			return DiffPart{Text: "diff --git a/b b/b\n+two\n"}, nil
		},
		Messages: func(context.Context) Retained {
			e.mu.Lock()
			defer e.mu.Unlock()
			return Retained{Session: "s1", Base: e.base, Messages: append([]Message(nil), e.msgs...), Unavailable: "Earlier history is not retained."}
		},
	}
}

func newEvidence(t *testing.T, sink bool, e *evidence) *Server {
	t.Helper()
	var s *Server
	var err error
	if sink {
		var log *sinkLog = &sinkLog{resp: Result{Status: "done"}}
		s, err = Start(fixtureRunning, log.act)
	} else {
		s, err = Start(fixtureRunning, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.ConfigureDetails(e.source())
	return s
}

func read(t *testing.T, s *Server, path string, into any) int {
	t.Helper()
	r := get(t, s, "GET", path, "", s.token)
	if into != nil && r.StatusCode == http.StatusOK {
		if err := json.NewDecoder(r.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
	return r.StatusCode
}

func page(id string) string { return "/api/details/page?id=" + url.QueryEscape(id) }

func TestDetailsPagesBoundToRun(t *testing.T) {
	e := &evidence{run: "run-a", commit: "c1", base: "m1", msgs: []Message{{Role: "x", Text: "hi"}}}
	s := newEvidence(t, false, e)
	if r := get(t, s, "GET", "/api/details", "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d", r.StatusCode)
	}
	var d DetailsReply
	if code := read(t, s, "/api/details?run=run-a", &d); code != 200 || d.Run != "run-a" {
		t.Fatalf("details %d %+v", code, d)
	}
	if code := read(t, s, "/api/details?run=other", nil); code != http.StatusConflict {
		t.Errorf("foreign run: %d", code)
	}
	if strings.Contains(d.MCP, "aaaaaaaa") || strings.Contains(d.Paused.Raw, "bbbbbbbb") {
		t.Errorf("secrets leaked: %q %q", d.MCP, d.Paused.Raw)
	}
	id := d.Tasks[0].DiffID
	if id == "" {
		t.Fatal("no diff id")
	}
	if code := read(t, s, page(id), nil); code != 200 {
		t.Fatalf("diff page: %d", code)
	}
	for name, bad := range map[string]string{"tampered": id[:len(id)-2] + "AA", "raw cursor": "T1@c1#1", "path": "../../etc/passwd", "empty": ""} {
		if code := read(t, s, page(bad), nil); code != http.StatusBadRequest {
			t.Errorf("%s id: %d", name, code)
		}
	}
	// The run changes: every id of the old run is refused.
	e.mu.Lock()
	e.run = "run-b"
	e.mu.Unlock()
	if code := read(t, s, page(id), nil); code != http.StatusConflict {
		t.Errorf("id of another run: %d", code)
	}
	// A rotated session shuts out ids it did not mint.
	e.mu.Lock()
	e.run = "run-a"
	e.mu.Unlock()
	s.tokenMu.Lock()
	s.token = "rotated-token-value"
	s.tokenMu.Unlock()
	if r := get(t, s, "GET", page(id), "", "rotated-token-value"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("id after rotation: %d", r.StatusCode)
	}
}

func TestDiffAndTranscriptPageBindings(t *testing.T) {
	var msgs []Message
	for i := 0; i < 6; i++ {
		msgs = append(msgs, Message{Role: "r" + strconv.Itoa(i), Text: strings.Repeat("m", 1500)})
	}
	e := &evidence{run: "run-a", commit: "c1", base: "m1", msgs: msgs}
	s := newEvidence(t, true, e)
	var d DetailsReply
	read(t, s, "/api/details", &d)
	var first DiffReply
	read(t, s, page(d.Tasks[0].DiffID), &first)
	if first.Next == "" || !strings.Contains(first.Text, "+one") {
		t.Fatalf("first diff page %+v", first)
	}
	var second DiffReply
	if read(t, s, page(first.Next), &second) != 200 || !strings.Contains(second.Text, "+two") || second.Next != "" {
		t.Fatalf("second diff page %+v", second)
	}
	// The reviewed result changed: both the id and the cursor are stale.
	e.mu.Lock()
	e.commit = "c2"
	e.mu.Unlock()
	if code := read(t, s, page(d.Tasks[0].DiffID), nil); code != http.StatusConflict {
		t.Errorf("diff id after the result changed: %d", code)
	}
	if code := read(t, s, page(first.Next), nil); code != http.StatusConflict {
		t.Errorf("diff cursor after the result changed: %d", code)
	}

	// The conversation: the newest page is bounded and pages back and forth.
	tp := d.Transcript
	total := 0
	for _, m := range tp.Messages {
		total += len([]rune(m.Text))
	}
	if total > TranscriptChars || len(tp.Messages) == 0 || tp.Older == "" || tp.Newer != "" || tp.Unavailable != "" {
		t.Fatalf("first transcript page: %d chars, %d messages, %+v", total, len(tp.Messages), tp)
	}
	if !strings.Contains(tp.Note, "not verified") {
		t.Errorf("roles are not marked unverified: %q", tp.Note)
	}
	var older TranscriptPage
	read(t, s, page(tp.Older), &older)
	if len(older.Messages) == 0 || older.Newer == "" || older.Older == "" {
		t.Fatalf("older page %+v", older)
	}
	var back TranscriptPage
	read(t, s, page(older.Newer), &back)
	if len(back.Messages) == 0 || back.Messages[0].Role != tp.Messages[0].Role && back.Messages[0].Role == older.Messages[0].Role {
		t.Errorf("newer page %+v", back)
	}
	var oldest TranscriptPage
	cur := older
	for i := 0; cur.Older != "" && i < 10; i++ {
		read(t, s, page(cur.Older), &oldest)
		cur = oldest
	}
	if cur.Unavailable == "" || cur.Older != "" {
		t.Errorf("start of retained history does not say earlier history is unavailable: %+v", cur)
	}
	// Appended messages keep an old page valid; trimmed history does not.
	e.mu.Lock()
	e.msgs = append(e.msgs, Message{Role: "new", Text: "later"})
	e.mu.Unlock()
	if code := read(t, s, page(tp.Older), nil); code != 200 {
		t.Errorf("older page after an append: %d", code)
	}
	e.mu.Lock()
	e.base = "m2"
	e.mu.Unlock()
	if code := read(t, s, page(tp.Older), nil); code != http.StatusConflict {
		t.Errorf("older page after history was trimmed: %d", code)
	}
}

func TestPausedDetailAlwaysReachable(t *testing.T) {
	e := &evidence{run: "run-a", commit: "c1", base: "m1"}
	// Read-only Board: no sink at all.
	s := newEvidence(t, false, e)
	var d DetailsReply
	if code := read(t, s, "/api/details", &d); code != 200 || d.Paused == nil || d.Paused.Reason != "Stopped" || d.MCP == "" {
		t.Fatalf("read-only details %d %+v", code, d)
	}
	if d.Transcript.Unavailable == "" && len(d.Transcript.Messages) == 0 {
		t.Error("an empty conversation does not say so")
	}

	// With actions: a decision card is shown, and a read neither needs nor
	// spends an action token.
	b := fixtureRunning()
	b.Decision = &vm.Decision{Kind: "ask", Title: "Marshal asks · which", Summary: "s", Revision: "ask:1",
		Choices: []vm.Choice{{ID: "card.choose", Value: "ask:0", Key: "⏎", Label: "one"}}}
	log := &sinkLog{resp: Result{Status: "done"}}
	s2, err := Start(func() vm.Board { return b }, log.act)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	s2.ConfigureDetails(e.source())
	before := boardFrame(t, s2)
	for i := 0; i < 3; i++ {
		if code := read(t, s2, "/api/details", &d); code != 200 || d.Paused == nil {
			t.Fatalf("details under a decision card: %d", code)
		}
	}
	after := boardFrame(t, s2)
	if before.Act == nil || after.Act == nil || before.Act.Card != after.Act.Card || before.Act.Tokens["say"] != after.Act.Tokens["say"] {
		t.Error("reading details changed the action tokens")
	}
	// A server with no details source says so instead of guessing.
	s3 := startTest(t, nil)
	if code := read(t, s3, "/api/details", nil); code != http.StatusNotFound {
		t.Errorf("no source: %d", code)
	}
	// Details are a read: POST is refused.
	if r := get(t, s2, "POST", "/api/details", "", s2.token); r.StatusCode == 200 {
		t.Error("POST /api/details was accepted")
	}
}
