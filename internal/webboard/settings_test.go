package webboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

type settingsHarness struct {
	s  *Server
	st *store.Store
}

func newSettingsHarness(t *testing.T) settingsHarness {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err = st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = st.InitProject(t.Context(), model.Project{ID: "project", Repository: "repo", DefaultBranch: "main", PackVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	s, _, _ := startActing(t, vm.Board{})
	s.sink = func(_ context.Context, a Action) Result {
		return a.Commit(func() Result {
			_, err := st.ApplyWebSetting(context.Background(), *a.Command)
			if err != nil {
				return Result{Status: "refused"}
			}
			return Result{Status: "done"}
		})
	}
	err = s.ConfigureSettings(SettingsConfig{Store: st, Source: func() SettingsState {
		rec, err := st.GetMarshalSettings(context.Background(), "project")
		if err != nil {
			panic(err)
		}
		return SettingsState{ProjectID: "project", Fence: "session", Capabilities: []SettingCapability{{Op: "settings/set", Target: Target{"setting", "rework-limit"}, Revision: rec.Revision}}}
	}})
	if err != nil {
		t.Fatal(err)
	}
	return settingsHarness{s, st}
}
func (h settingsHarness) command(id string) actRequest {
	cap := h.s.settingsFrame().Capabilities[0]
	return actRequest{Action: "cmd", Token: cap.Token, Command: Command{RequestID: id, ProjectID: "project", Op: cap.Op, Target: cap.Target, Revision: cap.Revision, Payload: SettingPayload{Key: "rework-limit", Value: "4"}}}
}
func (h settingsHarness) send(req actRequest, origin string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(req)
	q := httptest.NewRequest(http.MethodPost, "http://"+h.s.addr+"/api/act", bytes.NewReader(raw))
	q.Header.Set("Origin", origin)
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Authorization", "Bearer "+h.s.token)
	q.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, q)
	return w
}
func (h settingsHarness) preview(t *testing.T, req actRequest) actRequest {
	t.Helper()
	req.Preview = true
	w := h.send(req, "http://"+h.s.addr)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var res struct {
		Confirmation Confirmation `json:"confirmation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	req.Preview = false
	req.Confirmation = &res.Confirmation
	req.Token = h.s.settingsFrame().Capabilities[0].Token
	return req
}
func TestCmdRejectsCrossOriginAndReplay(t *testing.T) {
	h := newSettingsHarness(t)
	req := h.command("00112233445566778899aabbccddeeff")
	if w := h.send(req, "https://other.invalid"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	req = h.preview(t, req)
	if w := h.send(req, "http://"+h.s.addr); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Identical request identities recover the original receipt even with spent credentials.
	if w := h.send(req, "http://"+h.s.addr); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	req.RequestID = "10112233445566778899aabbccddeeff"
	if w := h.send(req, "http://"+h.s.addr); w.Code != 409 {
		t.Fatal(w.Code)
	}
	rec, _ := h.st.GetMarshalSettings(t.Context(), "project")
	if rec.Revision != 1 {
		t.Fatal(rec)
	}
}
func TestCmdConfirmationBindsExactPayload(t *testing.T) {
	h := newSettingsHarness(t)
	req := h.preview(t, h.command("00112233445566778899aabbccddeeff"))
	req.Payload.Value = "5"
	if w := h.send(req, "http://"+h.s.addr); w.Code != 409 {
		t.Fatal(w.Code)
	}
	req.Payload.Value = "4"
	if w := h.send(req, "http://"+h.s.addr); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	req.Payload.Value = "6"
	if w := h.send(req, "http://"+h.s.addr); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestConfirmationNonceCannotReplayOrChangePayload(t *testing.T) {
	h := newSettingsHarness(t)
	first := h.preview(t, h.command("00112233445566778899aabbccddeeff"))
	other := first
	other.RequestID = "10112233445566778899aabbccddeeff"
	other.Payload.Value = "6"
	if w := h.send(other, "http://"+h.s.addr); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := h.send(first, "http://"+h.s.addr); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	other.Payload = first.Payload
	other.Token = h.s.settingsFrame().Capabilities[0].Token
	if w := h.send(other, "http://"+h.s.addr); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestConcurrentTabsSpendTokenOnce(t *testing.T) {
	h := newSettingsHarness(t)
	first := h.preview(t, h.command("00112233445566778899aabbccddeeff"))
	second := h.preview(t, h.command("10112233445566778899aabbccddeeff"))
	// Both tabs hold the token from the same current frame.
	first.Token = h.s.settingsFrame().Capabilities[0].Token
	second.Token = first.Token
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, req := range []actRequest{first, second} {
		wg.Add(1)
		go func(req actRequest) { defer wg.Done(); codes <- h.send(req, "http://"+h.s.addr).Code }(req)
	}
	wg.Wait()
	close(codes)
	ok, conflict := 0, 0
	for code := range codes {
		if code == 200 {
			ok++
		}
		if code == 409 {
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
	rec, _ := h.st.GetMarshalSettings(t.Context(), "project")
	if rec.Revision != 1 {
		t.Fatal(rec)
	}
}
func TestReceiptLookupAfterLostApplyReply(t *testing.T) {
	h := newSettingsHarness(t)
	req := h.preview(t, h.command("00112233445566778899aabbccddeeff"))
	entered, release := make(chan struct{}), make(chan struct{})
	original := h.s.sink
	h.s.sink = func(ctx context.Context, a Action) Result { close(entered); <-release; return original(ctx, a) }
	raw, _ := json.Marshal(req)
	ctx, cancel := context.WithCancel(context.Background())
	q := httptest.NewRequest("POST", "http://"+h.s.addr+"/api/act", bytes.NewReader(raw)).WithContext(ctx)
	q.Header.Set("Origin", "http://"+h.s.addr)
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Authorization", "Bearer "+h.s.token)
	done := make(chan struct{})
	go func() { defer close(done); h.s.Handler().ServeHTTP(httptest.NewRecorder(), q) }()
	<-entered
	cancel()
	<-done
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		q = httptest.NewRequest("GET", "http://"+h.s.addr+"/api/act/receipts/"+req.RequestID, nil)
		q.Header.Set("Authorization", "Bearer "+h.s.token)
		w := httptest.NewRecorder()
		h.s.Handler().ServeHTTP(w, q)
		var r store.WebReceipt
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		if w.Code == 200 && r.Status == "applied" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(w.Code, w.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCmdOldSessionQueueRefusedAfterReauthentication(t *testing.T) {
	h := newSettingsHarness(t)
	req := h.preview(t, h.command("00112233445566778899aabbccddeeff"))
	entered, release := make(chan struct{}), make(chan struct{})
	original := h.s.sink
	h.s.sink = func(ctx context.Context, a Action) Result { close(entered); <-release; return original(ctx, a) }
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- h.send(req, "http://"+h.s.addr) }()
	<-entered
	secret := linkSecret(t, h.s)
	if r := spend(t, h.s, secret); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r := spend(t, h.s, secret); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	close(release)
	r := <-done
	var receipt store.WebReceipt
	_ = json.Unmarshal(r.Body.Bytes(), &receipt)
	if receipt.Status != "refused" {
		t.Fatal(r.Code, r.Body.String())
	}
	rec, _ := h.st.GetMarshalSettings(t.Context(), "project")
	if rec.Revision != 0 {
		t.Fatal(rec)
	}
}
func TestCmdAdmissionRefusalDoesNotSpendCredentials(t *testing.T) {
	h := newSettingsHarness(t)
	req := h.preview(t, h.command("00112233445566778899aabbccddeeff"))
	// An occupied revision refuses admission before either credential is spent.
	blocker := store.WebIntent{ProjectID: "project", Owner: "owner", Session: "other", RequestID: "10112233445566778899aabbccddeeff", Digest: "other", Op: "settings/set", Key: "rework-limit", Value: "6", Revision: 0}
	if _, err := h.st.AdmitWebSetting(t.Context(), blocker); err != nil {
		t.Fatal(err)
	}
	if w := h.send(req, "http://"+h.s.addr); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if _, err := h.st.RefuseWebSetting(t.Context(), blocker, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if w := h.send(req, "http://"+h.s.addr); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLegacyActionReleasesAdmissionWhileWaitingForLoop(t *testing.T) {
	h := newSettingsHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	original := h.s.sink
	h.s.sink = func(ctx context.Context, a Action) Result {
		if a.Kind != "cmd" {
			close(entered)
			<-release
			return Result{Status: "done"}
		}
		return original(ctx, a)
	}
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := boardFrame(t, h.s)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.send(actRequest{Action: "say", Text: "hello", Card: f.Act.Card, Token: f.Act.Tokens["say"]}, "http://"+h.s.addr)
	}()
	<-entered
	previewDone := make(chan int, 1)
	req := h.command("00112233445566778899aabbccddeeff")
	req.Preview = true
	go func() { previewDone <- h.send(req, "http://"+h.s.addr).Code }()
	select {
	case code := <-previewDone:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy action held admission while waiting")
	}
	once.Do(func() { close(release) })
	<-done
}

func TestSettingsFollowCurrentProjectStore(t *testing.T) {
	h := newSettingsHarness(t)
	next, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "next.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err = next.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = next.InitProject(t.Context(), model.Project{ID: "project", Repository: "repo", DefaultBranch: "main", PackVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	current := h.st
	generation := "first"
	currentStore := func() *store.Store { mu.Lock(); defer mu.Unlock(); return current }
	if err = h.s.ConfigureSettings(SettingsConfig{Store: h.st, CurrentStore: currentStore, Source: func() SettingsState {
		mu.Lock()
		st, fence := current, generation
		mu.Unlock()
		rec, e := st.GetMarshalSettings(context.Background(), "project")
		if e != nil {
			panic(e)
		}
		return SettingsState{ProjectID: "project", Fence: fence, Capabilities: []SettingCapability{{Op: "settings/set", Target: Target{"setting", "rework-limit"}, Revision: rec.Revision}}}
	}}); err != nil {
		t.Fatal(err)
	}
	h.s.sink = func(_ context.Context, a Action) Result {
		return a.Commit(func() Result {
			_, e := currentStore().ApplyWebSetting(context.Background(), *a.Command)
			if e != nil {
				return Result{Status: "refused"}
			}
			return Result{Status: "done"}
		})
	}
	old := h.preview(t, h.command("00112233445566778899aabbccddeeff"))
	unlock := h.s.LockSettingsTarget()
	mu.Lock()
	current = next
	generation = "second"
	mu.Unlock()
	unlock()
	if w := h.send(old, "http://"+h.s.addr); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	req := h.preview(t, h.command("10112233445566778899aabbccddeeff"))
	if w := h.send(req, "http://"+h.s.addr); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	before, _ := h.st.GetMarshalSettings(t.Context(), "project")
	after, _ := next.GetMarshalSettings(t.Context(), "project")
	if before.Revision != 0 || after.Revision != 1 || after.Value.ReworkLimit != 4 {
		t.Fatal(before, after)
	}
}

func TestIdleSettingsFrameKeepsItsTokens(t *testing.T) {
	h := newSettingsHarness(t)
	first := h.s.settingsFrame()
	second := h.s.settingsFrame()
	if first == nil || second == nil || len(first.Capabilities) == 0 {
		t.Fatal("settings frame has no capabilities")
	}
	for i := range first.Capabilities {
		if first.Capabilities[i].Token != second.Capabilities[i].Token {
			t.Fatalf("token for %v changed between idle frames", first.Capabilities[i].Target)
		}
	}
	claims, ok := h.s.readClaims(first.Capabilities[0].Token)
	if !ok {
		t.Fatal("token does not verify")
	}
	h.s.forgetSettingsToken(claims.Nonce)
	if third := h.s.settingsFrame(); third.Capabilities[0].Token == first.Capabilities[0].Token {
		t.Fatal("a spent token was offered again")
	}
}
