//go:build !governed

package tui

import (
	"encoding/json"
	"errors"
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

// ownerAcceptsArchitecture is the owner's single TUI step in Extreme.
func ownerAcceptsArchitecture(t *testing.T, w *Workspace) {
	t.Helper()
	ownerTypes(t, w, "accept architecture", time.Now().UTC().Add(time.Millisecond))
}

func activityCount(w *Workspace, needle string) int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return strings.Count(w.state.LastOutput, needle)
}

func TestExtremeOwnerAcceptArchitectureWithoutReviewLetsMarshalAcceptContract(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	if _, err := os.Lstat(w.runtime.StateDirectory() + "/architecture/review/state.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture unexpectedly has an architecture review: %v", err)
	}
	// An instruction typed before any draft is refused once, never re-bound.
	early := ownerTypes(t, w, "accept architecture", time.Now().UTC())
	for range 5 {
		w.observeAuthenticatedMarshalProposals(early)
	}
	if n := activityCount(w, "Architecture instruction refused"); n != 1 {
		t.Fatalf("refusal spam: %d refusals for one message", n)
	}
	// The long Uzbek message is not an explicit whole instruction.
	ownerTypes(t, w, "Boshla. Arxitekturani qabul qil va Goal'ni rasmiylashtir — Marshal'da bunga ruxsat beraman", time.Now().UTC())
	if n := activityCount(w, "Architecture instruction"); n != 1 {
		t.Fatalf("non-instruction text was observed: %d", n)
	}
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-owner", Revision: 1, Purpose: "Accept on the owner's explicit chat instruction", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Owner instruction precedes acceptance"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "owner-contract", "proposal": draft}, true)
	// The early message is never re-observed against the new draft.
	w.observeAuthenticatedMarshalProposals(early)
	if raw, _ := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal")); strings.Contains(string(raw), "contract_id=") {
		t.Fatal("pre-draft message was bound to the draft")
	}
	ownerAcceptsArchitecture(t, w)
	raw, err := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := draft.Contract.CanonicalDigest()
	if !regexp.MustCompile(`instruction_ref=operator:[0-9a-f]+; contract_id=ARCH-owner; revision=1; digest=` + digest).Match(raw) {
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
	// Live state: contract accepted, no architecture review file. Further
	// owner messages and repeated transcript scans are quiet, not refusals.
	later := ownerTypes(t, w, "accept architecture", time.Now().UTC().Add(time.Second))
	for range 5 {
		w.observeAuthenticatedMarshalProposals(later)
		w.observeAuthenticatedMarshalProposals(early)
	}
	if n := activityCount(w, "Architecture instruction refused"); n != 1 {
		t.Fatalf("refusals after acceptance: %d", n)
	}
	if n := activityCount(w, "Recorded owner instruction"); n != 1 {
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
