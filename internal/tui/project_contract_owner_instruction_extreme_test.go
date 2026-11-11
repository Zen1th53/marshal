//go:build !governed

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// ownerTypes delivers one whole owner message through the bound live Marshal
// transcript observer, exactly as the TUI's chat watcher does.
func ownerTypes(t *testing.T, w *Workspace, text string, at time.Time) importer.SessionTranscript {
	t.Helper()
	w.tmuxMu.Lock()
	session := w.tmuxActiveWins["marshal-chat"].sessionID
	w.tmuxMu.Unlock()
	tr := importer.SessionTranscript{SessionID: session, Messages: []importer.Message{{Role: "user", Content: text, Timestamp: at}}}
	w.observeAuthenticatedMarshalProposals(tr)
	return tr
}

// browserAcceptState is the accepted review state the browser hands the hooks.
func browserAcceptState(revision int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"architecture_id":"architecture-browser","revision":%d,"digest":"%s","builders":{}}`, revision, strings.Repeat("a", 64)))
}

// ownerPressesAccept is the owner's Accept architecture click: the hook that
// runs inside the acceptance, then, when it allowed it, the after-save hook.
func ownerPressesAccept(t *testing.T, w *Workspace) error {
	t.Helper()
	state := browserAcceptState(1)
	if err := w.beforeBrowserAccept()(t.Context(), state); err != nil {
		return err
	}
	w.browserAccepted(state)
	return nil
}

// ownerAcceptsArchitecture is the owner's single step in Extreme.
func ownerAcceptsArchitecture(t *testing.T, w *Workspace) {
	t.Helper()
	if err := ownerPressesAccept(t, w); err != nil {
		t.Fatal(err)
	}
}

func activityCount(w *Workspace, needle string) int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return strings.Count(w.state.LastOutput, needle)
}

func TestExtremeBrowserAcceptLetsMarshalAcceptContract(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	if _, err := os.Lstat(w.runtime.StateDirectory() + "/architecture/review/state.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture unexpectedly has an architecture review: %v", err)
	}
	// An Accept before any draft is refused and records nothing.
	if err := ownerPressesAccept(t, w); err == nil || err.Error() != "The Marshal's project summary is not ready yet. Try again in a moment." {
		t.Fatalf("Accept without a contract draft: %v", err)
	}
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-owner", Revision: 1, Purpose: "Accept on the owner's browser Accept", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Owner instruction precedes acceptance"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "owner-contract", "proposal": draft}, true)
	// The refused Accept is never bound to the new draft, and the old chat
	// phrase records nothing any more.
	ownerTypes(t, w, "accept architecture", time.Now().UTC().Add(time.Millisecond))
	if raw, _ := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal")); strings.Contains(string(raw), "contract_id=") {
		t.Fatal("pre-draft Accept or chat phrase was bound to the draft")
	}
	ownerAcceptsArchitecture(t, w)
	raw, err := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := draft.Contract.CanonicalDigest()
	if !regexp.MustCompile(`instruction_ref=browser:[0-9a-f]+; contract_id=ARCH-owner; revision=1; digest=` + digest).Match(raw) {
		t.Fatalf("instruction binding not delivered to the Marshal: %s", raw)
	}
	authorizeTestMemory(t, w, cfg, true) // The Marshal's own allow Decision; no popup.
	var receipt projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "owner-contract"}, true)), &receipt); err != nil {
		t.Fatal(err)
	}
	accepted, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project)
	if err != nil || !receipt.Accepted || !receipt.Current || accepted.Digest != digest {
		t.Fatalf("Marshal acceptance on owner instruction: %+v %+v %v", receipt, accepted, err)
	}
	instruction, _, err := w.store.ProjectContractInstructionReceipt(t.Context(), w.projectID, draft.Contract.ID, 1)
	if err != nil || instruction.Instruction != "accept architecture" || instruction.Session != "native-session" {
		t.Fatalf("acceptance lacks owner instruction receipt: %+v %v", instruction, err)
	}
	// Live state: contract accepted, none pending. A further Accept is a valid
	// acceptance that records no new instruction.
	if err := ownerPressesAccept(t, w); err != nil {
		t.Fatalf("Accept after the contract was accepted: %v", err)
	}
	raw, err = os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "Recorded owner instruction"); n != 1 {
		t.Fatalf("instruction re-recorded after acceptance: %d", n)
	}
}

func TestExtremeMarshalCannotAcceptContractWithoutOwnerInstruction(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-self", Revision: 1, Purpose: "Self-accepted draft", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"None"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "self-contract", "proposal": draft}, true)
	authorizeTestMemory(t, w, cfg, true)
	var receipt projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "self-contract"}, true)), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Accepted || receipt.Current {
		t.Fatalf("Marshal self-accepted without owner instruction: %+v", receipt)
	}
	if _, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("contract became current: %v", err)
	}
	// The refused allow left the draft pending. After the owner's instruction
	// the Marshal re-submits the same draft and its new allow succeeds.
	ownerAcceptsArchitecture(t, w)
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "self-contract", "proposal": draft}, true)
	authorizeTestMemory(t, w, cfg, true)
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "self-contract"}, true)), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Accepted || !receipt.Current {
		t.Fatalf("resubmission after owner instruction: %+v", receipt)
	}
}
