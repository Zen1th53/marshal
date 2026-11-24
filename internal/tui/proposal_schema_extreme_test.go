//go:build !governed

package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// contractSchemaWorkspace is a live Marshal workspace over a bootstrapped
// runtime and an active lease, with no terminal or socket, so the contract
// submission path runs on every platform.
func contractSchemaWorkspace(t *testing.T) (*Workspace, *proposalMCPLease) {
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
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	status, err := runtime.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorkspace(runtime.Store(), string(status.Project.ID), "SESSION-contract-schema")
	w.workDir = runtime.ProjectRoot()
	t.Cleanup(func() { w.Close() })
	w.AttachRuntime(runtime, projectid.ID(runtime.ProjectIdentity()))
	if err := w.store.InitMCPProposals(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w, &proposalMCPLease{id: "launch-schema", owner: "owner-schema", provider: "codex", session: "chat-schema", active: true}
}

func schemaContract(project *projectcontext.ProjectBinding) map[string]any {
	contract := map[string]any{
		"format_version": 1, "id": "app", "revision": 1, "purpose": "Serve the app",
		"components": []any{map[string]any{"id": "app", "responsibility": "Everything", "paths": []string{"app"}, "covers": []string{"The app starts"}}},
		"invariants": []string{"Keep history"}, "reviewed_inventory_digest": "sha256:inventory",
	}
	if project != nil {
		contract["project"] = project
	}
	return contract
}

func submitSchemaContract(t *testing.T, w *Workspace, l *proposalMCPLease, id string, contract any) (json.RawMessage, error) {
	t.Helper()
	proposal, err := json.Marshal(map[string]any{"action": "project-contract", "expected_version": 0, "contract": contract})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"submission_id": id, "proposal": json.RawMessage(proposal)})
	w.proposalMCP.mu.Lock()
	defer w.proposalMCP.mu.Unlock()
	return w.projectContractMCP(t.Context(), l, "project_contract_submit", raw)
}

func noContractWrite(t *testing.T, w *Workspace, l *proposalMCPLease, id string) {
	t.Helper()
	if _, err := w.store.MCPProposal(t.Context(), w.projectID, w.liveOccurrenceMCP(t.Context(), l, id)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("refused draft %s was written: %v", id, err)
	}
}

func TestContractFormatPrecedesCoverage(t *testing.T) {
	w, l := contractSchemaWorkspace(t)
	confirmed := [8]string{"Nobody covers this requirement"}
	w.req.confirmed = &confirmed
	for name, tc := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"mistyped": {func(c map[string]any) { c["components"].([]any)[0].(map[string]any)["paths"] = "app" }, "proposal.contract.components[0].paths: must be an array"},
		"unknown":  {func(c map[string]any) { c["summary"] = "s" }, "proposal.contract.summary: unknown field"},
		"missing":  {func(c map[string]any) { delete(c, "invariants") }, "proposal.contract.invariants: required"},
		"undrawable": {func(c map[string]any) {
			c["components"].([]any)[0].(map[string]any)["id"] = "internal/app"
		}, "Component IDs must be short identifiers"},
	} {
		t.Run(name, func(t *testing.T) {
			contract := schemaContract(nil)
			tc.mutate(contract)
			_, err := submitSchemaContract(t, w, l, "format-"+name, contract)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "not covered") {
				t.Fatalf("error %v, want %q before coverage", err, tc.want)
			}
			noContractWrite(t, w, l, "format-"+name)
		})
	}
	// A well-formed draft reaches the coverage rule.
	_, err := submitSchemaContract(t, w, l, "format-ok", schemaContract(nil))
	if err == nil || !strings.Contains(err.Error(), "not covered") || !strings.Contains(err.Error(), confirmed[0]) {
		t.Fatalf("coverage refusal = %v", err)
	}
	noContractWrite(t, w, l, "format-ok")
}

func TestContractOmittedProjectBindsLiveProject(t *testing.T) {
	w, l := contractSchemaWorkspace(t)
	_, err := submitSchemaContract(t, w, l, "omitted-project", schemaContract(nil))
	p, readErr := w.store.MCPProposal(t.Context(), w.projectID, w.liveOccurrenceMCP(t.Context(), l, "omitted-project"))
	if readErr != nil {
		t.Fatalf("draft without contract.project was not admitted: submit %v, read %v", err, readErr)
	}
	var sub store.ProjectContractSubmission
	if err := json.Unmarshal([]byte(p.Payload), &sub); err != nil {
		t.Fatal(err)
	}
	if sub.Contract.Project != w.runtime.ProjectContractBinding() {
		t.Fatalf("contract.project bound to %+v, want the live project %+v", sub.Contract.Project, w.runtime.ProjectContractBinding())
	}
}

func TestContractWrongProjectReturnsFieldError(t *testing.T) {
	w, l := contractSchemaWorkspace(t)
	live := w.runtime.ProjectContractBinding()
	wrong := projectcontext.ProjectBinding{CanonicalID: "PROJECT-elsewhere", StorageID: live.StorageID}
	_, err := submitSchemaContract(t, w, l, "wrong-project", schemaContract(&wrong))
	if err == nil || !strings.Contains(err.Error(), "proposal.contract.project: must be") || !strings.Contains(err.Error(), live.CanonicalID) {
		t.Fatalf("wrong project error = %v", err)
	}
	noContractWrite(t, w, l, "wrong-project")
	mistyped := schemaContract(nil)
	mistyped["project"] = map[string]any{"canonical_id": 7, "storage_id": live.StorageID}
	_, err = submitSchemaContract(t, w, l, "mistyped-project", mistyped)
	if err == nil || !strings.Contains(err.Error(), "proposal.contract.project.canonical_id: must be a string") {
		t.Fatalf("mistyped project error = %v", err)
	}
	noContractWrite(t, w, l, "mistyped-project")
}

// Every live action other than decide parses through the shared proposal
// parser when it has the schema's required keys, and decide, which only
// answers a live chat question, is not a file proposal.
func TestProposalSchemaExamplesMatchParser(t *testing.T) {
	values := map[string]string{
		"id": "task-1", "reason": "because", "language": "en", "question": "Which?",
		"choices": `[{"key":"enter","label":"Yes"},{"key":"b","label":"No"}]`, "card": requirementsCardExample(), "ids": `["task-1"]`,
		"key": "acceptance-mode", "value": "marshal", "path": "/abs/path", "provider": "codex",
		"task_id": "task-1", "target_worker": "claude",
	}
	for _, action := range proposalmcp.LiveProposalActions {
		required, _, _ := proposalmcp.ProposalActionKeys(action)
		proposal := map[string]any{"action": action}
		for _, k := range required {
			if k == "fresh_session" || k == "allow" {
				proposal[k] = true
			} else {
				proposal[k] = values[k]
			}
		}
		raw, _ := json.Marshal(proposal)
		if err := proposalmcp.ValidateProposal(raw); err != nil {
			t.Fatalf("%s: schema refuses its own example: %v", action, err)
		}
		_, err := parseMarshalProposalRaw(marshalProposalPrefix + string(raw))
		if action == "decide" {
			if err == nil {
				t.Fatal("decide parsed as a file proposal")
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: schema example %s refused by the parser: %v", action, raw, err)
		}
	}
}

func requirementsCardExample() string {
	var lines []map[string]string
	for _, l := range "abcdefgh" {
		lines = append(lines, map[string]string{"line": string(l), "text": "Item " + string(l), "source": "owner"})
	}
	raw, _ := json.Marshal(lines)
	return string(raw)
}
