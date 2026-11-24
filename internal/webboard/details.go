package webboard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/redaction"
)

// Run details are a read: why the run stopped, what failed, what the
// reviewers found, and the retained diff and conversation, paged. They are
// served to the authenticated page only, never spend an action token and
// never depend on the card on screen, so a failure stays reachable while
// other buttons are visible (or none are, on a read-only Board). Every id
// the page holds is a signed, opaque claim bound to the run and revision it
// was minted for; the page never sends a task, commit, path or raw cursor.

// Limits of the details reads.
const (
	// TranscriptChars is the most conversation text one page carries.
	TranscriptChars = 4000
	// DetailChars caps one error or reason text.
	DetailChars = 4000
	// diffPageChars caps the redacted diff text of one page. It is never
	// smaller than a source page, so nothing is cut without a cursor.
	diffPageChars = 5 << 20
)

// ErrStaleDetails is what a source returns for a subject that is not the
// current one any more.
var ErrStaleDetails = errors.New("web details: stale")

// PausedDetail is why the run does not move.
type PausedDetail struct {
	Kind, Code, Reason string
	Raw                string
	Resolutions        []string
}

// TaskEvidence is one task's review and evidence summary. DiffSubject names
// the diff the source can page; it never reaches the page, which gets DiffID.
type TaskEvidence struct {
	Task        string   `json:"task"`
	Title       string   `json:"title"`
	State       string   `json:"state"`
	Verdict     string   `json:"verdict,omitempty"`
	Reviewer    string   `json:"reviewer,omitempty"`
	Reasons     []string `json:"reasons,omitempty"`
	Checks      []string `json:"checks,omitempty"`
	Scope       []string `json:"scope,omitempty"`
	Changed     []string `json:"changed,omitempty"`
	Limits      []string `json:"limits,omitempty"`
	Base        string   `json:"base,omitempty"`
	Result      string   `json:"result,omitempty"`
	DiffSubject string   `json:"-"`
	DiffID      string   `json:"diffId,omitempty"`
}

// RunDetails is what the source knows of the run now. Scope names the run
// and its revision; the empty scope is no run.
type RunDetails struct {
	Run    string
	Scope  string
	Paused *PausedDetail
	MCP    string
	Tasks  []TaskEvidence
}

// PausedJSON is the wire form of PausedDetail.
type PausedJSON struct {
	Kind        string   `json:"kind"`
	Code        string   `json:"code"`
	Reason      string   `json:"reason"`
	Raw         string   `json:"raw"`
	Resolutions []string `json:"resolutions"`
}

// Message is one retained conversation message; Role is a claim made by the
// stored record, not verified.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
	At   string `json:"at"`
}

// Retained is the retained project conversation now. Base identifies its
// oldest retained message, so appended messages keep an older page valid
// and trimmed ones do not. Unavailable says why earlier history is missing
// ("" when none is).
type Retained struct {
	Session     string
	Base        string
	Messages    []Message
	Unavailable string
}

// DiffPart is one page of a reviewed diff; Next is the source's own cursor
// for the following page, "" at the end.
type DiffPart struct {
	Text, Next, Note string
}

// DetailsSource reads the facts a details page shows. The server never
// trusts what the page sends: it checks every id against Scope now.
type DetailsSource struct {
	Scope    func(context.Context) string
	Details  func(context.Context) RunDetails
	Diff     func(ctx context.Context, subject, cursor string) (DiffPart, error)
	Messages func(context.Context) Retained
}

type detailsPointer struct{ atomic.Pointer[DetailsSource] }

// ConfigureDetails enables the details reads.
func (s *Server) ConfigureDetails(c DetailsSource) { s.details.Store(&c) }

// pageClaims is what an id binds.
type pageClaims struct {
	Auth, Scope, Kind, Subject, Cursor string
	Start, End                         int
	Back                               bool
}

func (s *Server) signPage(c pageClaims) string {
	c.Auth = s.authIdentity()
	raw, _ := json.Marshal(c)
	m := hmac.New(sha256.New, s.actKey)
	m.Write([]byte("details\x00"))
	m.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) readPage(id string) (pageClaims, bool) {
	var c pageClaims
	body, sig, ok := strings.Cut(id, ".")
	if !ok || len(id) > 2048 {
		return c, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return c, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return c, false
	}
	m := hmac.New(sha256.New, s.actKey)
	m.Write([]byte("details\x00"))
	m.Write(raw)
	if !hmac.Equal(got, m.Sum(nil)) || json.Unmarshal(raw, &c) != nil || c.Auth != s.authIdentity() {
		return c, false
	}
	return c, true
}

// plain is text for the page: redacted, trimmed and capped.
func plain(text string, max int) string {
	text = strings.TrimSpace(redaction.RedactAll(strings.ToValidUTF8(text, "?")))
	if r := []rune(text); len(r) > max {
		return string(r[:max]) + "…"
	}
	return text
}

func plainAll(in []string) []string {
	var out []string
	for _, s := range in {
		if s = plain(s, DetailChars); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// TranscriptPage is one bounded page of the retained conversation.
type TranscriptPage struct {
	Session     string    `json:"session"`
	Messages    []Message `json:"messages"`
	Older       string    `json:"older,omitempty"`
	Newer       string    `json:"newer,omitempty"`
	Unavailable string    `json:"unavailable,omitempty"`
	Note        string    `json:"note"`
}

// DetailsReply is the body of GET /api/details.
type DetailsReply struct {
	Run        string         `json:"run"`
	Scope      string         `json:"scope"`
	Paused     *PausedJSON    `json:"paused"`
	MCP        string         `json:"mcp"`
	Tasks      []TaskEvidence `json:"tasks"`
	Transcript TranscriptPage `json:"transcript"`
}

// DiffReply is the body of a diff page.
type DiffReply struct {
	Text string `json:"text"`
	Next string `json:"next,omitempty"`
	Note string `json:"note,omitempty"`
}

func (s *Server) detailsFail(w http.ResponseWriter, status int, text string) {
	writeJSON(w, status, Result{Status: "refused", Text: text})
}

// pageOf takes messages ending before (or starting after) a position, up to
// TranscriptChars of text. It returns the range it took.
func pageOf(ms []Message, from int, back bool) (int, int) {
	used := 0
	if back {
		start := from
		for start > 0 {
			n := utf8.RuneCountInString(ms[start-1].Text)
			if n > TranscriptChars {
				n = TranscriptChars
			}
			if start < from && used+n > TranscriptChars {
				break
			}
			used += n
			start--
		}
		return start, from
	}
	end := from
	for end < len(ms) {
		n := utf8.RuneCountInString(ms[end].Text)
		if n > TranscriptChars {
			n = TranscriptChars
		}
		if end > from && used+n > TranscriptChars {
			break
		}
		used += n
		end++
	}
	return from, end
}

func (s *Server) transcriptPage(scope string, r Retained, start, end int) TranscriptPage {
	p := TranscriptPage{Session: plain(r.Session, 200), Messages: []Message{}}
	subject := r.Session + "@" + r.Base
	for _, m := range r.Messages[start:end] {
		p.Messages = append(p.Messages, Message{Role: plain(m.Role, 80), Text: plain(m.Text, TranscriptChars), At: plain(m.At, 40)})
	}
	if start > 0 {
		p.Older = s.signPage(pageClaims{Scope: scope, Kind: "transcript", Subject: subject, Start: start, End: end, Back: true})
	} else if r.Unavailable != "" {
		p.Unavailable = plain(r.Unavailable, DetailChars)
	}
	if end < len(r.Messages) {
		p.Newer = s.signPage(pageClaims{Scope: scope, Kind: "transcript", Subject: subject, Start: start, End: end})
	}
	p.Note = "Roles are what the saved record says; they are not verified."
	return p
}

// serveDetails is GET /api/details (the run's facts and the newest page of
// its conversation) and GET /api/details/page?id= (one diff or conversation
// page). The bearer is already checked.
func (s *Server) serveDetails(w http.ResponseWriter, q *http.Request) {
	src := s.details.Load()
	if src == nil || src.Scope == nil || src.Details == nil {
		s.detailsFail(w, http.StatusNotFound, "Run details are not available on this Board.")
		return
	}
	ctx := q.Context()
	scope := src.Scope(ctx)
	if q.URL.Path == "/api/details/page" {
		s.serveDetailsPage(w, q, src, scope)
		return
	}
	if run := q.URL.Query().Get("run"); run != "" && run != scope {
		s.detailsFail(w, http.StatusConflict, "That run is not the Board's run now; look again.")
		return
	}
	d := src.Details(ctx)
	if d.Scope != scope {
		s.detailsFail(w, http.StatusConflict, "The run changed while its details were read; look again.")
		return
	}
	reply := DetailsReply{Run: plain(d.Run, 200), Scope: d.Scope, MCP: plain(d.MCP, DetailChars)}
	if p := d.Paused; p != nil {
		reply.Paused = &PausedJSON{Kind: plain(p.Kind, 80), Code: plain(p.Code, 80), Reason: plain(p.Reason, DetailChars), Raw: plain(p.Raw, DetailChars), Resolutions: plainAll(p.Resolutions)}
	}
	reply.Tasks = make([]TaskEvidence, 0, len(d.Tasks))
	for _, t := range d.Tasks {
		t.Title, t.Task, t.State = plain(t.Title, 300), plain(t.Task, 80), plain(t.State, 80)
		t.Verdict, t.Reviewer = plain(t.Verdict, 80), plain(t.Reviewer, 120)
		t.Reasons, t.Checks, t.Scope = plainAll(t.Reasons), plainAll(t.Checks), plainAll(t.Scope)
		t.Changed, t.Limits = plainAll(t.Changed), plainAll(t.Limits)
		t.Base, t.Result = plain(t.Base, 80), plain(t.Result, 80)
		if t.DiffSubject != "" && scope != "" {
			t.DiffID = s.signPage(pageClaims{Scope: scope, Kind: "diff", Subject: t.DiffSubject})
		}
		reply.Tasks = append(reply.Tasks, t)
	}
	reply.Transcript = TranscriptPage{Messages: []Message{}, Unavailable: "No conversation is retained for this project.", Note: "Roles are what the saved record says; they are not verified."}
	if src.Messages != nil && scope != "" {
		r := src.Messages(ctx)
		if len(r.Messages) > 0 {
			start, end := pageOf(r.Messages, len(r.Messages), true)
			reply.Transcript = s.transcriptPage(scope, r, start, end)
		} else if r.Unavailable != "" {
			reply.Transcript.Unavailable = plain(r.Unavailable, DetailChars)
		}
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) serveDetailsPage(w http.ResponseWriter, q *http.Request, src *DetailsSource, scope string) {
	c, ok := s.readPage(q.URL.Query().Get("id"))
	if !ok || c.Scope == "" {
		s.detailsFail(w, http.StatusBadRequest, "That page is not one this Board gave out.")
		return
	}
	if c.Scope != scope {
		s.detailsFail(w, http.StatusConflict, "The run changed since this page was shown; look again.")
		return
	}
	ctx := q.Context()
	switch c.Kind {
	case "diff":
		if src.Diff == nil {
			s.detailsFail(w, http.StatusNotFound, "Diffs are not available on this Board.")
			return
		}
		part, err := src.Diff(ctx, c.Subject, c.Cursor)
		if errors.Is(err, ErrStaleDetails) || errors.Is(err, model.ErrPolicyDenied) {
			s.detailsFail(w, http.StatusConflict, "The reviewed result changed since this page was shown; look again.")
			return
		}
		if err != nil {
			s.detailsFail(w, http.StatusServiceUnavailable, "The diff could not be read.")
			return
		}
		out := DiffReply{Text: plain(part.Text, diffPageChars), Note: plain(part.Note, DetailChars)}
		if part.Next != "" {
			out.Next = s.signPage(pageClaims{Scope: scope, Kind: "diff", Subject: c.Subject, Cursor: part.Next})
		}
		writeJSON(w, http.StatusOK, out)
	case "transcript":
		if src.Messages == nil {
			s.detailsFail(w, http.StatusNotFound, "No conversation is retained for this project.")
			return
		}
		r := src.Messages(ctx)
		if c.Subject != r.Session+"@"+r.Base || c.Start < 0 || c.End > len(r.Messages) || c.Start > c.End {
			s.detailsFail(w, http.StatusConflict, "The conversation changed since this page was shown; look again.")
			return
		}
		from := c.End
		if c.Back {
			from = c.Start
		}
		start, end := pageOf(r.Messages, from, c.Back)
		writeJSON(w, http.StatusOK, s.transcriptPage(scope, r, start, end))
	default:
		s.detailsFail(w, http.StatusBadRequest, "That page is not one this Board gave out.")
	}
}
