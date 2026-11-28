package webboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// Source builds one Board frame. The server calls it on its own goroutines,
// never on the terminal's input loop, and the caller must not hold any lock
// the Board's collectors take.
type Source func() vm.Board

//go:embed page
var pageFS embed.FS

// Content-Security-Policy of every response: nothing but this origin, no
// inline script or style, no frames, no forms.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

var (
	// frameEvery is the shortest time between two frames (at most 4/s).
	frameEvery = 250 * time.Millisecond
	// heartbeatEvery keeps an idle event stream open through proxies and
	// lets the page notice a dead one.
	heartbeatEvery = 15 * time.Second
)

// maxStreams bounds the open event streams.
const maxStreams = 16

// Server is one running Web Board.
type Server struct {
	addr string // 127.0.0.1:PORT
	port string
	// token is the page's bearer secret for the API. It is never in a link,
	// on a screen or in a file: a page gets it only by spending a link
	// (POST /api/session). tokenMu guards it, since a reused link rotates it.
	tokenMu sync.Mutex
	token   string
	// links are the one-use secrets of the links handed out and not yet
	// opened (?t=…): each Link call makes a new one, so two opens are two
	// links. spent holds the links already used, so a second use of one is
	// recognised. A link is seen by whatever shows it to the owner (the
	// footer, the browser opener's argv, the browser's history); spending
	// it at once makes those copies worthless, and a copy used first by
	// anything else leaves the owner's page refused, which rotates token and
	// shuts that page out too.
	links map[string]bool
	spent map[string]bool
	// demo keeps one fixed link that is never spent (the demo only).
	demo   bool
	source Source
	sink   Sink
	// actKey signs the action tokens; epoch counts the actions taken, so a
	// token works once.
	actKey       []byte
	actMu        sync.Mutex
	settings     settingsPointer
	commandSpent map[string]int64
	// settingsTokens caches minted settings action tokens by binding.
	settingsTokMu  sync.Mutex
	settingsTokens map[string]cachedSettingsToken
	previews       map[string]Confirmation
	details        detailsPointer
	architecture   architecturePointer

	listener net.Listener
	http     *http.Server
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}

	frameMu   sync.Mutex
	closed    bool
	lastBuilt time.Time
	lastKey   []byte
	lastBody  []byte
	seq       uint64
	epoch     uint64

	subMu   sync.Mutex
	subs    map[chan struct{}]struct{}
	running bool

	closeOnce sync.Once
}

// Start serves source on 127.0.0.1 at a random port with a fresh 256-bit
// token. A nil sink keeps the page read-only. The server runs until Close.
func Start(source Source, sink Sink) (*Server, error) {
	return start("127.0.0.1:0", "", source, sink)
}

// randomToken is n random bytes, URL-safe base64 without padding.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// start is Start on addr; a non-empty token is used as given (the demo).
func start(addr, token string, source Source, sink Sink) (*Server, error) {
	if source == nil {
		return nil, errors.New("web board: no source")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return nil, fmt.Errorf("web board: %q is not a 127.0.0.1 address", addr)
	}
	demo := token != ""
	if !demo {
		if token, err = randomToken(32); err != nil {
			return nil, err
		}
	}
	actKey := make([]byte, 32)
	if _, err := rand.Read(actKey); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("web board: listen: %w", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		addr: "127.0.0.1:" + port, port: port, token: token, source: source, sink: sink, actKey: actKey, listener: ln,
		ctx: ctx, cancel: cancel, done: make(chan struct{}), subs: map[chan struct{}]struct{}{},
		links: map[string]bool{}, spent: map[string]bool{}, demo: demo,
	}
	// No Read or WriteTimeout: the event stream is long-lived. Closing the base
	// context ends every stream.
	s.http = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		defer close(s.done)
		_ = s.http.Serve(ln)
	}()
	return s, nil
}

// Addr is the host:port the server listens on.
func (s *Server) Addr() string { return s.addr }

// Done is closed once the server stopped serving.
func (s *Server) Done() <-chan struct{} { return s.done }

// Close stops the server and ends every open stream.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.actMu.Lock()
		s.cancel()
		s.actMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		_ = s.listener.Close()
		<-s.done
		// Wait for a frame build in flight: no Source call runs after Close.
		s.frameMu.Lock()
		s.closed = true
		s.frameMu.Unlock()
	})
}

// maxLinks bounds the links handed out and not yet opened, and the links
// remembered as spent; the oldest-by-chance is dropped past it.
const maxLinks = 64

// dropOne removes one entry of m once it holds more than maxLinks.
func dropOne(m map[string]bool) {
	if len(m) <= maxLinks {
		return
	}
	for k := range m {
		delete(m, k)
		return
	}
}

// Link is a new link for the owner to open: the address with a fresh
// one-use secret, so each open of the Web Board (each browser tab) has its
// own. The demo's link is its fixed token.
func (s *Server) Link() (string, error) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if s.demo {
		return "http://" + s.addr + "/?t=" + s.token, nil
	}
	link, err := randomToken(32)
	if err != nil {
		return "", err
	}
	s.links[link] = true
	dropOne(s.links)
	return "http://" + s.addr + "/?t=" + link, nil
}

// bearer is the request's "Authorization: Bearer <value>", "" for none.
func bearer(q *http.Request) string {
	v, _ := strings.CutPrefix(q.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(q.Header.Get("Authorization"), "Bearer ") {
		return ""
	}
	return v
}

func equalSecret(a, b string) bool {
	return a != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// authorized reports whether the request carries the page's API secret as
// "Authorization: Bearer <token>". A page gets it by spending its link
// (serveSession) and keeps it for its tab; no cookie is ever set, so no
// other page on this computer can ride on one. A link's own secret is not
// accepted here.
func (s *Server) authorized(q *http.Request) bool {
	s.tokenMu.Lock()
	token := s.token
	s.tokenMu.Unlock()
	return equalSecret(bearer(q), token)
}

// serveSession is POST /api/session: the page spends the link it was opened
// with (as its bearer) and gets the API secret. A link works once. A link
// used again means something other than the owner's page used it first:
// the API secret is replaced, so every page holding it (that one included)
// is shut out until the owner opens a new link.
func (s *Server) serveSession(w http.ResponseWriter, q *http.Request) {
	if !s.originAllowed(q.Header.Get("Origin")) {
		refuse(w, http.StatusForbidden, "Open the Web Board from its own link.")
		return
	}
	s.actMu.Lock()
	defer s.actMu.Unlock()
	presented := bearer(q)
	s.tokenMu.Lock()
	switch {
	case s.demo && equalSecret(presented, s.token), presented != "" && s.links[presented]:
		if !s.demo {
			delete(s.links, presented)
			s.spent[presented] = true
			dropOne(s.spent)
		}
		token := s.token
		s.tokenMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"session": token})
		return
	case presented != "" && s.spent[presented]:
		if fresh, err := randomToken(32); err == nil {
			s.token = fresh
			s.previews = nil
		}
		s.tokenMu.Unlock()
		s.spend()
		w.Header().Set("WWW-Authenticate", `Bearer realm="marshal-board"`)
		refuse(w, http.StatusUnauthorized, "This link was already used, so to be safe the Web Board closed every open page. Press w on the Board or run /marshal web for a new one-time link.")
		return
	}
	s.tokenMu.Unlock()
	w.Header().Set("WWW-Authenticate", `Bearer realm="marshal-board"`)
	refuse(w, http.StatusUnauthorized, "Open the link the Board printed. To get a new one-time link press w on the Board, or run /marshal web in the terminal.")
}

// hostAllowed is the DNS-rebinding guard: the Host header must name this
// server exactly.
func (s *Server) hostAllowed(host string) bool {
	return host == "127.0.0.1:"+s.port || host == "localhost:"+s.port
}

// static is the page: it holds no Board data, so it needs no token.
var static = map[string]struct{ name, contentType string }{
	"/":        {"page/index.html", "text/html; charset=utf-8"},
	"/app.js":  {"page/app.js", "text/javascript; charset=utf-8"},
	"/app.css": {"page/app.css", "text/css; charset=utf-8"},
}

func deny(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(text + "\n"))
}

// Handler is the Web Board's HTTP handler: GET everywhere, and POST on
// /api/act only.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		if !s.hostAllowed(q.Host) {
			deny(w, http.StatusMisdirectedRequest, "This Board answers only at "+s.addr+".")
			return
		}
		if q.URL.Path == "/api/session" {
			if q.Method != http.MethodPost {
				h.Set("Allow", http.MethodPost)
				deny(w, http.StatusMethodNotAllowed, "A link is spent with POST.")
				return
			}
			s.serveSession(w, q)
			return
		}
		if q.URL.Path == "/api/act" {
			if q.Method != http.MethodPost {
				h.Set("Allow", http.MethodPost)
				deny(w, http.StatusMethodNotAllowed, "Actions are sent with POST.")
				return
			}
			if !s.authorized(q) {
				h.Set("WWW-Authenticate", `Bearer realm="marshal-board"`)
				refuse(w, http.StatusUnauthorized, "Open the link the Board printed. To get a new one-time link press w on the Board, or run /marshal web in the terminal.")
				return
			}
			s.serveAct(w, q)
			return
		}
		if strings.HasPrefix(q.URL.Path, "/api/act/receipts/") && q.Method == http.MethodGet {
			s.serveReceipt(w, q)
			return
		}
		if q.Method != http.MethodGet {
			h.Set("Allow", http.MethodGet)
			deny(w, http.StatusMethodNotAllowed, "Only GET here; actions go to /api/act.")
			return
		}
		if f, ok := static[q.URL.Path]; ok {
			s.serveFile(w, f.name, f.contentType)
			return
		}
		if q.URL.Path != "/api/architecture" && q.URL.Path != "/api/board" && q.URL.Path != "/api/events" && q.URL.Path != "/api/details" && q.URL.Path != "/api/details/page" {
			deny(w, http.StatusNotFound, "Not found.")
			return
		}
		if !s.authorized(q) {
			h.Set("WWW-Authenticate", `Bearer realm="marshal-board"`)
			deny(w, http.StatusUnauthorized, "Open the link the Board printed. To get a new one-time link press w on the Board, or run /marshal web in the terminal.")
			return
		}
		if q.URL.Path == "/api/architecture" {
			s.serveArchitecture(w, q)
			return
		}
		if q.URL.Path == "/api/events" {
			s.serveEvents(w, q)
			return
		}
		if q.URL.Path == "/api/details" || q.URL.Path == "/api/details/page" {
			s.serveDetails(w, q)
			return
		}
		body, _, err := s.frame()
		if err != nil {
			deny(w, http.StatusServiceUnavailable, "The Board frame could not be built.")
			return
		}
		h.Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(body)
	})
}

func (s *Server) serveFile(w http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(pageFS, name)
	if err != nil {
		deny(w, http.StatusNotFound, "Not found.")
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

// frame is the current frame's JSON and its sequence number, rebuilt when
// the last one is older than frameEvery. Builds are serialized, so many
// readers cost one build.
func (s *Server) frame() ([]byte, uint64, error) {
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	if s.closed {
		return nil, 0, errors.New("web board: closed")
	}
	if s.lastBody != nil && time.Since(s.lastBuilt) < frameEvery {
		return s.lastBody, s.seq, nil
	}
	b, err := s.build()
	if err != nil {
		return nil, 0, err
	}
	f := FromBoard(b)
	f.SettingsActions = s.settingsFrame()
	if s.sink != nil {
		f.Act = s.mint(f, s.epoch)
	}
	body, err := encodeFrame(f)
	if err != nil {
		return nil, 0, err
	}
	// The change key is the frame without its clocks; the action tokens
	// are part of it, so a spent token makes the stream push new ones.
	kb := b
	kb.Now, kb.Top.Clock = time.Time{}, time.Time{}
	kf := FromBoard(kb)
	kf.Act = f.Act
	kf.SettingsActions = f.SettingsActions
	key, err := encodeFrame(kf)
	if err != nil {
		return nil, 0, err
	}
	s.lastBuilt = time.Now()
	s.lastBody = body
	if !bytes.Equal(key, s.lastKey) {
		s.lastKey = key
		s.seq++
	}
	return body, s.seq, nil
}

// currentFrame is a frame built now, never the cached one, and the action
// count its tokens are bound to. serveAct checks an action against it.
func (s *Server) currentFrame() (Frame, uint64, error) {
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	if s.closed {
		return Frame{}, 0, errors.New("web board: closed")
	}
	b, err := s.build()
	if err != nil {
		return Frame{}, 0, err
	}
	return FromBoard(b), s.epoch, nil
}

// build calls the source; a panic in it is a failed frame, never a crash of
// the Board process.
func (s *Server) build() (b vm.Board, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("web board frame: %v", r)
		}
	}()
	return s.source(), nil
}

// subscribe registers an event stream and starts the producer when it is
// the first. ok is false when too many streams are open.
func (s *Server) subscribe() (chan struct{}, bool) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if len(s.subs) >= maxStreams {
		return nil, false
	}
	ch := make(chan struct{}, 1)
	s.subs[ch] = struct{}{}
	if !s.running {
		s.running = true
		go s.produce()
	}
	return ch, true
}

func (s *Server) unsubscribe(ch chan struct{}) {
	s.subMu.Lock()
	delete(s.subs, ch)
	s.subMu.Unlock()
}

// produce rebuilds the frame every frameEvery while a stream is open and
// wakes the streams when what it shows changed. It stops with the last
// stream.
func (s *Server) produce() {
	t := time.NewTicker(frameEvery)
	defer t.Stop()
	var sent uint64
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		s.subMu.Lock()
		if len(s.subs) == 0 {
			s.running = false
			s.subMu.Unlock()
			return
		}
		s.subMu.Unlock()
		_, seq, err := s.frame()
		if err != nil || seq == sent {
			continue
		}
		sent = seq
		s.subMu.Lock()
		for ch := range s.subs {
			select {
			case ch <- struct{}{}:
			default: // the stream has a wake-up waiting already
			}
		}
		s.subMu.Unlock()
	}
}

// writeEvent writes one Server-Sent Event carrying a frame.
func writeEvent(w http.ResponseWriter, seq uint64, body []byte) error {
	var buf bytes.Buffer
	buf.WriteString("id: ")
	buf.WriteString(strconv.FormatUint(seq, 10))
	buf.WriteString("\nevent: board\ndata: ")
	// The JSON encoder never writes a raw newline, so the frame is one
	// data line.
	buf.Write(bytes.ReplaceAll(body, []byte("\n"), nil))
	buf.WriteString("\n\n")
	_, err := w.Write(buf.Bytes())
	return err
}

func (s *Server) serveEvents(w http.ResponseWriter, q *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		deny(w, http.StatusInternalServerError, "Streaming is not supported.")
		return
	}
	ch, ok := s.subscribe()
	if !ok {
		deny(w, http.StatusServiceUnavailable, "Too many Board pages are open.")
		return
	}
	defer s.unsubscribe(ch)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("retry: 2000\n\n"))
	body, seq, err := s.frame()
	if err == nil {
		if writeEvent(w, seq, body) != nil {
			return
		}
	}
	flusher.Flush()
	sent := seq
	beat := time.NewTicker(heartbeatEvery)
	defer beat.Stop()
	for {
		select {
		case <-q.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-beat.C:
			if !s.authorized(q) {
				return
			}
			if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-ch:
			if !s.authorized(q) {
				return
			}
			body, seq, err := s.frame()
			if err != nil || seq == sent {
				continue
			}
			sent = seq
			if writeEvent(w, seq, body) != nil {
				return
			}
			flusher.Flush()
		}
	}
}
