package webboard

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

func startTest(t *testing.T, src Source) *Server {
	t.Helper()
	if src == nil {
		src = fixtureRunning
	}
	s, err := Start(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// noRedirect is a client that shows redirects instead of following them.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 5 * time.Second}

// get sends method path with Host host ("" for the server's own) and, when
// bearer is set, "Authorization: Bearer <bearer>".
func get(t *testing.T, s *Server, method, path, host, bearer string) *http.Response {
	t.Helper()
	q, err := http.NewRequest(method, "http://"+s.Addr()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		q.Host = host
	}
	if bearer != "" {
		q.Header.Set("Authorization", "Bearer "+bearer)
	}
	r, err := noRedirect.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	return r
}

// checkHeaders checks the headers every response carries, and that no
// response sets a cookie or a CORS header.
func checkHeaders(t *testing.T, what string, r *http.Response) {
	t.Helper()
	want := map[string]string{
		"Content-Security-Policy": contentSecurityPolicy,
		"X-Frame-Options":         "DENY",
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	}
	for k, v := range want {
		if got := r.Header.Get(k); got != v {
			t.Errorf("%s: %s = %q, want %q", what, k, got, v)
		}
	}
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("%s: CORS header %s", what, k)
		}
	}
	if c := r.Header.Values("Set-Cookie"); len(c) != 0 {
		t.Errorf("%s: sets a cookie %q", what, c)
	}
}

func TestStartBindsLoopbackWithStrongToken(t *testing.T) {
	s := startTest(t, nil)
	host, port, err := net.SplitHostPort(s.Addr())
	if err != nil || host != "127.0.0.1" || port == "0" {
		t.Fatalf("addr %q", s.Addr())
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host != s.Addr() || u.Path != "/" {
		t.Fatalf("url %q", s.URL)
	}
	if tok := u.Query().Get("t"); tok != s.token || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(tok) {
		t.Fatalf("token %q is not 256 random bits", tok)
	}
	other := startTest(t, nil)
	if other.token == s.token {
		t.Fatal("two Boards share a token")
	}
	for _, addr := range []string{"0.0.0.0:0", ":0", "[::]:0", "192.168.1.2:0", "localhost:0"} {
		if s, err := start(addr, "", fixtureRunning, nil); err == nil {
			s.Close()
			t.Errorf("start(%q) bound a non-127.0.0.1 address", addr)
		}
	}
}

// DNS rebinding: a request naming any other host is refused, even with the
// right token.
func TestWrongHostIsRefused(t *testing.T) {
	s := startTest(t, nil)
	_, port, _ := net.SplitHostPort(s.Addr())
	for _, host := range []string{"evil.example:" + port, "evil.example", "127.0.0.1", "localhost", "127.0.0.1:1", "localhost:1", "127.0.0.2:" + port, "[::1]:" + port, "LOCALHOST:" + port, "127.0.0.1:" + port + ".evil.example"} {
		for _, path := range []string{"/", "/api/board", "/api/events", "/app.js", "/app.css", "/?t=" + s.token} {
			r := get(t, s, "GET", path, host, s.token)
			if r.StatusCode != http.StatusMisdirectedRequest {
				t.Errorf("Host %q %s: %d, want 421", host, path, r.StatusCode)
			}
			checkHeaders(t, "wrong host", r)
		}
	}
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		if r := get(t, s, "GET", "/api/board", host, s.token); r.StatusCode != http.StatusOK {
			t.Errorf("Host %q: %d, want 200", host, r.StatusCode)
		}
	}
}

// The page is static and served without the token; it holds no Board data.
// The API needs the token as a bearer header, never in the query, and no
// response sets a cookie.
func TestBearerToken(t *testing.T) {
	s := startTest(t, nil)
	for _, path := range []string{"/", "/?t=" + s.token, "/?t=wrong", "/app.js", "/app.css"} {
		r := get(t, s, "GET", path, "", "")
		body, _ := io.ReadAll(r.Body)
		if r.StatusCode != http.StatusOK {
			t.Errorf("%s: %d, want 200 without a token", path, r.StatusCode)
		}
		checkHeaders(t, path, r)
		for _, data := range []string{s.token, "marshal-work", "review-fixes", "T03", "claude-1", "sonnet-5.5"} {
			if strings.Contains(string(body), data) {
				t.Errorf("%s carries %q: the page must hold no Board data", path, data)
			}
		}
	}
	if r := get(t, s, "GET", "/", "", ""); !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Errorf("page content type %q", r.Header.Get("Content-Type"))
	}
	for _, path := range []string{"/api/board", "/api/events", "/api/board?t=" + s.token, "/api/events?t=" + s.token} {
		for _, bad := range []string{"", "x", strings.Repeat("A", 43), s.token[:42], s.token + "0", strings.ToUpper(s.token)} {
			r := get(t, s, "GET", path, "", bad)
			if r.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s with bearer %q: %d, want 401", path, bad, r.StatusCode)
			}
			checkHeaders(t, "unauthorized", r)
		}
	}
	// Not a bearer: the token as a cookie, in another scheme, or bare.
	for _, h := range []string{"Basic " + s.token, s.token, "bearer " + s.token, "Bearer  " + s.token} {
		q, _ := http.NewRequest("GET", "http://"+s.Addr()+"/api/board", nil)
		q.Header.Set("Authorization", h)
		q.AddCookie(&http.Cookie{Name: "t", Value: s.token})
		r, err := noRedirect.Do(q)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization %q: %d, want 401", h, r.StatusCode)
		}
	}
	r := get(t, s, "GET", "/api/board", "", s.token)
	var f Frame
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil || r.StatusCode != http.StatusOK || f.V != 1 || f.Screen != "running" {
		t.Fatalf("board: %d %v %+v", r.StatusCode, err, f)
	}
	checkHeaders(t, "board", r)
	if r := get(t, s, "GET", "/nothing", "", s.token); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path: %d", r.StatusCode)
	}
}

// The demo's fixed token works the same way.
func TestDemoToken(t *testing.T) {
	s, err := start("127.0.0.1:0", "demo", fixtureRunning, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !strings.HasSuffix(s.URL, "/?t=demo") {
		t.Fatalf("demo url %q", s.URL)
	}
	if r := get(t, s, "GET", "/?t=demo", "", ""); r.StatusCode != http.StatusOK {
		t.Fatalf("demo page: %d", r.StatusCode)
	}
	if r := get(t, s, "GET", "/api/board", "", "demo"); r.StatusCode != http.StatusOK {
		t.Fatalf("demo board: %d", r.StatusCode)
	}
}

// The read paths are GET only: every other method is refused, with or without
// the token.
func TestOnlyGet(t *testing.T) {
	s := startTest(t, nil)
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "TRACE"} {
		for _, path := range []string{"/", "/api/board", "/api/events", "/?t=" + s.token} {
			for _, bearer := range []string{"", s.token} {
				r := get(t, s, m, path, "", bearer)
				if r.StatusCode != http.StatusMethodNotAllowed || r.Header.Get("Allow") != "GET" {
					t.Errorf("%s %s: %d Allow=%q", m, path, r.StatusCode, r.Header.Get("Allow"))
				}
				checkHeaders(t, m, r)
			}
		}
	}
}

// Nothing is loaded from elsewhere and no script or style is inline.
func TestPageIsSelfContained(t *testing.T) {
	for _, name := range []string{"page/index.html", "page/app.js", "page/app.css"} {
		data, err := pageFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		for _, bad := range []string{`src="http`, `href="http`, "src='http", "//cdn", "googleapis", "@import", "url(", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "localStorage"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
	html, _ := pageFS.ReadFile("page/index.html")
	scripts := regexp.MustCompile(`<script[^>]*>[^<]*</script>`).FindAllString(string(html), -1)
	if len(scripts) != 1 || strings.Count(string(html), "<script") != 1 || scripts[0] != `<script src="/app.js" defer></script>` {
		t.Errorf("index.html scripts %q: only /app.js, none inline", scripts)
	}
	if strings.Contains(string(html), "<style") || regexp.MustCompile(`\sstyle=`).Match(html) || regexp.MustCompile(`\son[a-z]+=`).Match(html) {
		t.Error("index.html has inline style or an inline handler")
	}
}

// sseEvent is one Server-Sent Event: its fields, or "" for a comment.
type sseEvent struct{ id, event, data, comment string }

func readEvents(t *testing.T, body io.Reader, out chan<- sseEvent) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			out <- ev
			ev = sseEvent{}
		case strings.HasPrefix(line, ":"):
			ev.comment = line
		case strings.HasPrefix(line, "id: "):
			ev.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			ev.event = line[7:]
		case strings.HasPrefix(line, "data: "):
			if ev.data != "" {
				t.Errorf("an event has two data lines")
			}
			ev.data = line[6:]
		case strings.HasPrefix(line, "retry: "):
		default:
			t.Errorf("unexpected stream line %q", line)
		}
	}
	close(out)
}

func nextBoard(t *testing.T, ch <-chan sseEvent, within time.Duration) (sseEvent, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return sseEvent{}, false
			}
			if ev.event == "board" {
				return ev, true
			}
		case <-deadline:
			return sseEvent{}, false
		}
	}
}

func openStream(t *testing.T, s *Server, bearer string) (*http.Response, chan sseEvent) {
	t.Helper()
	q, _ := http.NewRequest("GET", "http://"+s.Addr()+"/api/events", nil)
	q.Header.Set("Authorization", "Bearer "+bearer)
	r, err := (&http.Client{}).Do(q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Body.Close() })
	if r.StatusCode != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	checkHeaders(t, "stream", r)
	ch := make(chan sseEvent, 64)
	go readEvents(t, r.Body, ch)
	return r, ch
}

func TestEventsPushChangesThrottled(t *testing.T) {
	oldBeat := heartbeatEvery
	heartbeatEvery = 300 * time.Millisecond
	t.Cleanup(func() { heartbeatEvery = oldBeat })
	var mu sync.Mutex
	b := fixtureRunning()
	var builds atomic.Int64
	src := func() vm.Board {
		builds.Add(1)
		mu.Lock()
		defer mu.Unlock()
		out := b
		out.Now = time.Now() // a clock tick alone pushes nothing
		return out
	}
	s := startTest(t, src)
	_, ch := openStream(t, s, s.token)
	first, ok := nextBoard(t, ch, 2*time.Second)
	if !ok {
		t.Fatal("no first frame")
	}
	var f Frame
	if err := json.Unmarshal([]byte(first.data), &f); err != nil || f.V != 1 || first.id == "" {
		t.Fatalf("first frame: %v %q", err, first.id)
	}
	if ev, ok := nextBoard(t, ch, 700*time.Millisecond); ok {
		t.Fatalf("an unchanged Board pushed a frame: %s", ev.id)
	}
	mu.Lock()
	b.NowLine.FromYou = "accept the plan"
	mu.Unlock()
	second, ok := nextBoard(t, ch, 2*time.Second)
	if !ok || second.id == first.id || !strings.Contains(second.data, "accept the plan") {
		t.Fatalf("the change was not pushed: %+v", second)
	}
	// A Board that changes all the time is pushed at most ~4 times a
	// second.
	stop := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			mu.Lock()
			b.Plan.Tokens = int64(i)
			mu.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()
	n := 0
	deadline := time.After(2 * time.Second)
count:
	for {
		select {
		case ev := <-ch:
			if ev.event == "board" {
				n++
			}
		case <-deadline:
			break count
		}
	}
	close(stop)
	if n < 3 || n > 10 {
		t.Fatalf("%d frames in 2s, want about 8", n)
	}
	// Heartbeats keep an idle stream open.
	sawBeat := false
	beatDeadline := time.After(2 * time.Second)
beat:
	for {
		select {
		case ev := <-ch:
			if strings.HasPrefix(ev.comment, ": heartbeat") {
				sawBeat = true
				break beat
			}
		case <-beatDeadline:
			break beat
		}
	}
	if !sawBeat {
		t.Fatal("no heartbeat")
	}
}

// Close ends open streams and stops the listener.
func TestCloseEndsStreams(t *testing.T) {
	s := startTest(t, nil)
	_, ch := openStream(t, s, s.token)
	if _, ok := nextBoard(t, ch, 2*time.Second); !ok {
		t.Fatal("no first frame")
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited on an open stream")
	}
	for range ch {
	}
	if conn, err := net.DialTimeout("tcp", s.Addr(), 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("the listener is still open")
	}
}

// A panicking source fails a frame; it never takes the Board down.
func TestSourcePanicIsAFailedFrame(t *testing.T) {
	s := startTest(t, func() vm.Board { panic("boom") })
	if r := get(t, s, "GET", "/api/board", "", s.token); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("panicking source: %d", r.StatusCode)
	}
}
