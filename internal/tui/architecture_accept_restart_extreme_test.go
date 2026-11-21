//go:build linux && !governed

package tui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// The live run: the Marshal submits the architecture, the owner accepts it
// in the browser, the Marshal chat restarts (a new launch), the restarted
// chat resubmits the identical contract and allows it. The owner's Accept of
// that exact revision and digest covers the identical copy: no second
// owner Accept is asked, the allow applies it, and the Board and
// marshal_project_contract_context agree it is accepted.
func TestBrowserAcceptSurvivesChatRestartAndIdenticalResubmission(t *testing.T) {
	w, first, cfg := liveMCPFixture(t)
	goal := model.GoalContract{ID: "GOAL-restart", SessionID: w.sessionID, ProjectID: w.runtime.ProjectIdentity(), OriginalRequest: "Create hello.py", ConstitutionVersion: constitution.Current.String(), DesiredOutcome: "Create hello.py", ExpectedArtifact: "hello.py", SuccessCriteria: []string{"hello.py prints Hello"}, Risk: model.R0, AuthoritySource: "operator", Confirmation: model.ConfirmationApproved, Assessment: map[string]string{"complexity": "LOW", "ambiguity": "LOW", "blast_radius": "LOW", "privilege": "LOW", "reversibility": "LOW", "external_effects": "LOW", "data_sensitivity": "LOW", "dependency_depth": "LOW", "verification_difficulty": "LOW", "operational_criticality": "LOW"}}
	if err := w.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "marshal-project-architecture", Revision: 1, Purpose: "Say hello", Components: []projectcontext.Component{{ID: "core", Responsibility: "Print the greeting", Paths: []string{"hello.py"}, Covers: []string{"hello.py prints Hello"}}}, Invariants: []string{"Print exactly once"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "contract-1", "proposal": draft}, true)
	digest, _ := draft.Contract.CanonicalDigest()

	// The owner accepts the contract-built page in the browser.
	session, err := w.startArchitectureReviewSession(t.Context(), w.runtime, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	location, err := url.Parse(session.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := location.Query().Get("token")
	call := func(body map[string]any) (map[string]any, int) {
		t.Helper()
		method, payload := http.MethodGet, ""
		if body != nil {
			method = http.MethodPost
			raw, _ := json.Marshal(body)
			payload = string(raw)
		}
		request, _ := http.NewRequest(method, "http://"+location.Host+"/api/review", strings.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			request.Header.Set("Origin", "http://"+location.Host)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var state map[string]any
		_ = json.NewDecoder(response.Body).Decode(&state)
		return state, response.StatusCode
	}
	state, _ := call(nil)
	accepted, code := call(map[string]any{"protocol_version": architecturereview.Version, "action": "approve", "revision": state["revision"], "digest": state["digest"], "ui_digest": state["ui_digest"], "language": state["language"], "builders_digest": state["builders_digest"]})
	if code != http.StatusOK || accepted["approved"] != true {
		t.Fatalf("browser Accept: %d %v", code, accepted["error"])
	}
	var inbox string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !strings.Contains(inbox, "instruction_ref=browser:"); time.Sleep(20 * time.Millisecond) {
		raw, _ := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
		inbox = string(raw)
	}
	if !strings.Contains(inbox, "digest="+digest) {
		t.Fatalf("Accept did not bind the contract: %s", inbox)
	}
	// The Marshal never answers in this chat: the queued request is lost.
	w.permissions.queue.Take(false)

	// The Board restarts the Marshal chat: a new launch.
	w.retireProposalMCPForResume(w.tmuxActiveWins["marshal-chat"])
	w.chatRestarted()
	cfg = bindWorkspaceMCP(t, w)
	live := w.proposalMCP.active
	if live == nil || live.id == first.id {
		t.Fatal("no new launch")
	}
	// What the old chat left open is not answered either.
	w.permissions.queue.Take(false)
	w.permissions.mu.Lock()
	clear(w.permissions.outstanding)
	w.permissions.mu.Unlock()

	// Before anything applies, the Board and the context agree: not
	// accepted; the owner's part is done and nothing asks the owner again.
	status, err := w.runtime.ArchitectureReviewStatus(t.Context())
	if err != nil || status.Accepted || !status.OwnerAccepted {
		t.Fatalf("Board status before the allow: %+v %v", status, err)
	}

	// The Marshal's context says the same: not applied, owner already accepted.
	w.proposalMCP.mu.Lock()
	before, err := w.readNativePlanningMCP(t.Context(), w.proposalMCP.active)
	w.proposalMCP.mu.Unlock()
	if err != nil || !strings.Contains(string(before), `"missing":"architecture"`) || !strings.Contains(string(before), "already accepted the architecture in the browser") {
		t.Fatalf("context before the allow: %s %v", before, err)
	}

	// The restarted chat resubmits the identical contract and allows it.
	child = memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "contract-again", "proposal": draft}, true)
	if c, err := w.readPendingContract(t.Context()); err != nil || c != nil {
		t.Fatalf("the identical resubmission asks the owner again: %+v %v", c, err)
	}
	authorizeTestMemory(t, w, cfg, true)
	current, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project)
	if err != nil || current.Digest != digest {
		t.Fatalf("the Marshal's allow of the identical resubmission: %+v %v", current, err)
	}
	instruction, _, err := w.store.ProjectContractInstructionReceipt(t.Context(), w.projectID, draft.Contract.ID, 1)
	if err != nil || !strings.HasPrefix(instruction.Ref, "browser:") || instruction.Launch != first.id {
		t.Fatalf("acceptance is not the owner's browser Accept: %+v %v", instruction, err)
	}

	// The Board and the Marshal's context now both read it as accepted.
	status, err = w.runtime.ArchitectureReviewStatus(t.Context())
	if err != nil || !status.Accepted || status.OwnerAccepted {
		t.Fatalf("Board status after the allow: %+v %v", status, err)
	}
	w.proposalMCP.mu.Lock()
	raw, err := w.readNativePlanningMCP(t.Context(), w.proposalMCP.active)
	w.proposalMCP.mu.Unlock()
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	// The context is served from the accepted contract: no missing
	// architecture, and the read mints a planning read for that contract.
	var got struct {
		Missing  string                     `json:"missing"`
		ReadID   string                     `json:"read_id"`
		Contract projectcontext.ContractRef `json:"contract"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Missing != "" || got.ReadID == "" || got.Contract.Digest != digest {
		t.Fatalf("marshal_project_contract_context after the allow: %s %v", raw, err)
	}
}
