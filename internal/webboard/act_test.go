package webboard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// sinkLog is a fake sink that records the actions it took.
type sinkLog struct {
	mu   sync.Mutex
	got  []Action
	resp Result
}

func (l *sinkLog) act(_ context.Context, a Action) Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, a)
	return l.resp
}

func (l *sinkLog) taken() []Action {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Action(nil), l.got...)
}

// boardSource is a Source whose Board a test can change.
type boardSource struct {
	mu sync.Mutex
	b  vm.Board
}

func (s *boardSource) get() vm.Board {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b
}

func (s *boardSource) set(b vm.Board) {
	s.mu.Lock()
	s.b = b
	s.mu.Unlock()
}

func startActing(t *testing.T, b vm.Board) (*Server, *boardSource, *sinkLog) {
	t.Helper()
	src := &boardSource{b: b}
	log := &sinkLog{resp: Result{Status: "done", Text: "Done."}}
	s, err := Start(src.get, log.act)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, src, log
}

// boardFrame is GET /api/board, decoded.
func boardFrame(t *testing.T, s *Server) Frame {
	t.Helper()
	r := get(t, s, "GET", "/api/board", "", s.token)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/board: %d", r.StatusCode)
	}
	var f Frame
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

// post is one POST /api/act; header edits the request before it is sent.
func post(t *testing.T, s *Server, body []byte, header func(h http.Header)) (*http.Response, Result) {
	t.Helper()
	q, err := http.NewRequest("POST", "http://"+s.Addr()+"/api/act", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	q.Header.Set("Authorization", "Bearer "+s.token)
	q.Header.Set("Origin", "http://"+s.Addr())
	q.Header.Set("Sec-Fetch-Site", "same-origin")
	q.Header.Set("Content-Type", "application/json")
	if header != nil {
		header(q.Header)
	}
	r, err := noRedirect.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	data, _ := io.ReadAll(r.Body)
	var res Result
	_ = json.Unmarshal(data, &res)
	return r, res
}

func keyBody(t *testing.T, f Frame, key string) []byte {
	t.Helper()
	if f.Act == nil {
		t.Fatal("frame has no action section")
	}
	b, _ := json.Marshal(map[string]any{"action": "key", "card": f.Act.Card, "key": key, "token": f.Act.Tokens[key]})
	return b
}

func TestActPressesTheCardsKey(t *testing.T) {
	s, _, log := startActing(t, fixturePaused())
	f := boardFrame(t, s)
	if f.Act == nil || !strings.HasPrefix(f.Act.Card, "paused:") || f.Act.SayMax != SayMax {
		t.Fatalf("act section: %+v", f.Act)
	}
	for _, k := range []string{"⏎", "w", "d", "x", "say"} {
		if f.Act.Tokens[k] == "" {
			t.Errorf("no token for %q: %v", k, f.Act.Tokens)
		}
	}
	if _, ok := f.Act.Tokens["esc"]; ok {
		t.Error("the paused card has no esc, but a token was minted for it")
	}
	r, res := post(t, s, keyBody(t, f, "⏎"), nil)
	if r.StatusCode != http.StatusOK || res.Status != "done" || res.Text != "Done." {
		t.Fatalf("POST: %d %+v", r.StatusCode, res)
	}
	checkHeaders(t, "POST /api/act", r)
	got := log.taken()
	if len(got) != 1 || got[0].Kind != "key" || got[0].Key != "⏎" || got[0].Card != f.Act.Card {
		t.Fatalf("sink got %+v", got)
	}
}

func TestActRefusals(t *testing.T) {
	s, _, log := startActing(t, fixturePaused())
	f := boardFrame(t, s)
	body := keyBody(t, f, "⏎")
	cases := []struct {
		name   string
		body   []byte
		header func(http.Header)
		want   int
	}{
		{"no bearer", body, func(h http.Header) { h.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong bearer", body, func(h http.Header) { h.Set("Authorization", "Bearer "+strings.Repeat("A", len(s.token))) }, http.StatusUnauthorized},
		{"missing Origin", body, func(h http.Header) { h.Del("Origin") }, http.StatusForbidden},
		{"foreign Origin", body, func(h http.Header) { h.Set("Origin", "http://127.0.0.1:1") }, http.StatusForbidden},
		{"null Origin", body, func(h http.Header) { h.Set("Origin", "null") }, http.StatusForbidden},
		{"https Origin", body, func(h http.Header) { h.Set("Origin", "https://"+s.Addr()) }, http.StatusForbidden},
		{"cross-site fetch", body, func(h http.Header) { h.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		{"same-site fetch", body, func(h http.Header) { h.Set("Sec-Fetch-Site", "same-site") }, http.StatusForbidden},
		{"form post", body, func(h http.Header) { h.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusUnsupportedMediaType},
		{"text/plain", body, func(h http.Header) { h.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
		{"no content type", body, func(h http.Header) { h.Del("Content-Type") }, http.StatusUnsupportedMediaType},
		{"oversize", append(append([]byte(`{"action":"say","text":"`), bytes.Repeat([]byte("a"), maxActBody)...), []byte(`"}`)...), nil, http.StatusRequestEntityTooLarge},
		{"unknown field", []byte(`{"action":"key","shell":"rm -rf /"}`), nil, http.StatusBadRequest},
		{"two bodies", append(append([]byte{}, body...), body...), nil, http.StatusBadRequest},
		{"unknown action", []byte(`{"action":"command","text":"/marshal stop"}`), nil, http.StatusBadRequest},
		{"wrong card", []byte(`{"action":"key","card":"paused:0","key":"⏎","token":"` + f.Act.Tokens["⏎"] + `"}`), nil, http.StatusConflict},
		{"no token", []byte(`{"action":"key","card":"` + f.Act.Card + `","key":"⏎"}`), nil, http.StatusConflict},
		{"another key's token", []byte(`{"action":"key","card":"` + f.Act.Card + `","key":"x","token":"` + f.Act.Tokens["⏎"] + `"}`), nil, http.StatusConflict},
		{"key not on the card", []byte(`{"action":"key","card":"` + f.Act.Card + `","key":"q","token":"` + f.Act.Tokens["⏎"] + `"}`), nil, http.StatusConflict},
	}
	for _, c := range cases {
		r, res := post(t, s, c.body, c.header)
		if r.StatusCode != c.want {
			t.Errorf("%s: %d, want %d (%+v)", c.name, r.StatusCode, c.want, res)
		}
		checkHeaders(t, c.name, r)
	}
	if got := log.taken(); len(got) != 0 {
		t.Fatalf("a refused action reached the sink: %+v", got)
	}
}

func TestActWrongHostIsRefused(t *testing.T) {
	s, _, log := startActing(t, fixturePaused())
	f := boardFrame(t, s)
	q, _ := http.NewRequest("POST", "http://"+s.Addr()+"/api/act", bytes.NewReader(keyBody(t, f, "⏎")))
	q.Host = "evil.example:" + s.port
	q.Header.Set("Authorization", "Bearer "+s.token)
	q.Header.Set("Origin", "http://"+s.Addr())
	q.Header.Set("Content-Type", "application/json")
	r, err := noRedirect.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("wrong Host: %d", r.StatusCode)
	}
	if len(log.taken()) != 0 {
		t.Fatal("reached the sink")
	}
}

// A token works once: a replay, or a second tab acting on the same frame,
// is refused, and the next frame carries a new token.
func TestActTokenIsSpent(t *testing.T) {
	s, _, log := startActing(t, fixturePaused())
	f := boardFrame(t, s)
	body := keyBody(t, f, "d")
	if r, _ := post(t, s, body, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("first: %d", r.StatusCode)
	}
	if r, res := post(t, s, body, nil); r.StatusCode != http.StatusConflict || res.Status != "refused" {
		t.Fatalf("replay: %d %+v", r.StatusCode, res)
	}
	other := keyBody(t, f, "⏎")
	if r, _ := post(t, s, other, nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("another token of the same frame: %d", r.StatusCode)
	}
	g := boardFrame(t, s)
	if g.Act.Card != f.Act.Card || g.Act.Tokens["d"] == f.Act.Tokens["d"] {
		t.Fatalf("the next frame did not get a new token: %+v", g.Act)
	}
	if r, _ := post(t, s, keyBody(t, g, "d"), nil); r.StatusCode != http.StatusOK {
		t.Fatalf("fresh token: %d", r.StatusCode)
	}
	if n := len(log.taken()); n != 2 {
		t.Fatalf("sink took %d actions, want 2", n)
	}
}

// A token is bound to the card: once the card changed (another cause, a
// decision instead), the page's token is refused.
func TestActStaleCard(t *testing.T) {
	s, src, log := startActing(t, fixturePaused())
	f := boardFrame(t, s)
	b := fixturePaused()
	p := *b.Paused
	p.Cause = "agy-2 hit the agy Gemini weekly limit."
	b.Paused = &p
	src.set(b)
	time.Sleep(frameEvery + 50*time.Millisecond)
	if r, res := post(t, s, keyBody(t, f, "⏎"), nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("changed card: %d %+v", r.StatusCode, res)
	}
	// A clock that moves on the card is not a change of card.
	b2 := b
	p2 := p
	p2.Resets = "resets 21:11"
	p2.Tasks = []vm.PausedTask{{Title: "T06 history paging", Progress: "tests running"}, {Title: "T09 dashed-change styles"}}
	b2.Paused = &p2
	g := boardFrame(t, s)
	src.set(b2)
	if r, _ := post(t, s, keyBody(t, g, "⏎"), nil); r.StatusCode != http.StatusOK {
		t.Fatalf("progress only: %d", r.StatusCode)
	}
	src.set(fixtureDecision())
	if r, _ := post(t, s, keyBody(t, boardFrame(t, s), "⏎"), nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("the decision card has no ⏎: %d", r.StatusCode)
	}
	if n := len(log.taken()); n != 1 {
		t.Fatalf("sink took %d, want 1", n)
	}
}

// What the main button does is part of the card: when only its label and
// what it will do change (the tasks now go to codex), an old click is
// refused rather than doing the new thing.
func TestActStaleWhenTheButtonChanged(t *testing.T) {
	s, src, log := startActing(t, fixturePaused())
	f := boardFrame(t, s)
	b := fixturePaused()
	p := *b.Paused
	p.Will = "move T06 and T09 to codex in fresh sessions, keep commits."
	p.Actions = append([]vm.Key{{Key: "⏎", Label: "move to codex"}}, p.Actions[1:]...)
	b.Paused = &p
	src.set(b)
	if r, _ := post(t, s, keyBody(t, f, "⏎"), nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("changed button: %d", r.StatusCode)
	}
	if n := len(log.taken()); n != 0 {
		t.Fatalf("sink took %d", n)
	}
}

// The sink's own "the card changed" is a 409 too.
func TestActSinkStale(t *testing.T) {
	s, _, log := startActing(t, fixtureDecision())
	log.resp = Result{Status: "stale", Text: "The Board changed."}
	f := boardFrame(t, s)
	if f.Act.Tokens["esc"] == "" || f.Act.Tokens["a"] == "" {
		t.Fatalf("decision tokens: %v", f.Act.Tokens)
	}
	if r, _ := post(t, s, keyBody(t, f, "a"), nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("stale: %d", r.StatusCode)
	}
}

func TestActSay(t *testing.T) {
	s, _, log := startActing(t, fixtureRunning())
	f := boardFrame(t, s)
	if f.Act == nil || f.Act.Card != "screen:running" || len(f.Act.Tokens) != 1 {
		t.Fatalf("running screen: only say, got %+v", f.Act)
	}
	say := func(f Frame, text string) (*http.Response, Result) {
		b, _ := json.Marshal(map[string]any{"action": "say", "card": f.Act.Card, "text": text, "token": f.Act.Tokens["say"]})
		return post(t, s, b, nil)
	}
	if r, _ := say(f, strings.Repeat("é", SayMax+1)); r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("too long: %d", r.StatusCode)
	}
	if r, _ := say(f, "/marshal stop"); r.StatusCode != http.StatusOK {
		t.Fatalf("say: %d", r.StatusCode)
	}
	got := log.taken()
	if len(got) != 1 || got[0].Kind != "say" || got[0].Text != "/marshal stop" {
		t.Fatalf("sink: %+v", got)
	}
	// A key token cannot be minted for a screen without a card.
	b, _ := json.Marshal(map[string]any{"action": "key", "card": f.Act.Card, "key": "x", "token": f.Act.Tokens["say"]})
	if r, _ := post(t, s, b, nil); r.StatusCode != http.StatusConflict {
		t.Fatalf("key on the running screen: %d", r.StatusCode)
	}
}

// Without a sink the server stays read-only: frames carry no actions and
// POST is refused.
func TestActReadOnlyServer(t *testing.T) {
	s := startTest(t, fixturePaused)
	f := boardFrame(t, s)
	if f.Act != nil {
		t.Fatalf("read-only frame carries actions: %+v", f.Act)
	}
	r, _ := post(t, s, []byte(`{"action":"say","card":"x","text":"hi","token":"x"}`), nil)
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only POST: %d", r.StatusCode)
	}
}

// Only /api/act takes POST; it takes nothing else; the read paths stay
// GET only.
func TestActMethods(t *testing.T) {
	s, _, _ := startActing(t, fixturePaused())
	for _, m := range []string{"GET", "PUT", "DELETE", "OPTIONS", "HEAD"} {
		r := get(t, s, m, "/api/act", "", s.token)
		if r.StatusCode != http.StatusMethodNotAllowed || r.Header.Get("Allow") != "POST" {
			t.Errorf("%s /api/act: %d Allow=%q", m, r.StatusCode, r.Header.Get("Allow"))
		}
		checkHeaders(t, m+" /api/act", r)
	}
	for _, path := range []string{"/", "/api/board", "/api/events", "/app.js"} {
		r := get(t, s, "POST", path, "", s.token)
		if r.StatusCode != http.StatusMethodNotAllowed || r.Header.Get("Allow") != "GET" {
			t.Errorf("POST %s: %d Allow=%q", path, r.StatusCode, r.Header.Get("Allow"))
		}
	}
}

// After an action the event stream pushes a frame with new tokens.
func TestActPushesNewTokens(t *testing.T) {
	s, _, _ := startActing(t, fixturePaused())
	_, ch := openStream(t, s, s.token)
	first, ok := nextBoard(t, ch, 2*time.Second)
	if !ok {
		t.Fatal("no first frame")
	}
	var f Frame
	if err := json.Unmarshal([]byte(first.data), &f); err != nil {
		t.Fatal(err)
	}
	if r, _ := post(t, s, keyBody(t, f, "w"), nil); r.StatusCode != http.StatusOK {
		t.Fatalf("POST: %d", r.StatusCode)
	}
	next, ok := nextBoard(t, ch, 2*time.Second)
	if !ok {
		t.Fatal("no frame after the action")
	}
	var g Frame
	if err := json.Unmarshal([]byte(next.data), &g); err != nil {
		t.Fatal(err)
	}
	if g.Act.Tokens["w"] == f.Act.Tokens["w"] {
		t.Fatal("the pushed frame carries the spent token")
	}
}
