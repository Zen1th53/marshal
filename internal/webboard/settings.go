package webboard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type SettingPayload struct {
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}
type Confirmation struct {
	Digest   string `json:"digest"`
	Nonce    string `json:"nonce"`
	Revision int64  `json:"revision"`
	Expires  int64  `json:"expires"`
}
type Command struct {
	RequestID    string         `json:"requestId"`
	Target       Target         `json:"target"`
	Op           string         `json:"op"`
	ProjectID    string         `json:"projectId"`
	RunID        string         `json:"runId,omitempty"`
	Revision     int64          `json:"revision"`
	AttemptID    string         `json:"attemptId,omitempty"`
	Payload      SettingPayload `json:"payload"`
	Confirmation *Confirmation  `json:"confirmation,omitempty"`
	Preview      bool           `json:"preview,omitempty"`
}
type SettingCapability struct {
	Op       string   `json:"op"`
	Target   Target   `json:"target"`
	Revision int64    `json:"revision"`
	Token    string   `json:"token"`
	Values   []string `json:"values,omitempty"`
}
type SettingsState struct {
	ProjectID    string              `json:"projectId"`
	Fence        string              `json:"-"`
	Capabilities []SettingCapability `json:"capabilities"`
}
type SettingsConfig struct {
	Source       func() SettingsState
	Store        *store.Store
	CurrentStore func() *store.Store
	journals     map[settingsJournal]bool
	Session      string
	Reconcile    func(context.Context)
}
type settingsPointer struct{ atomic.Pointer[SettingsConfig] }
type settingsJournal struct {
	store   *store.Store
	project string
}

// journal is called under admission so a target switch cannot replace its store.
func (c *SettingsConfig) journal(project string) (*store.Store, error) {
	st := c.Store
	if c.CurrentStore != nil {
		st = c.CurrentStore()
	}
	if st == nil {
		return nil, errors.New("settings store unavailable")
	}
	key := settingsJournal{st, project}
	if !c.journals[key] {
		if err := st.FenceWebSettings(context.Background(), project, c.Session); err != nil {
			return nil, err
		}
		if c.journals == nil {
			c.journals = map[settingsJournal]bool{}
		}
		c.journals[key] = true
	}
	return st, nil
}

// ConfigureSettings enables only operations supplied by the ready settings writer.
func (s *Server) ConfigureSettings(c SettingsConfig) error {
	if c.Store == nil || c.Source == nil {
		return errors.New("settings writer unavailable")
	}
	session, err := randomToken(32)
	if err != nil {
		return err
	}
	c.Session = session
	state := c.Source()
	if _, err = c.journal(state.ProjectID); err != nil {
		return err
	}
	s.settings.Store(&c)
	s.frameMu.Lock()
	s.lastBody = nil
	s.frameMu.Unlock()
	return nil
}

// SettingsEpoch conceals the workspace session identity while binding its generation.
func SettingsEpoch(fence string) string {
	sum := sha256.Sum256([]byte(fence))
	return hex.EncodeToString(sum[:])
}

// LockSettingsTarget serializes trusted workspace target changes with admission.
// Release it before waiting for the input loop or starting external work.
func (s *Server) LockSettingsTarget() func() {
	s.actMu.Lock()
	return func() { s.spend(); s.actMu.Unlock() }
}

type commandClaims struct {
	Project, Fence, Auth, Op string
	Target                   Target
	Revision                 int64
	Nonce                    string
	Expires                  int64
}

func (s *Server) authIdentity() string {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	sum := sha256.Sum256([]byte(s.token))
	return hex.EncodeToString(sum[:])
}
func (s *Server) signClaims(c commandClaims) string {
	raw, _ := json.Marshal(c)
	m := hmac.New(sha256.New, s.actKey)
	m.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (s *Server) readClaims(token string) (commandClaims, bool) {
	var c commandClaims
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return c, false
	}
	raw, e := base64.RawURLEncoding.DecodeString(body)
	if e != nil {
		return c, false
	}
	actual, e := base64.RawURLEncoding.DecodeString(sig)
	if e != nil {
		return c, false
	}
	m := hmac.New(sha256.New, s.actKey)
	m.Write(raw)
	if !hmac.Equal(actual, m.Sum(nil)) || json.Unmarshal(raw, &c) != nil {
		return c, false
	}
	return c, true
}
func (s *Server) settingsFrame() *SettingsState {
	c := s.settings.Load()
	if c == nil {
		return nil
	}
	state := c.Source()
	auth := s.authIdentity()
	now := time.Now()
	s.settingsTokMu.Lock()
	defer s.settingsTokMu.Unlock()
	if s.settingsTokens == nil {
		s.settingsTokens = map[string]cachedSettingsToken{}
	}
	for key, t := range s.settingsTokens {
		if t.expires <= now.Unix() {
			delete(s.settingsTokens, key)
		}
	}
	for n := range state.Capabilities {
		cap := &state.Capabilities[n]
		// Reuse an unspent token for the same binding while it has at least a
		// minute left, so an idle Board's frame (and its stream) stays stable.
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%v\x00%v", state.ProjectID, SettingsEpoch(state.Fence), auth, cap.Op, cap.Target, cap.Revision)
		if t, ok := s.settingsTokens[key]; ok && t.expires-now.Unix() >= 60 {
			cap.Token = t.token
			continue
		}
		nonce, err := randomToken(16)
		if err != nil {
			return nil
		}
		expires := now.Add(2 * time.Minute).Unix()
		cap.Token = s.signClaims(commandClaims{state.ProjectID, SettingsEpoch(state.Fence), auth, cap.Op, cap.Target, cap.Revision, nonce, expires})
		s.settingsTokens[key] = cachedSettingsToken{token: cap.Token, nonce: nonce, expires: expires}
	}
	return &state
}

// cachedSettingsToken is a minted settings action token kept until it is
// spent or close to expiry.
type cachedSettingsToken struct {
	token, nonce string
	expires      int64
}

// forgetSettingsToken drops a spent token so the next frame carries a new one.
func (s *Server) forgetSettingsToken(nonce string) {
	s.settingsTokMu.Lock()
	defer s.settingsTokMu.Unlock()
	for key, t := range s.settingsTokens {
		if t.nonce == nonce {
			delete(s.settingsTokens, key)
		}
	}
}
func commandDigest(c Command) string {
	c.Confirmation = nil
	c.Preview = false
	// Request identity and credentials do not describe the requested effect.
	c.RequestID = ""
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Server) commandEligible(c Command, claims commandClaims) bool {
	config := s.settings.Load()
	if config == nil {
		return false
	}
	state := config.Source()
	if state.ProjectID != c.ProjectID || claims.Project != state.ProjectID || claims.Fence != SettingsEpoch(state.Fence) || claims.Auth != s.authIdentity() || claims.Expires <= time.Now().Unix() {
		return false
	}
	for _, cap := range state.Capabilities {
		if cap.Op == c.Op && cap.Target == c.Target && cap.Revision == c.Revision && claims.Op == c.Op && claims.Target == c.Target && claims.Revision == c.Revision {
			return true
		}
	}
	return false
}
func validRequestID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && id == strings.ToLower(id)
}

// serveCommand runs only after the shared bounded JSON and browser-origin checks.
func (s *Server) serveCommand(w http.ResponseWriter, q *http.Request, req actRequest) {
	c := req.Command
	if !validRequestID(c.RequestID) || (c.Target.Kind != "setting" && c.Target.Kind != "entitlement-request") || c.RunID != "" || c.AttemptID != "" || c.Revision < 0 {
		refuse(w, 400, "Invalid settings request.")
		return
	}
	key, value := c.Payload.Key, c.Payload.Value
	switch c.Op {
	case "settings/set":
		if c.Target.Kind != "setting" {
			refuse(w, 400, "Invalid setting target.")
			return
		}
		if key != c.Target.ID || key == "" || c.Payload.Enabled != nil {
			refuse(w, 400, "Invalid setting payload.")
			return
		}
	case "settings/ultra.set":
		if c.Target.Kind != "setting" || c.Target.ID != "ultra-execution" || c.Payload.Enabled == nil || key != "" || value != "" {
			refuse(w, 400, "Invalid execution payload.")
			return
		}
		key = "ultra-execution"
		value = "false"
		if *c.Payload.Enabled {
			value = "true"
		}
	case "settings/ultra.request":
		if c.Target.Kind != "entitlement-request" || c.Payload.Key != "" || c.Payload.Value != "" || c.Payload.Enabled != nil {
			refuse(w, 400, "Invalid request payload.")
			return
		}
		key = c.Target.ID
	default:
		refuse(w, 400, "This operation is not available.")
		return
	}
	config := s.settings.Load()
	if config == nil {
		refuse(w, 403, "Settings are read-only.")
		return
	}
	digest := commandDigest(c)
	s.actMu.Lock()
	unlock := true
	defer func() {
		if unlock {
			s.actMu.Unlock()
		}
	}()
	// Recheck authentication inside admission, serialized with reauthentication.
	if !s.authorized(q) {
		refuse(w, 401, "Reopen the Board to authenticate.")
		return
	}
	state := config.Source()
	if state.ProjectID != c.ProjectID {
		refuse(w, 409, "The project changed.")
		return
	}
	journal, err := config.journal(c.ProjectID)
	if err != nil {
		refuse(w, 503, "Settings store unavailable; check again before retrying.")
		return
	}
	if !c.Preview {
		receipt, err := journal.WebReceipt(q.Context(), c.ProjectID, "owner", c.RequestID, digest)
		if err == nil {
			writeJSON(w, 200, receipt)
			return
		}
		if !errors.Is(err, model.ErrNotFound) {
			refuse(w, 409, "Request identity conflicts with an earlier request.")
			return
		}
	}
	claims, ok := s.readClaims(req.Token)
	if !ok || !s.commandEligible(c, claims) {
		refuse(w, 409, "The settings or session changed; refresh the page.")
		return
	}
	now := time.Now().Unix()
	if s.commandSpent == nil {
		s.commandSpent = map[string]int64{}
	}
	if s.previews == nil {
		s.previews = map[string]Confirmation{}
	}
	for id, expiry := range s.commandSpent {
		if expiry <= now {
			delete(s.commandSpent, id)
		}
	}
	for id, p := range s.previews {
		if p.Expires <= now {
			delete(s.previews, id)
		}
	}
	if _, spent := s.commandSpent[claims.Nonce]; spent {
		refuse(w, 409, "This action token was already used.")
		return
	}
	if c.Preview {
		if len(s.previews) >= 128 {
			refuse(w, 503, "Too many pending confirmations.")
			return
		}
		nonce, err := randomToken(16)
		if err != nil {
			refuse(w, 503, "Could not prepare confirmation.")
			return
		}
		preview := Confirmation{digest, nonce, c.Revision, time.Now().Add(time.Minute).Unix()}
		s.previews[nonce] = preview
		s.frameMu.Lock()
		s.lastBody = nil
		s.frameMu.Unlock()
		s.commandSpent[claims.Nonce] = claims.Expires
		s.forgetSettingsToken(claims.Nonce)
		writeJSON(w, 200, map[string]any{"status": "preview", "confirmation": preview})
		return
	}
	if c.Confirmation == nil {
		refuse(w, 409, "Preview this exact change before confirming.")
		return
	}
	preview, ok := s.previews[c.Confirmation.Nonce]
	if !ok || preview != *c.Confirmation || preview.Digest != digest || preview.Revision != c.Revision || preview.Expires <= now {
		refuse(w, 409, "The confirmation expired or the change differs from its preview.")
		return
	}
	i := store.WebIntent{AuthGeneration: claims.Auth, ProjectEpoch: claims.Fence, TargetKind: c.Target.Kind, RequestID: c.RequestID, ProjectID: c.ProjectID, Owner: "owner", Session: config.Session, Digest: digest, Op: c.Op, Key: key, Value: value, Revision: c.Revision}
	receipt, err := journal.AdmitWebSetting(q.Context(), i)
	if err != nil {
		refuse(w, 409, "The settings queue could not accept this change; refresh and try the same request again.")
		return
	}
	s.commandSpent[claims.Nonce] = claims.Expires
	s.forgetSettingsToken(claims.Nonce)
	delete(s.previews, preview.Nonce)
	s.actMu.Unlock()
	unlock = false
	// HTTP cancellation never cancels an admitted operation.
	done := make(chan struct{})
	go func() {
		defer close(done)
		a := Action{Kind: "cmd", Command: &i}
		a.Commit = func(effect func() Result) Result {
			s.actMu.Lock()
			defer s.actMu.Unlock()
			if !s.commandEligible(c, claims) || s.ctx.Err() != nil {
				_, _ = journal.RefuseWebSetting(context.Background(), i, "authority_changed")
				return Result{Status: "refused", Text: "Settings authority changed before the change started."}
			}
			return effect()
		}
		res := s.sink(s.ctx, a)
		s.frameMu.Lock()
		s.lastBody = nil
		s.frameMu.Unlock()
		if res.Status != "done" && res.Status != "started" {
			_, _ = journal.RefuseWebSetting(context.Background(), i, "writer_refused")
		}
	}()
	select {
	case <-done:
	case <-q.Context().Done():
		return
	case <-time.After(actWait):
	}
	receipt, err = journal.WebReceipt(context.Background(), c.ProjectID, "owner", c.RequestID, digest)
	if err != nil {
		refuse(w, 503, "Receipt unavailable; query this request before retrying.")
		return
	}
	writeJSON(w, 200, receipt)
}
func (s *Server) serveReceipt(w http.ResponseWriter, q *http.Request) {
	c := s.settings.Load()
	id := strings.TrimPrefix(q.URL.Path, "/api/act/receipts/")
	if c == nil || !validRequestID(id) {
		refuse(w, 404, "Receipt not found.")
		return
	}
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if !s.authorized(q) {
		refuse(w, 401, "Reopen the Board to authenticate.")
		return
	}
	if c.Reconcile != nil {
		c.Reconcile(q.Context())
	}
	project := c.Source().ProjectID
	journal, err := c.journal(project)
	if err != nil {
		refuse(w, 503, "Receipt unavailable; check again before retrying.")
		return
	}
	r, err := journal.WebReceipt(q.Context(), project, "owner", id, "")
	if errors.Is(err, model.ErrNotFound) {
		refuse(w, 404, "Receipt not found.")
		return
	}
	if err != nil {
		refuse(w, 503, "Receipt unavailable; check again before retrying.")
		return
	}
	writeJSON(w, 200, r)
}
