package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/webboard"
)

type outsideRunChatTransport struct {
	events chan chattransport.Event
	perms  chan chattransport.PermissionAsk

	mu        sync.Mutex
	sessionID string
	sent      []string
	decided   map[string]chattransport.Decision
	sendErr   error
	closed    bool
}

func newOutsideRunChatTransport(sessionID string) *outsideRunChatTransport {
	return &outsideRunChatTransport{
		events:    make(chan chattransport.Event, 16),
		perms:     make(chan chattransport.PermissionAsk, 16),
		sessionID: sessionID,
		decided:   map[string]chattransport.Decision{},
	}
}

func (f *outsideRunChatTransport) Start(_ context.Context, s chattransport.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.SessionID != "" {
		f.sessionID = s.SessionID
	}
	return nil
}

func (f *outsideRunChatTransport) Send(_ context.Context, turn chattransport.Turn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, turn.Text)
	return nil
}

func (f *outsideRunChatTransport) Events() <-chan chattransport.Event              { return f.events }
func (f *outsideRunChatTransport) Permissions() <-chan chattransport.PermissionAsk { return f.perms }

func (f *outsideRunChatTransport) Decide(_ context.Context, ask chattransport.PermissionAsk, d chattransport.Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decided[ask.ID] = d
	return nil
}

func (f *outsideRunChatTransport) Resume(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionID = id
	return nil
}

func (f *outsideRunChatTransport) SessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessionID
}

func (f *outsideRunChatTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *outsideRunChatTransport) sentTurns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *outsideRunChatTransport) decisionFor(id string) (chattransport.Decision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.decided[id]
	return d, ok
}

func outsideRunWorkspaceFixture(t *testing.T, sessionID string) (*Workspace, *app.Runtime, string) {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rt, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	status, err := rt.Status(context.Background())
	if err != nil {
		t.Fatalf("runtime status: %v", err)
	}
	ws := NewWorkspace(rt.Store(), string(status.Project.ID), sessionID)
	ws.workDir = rt.ProjectRoot()
	ws.AttachRuntime(rt, projectid.ID(rt.ProjectIdentity()))
	t.Cleanup(func() { ws.Close() })
	return ws, rt, rt.ProjectRoot()
}

func gitOutputInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func portableAskProposal(t *testing.T, question, choices string) marshalProposal {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"action":   "ask",
		"question": question,
		"choices":  choices,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseMarshalProposal(marshalProposalPrefix + string(data))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestStaleAskCannotAnswerNewQuestion verifies that answering a superseded or
// mismatched ask (old ID, old revision, wrong launch, unauthorized actor, or
// identical text from an earlier question) is refused and never answers the
// newer open question. It also checks that legacy proposal file asks adopt
// once by stable ID across restarts.
func TestStaleAskCannotAnswerNewQuestion(t *testing.T) {
	w, rt, root := outsideRunWorkspaceFixture(t, "SESSION-stale-ask")
	tr := newOutsideRunChatTransport("sess-live-1")
	w.attachNativeChat(tr, root, nil, nil, nil, nil, "", false)

	const choices = `[{"key":"enter","label":"Proceed"},{"key":"s","label":"Skip"}]`

	// Open question 1 and capture its stale card binding (simulating an old tab).
	p1 := portableAskProposal(t, "Should we migrate the schema first?", choices)
	w.openAsk(p1)
	card1 := w.currentAsk()
	if card1 == nil || card1.ID == "" || card1.Launch == "" || card1.Revision != 1 || card1.Status != askStatusOpen {
		t.Fatalf("ask 1 card invalid: %+v", card1)
	}
	staleTab := askAnswerBinding{
		Project:  card1.Project,
		ID:       card1.ID,
		Launch:   card1.Launch,
		Revision: card1.Revision,
		Actor:    "owner:web",
		Choice:   0,
	}

	// Open question 2: question 1 is durably marked superseded.
	p2 := portableAskProposal(t, "Deploy canary before full rollout?", choices)
	w.openAsk(p2)
	card2 := w.currentAsk()
	if card2 == nil || card2.ID == card1.ID || card2.Revision != 2 || card2.Status != askStatusOpen {
		t.Fatalf("ask 2 card invalid: %+v", card2)
	}
	rec1, ok := w.askRecordByID(card1.ID)
	if !ok || rec1.Status != askStatusSuperseded {
		t.Fatalf("ask 1 status = %+v (found=%v), want superseded", rec1, ok)
	}

	// Stale tab submits answer for question 1: must be refused, question 2 untouched.
	res := w.answerAskBound(context.Background(), staleTab)
	if res.Status != "stale" || res.Err == nil {
		t.Fatalf("stale ask 1 answer should be refused: %+v", res)
	}
	if got := tr.sentTurns(); len(got) != 0 {
		t.Fatalf("stale ask delivered a turn: %q", got)
	}
	if cur := w.currentAsk(); cur == nil || cur.ID != card2.ID {
		t.Fatalf("ask 2 was disturbed by stale answer: %+v", cur)
	}

	// Wrong revision, wrong launch, wrong project, or unauthorized actor on ask 2 are all refused.
	for name, bad := range map[string]askAnswerBinding{
		"wrong-revision": {Project: card2.Project, ID: card2.ID, Launch: card2.Launch, Revision: card1.Revision, Actor: "owner:web", Choice: 0},
		"wrong-launch":   {Project: card2.Project, ID: card2.ID, Launch: "native:claude:stale-launch", Revision: card2.Revision, Actor: "owner:web", Choice: 0},
		"wrong-project":  {Project: "other-project", ID: card2.ID, Launch: card2.Launch, Revision: card2.Revision, Actor: "owner:web", Choice: 0},
		"agent-actor":    {Project: card2.Project, ID: card2.ID, Launch: card2.Launch, Revision: card2.Revision, Actor: "marshal", Choice: 0},
		"empty-actor":    {Project: card2.Project, ID: card2.ID, Launch: card2.Launch, Revision: card2.Revision, Actor: "", Choice: 0},
	} {
		r := w.answerAskBound(context.Background(), bad)
		if r.Err == nil || r.Status == "answered" || r.Status == "replayed" {
			t.Fatalf("%s was accepted: %+v", name, r)
		}
	}
	if len(tr.sentTurns()) != 0 {
		t.Fatalf("invalid bindings delivered turns: %q", tr.sentTurns())
	}

	// Exact current binding answers ask 2 once.
	okRes := w.answerAskBound(context.Background(), askAnswerBinding{
		Project:  card2.Project,
		ID:       card2.ID,
		Launch:   card2.Launch,
		Revision: card2.Revision,
		Actor:    "owner:web",
		Choice:   1,
	})
	if okRes.Err != nil || okRes.Status != "answered" || okRes.Record.Status != askStatusAnswered || okRes.Record.AnsweredAt == nil {
		t.Fatalf("valid ask 2 answer failed: %+v", okRes)
	}
	if got := tr.sentTurns(); len(got) != 1 || got[0] != "Skip" {
		t.Fatalf("delivered turns = %q, want [Skip]", got)
	}

	// Two sequential asks with identical question/choices still get distinct IDs/revisions.
	w.openAsk(portableAskProposal(t, "Continue with next step?", choices))
	dup1 := w.currentAsk()
	w.openAsk(portableAskProposal(t, "Continue with next step?", choices))
	dup2 := w.currentAsk()
	if dup1.ID == dup2.ID || dup1.Revision == dup2.Revision {
		t.Fatalf("sequential identical asks collided: dup1=%+v dup2=%+v", dup1, dup2)
	}
	if r := w.answerAskBound(context.Background(), askAnswerBinding{
		Project: dup1.Project, ID: dup1.ID, Launch: dup1.Launch, Revision: dup1.Revision, Actor: "owner:web", Choice: 0,
	}); r.Status != "stale" || r.Err == nil {
		t.Fatalf("earlier identical ask was not refused: %+v", r)
	}
	if cur := w.currentAsk(); cur == nil || cur.ID != dup2.ID {
		t.Fatalf("newer identical ask was cleared by stale answer: %+v", cur)
	}

	// Legacy file ask adopts once by stable ID even if re-dropped with a new mtime after restart.
	propDir := filepath.Join(root, statepath.DirName, "proposals")
	if err := os.MkdirAll(propDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyBody := `{"action":"ask","question":"Keep legacy file ask?","choices":"[{\"key\":\"enter\",\"label\":\"Keep\"},{\"key\":\"d\",\"label\":\"Drop\"}]"}`
	legacyPath := filepath.Join(propDir, "legacy-ask.json")
	if err := os.WriteFile(legacyPath, []byte(legacyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(root)
	adopted := w.currentAsk()
	if adopted == nil || !strings.HasPrefix(adopted.ID, "legacy-ask:") {
		t.Fatalf("legacy ask did not adopt with stable ID: %+v", adopted)
	}
	// Answer it, then simulate a restart and re-drop the same legacy file with a newer mtime.
	if r := w.answerAskBound(context.Background(), askAnswerBinding{
		Project: adopted.Project, ID: adopted.ID, Launch: adopted.Launch, Revision: adopted.Revision, Actor: "owner:tui", Choice: 0,
	}); r.Err != nil {
		t.Fatalf("answer legacy ask: %v", r.Err)
	}
	restarted := NewWorkspace(rt.Store(), w.projectID, "SESSION-stale-ask-restarted")
	restarted.workDir = root
	restarted.AttachRuntime(rt, projectid.ID(rt.ProjectIdentity()))
	defer restarted.Close()
	if err := os.WriteFile(legacyPath, []byte(legacyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Minute)
	_ = os.Chtimes(legacyPath, future, future)
	restarted.observeMarshalProposalFiles(root)
	if restarted.currentAsk() != nil {
		t.Fatalf("legacy file ask re-adopted after restart: %+v", restarted.currentAsk())
	}
}

// TestAskAnswerAckReplaysOnce verifies journal-before-delivery, marking
// answered only after durable receiver admission, idempotent replay by ID
// (including across workspace restart), and refusing a different action on a
// stale already-answered card.
func TestAskAnswerAckReplaysOnce(t *testing.T) {
	w, rt, root := outsideRunWorkspaceFixture(t, "SESSION-ask-ack-replay")
	p := portableAskProposal(t, "Use Postgres or SQLite?", `[{"key":"enter","label":"SQLite"},{"key":"p","label":"Postgres"}]`)
	w.openAsk(p)
	card := w.currentAsk()
	if card == nil {
		t.Fatal("ask card did not open")
	}

	var deliveries atomic.Int32
	var sawJournalBeforeDelivery atomic.Bool
	var failDelivery atomic.Bool
	failDelivery.Store(true)

	ledger := w.askLedger()
	ledger.mu.Lock()
	ledger.deliverHook = func(_ context.Context, rec askRecord, _ string) error {
		deliveries.Add(1)
		// Verify receipt is ALREADY durably journaled in store before admission.
		stored, ok := w.loadAskRecordFromStore(rec.ID)
		if ok && stored.Status == askStatusOpen && stored.RequestReceipt != nil && stored.RequestReceipt.DeliveryState == "journaled" {
			sawJournalBeforeDelivery.Store(true)
		}
		if failDelivery.Load() {
			return errors.New("receiver unavailable")
		}
		return nil
	}
	ledger.mu.Unlock()

	binding := askAnswerBinding{
		Project:  card.Project,
		ID:       card.ID,
		Launch:   card.Launch,
		Revision: card.Revision,
		Actor:    "owner:web",
		Choice:   0,
	}

	// Attempt 1: receiver fails -> journaled before delivery, NOT marked answered.
	r1 := w.answerAskBound(context.Background(), binding)
	if r1.Err == nil || r1.Status != "refused" {
		t.Fatalf("failed delivery should refuse: %+v", r1)
	}
	if !sawJournalBeforeDelivery.Load() {
		t.Fatal("ask answer was not durably journaled before receiver delivery")
	}
	storedAfterFail, ok := w.loadAskRecordFromStore(card.ID)
	if !ok || storedAfterFail.Status != askStatusOpen || storedAfterFail.AnsweredAt != nil || storedAfterFail.RequestReceipt == nil || storedAfterFail.RequestReceipt.DeliveryState != "failed" {
		t.Fatalf("failed delivery mutated status unexpectedly: %+v", storedAfterFail)
	}
	if w.currentAsk() == nil {
		t.Fatal("ask card closed before receiver durably admitted")
	}

	// Attempt 2: receiver admits -> marked answered with AnsweredAt and admitted receipt.
	failDelivery.Store(false)
	r2 := w.answerAskBound(context.Background(), binding)
	if r2.Err != nil || r2.Status != "answered" || r2.Record.Status != askStatusAnswered || r2.Record.AnsweredAt == nil {
		t.Fatalf("admitted delivery failed: %+v", r2)
	}
	if r2.Receipt == nil || r2.Receipt.DeliveryState != "admitted" || r2.Receipt.AdmittedAt == nil {
		t.Fatalf("missing admitted receipt: %+v", r2.Receipt)
	}
	if w.currentAsk() != nil {
		t.Fatal("open ask card was not cleared after admission")
	}
	if deliveries.Load() != 2 {
		t.Fatalf("deliveries = %d, want 2 (1 failed + 1 admitted)", deliveries.Load())
	}

	// Attempt 3: replay same ID/launch/revision/actor/choice -> returns replayed receipt WITHOUT re-delivering.
	r3 := w.answerAskBound(context.Background(), binding)
	if r3.Err != nil || r3.Status != "replayed" || r3.Receipt == nil || r3.Receipt.ReceiptID != r2.Receipt.ReceiptID {
		t.Fatalf("replay by ID failed: %+v", r3)
	}
	if deliveries.Load() != 2 {
		t.Fatalf("replay re-delivered answer: deliveries=%d", deliveries.Load())
	}

	// Attempt 4: different action on already-answered stale card -> refused, no delivery.
	diffAction := binding
	diffAction.Choice = 1
	r4 := w.answerAskBound(context.Background(), diffAction)
	if r4.Err == nil || r4.Status != "stale" {
		t.Fatalf("different action on answered card should be refused: %+v", r4)
	}
	if deliveries.Load() != 2 {
		t.Fatalf("different action triggered delivery: deliveries=%d", deliveries.Load())
	}

	// Attempt 5: replay across workspace restart backed by the same durable store.
	restarted := NewWorkspace(rt.Store(), w.projectID, "SESSION-ask-ack-replay-2")
	restarted.workDir = root
	restarted.AttachRuntime(rt, projectid.ID(rt.ProjectIdentity()))
	defer restarted.Close()
	r5 := restarted.answerAskBound(context.Background(), binding)
	if r5.Err != nil || r5.Status != "replayed" || r5.Receipt == nil || r5.Receipt.ReceiptID != r2.Receipt.ReceiptID {
		t.Fatalf("cross-restart replay failed: %+v", r5)
	}
	r6 := restarted.answerAskBound(context.Background(), diffAction)
	if r6.Err == nil || r6.Status != "stale" {
		t.Fatalf("cross-restart different action should be refused: %+v", r6)
	}
}

// TestOutsideRunEvidenceUsesStateStorage verifies that all out-of-run evidence
// ingestion paths persist redacted timestamped, latest and metadata files
// under state/evidence with run/launch/event and opaque digest, create no
// out-of-run Git commit, preserve reader compatibility for both tmux paths,
// and attribute historical commits only when retained evidence identifies an
// actor (otherwise "unknown").
func TestOutsideRunEvidenceUsesStateStorage(t *testing.T) {
	w, _, root := outsideRunWorkspaceFixture(t, "SESSION-outside-evidence")
	baseHead := gitOutputInDir(t, root, "rev-parse", "HEAD")

	// 1. Out-of-run Git evidence commits are refused at the runtime Git writer boundary.
	if err := refuseOutOfRunEvidenceCommit("default", runtime.GOOS); err == nil {
		t.Fatal("default edition allowed out-of-run evidence commit")
	}
	if err := refuseOutOfRunEvidenceCommit("governed", runtime.GOOS); err == nil {
		t.Fatal("governed edition allowed out-of-run evidence commit")
	}

	// 2. Single runtime writer persists redacted timestamped + latest + meta under state/evidence.
	secret := "sk-proj-abc1234567890SECRETTOKENXYZ987654321"
	receipt, err := w.recordOutsideRunEvidence(root, "chat-capture", "captured line with "+secret+"\n", runtimeEvidenceMeta{
		RunID:    "RUN-OUTSIDE-1",
		LaunchID: "launch-42",
		Event:    "outside-run-capture",
		Source:   "runtime",
		Actor:    "operator",
	})
	if err != nil {
		t.Fatalf("recordOutsideRunEvidence: %v", err)
	}
	if !strings.HasPrefix(receipt.Digest, "sha256:") || len(receipt.Digest) != len("sha256:")+64 {
		t.Fatalf("unexpected digest: %q", receipt.Digest)
	}
	evDir := filepath.Join(root, statepath.DirName, "evidence")
	latestBytes, err := os.ReadFile(filepath.Join(evDir, "chat-capture-latest.txt"))
	if err != nil {
		t.Fatalf("read latest evidence: %v", err)
	}
	if strings.Contains(string(latestBytes), secret) {
		t.Fatalf("secret leaked into latest evidence: %q", string(latestBytes))
	}
	tsBytes, err := os.ReadFile(filepath.Join(evDir, receipt.Timestamped))
	if err != nil {
		t.Fatalf("read timestamped evidence: %v", err)
	}
	if string(tsBytes) != string(latestBytes) {
		t.Fatalf("timestamped and latest evidence differ")
	}
	metaBytes, err := os.ReadFile(filepath.Join(evDir, "chat-capture-latest.meta.json"))
	if err != nil {
		t.Fatalf("read meta evidence: %v", err)
	}
	var decoded runtimeEvidenceReceipt
	if err := json.Unmarshal(metaBytes, &decoded); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if decoded.RunID != "RUN-OUTSIDE-1" || decoded.LaunchID != "launch-42" || decoded.Event != "outside-run-capture" || decoded.Digest != receipt.Digest {
		t.Fatalf("meta mismatch: %+v", decoded)
	}

	// 3. Relay bootstrap caller (tmux_native.go) preserves <id>-relay-latest.txt reader compatibility.
	if _, err := writeRuntimeEvidence(root, "task-compat-relay", "relay line\n", runtimeEvidenceMeta{
		LaunchID: "task-compat",
		Event:    "relay-bootstrap",
		Source:   "tmux-relay",
	}); err != nil {
		t.Fatalf("relay bootstrap write: %v", err)
	}

	// 4. Identity capture caller (tmux_identity.go) reads relay file and writes canonical evidence.
	fakeTmux := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\ncase \"$1\" in display-message) printf '%%12\\n';; capture-pane) printf 'pane output with " + secret + "\\n';; kill-pane) exit 0;; esac\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fakeTmux)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxSession = tmux.SessionName(root)
	agent := &activeTmuxAgent{
		id:          "task-compat",
		role:        "task",
		provider:    "codex",
		paneID:      "%12",
		runID:       "RUN-COMPAT",
		mcpLaunchID: "launch-compat",
	}
	w.tmuxActiveWins[agent.id] = agent
	if err := w.retainAndCloseAgent(context.Background(), agent, root, "completed"); err != nil {
		t.Fatalf("retainAndCloseAgent: %v", err)
	}
	retained, err := os.ReadFile(filepath.Join(evDir, "task-compat-latest.txt"))
	if err != nil {
		t.Fatalf("read retained identity evidence: %v", err)
	}
	if strings.Contains(string(retained), secret) {
		t.Fatalf("secret leaked into retained pane evidence: %q", string(retained))
	}
	if !strings.Contains(string(retained), "Full relay output:\nrelay line") {
		t.Fatalf("identity capture lost relay compatibility: %q", string(retained))
	}
	idMetaRaw, err := os.ReadFile(filepath.Join(evDir, "task-compat-latest.meta.json"))
	if err != nil {
		t.Fatalf("read identity meta: %v", err)
	}
	var idMeta runtimeEvidenceReceipt
	if err := json.Unmarshal(idMetaRaw, &idMeta); err != nil {
		t.Fatal(err)
	}
	if idMeta.RunID != "RUN-COMPAT" || idMeta.LaunchID != "launch-compat" || idMeta.Event != "agent-retain:completed" || !strings.HasPrefix(idMeta.Digest, "sha256:") {
		t.Fatalf("identity meta mismatch: %+v", idMeta)
	}

	// 5. No out-of-run Git commit was created and no project files were modified.
	if gotHead := gitOutputInDir(t, root, "rev-parse", "HEAD"); gotHead != baseHead {
		t.Fatalf("HEAD moved after out-of-run evidence writes: got %s want %s", gotHead, baseHead)
	}

	// 6. Historical Git commit attribution: unknown without evidence, attributed only when evidence identifies actor.
	unknownAttr := attributeHistoricalCommit(context.Background(), root, baseHead, nil, nil)
	if unknownAttr.Identified || unknownAttr.Actor != "unknown" {
		t.Fatalf("unattributed historical commit was guessed as %+v", unknownAttr)
	}
	guardAttr := attributeHistoricalCommit(context.Background(), root, baseHead, []string{"commit=" + baseHead + " actor=marshal:codex"}, nil)
	if !guardAttr.Identified || guardAttr.Actor != "marshal:codex" || guardAttr.Source != "guard" {
		t.Fatalf("guard attribution failed: %+v", guardAttr)
	}
	provAttr := attributeHistoricalCommit(context.Background(), root, baseHead, nil, []string{"commit=" + baseHead + " actor=provider:claude"})
	if !provAttr.Identified || provAttr.Actor != "provider:claude" || provAttr.Source != "provider" {
		t.Fatalf("provider attribution failed: %+v", provAttr)
	}
	// Commit carrying explicit Marshal-Actor trailer is attributed via commit evidence.
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutputInDir(t, root, "add", "note.txt")
	gitOutputInDir(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "Add note\n\nMarshal-Actor: operator:test")
	taggedHead := gitOutputInDir(t, root, "rev-parse", "HEAD")
	commitAttr := attributeHistoricalCommit(context.Background(), root, taggedHead, nil, nil)
	if !commitAttr.Identified || commitAttr.Actor != "operator:test" || commitAttr.Source != "commit" {
		t.Fatalf("commit trailer attribution failed: %+v", commitAttr)
	}
}

// TestReadOnlyMarshalCannotCommitFromChat verifies that a read-only Marshal
// chat cannot commit to Git or mutate Git metadata across supported
// confinement paths (governed permission fixture + verified provider
// confinement + Linux bubblewrap OS sandbox when available).
func TestReadOnlyMarshalCannotCommitFromChat(t *testing.T) {
	w, rt, root := outsideRunWorkspaceFixture(t, "SESSION-readonly-no-commit")
	appFile := filepath.Join(root, "app.go")
	if err := os.WriteFile(appFile, []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutputInDir(t, root, "add", "app.go")
	gitOutputInDir(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	baseHead := gitOutputInDir(t, root, "rev-parse", "HEAD")

	// Path 1: Governed permission fixture — a read-only chat requesting git commit
	// via MarshalToolPermissionKind is refused even when decidePermission receives allow=true.
	tr := newOutsideRunChatTransport("sess-ro-1")
	w.attachNativeChat(tr, root, nil, nil, nil, nil, "", false)
	commitAsk := chattransport.PermissionAsk{
		ID:       "toolu_git_commit",
		Provider: "claude",
		ToolName: "Bash",
		Summary:  "git commit -am 'unapproved chat commit'",
	}
	w.routeChatAsk(commitAsk)
	inboxItems := w.boardInbox()
	if len(inboxItems) != 1 {
		t.Fatalf("expected 1 pending inbox item, got %d", len(inboxItems))
	}
	err := w.decidePermission(context.Background(), inboxItems[0].Request, true, "operator inbox; action=a")
	if err == nil {
		t.Fatal("read-only Marshal chat permission adapter allowed git commit outside a run")
	}
	if d, ok := tr.decisionFor("toolu_git_commit"); !ok || d.Allow {
		t.Fatalf("provider did not receive denial for git commit: %+v (ok=%v)", d, ok)
	}
	events, err := rt.Store().ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundDenial := false
	for _, ev := range events {
		if ev.Type == "PERMISSION_DECIDED" && ev.Data["approval_binding"] == "toolu_git_commit" {
			if ev.Data["allow"] != false {
				t.Fatalf("audit recorded allow=%v for refused git commit", ev.Data["allow"])
			}
			foundDenial = true
		}
	}
	if !foundDenial {
		t.Fatal("missing PERMISSION_DECIDED denial audit for refused git commit")
	}
	// Legitimate non-mutating tool grant remains preserved in both editions.
	readAsk := chattransport.PermissionAsk{
		ID:       "toolu_read_only",
		Provider: "claude",
		ToolName: "Bash",
		Summary:  "git status --short",
	}
	w.routeChatAsk(readAsk)
	var readReq = w.chatAskRequest(readAsk)
	if err := w.decidePermission(context.Background(), readReq, true, "operator inbox; action=a"); err != nil {
		t.Fatalf("explicit operator grant for read-only command was blocked: %v", err)
	}
	if d, ok := tr.decisionFor("toolu_read_only"); !ok || !d.Allow {
		t.Fatalf("operator read-only grant was not delivered: %+v (ok=%v)", d, ok)
	}

	// Path 2: Verified provider confinement path protects worktree and Git metadata.
	bFalse := false
	for _, provider := range []string{"codex", "claude"} {
		args, err := confineMarshalChat(provider, root, marshalKickoffArgs(provider), marshal.RightsReadOnly)
		if err != nil {
			t.Fatalf("confineMarshalChat(%s): %v", provider, err)
		}
		if len(args) <= len(marshalKickoffArgs(provider)) {
			t.Fatalf("%s read-only confinement added no flags: %q", provider, args)
		}
		d := evaluateMarshalConfinement(marshalConfinementRequest{
			Platform:           "darwin",
			OSSandboxAvailable: &bFalse,
			Surface:            surfaceWorkspaceNativeLaunch,
			Provider:           provider,
			Rights:             marshal.RightsReadOnly,
		})
		if !d.Allowed || !d.ProtectsWorktreeAndGit || d.AllowProjectWrite || d.AllowGitWrite {
			t.Fatalf("%s provider confinement decision unsafe: %+v", provider, d)
		}
	}

	// Path 3: Linux OS bubblewrap sandbox path when available.
	if runtime.GOOS == "linux" {
		if spec, err := wrapMarshalReadOnlySandbox(context.Background(), root, []string{"/bin/sh", "-c", "echo hack >> app.go && git commit -am hack"}); err == nil {
			cmd := exec.Command(spec.Path, spec.Args...)
			cmd.Dir = root
			if out, runErr := cmd.CombinedOutput(); runErr == nil {
				t.Fatalf("read-only bwrap sandbox allowed project write/commit: %s", out)
			}
		}
	}

	// Verify repository HEAD and tracked file remain completely untouched.
	if gotHead := gitOutputInDir(t, root, "rev-parse", "HEAD"); gotHead != baseHead {
		t.Fatalf("HEAD changed from %s to %s", baseHead, gotHead)
	}
	gotApp, err := os.ReadFile(appFile)
	if err != nil || string(gotApp) != "package demo\n" {
		t.Fatalf("app.go modified: %q (%v)", gotApp, err)
	}
}

// TestOutOfRunWriterAndLaunchBoundaryMatrix validates the recorded edition and
// platform boundary matrix across all out-of-run surfaces, providers and rights.
func TestOutOfRunWriterAndLaunchBoundaryMatrix(t *testing.T) {
	rows := marshalBoundaryMatrix()
	if len(rows) == 0 {
		t.Fatal("boundary matrix is empty")
	}

	seenEditions := map[string]bool{}
	seenSurfaces := map[marshalBoundarySurface]bool{}
	seenPlatforms := map[string]bool{}
	sawOSBwrap := false
	sawToolOnly := false
	sawGovernedGate := false
	sawProviderVerified := false

	for _, row := range rows {
		seenEditions[row.Edition] = true
		seenSurfaces[row.Surface] = true
		seenPlatforms[row.Platform] = true

		if !row.GitDetectionIndependent {
			t.Fatalf("Git detection not independent on row: %+v", row)
		}
		if row.AllowGitWrite {
			t.Fatalf("out-of-run matrix row allowed Git write: %+v", row)
		}
		if row.Rights != marshal.RightsSmallTasks && row.AllowProjectWrite {
			t.Fatalf("read-only matrix row allowed project write: %+v", row)
		}
		if row.Surface == surfaceRuntimeGitWriter && row.Allowed {
			t.Fatalf("runtime Git writer allowed out-of-run evidence commit: %+v", row)
		}
		if row.Surface == surfaceCheckoutWorktree && row.Allowed {
			t.Fatalf("unauthorized out-of-run checkout/worktree allowed: %+v", row)
		}
		if row.Surface == surfaceFileMCPApply && row.Allowed {
			t.Fatalf("unauthorized out-of-run file/MCP project write allowed: %+v", row)
		}
		if row.Surface == surfacePermissionAdapter && row.Allowed {
			t.Fatalf("read-only permission adapter allowed out-of-run git commit: %+v", row)
		}
		if row.Edition == "governed" && !row.RequiresOperatorGrant {
			t.Fatalf("governed row dropped operator grant requirement: %+v", row)
		}
		switch row.Mechanism {
		case confinementOSBwrap:
			sawOSBwrap = true
		case confinementToolOnlyRead:
			sawToolOnly = true
		case confinementGovernedOperator:
			sawGovernedGate = true
		case confinementProviderVerified:
			sawProviderVerified = true
		}
	}

	for _, ed := range []string{"default", "governed"} {
		if !seenEditions[ed] {
			t.Fatalf("matrix missing edition %q", ed)
		}
	}
	for _, s := range allMarshalBoundarySurfaces {
		if !seenSurfaces[s] {
			t.Fatalf("matrix missing surface %q", s)
		}
	}
	if !seenPlatforms["linux"] || !seenPlatforms["darwin"] {
		t.Fatalf("matrix missing expected platforms: %+v", seenPlatforms)
	}
	if !sawOSBwrap || !sawToolOnly || !sawGovernedGate || !sawProviderVerified {
		t.Fatalf("matrix missing mechanisms: bwrap=%v toolOnly=%v governed=%v provider=%v", sawOSBwrap, sawToolOnly, sawGovernedGate, sawProviderVerified)
	}

	// Small-tasks with explicit scoped runtime worktree authorization is permitted
	// on launch/worktree/file-apply boundaries (and requires operator grant in governed).
	for _, ed := range []string{"default", "governed"} {
		dUnauth := evaluateMarshalConfinement(marshalConfinementRequest{
			Edition:            ed,
			Platform:           "darwin",
			Surface:            surfaceCheckoutWorktree,
			Rights:             marshal.RightsSmallTasks,
			ScopedWorktreeAuth: false,
		})
		if dUnauth.Allowed || !dUnauth.RequiresScopedWorktree {
			t.Fatalf("%s small-tasks without scoped worktree auth was allowed: %+v", ed, dUnauth)
		}
		dAuth := evaluateMarshalConfinement(marshalConfinementRequest{
			Edition:               ed,
			Platform:              "darwin",
			Surface:               surfaceCheckoutWorktree,
			Rights:                marshal.RightsSmallTasks,
			ScopedWorktreeAuth:    true,
			ExplicitOperatorGrant: ed == "governed",
		})
		if !dAuth.Allowed || dAuth.Mechanism != confinementSmallTasksScoped || dAuth.AllowGitWrite {
			t.Fatalf("%s small-tasks with scoped worktree auth failed: %+v", ed, dAuth)
		}
	}
}

// TestUnsupportedConfinementFallsBackToToolOnly verifies that when Linux OS
// isolation is unavailable and a provider has no proved read-only confinement,
// project-bound execution and writes are refused with a scoped tool-only read
// fallback while Git detection remains independent.
func TestUnsupportedConfinementFallsBackToToolOnly(t *testing.T) {
	bFalse := false
	for _, ed := range []string{"default", "governed"} {
		for _, platform := range []string{"linux", "darwin"} {
			for _, provider := range []string{"opencode", "antigravity", "agy", "custom-unproved"} {
				for _, surface := range []marshalBoundarySurface{
					surfaceWorkspaceNativeLaunch,
					surfaceWorkspaceNativeReattach,
					surfaceTmuxExecution,
					surfaceCLIHeadlessStart,
				} {
					d := evaluateMarshalConfinement(marshalConfinementRequest{
						Edition:            ed,
						Platform:           platform,
						OSSandboxAvailable: &bFalse,
						Surface:            surface,
						Provider:           provider,
						Rights:             marshal.RightsReadOnly,
					})
					if !d.ToolOnlyFallback || d.Mechanism != confinementToolOnlyRead {
						t.Fatalf("%s/%s/%s/%s did not fall back to tool-only read: %+v", ed, platform, provider, surface, d)
					}
					if d.AllowProjectExec || d.AllowProjectWrite || d.AllowGitWrite {
						t.Fatalf("%s/%s/%s/%s permitted project exec/write under unsupported confinement: %+v", ed, platform, provider, surface, d)
					}
					if !d.GitDetectionIndependent {
						t.Fatalf("%s/%s/%s/%s lost independent Git detection", ed, platform, provider, surface)
					}
					env := marshalConfinementEnv(d)
					joined := strings.Join(env, "\n")
					for _, want := range []string{
						"MARSHAL_CONFINEMENT_MODE=tool-only-read",
						"MARSHAL_ALLOW_PROJECT_EXEC=0",
						"MARSHAL_ALLOW_PROJECT_WRITE=0",
						"MARSHAL_ALLOW_GIT_WRITE=0",
					} {
						if !strings.Contains(joined, want) {
							t.Fatalf("confinement env missing %q: %q", want, env)
						}
					}
				}
			}
		}
	}

	// Workspace launch confinement records the tool-only fallback while the
	// plan-only guard keeps detecting working-tree changes independently.
	w, _, root := outsideRunWorkspaceFixture(t, "SESSION-tool-only-fallback")
	w.noteMarshalWrites("opencode", marshal.RightsReadOnly)
	if runtime.GOOS != "linux" {
		d := w.enforceMarshalLaunchConfinement(context.Background(), surfaceTmuxExecution, "opencode", root, marshal.RightsReadOnly)
		if !d.ToolOnlyFallback || d.Mechanism != confinementToolOnlyRead {
			t.Fatalf("workspace launch did not record tool-only fallback: %+v", d)
		}
	}
	files := map[string]guardEntry{}
	w.guardSource = func(context.Context) (guardSnapshot, error) {
		snap := map[string]guardEntry{}
		for k, v := range files {
			snap[k] = v
		}
		return guardSnapshot{top: root, files: snap}, nil
	}
	var told []string
	w.guardTell = func(text string) { told = append(told, text) }
	w.guardTick(context.Background(), true)
	files["unexpected.go"] = guardEntry{status: "??", stamp: "1:1"}
	w.guardTick(context.Background(), true)
	alert, _, _, _ := w.guardPaint()
	if alert == nil || len(alert.paths) != 1 || alert.paths[0] != "unexpected.go" || len(told) != 1 {
		t.Fatalf("independent Git detection failed under tool-only fallback: alert=%+v told=%q", alert, told)
	}
}

func TestAskBrowserBindingChangesForIdenticalQuestions(t *testing.T) {
	w, _, root := outsideRunWorkspaceFixture(t, "SESSION-browser-ask")
	tr := newOutsideRunChatTransport("browser-session")
	w.attachNativeChat(tr, root, nil, nil, nil, nil, "", false)
	p := portableAskProposal(t, "Continue?", `[{"key":"enter","label":"Continue"},{"key":"s","label":"Stop"}]`)
	w.openAsk(p)
	first := webboard.CardID(webboard.FromBoard(w.webBoardFrame()))
	w.openAsk(p)
	second := webboard.CardID(webboard.FromBoard(w.webBoardFrame()))
	if first == second {
		t.Fatal("identical successive questions share a browser action binding")
	}
	result := w.webAct(namedTestAction(w, webboard.Action{Kind: "key", Card: first, Key: "⏎"}))
	if result.res.Status != "stale" || len(tr.sentTurns()) != 0 {
		t.Fatalf("old browser action delivered: %+v", result)
	}
	result = w.webAct(namedTestAction(w, webboard.Action{Kind: "key", Card: second, Key: "⏎"}))
	if result.res.Status != "done" || len(tr.sentTurns()) != 1 {
		t.Fatalf("current browser action failed: %+v", result)
	}
}

func TestEvidenceMetadataRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, statepath.DirName, "evidence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "capture-latest.meta.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRuntimeEvidence(root, "capture", "output", runtimeEvidenceMeta{}); err == nil {
		t.Fatal("metadata link accepted")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("link target changed: %q %v", got, err)
	}
}

func TestAskConcurrentReplayDeliversOnce(t *testing.T) {
	w, _, _ := outsideRunWorkspaceFixture(t, "SESSION-concurrent-answer")
	w.openAsk(portableAskProposal(t, "Continue?", `[{"key":"enter","label":"Continue"},{"key":"s","label":"Stop"}]`))
	c := w.currentAsk()
	var delivered atomic.Int32
	l := w.askLedger()
	l.deliverHook = func(context.Context, askRecord, string) error { delivered.Add(1); return nil }
	b := askAnswerBinding{Project: c.Project, ID: c.ID, Launch: c.Launch, Revision: c.Revision, Actor: "owner:tui"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := w.answerAskBound(context.Background(), b); r.Err != nil {
				t.Errorf("answer: %v", r.Err)
			}
		}()
	}
	wg.Wait()
	if delivered.Load() != 1 {
		t.Fatalf("delivered %d times", delivered.Load())
	}
}

func TestAskAdmittedReceiptSurvivesInterruptedFinalization(t *testing.T) {
	w, rt, root := outsideRunWorkspaceFixture(t, "SESSION-admitted-answer")
	w.openAsk(portableAskProposal(t, "Continue?", `[{"key":"enter","label":"Continue"},{"key":"s","label":"Stop"}]`))
	c := w.currentAsk()
	l := w.askLedger()
	l.deliverHook = func(context.Context, askRecord, string) error { return nil }
	b := askAnswerBinding{Project: c.Project, ID: c.ID, Launch: c.Launch, Revision: c.Revision, Actor: "owner:tui"}
	result := w.answerAskBound(context.Background(), b)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	// Retain receiver admission but simulate interruption before final card state.
	rec := result.Record
	rec.Status = askStatusOpen
	rec.AnsweredAt = nil
	rec.RequestReceipt.DeliveryState = "journaled"
	rec.RequestReceipt.AdmittedAt = nil
	if err := w.persistAskRecordLocked(&rec); err != nil {
		t.Fatal(err)
	}
	restarted := NewWorkspace(rt.Store(), w.projectID, "SESSION-admitted-answer-restart")
	restarted.workDir = root
	defer restarted.Close()
	replay := restarted.answerAskBound(context.Background(), b)
	if replay.Err != nil || replay.Status != "replayed" || replay.Record.Status != askStatusAnswered {
		t.Fatalf("admitted answer was not recovered: %+v", replay)
	}
}

func TestUnsupportedProviderCommandDoesNotExecute(t *testing.T) {
	if _, available := probeMarshalOSSandbox(context.Background()); available {
		t.Skip("OS confinement is available")
	}
	if command, err := confineMarshalCommand(context.Background(), "unproved", t.TempDir(), marshal.RightsReadOnly, []string{"sh", "-c", "exit 0"}); err == nil || command != nil {
		t.Fatalf("unconfined command admitted: %q %v", command, err)
	}
}

func TestReadOnlyLaunchRejectsConflictingOverrides(t *testing.T) {
	for _, args := range [][]string{{"--tools", "Bash"}, {"--tools=default"}, {"--restricted", "--tools", "Read,Glob,Grep", "Bash"}} {
		if _, err := confineMarshalChat("claude", t.TempDir(), args, marshal.RightsReadOnly); err == nil {
			t.Fatalf("unsafe built-in tools accepted: %q", args)
		}
	}
	for _, args := range [][]string{{"--yolo"}, {"-c", `default_permissions="unrestricted"`}} {
		if _, err := confineMarshalChat("codex", t.TempDir(), args, marshal.RightsReadOnly); err == nil {
			t.Fatalf("unsafe sandbox override accepted: %q", args)
		}
	}
}

func TestAskTmuxDoesNotAcknowledgeAnUndeliveredAnswer(t *testing.T) {
	w, _, _ := outsideRunWorkspaceFixture(t, "SESSION-tmux-answer")
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'Working on the current turn...\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(binary)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{paneID: "%12", provider: "codex", mcpLaunchID: "question-launch"}
	w.openAsk(portableAskProposal(t, "Continue?", `[{"key":"enter","label":"Continue"},{"key":"s","label":"Stop"}]`))
	c := w.currentAsk()
	r := w.answerAskBound(context.Background(), askAnswerBinding{Project: c.Project, ID: c.ID, Launch: c.Launch, Revision: c.Revision, Actor: "owner:tui"})
	if r.Err == nil || r.Record.Status != askStatusOpen || w.currentAsk() == nil {
		t.Fatalf("undelivered answer acknowledged: %+v", r)
	}
	if len(w.say.pending) != 0 {
		t.Fatal("answer was acknowledged through a volatile queue")
	}
	if _, found, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerAskAdmissionPrefix+r.Receipt.ReceiptID); err != nil || found {
		t.Fatalf("undelivered answer has an admission receipt: found=%v err=%v", found, err)
	}
}

func TestAskNativeReattachCannotReuseConversationBinding(t *testing.T) {
	w, _, root := outsideRunWorkspaceFixture(t, "SESSION-reattach-binding")
	first := newOutsideRunChatTransport("same-conversation")
	w.attachNativeChat(first, root, nil, nil, nil, nil, "", false)
	w.openAsk(portableAskProposal(t, "Continue?", `[{"key":"enter","label":"Continue"},{"key":"s","label":"Stop"}]`))
	c := w.currentAsk()
	w.chat.mu.Lock()
	cancel := w.chat.cancel
	w.chat.mu.Unlock()
	cancel()
	_ = first.Close()
	second := newOutsideRunChatTransport("same-conversation")
	w.attachNativeChat(second, root, nil, nil, nil, nil, "", false)
	if w.currentAskLaunchID(marshalProposal{}) == c.Launch {
		t.Fatal("reattachment reused the conversation's old launch binding")
	}
	r := w.answerAskBound(context.Background(), askAnswerBinding{Project: c.Project, ID: c.ID, Launch: c.Launch, Revision: c.Revision, Actor: "owner:tui"})
	if r.Err == nil || len(second.sentTurns()) != 0 {
		t.Fatalf("old question answered a new launch: %+v", r)
	}
}
