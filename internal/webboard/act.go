package webboard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
)

// Owner actions (phase 2). The page sends POST /api/act; the server checks
// who is asking and that the page saw the card the Board shows now, then
// hands the action to the Sink, which presses the same key the terminal
// Board would on the Board's own input loop. Nothing here runs a command.

// Action is one owner action from the page.
type Action struct {
	// Kind is "key" (a button of the card on screen) or "say" (a message
	// to the Marshal, as the Board's s composer sends it).
	Kind string
	// Card is the card the page acted on (CardID of its frame).
	Card string
	// Key is the card's key for "key" ("⏎", "x", "a", "esc", …).
	Key string
	// Text is the message for "say", or what a hard gate's confirmation
	// asks to type (the canonical id) for "key".
	Text string
	// Confirmed is set on the second click of a two-step action.
	Confirmed bool
}

// Result is what happened, for the page to say.
type Result struct {
	// Status is "done", "started" (a command the key started is still
	// running), "sent", "queued" or "refused"; "stale" means the
	// card changed on the Board (the page gets 409 and looks again).
	Status string `json:"status"`
	// Text is the Board's own answer in plain words.
	Text string `json:"text"`
	// Link is the architecture review's private link after "open the
	// review": it goes only into this answer, never into a frame.
	Link string `json:"link,omitempty"`
	// Confirm is set when the key armed a second step (the plan's reply,
	// the inbox's allow or deny): the page shows a Confirm button.
	Confirm *Confirm `json:"confirm,omitempty"`
}

// Confirm is a second step the Board waits for.
type Confirm struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
	// Text is set when the confirmation reads typed text (a hard gate's
	// canonical id).
	Text bool `json:"text"`
}

// Sink runs an action on the Board. It is nil for a read-only server.
type Sink func(ctx context.Context, a Action) Result

// Act is a frame's action section: the card on screen and one token per
// action the page may send for it. Each token is bound to the card's
// identity and content, the action, and the server's action count, so it
// works once and only for the card the page shows.
type Act struct {
	Card   string            `json:"card"`
	Tokens map[string]string `json:"tokens"`
	SayMax int               `json:"sayMax"`
}

// Limits of an action request.
const (
	maxActBody = 16 << 10
	// SayMax is the longest message to the Marshal, in characters.
	SayMax = 2000
	// actWait is how long the page waits for the Board to take an action.
	actWait = 20 * time.Second
)

// sayKey is the token name for a message to the Marshal.
const sayKey = "say"

// cardDigest is a short digest of v's JSON.
func cardDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:12])
}

// CardID names the card f shows: its kind and a digest of what it says, so
// a card whose content changed is a different card: everything the owner
// decides on (what the buttons say, what the main one will do, the lines and
// parts shown) is in it. Only text that moves by itself is left out (the
// reset note, a task's progress and what of it is kept, the room left on
// other providers, the other lanes), so a card does not change under the
// owner's finger while nothing about the choice changed. With no card it is
// the screen.
func CardID(f Frame) string {
	switch {
	case f.Decision != nil:
		d := f.Decision
		return "decision:" + cardDigest(struct {
			Kind, Title, Corner, Summary, Changed, Self string
			Nodes                                       []Node
			Lines                                       []CardLine
			Actions                                     []Key
		}{d.Kind, d.Title, d.Corner, d.Summary, d.ChangedNote, d.SelfApproval, d.Nodes, d.Lines, d.Actions})
	case f.Paused != nil:
		p := f.Paused
		titles := make([]string, 0, len(p.Tasks))
		for _, t := range p.Tasks {
			titles = append(titles, t.Title)
		}
		return "paused:" + cardDigest(struct {
			Kind, Title, Cause, Will string
			Tasks                    []string
			Actions                  []Key
		}{p.Kind, p.Title, p.Cause, p.Will, titles, p.Actions})
	}
	return "screen:" + f.Screen + ":" + cardDigest(f.Keys)
}

func actionKeys(ks []Key) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, k.Key)
	}
	return out
}

// ActionKeys are the keys the page may send for f's card: the card's
// buttons, and esc ("later") on a decision; with no card, the screen's
// buttons in f.Keys.
func ActionKeys(f Frame) []string {
	switch {
	case f.Decision != nil:
		return append(actionKeys(f.Decision.Actions), "esc")
	case f.Paused != nil:
		return actionKeys(f.Paused.Actions)
	}
	// With no card, the screen's own buttons the Board put in the key list
	// (retry the stuck task, stop all workers).
	return actionKeys(f.Keys)
}

// actToken is the token for action on card at epoch.
func (s *Server) actToken(card string, epoch uint64, action string) string {
	m := hmac.New(sha256.New, s.actKey)
	m.Write([]byte(card))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatUint(epoch, 10)))
	m.Write([]byte{0})
	m.Write([]byte(action))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// mint is f's action section at epoch.
func (s *Server) mint(f Frame, epoch uint64) *Act {
	card := CardID(f)
	a := &Act{Card: card, Tokens: map[string]string{}, SayMax: SayMax}
	for _, k := range ActionKeys(f) {
		a.Tokens[k] = s.actToken(card, epoch, k)
	}
	a.Tokens[sayKey] = s.actToken(card, epoch, sayKey)
	return a
}

// originAllowed is the page's own origin, exactly.
func (s *Server) originAllowed(origin string) bool {
	return origin == "http://127.0.0.1:"+s.port || origin == "http://localhost:"+s.port
}

// actRequest is the body of POST /api/act.
type actRequest struct {
	Action    string `json:"action"` // "key" or "say"
	Card      string `json:"card"`
	Key       string `json:"key"`
	Text      string `json:"text"`
	Token     string `json:"token"`
	Confirmed bool   `json:"confirmed"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refuse(w http.ResponseWriter, status int, text string) {
	writeJSON(w, status, Result{Status: "refused", Text: text})
}

// serveAct is POST /api/act, after the Host and bearer checks.
func (s *Server) serveAct(w http.ResponseWriter, q *http.Request) {
	// The page's own origin only; a POST without Origin is refused (every
	// browser sends it on a fetch POST).
	if !s.originAllowed(q.Header.Get("Origin")) {
		refuse(w, http.StatusForbidden, "Actions are taken only from the Board's own page.")
		return
	}
	if site := q.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		refuse(w, http.StatusForbidden, "Actions are taken only from the Board's own page.")
		return
	}
	if mt, _, err := mime.ParseMediaType(q.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		refuse(w, http.StatusUnsupportedMediaType, "Send the action as application/json.")
		return
	}
	var req actRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, q.Body, maxActBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			refuse(w, http.StatusRequestEntityTooLarge, "The action is too large.")
			return
		}
		refuse(w, http.StatusBadRequest, "The action could not be read.")
		return
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		refuse(w, http.StatusBadRequest, "The action could not be read.")
		return
	}
	if s.sink == nil {
		refuse(w, http.StatusForbidden, "This Board is read-only.")
		return
	}
	a := Action{Card: req.Card, Text: req.Text, Confirmed: req.Confirmed}
	name := req.Key
	switch req.Action {
	case "key":
		a.Kind = "key"
		a.Key = req.Key
		if utf8.RuneCountInString(req.Text) > 200 {
			refuse(w, http.StatusRequestEntityTooLarge, "The typed text is too long.")
			return
		}
	case "say":
		a.Kind, name = "say", sayKey
		if utf8.RuneCountInString(req.Text) > SayMax {
			refuse(w, http.StatusRequestEntityTooLarge, "The message is too long: at most "+strconv.Itoa(SayMax)+" characters.")
			return
		}
	default:
		refuse(w, http.StatusBadRequest, "Unknown action.")
		return
	}

	// One action at a time: the token is checked against the card the Board
	// shows now and spent before the action runs, so a replay or a second
	// tab acting on the same frame is refused.
	s.actMu.Lock()
	defer s.actMu.Unlock()
	f, epoch, err := s.currentFrame()
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, "The Board frame could not be built.")
		return
	}
	act := s.mint(f, epoch)
	want, listed := act.Tokens[name]
	switch {
	case req.Card != act.Card:
		refuse(w, http.StatusConflict, "The Board changed since this page showed it; look again.")
		return
	case !listed:
		refuse(w, http.StatusConflict, "That button is not on the Board now; look again.")
		return
	case len(req.Token) != len(want) || !hmac.Equal([]byte(req.Token), []byte(want)):
		refuse(w, http.StatusConflict, "This action was already sent or the page is out of date; look again.")
		return
	}
	s.spend()
	ctx, cancel := context.WithTimeout(q.Context(), actWait)
	defer cancel()
	res := s.sink(ctx, a)
	if res.Status == "" {
		res.Status = "done"
	}
	status := http.StatusOK
	if res.Status == "stale" {
		status = http.StatusConflict
	}
	writeJSON(w, status, res)
}

// spend makes every token minted so far stale and drops the cached frame,
// so the next frame (and the event stream) carries new ones.
func (s *Server) spend() {
	s.frameMu.Lock()
	s.epoch++
	s.lastBody = nil
	s.frameMu.Unlock()
}
