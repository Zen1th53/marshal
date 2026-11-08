//go:build !governed

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestProjectContractNativeSubmissionClassifiesAll18AcceptedMemories(t *testing.T) {
	w, lease, cfg := liveMCPFixture(t)
	now := time.Now().UTC().Add(-time.Second)
	for i := 0; i < 18; i++ {
		r := model.MemoryRecordV2{ID: fmt.Sprintf("MEM-required-%02d", i), ProjectID: w.projectID, Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Accepted preservation policy", Body: "Preserve all accepted policy history.", Scope: "project", ScopeID: w.projectID, ACLScope: lease.owner, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
		r.ContentDigest = r.CanonicalDigest()
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-all-history", Revision: 1, Purpose: "Preserve all applicable accepted policies", Components: []projectcontext.Component{{ID: "core", Responsibility: "Govern project work", Paths: []string{"src"}}}, Invariants: []string{"Every accepted record is explicitly classified"}}}
	after := ""
	for {
		var page store.ProjectContractInventoryPage
		if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 2, "after": after}, true)), &page); err != nil {
			t.Fatal(err)
		}
		if draft.Contract.ReviewedInventoryDigest != "" && draft.Contract.ReviewedInventoryDigest != page.CompletenessDigest {
			t.Fatal("inventory changed between pages")
		}
		draft.Contract.ReviewedInventoryDigest = page.CompletenessDigest
		for _, e := range page.Entries {
			draft.Contract.Required = append(draft.Contract.Required, projectcontext.MemoryRef{ID: e.ID, ProjectID: e.ProjectID, Revision: e.Revision, Scope: e.Scope, ScopeID: e.ScopeID, ContentDigest: e.ContentDigest, StateFingerprint: e.StateFingerprint, Class: projectcontext.Policy, Applies: projectcontext.Applicability{ProjectWide: true}, AcceptanceOccurrence: "supported-owner-policy", Reason: "Accepted mandatory preservation policy"})
		}
		if page.NextAfterID == "" {
			break
		}
		after = page.NextAfterID
	}
	if len(draft.Contract.Required) != 18 {
		t.Fatal("native inventory silently lost accepted history")
	}
	raw, _ := json.Marshal(draft)
	if len(raw) <= 4096 {
		t.Fatal("draft does not exercise larger bounded input")
	}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "all-eighteen", "proposal": draft}, true)
	ownerAcceptsArchitecture(t, w)
	authorizeTestMemory(t, w, cfg, true)
	var receipt projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "all-eighteen"}, true)), &receipt); err != nil {
		t.Fatal(err)
	}
	accepted, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project)
	if err != nil || !receipt.Accepted || len(accepted.Contract.Required) != 18 {
		t.Fatalf("full contract not accepted: %+v %v", receipt, err)
	}
	oversized := draft
	oversized.Contract.Purpose = strings.Repeat("x", 64<<10)
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "oversized-contract", "proposal": oversized}, false)
}

func TestProjectContractOwnerCanAcceptCanonicalTaskMemoryWithoutWorkerGrant(t *testing.T) {
	w, lease, cfg := liveMCPFixture(t)
	const taskID = "TASK-contract-inspection"
	if _, err := w.store.ImportTasks(t.Context(), []model.Task{{ID: taskID, Title: "Inspect scoped memory", Status: model.TaskProposed, Risk: model.R0}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Second)
	record := model.MemoryRecordV2{ID: "MEM-task-policy", ProjectID: w.projectID, Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Preserve task policy", Body: "Preserve the accepted task policy.", Scope: string(model.ScopeTask), ScopeID: taskID, ACLScope: lease.owner, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	record.ContentDigest = record.CanonicalDigest()
	if err := w.store.WriteMemoryV2(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	// A same-project private record and a noncanonical task scope must remain
	// hidden. Native owner inspection is not a worker task grant or an ACL bypass.
	for _, hidden := range []struct{ id, scope, actor string }{
		{"MEM-hidden-private", taskID, "other-owner"},
		{"MEM-hidden-foreign-scope", "TASK-not-in-project", lease.owner},
	} {
		r := record
		r.ID, r.ScopeID, r.ACLScope = hidden.id, hidden.scope, hidden.actor
		r.ContentDigest = r.CanonicalDigest()
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 8}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Entries) != 1 || inventory.Entries[0].ID != record.ID {
		t.Fatalf("owner lost canonical task memory or disclosed hidden scopes: %+v", inventory.Entries)
	}
	entry := inventory.Entries[0]
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-task-memory", Revision: 1, Purpose: "Preserve accepted task policies", Components: []projectcontext.Component{{ID: "core", Responsibility: "Govern task work", Paths: []string{"src"}}}, Invariants: []string{"Task policies are mandatory"}, ReviewedInventoryDigest: inventory.CompletenessDigest, Required: []projectcontext.MemoryRef{
		{ID: entry.ID, ProjectID: entry.ProjectID, Revision: entry.Revision, Scope: entry.Scope, ScopeID: entry.ScopeID, ContentDigest: entry.ContentDigest, StateFingerprint: entry.StateFingerprint, Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{taskID}}, AcceptanceOccurrence: "supported-task-policy", Reason: "Required task preservation policy"},
		// Hidden accepted rows require classification, but their bodies and IDs
		// cannot be invented by this reader. Acceptance must refuse incomplete
		// classification; first isolate the positive case in a fresh fixture below.
	}}}
	// The metadata API proves scope/ACL isolation above. Remove neither hidden
	// accepted history nor its classification requirement: use a separate clean
	// fixture for canonical acceptance.
	w2, lease2, cfg2 := liveMCPFixture(t)
	if _, err := w2.store.ImportTasks(t.Context(), []model.Task{{ID: taskID, Title: "Inspect scoped memory", Status: model.TaskProposed, Risk: model.R0}}); err != nil {
		t.Fatal(err)
	}
	record.ProjectID, record.ACLScope = w2.projectID, lease2.owner
	record.ContentDigest = record.CanonicalDigest()
	if err := w2.store.WriteMemoryV2(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	child2 := memoryChild(t, w2.runtime.ProjectRoot(), &cfg2)
	if err := json.Unmarshal([]byte(child2.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	entry = inventory.Entries[0]
	draft.Contract.Project, draft.Contract.ReviewedInventoryDigest = w2.runtime.ProjectContractBinding(), inventory.CompletenessDigest
	draft.Contract.Required[0] = projectcontext.MemoryRef{ID: entry.ID, ProjectID: entry.ProjectID, Revision: entry.Revision, Scope: entry.Scope, ScopeID: entry.ScopeID, ContentDigest: entry.ContentDigest, StateFingerprint: entry.StateFingerprint, Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{taskID}}, AcceptanceOccurrence: "supported-task-policy", Reason: "Required task preservation policy"}
	child2.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "task-contract", "proposal": draft}, true)
	ownerAcceptsArchitecture(t, w2)
	authorizeTestMemory(t, w2, cfg2, true)
	var receipt projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child2.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "task-contract"}, true)), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Accepted || !receipt.Current {
		t.Fatalf("canonical task memory cannot be accepted: %+v", receipt)
	}
	var read struct {
		Contract  projectcontext.Contract `json:"contract"`
		Qualified bool                    `json:"qualified"`
	}
	if err := json.Unmarshal([]byte(child2.call(t, "marshal_project_contract_read", map[string]any{"limit": 1}, true)), &read); err != nil {
		t.Fatal(err)
	}
	if read.Qualified || len(read.Contract.Required) != 1 || read.Contract.Required[0].ID != record.ID {
		t.Fatalf("owner lost scoped accepted architecture or minted execution authority: %+v", read)
	}
}

func TestProjectContractSeparateChildDraftDecisionReceiptAndReopen(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.CompletenessDigest == "" || len(inventory.Entries) != 0 {
		t.Fatal("fresh project classification inventory is not exact")
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", ExpectedVersion: 0, Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-project", Revision: 1, Purpose: "Implement the approved project goal and preserve accepted constraints", Components: []projectcontext.Component{{ID: "core", Responsibility: "Govern task execution and durable history", Paths: []string{"src"}}}, Invariants: []string{"All applicable accepted constraints are mandatory before execution"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	args := map[string]any{"submission_id": "project-contract-1", "proposal": draft}
	var pending projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_submit", args, true)), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Accepted || pending.Current || pending.State != "pending_permission" {
		t.Fatalf("draft became accepted without decision: %+v", pending)
	}
	if _, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("unreviewed architecture became current: %v", err)
	}
	var duplicate projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_submit", args, true)), &duplicate); err != nil {
		t.Fatal(err)
	}
	if duplicate.OccurrenceID != pending.OccurrenceID || duplicate.Accepted {
		t.Fatal("duplicate draft grants or changes occurrence")
	}
	ownerAcceptsArchitecture(t, w)
	authorizeTestMemory(t, w, cfg, true) // Genuine separate parent-issued Decision.
	var accepted projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "project-contract-1"}, true)), &accepted); err != nil {
		t.Fatal(err)
	}
	if !accepted.Accepted || !accepted.Current || accepted.State != "committed" || accepted.Contract.Digest == "" {
		t.Fatalf("durable acceptance missing: %+v", accepted)
	}
	// Repeated submission inspects the same durable outcome; it must not mint
	// another Decision, rewrite architecture, or admit a model invocation.
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_submit", args, true)), &duplicate); err != nil {
		t.Fatal(err)
	}
	if duplicate != accepted || !w.permissions.queue.Empty() {
		t.Fatal("accepted replay changed outcome or queued authority")
	}
	fresh := memoryChild(t, w.runtime.ProjectRoot(), nil)
	fresh.call(t, "marshal_project_contract_submit", args, false)
	cfg2 := bindWorkspaceMCP(t, w)
	reader := memoryChild(t, w.runtime.ProjectRoot(), &cfg2)
	var inspected struct {
		Contract  projectcontext.Contract `json:"contract"`
		Version   int64                   `json:"version"`
		Digest    string                  `json:"digest"`
		Qualified bool                    `json:"qualified"`
	}
	if err := json.Unmarshal([]byte(reader.call(t, "marshal_project_contract_read", map[string]any{"limit": 1}, true)), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Version != 1 || inspected.Digest != accepted.Contract.Digest || inspected.Contract.Purpose != draft.Contract.Purpose || inspected.Qualified {
		t.Fatal("fresh reader lost accepted history or claimed execution qualification")
	}
}

func TestProjectContractDeniedDraftCannotBecomeCurrent(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-denied", Revision: 1, Purpose: "A draft requiring an independent acceptance decision", Components: []projectcontext.Component{{ID: "core", Responsibility: "Project core", Paths: []string{"src"}}}, Invariants: []string{"Preserve history"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	args := map[string]any{"submission_id": "deny-contract", "proposal": draft}
	child.call(t, "marshal_project_contract_submit", args, true)
	authorizeTestMemory(t, w, cfg, false)
	var denied projectContractMCPReceipt
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_receipt", map[string]string{"submission_id": "deny-contract"}, true)), &denied); err != nil {
		t.Fatal(err)
	}
	if denied.Accepted || denied.Current || denied.State != "denied" {
		t.Fatal("denied draft claimed acceptance")
	}
	if _, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("denial changed active contract: %v", err)
	}
	child.call(t, "marshal_project_contract_submit", args, true)
	if !w.permissions.queue.Empty() {
		t.Fatal("denied replay created another decision")
	}
}

func TestProjectContractForeignDraftAndCallerAuthorityRefused(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil {
		t.Fatal(err)
	}
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-exact", Revision: 1, Purpose: "Exact project binding", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Preserve private state"}, Required: []projectcontext.MemoryRef{}, ReviewedInventoryDigest: inventory.CompletenessDigest}}
	foreign := draft
	foreign.Contract.Project.CanonicalID = "PROJECT-foreign"
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "foreign-canonical", "proposal": foreign}, false)
	foreign = draft
	foreign.Contract.Project.StorageID = "PROJECT-other-store"
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "foreign-storage", "proposal": foreign}, false)
	for _, authority := range []map[string]any{
		{"submission_id": "caller-accepted", "proposal": draft, "accepted": true},
		{"submission_id": "caller-actor", "proposal": draft, "actor": "owner", "role": "marshal"},
	} {
		child.call(t, "marshal_project_contract_submit", authority, false)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("foreign or caller authority draft queued a decision")
	}
	if _, err := w.store.ReadAcceptedProjectContract(t.Context(), draft.Contract.Project); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatal("foreign draft mutated canonical acceptance")
	}
}
