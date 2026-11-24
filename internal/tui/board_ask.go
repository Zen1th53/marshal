package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/chat"
)

// Ask cards: a question the Marshal asks the owner, with one key or one
// click per answer. Each ask is persisted durably with its project, ID,
// launch, revision and lifecycle status (open, answered or superseded).
// Answers require the exact current ID, launch, revision and an authorized
// owner actor. Answers are journaled before delivery, marked answered only
// after the assigned receiver durably admits them, replay idempotently by ID
// for the same action, and refuse different actions on stale or superseded
// cards.

// askStatus is the persisted lifecycle status of an ask record.
type askStatus string

const (
	askStatusOpen       askStatus = "open"
	askStatusAnswered   askStatus = "answered"
	askStatusSuperseded askStatus = "superseded"
)

const (
	markerAskRecordPrefix    = "card:ask:record:"
	markerAskCurrentKey      = "card:ask:current"
	markerAskLegacyPrefix    = "card:ask:legacy:"
	markerAskAdmissionPrefix = "card:ask:admission:"
)

// askChoice is one answer the owner can pick: Key is "enter" for the first
// (recommended) choice, otherwise one lowercase ASCII letter.
type askChoice struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// askRequestReceipt records the journaled and admitted answer delivery for an
// ask so replays by ID return the original receipt without re-delivering.
type askRequestReceipt struct {
	ReceiptID     string     `json:"receiptId"`
	Action        string     `json:"action"`
	ChoiceIndex   int        `json:"choiceIndex"`
	Label         string     `json:"label"`
	Actor         string     `json:"actor"`
	Receiver      string     `json:"receiver"`
	JournaledAt   time.Time  `json:"journaledAt"`
	AdmittedAt    *time.Time `json:"admittedAt,omitempty"`
	DeliveryState string     `json:"deliveryState"` // "journaled", "admitted", "failed"
}

// askRecord is the durable representation of one ask question and its answer
// receipt.
type askRecord struct {
	Project        string             `json:"project"`
	ID             string             `json:"id"`
	Launch         string             `json:"launch"`
	Revision       int64              `json:"revision"`
	Status         askStatus          `json:"status"`
	Question       string             `json:"question"`
	Choices        []askChoice        `json:"choices"`
	Digest         string             `json:"digest"`
	OpenedAt       time.Time          `json:"openedAt"`
	AnsweredAt     *time.Time         `json:"answeredAt,omitempty"`
	RequestReceipt *askRequestReceipt `json:"requestReceipt,omitempty"`
}

type askCurrentPointer struct {
	Project  string `json:"project"`
	ID       string `json:"id"`
	Launch   string `json:"launch"`
	Revision int64  `json:"revision"`
}

// askAnswerBinding carries the exact identity, launch, revision and actor
// required to answer an ask card.
type askAnswerBinding struct {
	Project  string
	ID       string
	Launch   string
	Revision int64
	Actor    string
	Choice   int
	Key      string
}

// askAnswerResult describes the outcome of answerAskBound.
type askAnswerResult struct {
	Status  string // "answered", "replayed", "refused", "stale"
	Record  askRecord
	Receipt *askRequestReceipt
	Err     error
}

// askLedgerState holds per-workspace durable ask state and delivery seams.
type askLedgerState struct {
	answerMu       sync.Mutex
	mu             sync.Mutex
	rev            int64
	currentID      string
	records        map[string]*askRecord
	legacyAdopted  map[string]string
	loaded         bool
	deliverHook    func(ctx context.Context, rec askRecord, label string) error
	launchOverride string
}

// askCard is the question the Marshal is waiting on, with its choices.
type askCard struct {
	Project        string
	ID             string
	Launch         string
	Revision       int64
	Status         askStatus
	Question       string
	Choices        []askChoice
	At             time.Time
	AnsweredAt     *time.Time
	RequestReceipt *askRequestReceipt
	// digest identifies this question and its choices so an already-answered
	// ask proposal is not shown again after a control-centre restart.
	digest string
	// guard is the changed-files alert this question decides, nil when it
	// is about something else (askAboutGuard): answering one resolves or
	// updates the other.
	guard *guardAlert
}

func (w *Workspace) askLedger() *askLedgerState {
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	if w.permissions.askLedger == nil {
		w.permissions.askLedger = &askLedgerState{
			records:       map[string]*askRecord{},
			legacyAdopted: map[string]string{},
		}
	}
	return w.permissions.askLedger
}

func (w *Workspace) askProjectID() string {
	if w.projectID != "" {
		return w.projectID
	}
	return w.providerRoot()
}

func (w *Workspace) currentAskLaunchID(p marshalProposal) string {
	ledger := w.askLedger()
	ledger.mu.Lock()
	override := ledger.launchOverride
	ledger.mu.Unlock()
	if override != "" {
		return override
	}
	w.chat.mu.Lock()
	tr := w.chat.tr
	chatProvider := w.chat.provider
	chatLaunch := w.chat.launch
	w.chat.mu.Unlock()
	if tr != nil {
		sid := chatLaunch
		if sid == "" {
			sid = strings.TrimSpace(tr.SessionID())
		}
		if sid == "" {
			sid = "active"
		}
		if chatProvider == "" {
			chatProvider = "native"
		}
		return "native:" + canonicalNeutralProvider(chatProvider) + ":" + sid
	}
	w.tmuxMu.Lock()
	chatAgent := w.tmuxActiveWins["marshal-chat"]
	var mcpLaunch, paneID, sessionID string
	if chatAgent != nil {
		mcpLaunch = chatAgent.mcpLaunchID
		paneID = chatAgent.paneID
		sessionID = chatAgent.sessionID
	}
	w.tmuxMu.Unlock()
	if mcpLaunch != "" {
		return "mcp:" + mcpLaunch
	}
	if paneID != "" {
		if sessionID != "" {
			return "tmux:" + paneID + ":" + sessionID
		}
		return "tmux:" + paneID
	}
	if p.sourceSession != "" {
		return "session:" + p.sourceSession
	}
	return "workspace:" + w.workspaceInstanceID()
}

func cloneAskRecord(rec *askRecord) askRecord {
	if rec == nil {
		return askRecord{}
	}
	cp := *rec
	cp.Choices = append([]askChoice(nil), rec.Choices...)
	if rec.AnsweredAt != nil {
		t := *rec.AnsweredAt
		cp.AnsweredAt = &t
	}
	if rec.RequestReceipt != nil {
		r := *rec.RequestReceipt
		if r.AdmittedAt != nil {
			t := *r.AdmittedAt
			r.AdmittedAt = &t
		}
		cp.RequestReceipt = &r
	}
	return cp
}

func (w *Workspace) persistAskRecordLocked(rec *askRecord) error {
	if rec == nil || w.store == nil || w.projectID == "" {
		return nil
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return w.store.RecordMarshalProposalMarker(context.Background(), w.projectID, markerAskRecordPrefix+rec.ID, string(data), string(rec.Status))
}

func (w *Workspace) persistAskCurrentLocked(ptr askCurrentPointer) error {
	if w.store == nil || w.projectID == "" {
		return nil
	}
	data, err := json.Marshal(ptr)
	if err != nil {
		return err
	}
	return w.store.RecordMarshalProposalMarker(context.Background(), w.projectID, markerAskCurrentKey, string(data), "current")
}

func (w *Workspace) loadAskRecordFromStore(id string) (*askRecord, bool) {
	if id == "" || w.store == nil || w.projectID == "" {
		return nil, false
	}
	raw, ok, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerAskRecordPrefix+id)
	if err != nil || !ok || strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var rec askRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, false
	}
	return &rec, true
}

func (w *Workspace) ensureAskLedgerLoadedLocked(ledger *askLedgerState) {
	if ledger.loaded {
		return
	}
	ledger.loaded = true
	if w.store == nil || w.projectID == "" {
		return
	}
	raw, ok, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerAskCurrentKey)
	if err != nil || !ok || strings.TrimSpace(raw) == "" {
		return
	}
	var ptr askCurrentPointer
	if json.Unmarshal([]byte(raw), &ptr) != nil {
		return
	}
	if ptr.Revision > ledger.rev {
		ledger.rev = ptr.Revision
	}
	if ptr.ID != "" {
		if rec, found := w.loadAskRecordFromStore(ptr.ID); found {
			ledger.records[rec.ID] = rec
			if rec.Revision > ledger.rev {
				ledger.rev = rec.Revision
			}
			if rec.Status == askStatusOpen {
				ledger.currentID = rec.ID
			}
		}
	}
}

// askRecordByID returns a copy of the persisted ask record for id, loading
// from durable storage if needed.
func (w *Workspace) askRecordByID(id string) (askRecord, bool) {
	ledger := w.askLedger()
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	w.ensureAskLedgerLoadedLocked(ledger)
	if rec, ok := ledger.records[id]; ok {
		return cloneAskRecord(rec), true
	}
	if rec, ok := w.loadAskRecordFromStore(id); ok {
		ledger.records[rec.ID] = rec
		return cloneAskRecord(rec), true
	}
	return askRecord{}, false
}

// recoverOpenAsk restores an open persisted ask card after a workspace restart.
func (w *Workspace) recoverOpenAsk() *askCard {
	ledger := w.askLedger()
	ledger.mu.Lock()
	w.ensureAskLedgerLoadedLocked(ledger)
	var rec *askRecord
	if ledger.currentID != "" {
		if cur, ok := ledger.records[ledger.currentID]; ok && cur.Status == askStatusOpen {
			cp := cloneAskRecord(cur)
			rec = &cp
		}
	}
	ledger.mu.Unlock()
	if rec == nil {
		return nil
	}
	card := askCardFromRecord(*rec, nil)
	w.ask.mu.Lock()
	if w.ask.card == nil {
		w.ask.card = card
	} else {
		card = w.ask.card
	}
	w.ask.mu.Unlock()
	return card
}

func askCardFromRecord(rec askRecord, guard *guardAlert) *askCard {
	cp := cloneAskRecord(&rec)
	return &askCard{
		Project:        cp.Project,
		ID:             cp.ID,
		Launch:         cp.Launch,
		Revision:       cp.Revision,
		Status:         cp.Status,
		Question:       cp.Question,
		Choices:        cp.Choices,
		At:             cp.OpenedAt,
		AnsweredAt:     cp.AnsweredAt,
		RequestReceipt: cp.RequestReceipt,
		digest:         cp.Digest,
		guard:          guard,
	}
}

// legacyFileAskID derives a stable identifier for a legacy proposal file ask
// independent of file modification timestamps.
func legacyFileAskID(name, question, choicesRaw string) string {
	digest := strings.TrimSpace(question) + "\x00" + strings.TrimSpace(choicesRaw)
	if choices, err := parseAskChoices(choicesRaw); err == nil {
		digest = askCardDigest(strings.TrimSpace(question), choices)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(name) + "\x00" + digest))
	return "legacy-ask:" + hex.EncodeToString(sum[:12])
}

// adoptLegacyFileAsk adopts a legacy file ask at most once by its stable ID.
// It returns true only on the first adoption for this project.
func (w *Workspace) adoptLegacyFileAsk(stableID string) bool {
	if strings.TrimSpace(stableID) == "" {
		return false
	}
	ledger := w.askLedger()
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if _, exists := ledger.legacyAdopted[stableID]; exists {
		return false
	}
	if w.store != nil && w.projectID != "" {
		if _, ok, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerAskLegacyPrefix+stableID); err == nil && ok {
			ledger.legacyAdopted[stableID] = stableID
			return false
		}
		if err := w.store.RecordMarshalProposalMarker(context.Background(), w.projectID, markerAskLegacyPrefix+stableID, stableID, "adopted"); err != nil {
			return false
		}
	}
	ledger.legacyAdopted[stableID] = stableID
	return true
}

// askCardDigest hashes the normalized question and choices of an ask card.
func askCardDigest(question string, choices []askChoice) string {
	var b strings.Builder
	b.WriteString(strings.Join(strings.Fields(question), " "))
	for _, c := range choices {
		b.WriteByte('\x00')
		b.WriteString(c.Key)
		b.WriteByte('=')
		b.WriteString(strings.TrimSpace(c.Label))
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
}

// errAskChoices is the one error parseAskChoices returns: the proposal is
// refused as invalid, like any other malformed Marshal proposal.
var errAskChoices = errors.New("invalid ask choices")

// parseAskChoices reads the proposal's choices array: 2 to 4 objects with
// exactly the string keys "key" and "label". The first key is "enter"; every
// other key is one lowercase ASCII letter, unique and not "q". Each label is
// trimmed non-empty, at most 40 bytes, and unique across the choices.
func parseAskChoices(s string) ([]askChoice, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var raw []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	if err := dec.Decode(&raw); err != nil {
		return nil, errAskChoices
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errAskChoices
	}
	if len(raw) < 2 || len(raw) > 4 {
		return nil, errAskChoices
	}
	keys := map[string]bool{}
	labels := map[string]bool{}
	out := make([]askChoice, 0, len(raw))
	for i, c := range raw {
		if i == 0 {
			if c.Key != "enter" {
				return nil, errAskChoices
			}
		} else {
			if len(c.Key) != 1 || c.Key[0] < 'a' || c.Key[0] > 'z' || c.Key == "q" {
				return nil, errAskChoices
			}
		}
		if keys[c.Key] {
			return nil, errAskChoices
		}
		keys[c.Key] = true
		if strings.TrimSpace(c.Label) == "" || len(c.Label) > 40 {
			return nil, errAskChoices
		}
		if labels[c.Label] {
			return nil, errAskChoices
		}
		labels[c.Label] = true
		out = append(out, askChoice{Key: c.Key, Label: c.Label})
	}
	return out, nil
}

// openAsk shows the ask card, superseding any earlier open card: one open
// question at a time. It persists the ask record with project, ID, launch,
// revision and status before showing the card, records the question in
// Activity and resolves the stored proposal as applied. No popup.
func (w *Workspace) openAsk(p marshalProposal) {
	choices, err := parseAskChoices(p.choices)
	if err != nil {
		return
	}
	question := strings.TrimSpace(p.question)
	digest := askCardDigest(question, choices)
	launch := w.currentAskLaunchID(p)
	project := w.askProjectID()
	now := time.Now().UTC()

	ledger := w.askLedger()
	ledger.answerMu.Lock()
	defer ledger.answerMu.Unlock()
	ledger.mu.Lock()
	w.ensureAskLedgerLoadedLocked(ledger)

	id := strings.TrimSpace(p.occurrence)
	if id == "" {
		id = strings.TrimSpace(p.id)
	}
	if id == "" {
		id = fmt.Sprintf("ask:%s:%d", digest[:12], ledger.rev+1)
	}
	if existing, ok := ledger.records[id]; !ok {
		if loaded, found := w.loadAskRecordFromStore(id); found {
			ledger.records[id] = loaded
			existing = loaded
			ok = true
		}
		if ok && existing.Status != askStatusOpen {
			ledger.mu.Unlock()
			return
		}
	} else if existing.Status != askStatusOpen {
		ledger.mu.Unlock()
		return
	}

	if existing := ledger.records[id]; existing != nil {
		if existing.Launch != launch || existing.Digest != digest {
			ledger.mu.Unlock()
			return
		}
		card := askCardFromRecord(cloneAskRecord(existing), nil)
		ledger.mu.Unlock()
		w.ask.mu.Lock()
		w.ask.card = card
		w.ask.mu.Unlock()
		w.requestRepaint()
		return
	}

	if ledger.currentID != "" && ledger.currentID != id {
		if prev, ok := ledger.records[ledger.currentID]; ok && prev.Status == askStatusOpen {
			prev.Status = askStatusSuperseded
			if err := w.persistAskRecordLocked(prev); err != nil {
				ledger.mu.Unlock()
				return
			}
		}
	}

	ledger.rev++
	rec := &askRecord{
		Project:  project,
		ID:       id,
		Launch:   launch,
		Revision: ledger.rev,
		Status:   askStatusOpen,
		Question: question,
		Choices:  choices,
		Digest:   digest,
		OpenedAt: now,
	}
	ledger.records[id] = rec
	ledger.currentID = id
	if err := w.persistAskRecordLocked(rec); err != nil {
		ledger.mu.Unlock()
		return
	}
	if err := w.persistAskCurrentLocked(askCurrentPointer{
		Project:  rec.Project,
		ID:       rec.ID,
		Launch:   rec.Launch,
		Revision: rec.Revision,
	}); err != nil {
		ledger.mu.Unlock()
		return
	}
	recCopy := cloneAskRecord(rec)
	ledger.mu.Unlock()

	var guard *guardAlert
	if a := w.guardAlertNow(); a != nil && askAboutGuard(recCopy.Question, choices) {
		guard = a
	}
	card := askCardFromRecord(recCopy, guard)
	w.ask.mu.Lock()
	w.ask.card = card
	w.ask.mu.Unlock()
	w.RecordActivity("Marshal asks: " + p.question)
	if w.store != nil && p.occurrence != "" {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
	}
	w.recordProposalCardLatest("ask")
	w.requestRepaint()
}

// currentAsk is the open card, nil for none.
func (w *Workspace) currentAsk() *askCard {
	w.ask.mu.Lock()
	defer w.ask.mu.Unlock()
	return w.ask.card
}

// clearAsk closes the open card and marks its persisted record superseded if
// it was still open.
func (w *Workspace) clearAsk() {
	serial := w.askLedger()
	serial.answerMu.Lock()
	defer serial.answerMu.Unlock()
	w.ask.mu.Lock()
	prev := w.ask.card
	w.ask.card = nil
	w.ask.mu.Unlock()
	if prev != nil && prev.ID != "" {
		ledger := w.askLedger()
		ledger.mu.Lock()
		if rec, ok := ledger.records[prev.ID]; ok && rec.Status == askStatusOpen {
			rec.Status = askStatusSuperseded
			_ = w.persistAskRecordLocked(rec)
			if ledger.currentID == rec.ID {
				ledger.currentID = ""
			}
		}
		ledger.mu.Unlock()
	}
	w.requestRepaint()
}

// handleAskKey is the ask card's keys on the Board, taken before the gate
// keys while a card is open and no body view or inspector covers the Board:
// Enter picks the first (recommended) choice, each letter picks its choice.
// Every other key, the digits and q, ?, Space and Ctrl+X among them, falls
// through to keep its meaning.
func (w *Workspace) handleAskKey(event KeyEvent) bool {
	card := w.currentAsk()
	if card == nil || w.anyBodyView() {
		return false
	}
	if event.Type == KeyEnter {
		if w.boardStopWaiting() {
			// A stopped run leads the inbox: Enter is its key, never a
			// card's below it.
			return false
		}
		w.answerAsk(0)
		return true
	}
	if event.Type != KeyRune {
		return false
	}
	for i, c := range card.Choices {
		if i == 0 {
			continue
		}
		if len(c.Key) == 1 && rune(c.Key[0]) == event.Rune {
			w.answerAsk(i)
			return true
		}
	}
	return false
}

func isAuthorizedAskActor(actor string) bool {
	a := strings.ToLower(strings.TrimSpace(actor))
	if a == "" {
		return false
	}
	switch a {
	case "owner", "operator", "owner:tui", "owner:web", "operator:tui", "operator:web":
		return true
	}
	if strings.HasPrefix(a, "owner:") || strings.HasPrefix(a, "operator:") {
		return strings.TrimSpace(a[strings.IndexByte(a, ':')+1:]) != ""
	}
	return false
}

func askReceiptID(id, launch string, rev int64, choice int, actor string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", id, launch, rev, choice, actor)))
	return "ask-rcpt-" + hex.EncodeToString(sum[:12])
}

func (w *Workspace) assignedAskReceiver(expectedLaunch string) (string, bool) {
	ledger := w.askLedger()
	ledger.mu.Lock()
	hasHook := ledger.deliverHook != nil
	override := ledger.launchOverride
	ledger.mu.Unlock()
	if override != "" && expectedLaunch != override {
		return "", false
	}
	if hasHook {
		return "hook:" + expectedLaunch, true
	}
	if w.nativeChatShown() {
		w.chat.mu.Lock()
		tr := w.chat.tr
		provider := w.chat.provider
		w.chat.mu.Unlock()
		if tr != nil {
			liveLaunch := w.currentAskLaunchID(marshalProposal{})
			if expectedLaunch != liveLaunch {
				return "", false
			}
			return "native-chat:" + canonicalNeutralProvider(provider), true
		}
	}
	if pane := w.sayPane(); pane != "" {
		liveLaunch := w.currentAskLaunchID(marshalProposal{})
		if expectedLaunch != liveLaunch {
			return "", false
		}
		return "tmux-chat:" + pane, true
	}
	return "", false
}

// A provider pipe can accept bytes before its acknowledgement is retained.
// Such an interrupted delivery is uncertain and must never be blindly resent.
var errAskAdmissionUncertain = errors.New("answer admission is uncertain")

func (w *Workspace) persistAskAdmission(rec askRecord) error {
	if w.store == nil || w.projectID == "" {
		return nil
	}
	if rec.RequestReceipt == nil {
		return errors.New("missing answer receipt")
	}
	now := time.Now().UTC()
	rec.RequestReceipt.AdmittedAt = &now
	rec.RequestReceipt.DeliveryState = "admitted"
	data, err := json.Marshal(rec.RequestReceipt)
	if err != nil {
		return err
	}
	return w.store.RecordMarshalProposalMarker(context.Background(), w.projectID, markerAskAdmissionPrefix+rec.RequestReceipt.ReceiptID, string(data), "admitted")
}

// deliverAskAnswer delivers the answer to the assigned receiver and returns
// nil only after the receiver durably admits the turn.
func (w *Workspace) deliverAskAnswer(ctx context.Context, rec askRecord, label string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ledger := w.askLedger()
	ledger.mu.Lock()
	hook := ledger.deliverHook
	ledger.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, rec, label); err != nil {
			return err
		}
		return w.persistAskAdmission(rec)
	}
	w.chat.mu.Lock()
	tr := w.chat.tr
	now := time.Now()
	if tr != nil {
		w.chat.view.ScrollEnd()
		w.chat.view.Append(chat.Entry{Role: chat.You, At: now, Text: label})
		w.chat.view.SetThinking(true)
	}
	w.chat.mu.Unlock()
	if tr != nil {
		w.markMarshalAnswered()
		w.say.mu.Lock()
		w.say.next++
		echoID := w.say.next
		w.say.mu.Unlock()
		if err := tr.Send(ctx, chattransport.Turn{Text: label}); err != nil {
			w.sayNote(sayEcho{id: echoID, At: now, Text: label, State: sayFailed, Why: err.Error()})
			w.memoryAnswer("The Marshal chat did not take your message: "+err.Error(), true)
			return fmt.Errorf("%w: %v", errAskAdmissionUncertain, err)
		}
		if err := w.persistAskAdmission(rec); err != nil {
			return fmt.Errorf("%w: %v", errAskAdmissionUncertain, err)
		}
		w.sayNote(sayEcho{id: echoID, At: now, Text: label, State: saySent})
		return nil
	}
	if pane := w.sayPane(); pane != "" {
		// Prompt delivery must succeed before a durable acknowledgement. An
		// in-memory chat queue alone cannot acknowledge an answer.
		if _, err := w.typeIntoChat(ctx, pane, label); err != nil {
			if errors.Is(err, errChatBusy) || errors.Is(err, errChatNotReady) {
				return err
			}
			return fmt.Errorf("%w: %v", errAskAdmissionUncertain, err)
		}
		if err := w.persistAskAdmission(rec); err != nil {
			return fmt.Errorf("%w: %v", errAskAdmissionUncertain, err)
		}
		w.markMarshalAnswered()
		return nil
	}
	return errors.New("no Marshal chat is open")
}

// answerAskBound answers an ask card with strict project, ID, launch,
// revision and actor binding. It journals the request receipt before delivery,
// marks the ask answered only after the assigned receiver durably admits it,
// replays idempotently by ID for the exact same action, and refuses different
// actions on stale or superseded cards.
func (w *Workspace) answerAskBound(ctx context.Context, req askAnswerBinding) askAnswerResult {
	serial := w.askLedger()
	serial.answerMu.Lock()
	defer serial.answerMu.Unlock()
	if !isAuthorizedAskActor(req.Actor) {
		return askAnswerResult{
			Status: "refused",
			Err:    errors.New("ask answer requires an authorized owner actor"),
		}
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Launch) == "" || req.Revision <= 0 {
		return askAnswerResult{
			Status: "stale",
			Err:    errors.New("ask answer requires exact current ID, launch and revision"),
		}
	}
	if req.Project != w.askProjectID() {
		return askAnswerResult{
			Status: "stale",
			Err:    errors.New("ask belongs to a different project"),
		}
	}

	ledger := w.askLedger()
	ledger.mu.Lock()
	w.ensureAskLedgerLoadedLocked(ledger)
	rec, ok := ledger.records[req.ID]
	if !ok {
		if loaded, found := w.loadAskRecordFromStore(req.ID); found {
			ledger.records[loaded.ID] = loaded
			rec, ok = loaded, true
		}
	}
	if !ok {
		ledger.mu.Unlock()
		return askAnswerResult{
			Status: "stale",
			Err:    errors.New("ask is unknown or no longer open"),
		}
	}
	if rec.Launch != req.Launch || rec.Revision != req.Revision {
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{
			Status: "stale",
			Record: cp,
			Err:    errors.New("ask launch or revision does not match the current question"),
		}
	}

	choiceIdx := req.Choice
	if req.Key != "" {
		matched := -1
		for i, c := range rec.Choices {
			if c.Key == req.Key {
				matched = i
				break
			}
		}
		if matched < 0 {
			cp := cloneAskRecord(rec)
			ledger.mu.Unlock()
			return askAnswerResult{
				Status: "refused",
				Record: cp,
				Err:    errors.New("ask choice key does not match this question"),
			}
		}
		choiceIdx = matched
	}
	if choiceIdx < 0 || choiceIdx >= len(rec.Choices) {
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{
			Status: "refused",
			Record: cp,
			Err:    errors.New("ask choice out of range"),
		}
	}

	switch rec.Status {
	case askStatusSuperseded:
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{
			Status: "stale",
			Record: cp,
			Err:    errors.New("ask was superseded by a newer question"),
		}
	case askStatusAnswered:
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		if cp.RequestReceipt != nil && cp.RequestReceipt.ChoiceIndex == choiceIdx && cp.RequestReceipt.Actor == req.Actor {
			return askAnswerResult{
				Status:  "replayed",
				Record:  cp,
				Receipt: cp.RequestReceipt,
			}
		}
		return askAnswerResult{
			Status: "stale",
			Record: cp,
			Err:    errors.New("stale ask card: different action refused"),
		}
	}

	// Open ask: verify it is still the exact current open question on this workspace.
	card := w.currentAsk()
	if ledger.currentID != rec.ID || (card != nil && (card.ID != rec.ID || card.Launch != rec.Launch || card.Revision != rec.Revision)) {
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{
			Status: "stale",
			Record: cp,
			Err:    errors.New("ask is no longer the current open question"),
		}
	}

	// If already journaled in flight, refuse a conflicting action.
	if rec.RequestReceipt != nil {
		if rec.RequestReceipt.ChoiceIndex != choiceIdx || rec.RequestReceipt.Actor != req.Actor {
			cp := cloneAskRecord(rec)
			ledger.mu.Unlock()
			return askAnswerResult{
				Status: "stale",
				Record: cp,
				Err:    errors.New("ask answer already journaled with a different action"),
			}
		}
		if rec.RequestReceipt.DeliveryState == "journaled" {
			if w.store != nil && w.projectID != "" {
				raw, found, err := w.store.MarshalProposalMarker(context.Background(), w.projectID, markerAskAdmissionPrefix+rec.RequestReceipt.ReceiptID)
				var admitted askRequestReceipt
				if err == nil && found && json.Unmarshal([]byte(raw), &admitted) == nil && admitted.ReceiptID == rec.RequestReceipt.ReceiptID && admitted.DeliveryState == "admitted" {
					rec.RequestReceipt = &admitted
					rec.Status = askStatusAnswered
					rec.AnsweredAt = admitted.AdmittedAt
					err = w.persistAskRecordLocked(rec)
					cp := cloneAskRecord(rec)
					ledger.currentID = ""
					ledger.mu.Unlock()
					w.ask.mu.Lock()
					if w.ask.card != nil && w.ask.card.ID == cp.ID {
						w.ask.card = nil
					}
					w.ask.mu.Unlock()
					if err != nil {
						return askAnswerResult{Status: "refused", Record: cp, Err: err}
					}
					return askAnswerResult{Status: "replayed", Record: cp, Receipt: cp.RequestReceipt}
				}
			}
			cp := cloneAskRecord(rec)
			ledger.mu.Unlock()
			return askAnswerResult{Status: "refused", Record: cp, Err: errors.New("answer delivery awaits acknowledgement; it will not be sent twice")}
		}
	}
	ledger.mu.Unlock()

	receiver, ok := w.assignedAskReceiver(rec.Launch)
	if !ok {
		w.setOutput("No Marshal chat is open; the answer was not sent.", true)
		return askAnswerResult{
			Status: "refused",
			Err:    errors.New("no Marshal chat is open; the answer was not sent"),
		}
	}

	choice := rec.Choices[choiceIdx]
	label := choice.Label
	now := time.Now().UTC()

	// Journal receipt durably BEFORE delivering to the assigned receiver.
	ledger.mu.Lock()
	if rec.Status != askStatusOpen || ledger.currentID != rec.ID {
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{
			Status: "stale",
			Record: cp,
			Err:    errors.New("ask changed before delivery"),
		}
	}
	receipt := &askRequestReceipt{
		ReceiptID:     askReceiptID(rec.ID, rec.Launch, rec.Revision, choiceIdx, req.Actor),
		Action:        choice.Key,
		ChoiceIndex:   choiceIdx,
		Label:         label,
		Actor:         req.Actor,
		Receiver:      receiver,
		JournaledAt:   now,
		DeliveryState: "journaled",
	}
	rec.RequestReceipt = receipt
	if err := w.persistAskRecordLocked(rec); err != nil {
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{
			Status: "refused",
			Record: cp,
			Err:    fmt.Errorf("journal ask answer: %w", err),
		}
	}
	recForDelivery := cloneAskRecord(rec)
	ledger.mu.Unlock()

	// Deliver to the assigned receiver and wait for durable admission.
	if err := w.deliverAskAnswer(ctx, recForDelivery, label); err != nil {
		ledger.mu.Lock()
		if cur, exists := ledger.records[rec.ID]; exists && cur.Status == askStatusOpen && cur.RequestReceipt != nil {
			if !errors.Is(err, errAskAdmissionUncertain) {
				cur.RequestReceipt.DeliveryState = "failed"
			}
			_ = w.persistAskRecordLocked(cur)
		}
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{
			Status:  "refused",
			Record:  cp,
			Receipt: cp.RequestReceipt,
			Err:     err,
		}
	}

	// Mark answered ONLY after the assigned receiver durably admitted the turn.
	admittedAt := time.Now().UTC()
	ledger.mu.Lock()
	rec.Status = askStatusAnswered
	rec.AnsweredAt = &admittedAt
	if rec.RequestReceipt != nil {
		rec.RequestReceipt.AdmittedAt = &admittedAt
		rec.RequestReceipt.DeliveryState = "admitted"
	}
	if err := w.persistAskRecordLocked(rec); err != nil {
		cp := cloneAskRecord(rec)
		ledger.mu.Unlock()
		return askAnswerResult{Status: "refused", Record: cp, Err: fmt.Errorf("record admitted answer: %w", err)}
	}
	if ledger.currentID == rec.ID {
		ledger.currentID = ""
	}
	finalRec := cloneAskRecord(rec)
	ledger.mu.Unlock()

	var guard *guardAlert
	w.ask.mu.Lock()
	if w.ask.card != nil && w.ask.card.ID == rec.ID && w.ask.card.Revision == rec.Revision {
		guard = w.ask.card.guard
		w.ask.card = nil
	}
	w.ask.mu.Unlock()
	w.requestRepaint()

	w.recordAskAnswered(finalRec.Digest)
	w.RecordActivity("You answered: " + label)
	// A question about the changed files answers their card too: keep
	// keeps them; an undo choice leaves the card open, since nothing is
	// undone yet, and the card says what was answered.
	if guard != nil {
		switch {
		case askChoiceKeeps(label):
			w.guardKeepAlert(guard)
		case askChoiceUndoes(label):
			w.guardAnswered(guard, label)
		}
	}
	return askAnswerResult{
		Status:  "answered",
		Record:  finalRec,
		Receipt: finalRec.RequestReceipt,
	}
}

// answerAsk sends choice i's label to the Marshal chat as the owner's own
// message and closes the card once admitted. With no chat open the card stays
// and nothing is sent.
func (w *Workspace) answerAsk(i int) {
	card := w.currentAsk()
	if card == nil || i < 0 || i >= len(card.Choices) {
		return
	}
	ctx := w.workers.context()
	if ctx == nil {
		ctx = context.Background()
	}
	_ = w.answerAskBound(ctx, askAnswerBinding{
		Project:  card.Project,
		ID:       card.ID,
		Launch:   card.Launch,
		Revision: card.Revision,
		Actor:    "owner:tui",
		Choice:   i,
	})
}

// askAboutGuard reports whether a question decides the changed-files alert
// open when it was asked: it is about changes or files, and one of its
// choices keeps or undoes them.
func askAboutGuard(question string, choices []askChoice) bool {
	q := strings.ToLower(question)
	if !strings.Contains(q, "change") && !strings.Contains(q, "file") {
		return false
	}
	for _, c := range choices {
		if askChoiceKeeps(c.Label) || askChoiceUndoes(c.Label) {
			return true
		}
	}
	return false
}

// askChoiceKeeps and askChoiceUndoes read a choice's label as keeping or
// undoing the changed files.
func askChoiceKeeps(label string) bool {
	l := strings.ToLower(label)
	return strings.Contains(l, "keep") && !askChoiceUndoes(label)
}

func askChoiceUndoes(label string) bool {
	l := strings.ToLower(label)
	return strings.Contains(l, "undo") || strings.Contains(l, "revert") || strings.Contains(l, "discard")
}

// askForGuard is the open question about alert a and its choice that keeps
// the files (-1 for none), or nil.
func (w *Workspace) askForGuard(a *guardAlert) (*askCard, int) {
	card := w.currentAsk()
	if card == nil || a == nil || card.guard != a {
		return nil, -1
	}
	for i, c := range card.Choices {
		if askChoiceKeeps(c.Label) {
			return card, i
		}
	}
	return card, -1
}

// askCount is 1 while a card is open, for the inbox's counts.
func (v boardVisual) askCount() int {
	if v.ask != nil {
		return 1
	}
	return 0
}
