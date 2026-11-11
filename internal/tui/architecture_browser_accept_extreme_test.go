//go:build !governed

package tui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturereview"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestBrowserAcceptRecordsContractInstruction(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	if reason := w.browserAcceptBlocked()(); reason != "The Marshal is still preparing the project summary." {
		t.Fatalf("Accept not blocked before a contract draft: %q", reason)
	}
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-browser", Revision: 1, Purpose: "Accepted in the browser", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Owner accepts in the browser"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "browser-contract", "proposal": draft}, true)
	if reason := w.browserAcceptBlocked()(); reason != "" {
		t.Fatalf("Accept blocked with a pending contract: %q", reason)
	}
	state := json.RawMessage(`{"architecture_id":"architecture-browser","revision":1,"digest":"` + strings.Repeat("a", 64) + `","builders":{"core":"codex"}}`)
	if err := w.beforeBrowserAccept()(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	w.browserAccepted(state)
	raw, err := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := draft.Contract.CanonicalDigest()
	for _, want := range []string{"The owner accepted architecture rev 1 in the browser.", "Builders: core=codex.", "instruction_ref=browser:", "contract_id=ARCH-browser; revision=1; digest=" + digest, "Continue: plan and launch now."} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("inbox lacks %q: %s", want, raw)
		}
	}
	if activityCount(w, "Architecture accepted in the browser (rev 1).") != 1 {
		w.mu.RLock()
		defer w.mu.RUnlock()
		t.Fatalf("Activity lacks the acceptance: %q", w.state.LastOutput)
	}
}

func TestBrowserAcceptRefusedWithoutContract(t *testing.T) {
	w, _, _ := liveMCPFixture(t)
	err := w.beforeBrowserAccept()(t.Context(), browserAcceptState(1))
	if err == nil || err.Error() != "The Marshal's project summary is not ready yet. Try again in a moment." {
		t.Fatalf("Accept without a contract: %v", err)
	}
	if raw, _ := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal")); strings.Contains(string(raw), "instruction_ref=") {
		t.Fatalf("refused Accept told the Marshal: %s", raw)
	}
}

// A fresh project whose Marshal submitted a project contract has no browser
// review yet, and no Marshal model turn may run before the contract is
// accepted. Opening the architecture builds the page from the pending
// contract itself; its Accept records the owner's instruction bound to that
// contract, and the Marshal's allow then accepts it.
func TestBrowserAcceptOnContractBuiltReviewLetsMarshalAcceptContract(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	// Any provider turn would run this script and leave a marker.
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "model-turn")
	for _, name := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\ntouch "+marker+"\nexit 3\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "hello-uz", Revision: 1, Purpose: "Print Salom, NAME!", Components: []projectcontext.Component{{ID: "cli", Responsibility: "Print the greeting", Paths: []string{"main.go"}, Dependencies: []string{"greet"}}, {ID: "greet", Responsibility: "Format the greeting", Paths: []string{"greet.go"}}}, Invariants: []string{"standard library only"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "contract-page", "proposal": draft}, true)
	digest, _ := draft.Contract.CanonicalDigest()

	session, err := w.startArchitectureReviewSession(t.Context(), w.runtime, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("opening the contract-built review ran a model turn")
	}
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
	graph := state["architecture"].(map[string]any)
	source := graph["source_contract"].(map[string]any)
	if graph["title"] != "hello-uz" || graph["purpose"] != "Print Salom, NAME!" || source["id"] != "hello-uz" || source["digest"] != digest || len(graph["components"].([]any)) != 2 || len(graph["connections"].([]any)) != 1 {
		t.Fatalf("page is not the pending contract: %v", graph)
	}
	if _, blocked := state["accept_blocked"]; blocked || state["approved"] != false {
		t.Fatalf("Accept is not available on the contract-built page: %v %v", state["accept_blocked"], state["approved"])
	}
	accepted, status := call(map[string]any{"protocol_version": architecturereview.Version, "action": "approve", "revision": state["revision"], "digest": state["digest"], "ui_digest": state["ui_digest"], "language": state["language"], "builders_digest": state["builders_digest"]})
	if status != http.StatusOK || accepted["approved"] != true {
		t.Fatalf("Accept on the contract-built page: %d %v", status, accepted["error"])
	}
	var inbox string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !strings.Contains(inbox, "Continue: plan and launch now."); time.Sleep(20 * time.Millisecond) {
		raw, _ := os.ReadFile(inboxPath(w.runtime.ProjectRoot(), "marshal"))
		inbox = string(raw)
	}
	if !strings.Contains(inbox, "instruction_ref=browser:") || !strings.Contains(inbox, "contract_id=hello-uz; revision=1; digest="+digest) {
		t.Fatalf("Accept did not bind the shown contract: %s", inbox)
	}
	authorizeTestMemory(t, w, cfg, true) // the Marshal's own allow Decision
	var receipt projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "contract-page"}, true)), &receipt); err != nil {
		t.Fatal(err)
	}
	current, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project)
	if err != nil || !receipt.Accepted || !receipt.Current || current.Digest != digest {
		t.Fatalf("Marshal allow after the browser Accept: %+v %+v %v", receipt, current, err)
	}
	instruction, _, err := w.store.ProjectContractInstructionReceipt(t.Context(), w.projectID, "hello-uz", 1)
	if err != nil || instruction.Instruction != store.BrowserAcceptanceInstruction || !strings.HasPrefix(instruction.Ref, "browser:") {
		t.Fatalf("acceptance lacks the browser instruction: %+v %v", instruction, err)
	}
	if err := w.runtime.RequireArchitectureAcceptance(t.Context()); err != nil {
		t.Fatalf("accepted contract-built review does not satisfy planning: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a model turn ran before the contract was accepted")
	}
}

// A contract-built page accepts exactly its own contract: once the Marshal
// submitted a newer draft, the old page's Accept is refused and records
// nothing.
func TestBrowserAcceptRefusesSupersededContractPage(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "hello-uz", Revision: 1, Purpose: "Newer summary", Components: []projectcontext.Component{{ID: "cli", Responsibility: "Print", Paths: []string{"main.go"}}}, Invariants: []string{"none"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "newer", "proposal": draft}, true)
	stale := json.RawMessage(`{"architecture_id":"a","revision":1,"digest":"` + strings.Repeat("a", 64) + `","architecture":{"source_contract":{"id":"hello-uz","revision":1,"digest":"older-digest"}}}`)
	if err := w.beforeBrowserAccept()(t.Context(), stale); err == nil || !strings.Contains(err.Error(), "newer project summary") {
		t.Fatalf("Accept of a superseded contract page: %v", err)
	}
	if c, err := w.readPendingContract(t.Context()); err != nil || c == nil {
		t.Fatalf("the refused Accept recorded an instruction for the newer draft: %v %v", c, err)
	}
}
