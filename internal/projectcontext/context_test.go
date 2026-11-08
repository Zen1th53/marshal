package projectcontext

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func contextRecord() model.MemoryRecordV2 {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	r := model.MemoryRecordV2{ID: "MEM-policy", ProjectID: "stored-project", Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Keep history", Body: "Preserve accepted history.", Scope: "project", ScopeID: "stored-project", ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	r.ContentDigest = r.CanonicalDigest()
	return r
}
func contextContract() Contract {
	r := contextRecord()
	return Contract{FormatVersion: 1, Project: ProjectBinding{CanonicalID: "PROJECT-canonical", StorageID: r.ProjectID}, ID: "CONTRACT-1", Revision: 1, Purpose: "Govern project work", Components: []Component{{ID: "store", Responsibility: "Preserve canonical state", Paths: []string{"internal/store"}}}, Invariants: []string{"Keep accepted history"}, Required: []MemoryRef{{ID: r.ID, Scope: r.Scope, ScopeID: r.ScopeID, ProjectID: r.ProjectID, Revision: r.Revision, ContentDigest: r.ContentDigest, StateFingerprint: MemoryFingerprint(r), Class: Policy, Applies: Applicability{ProjectWide: true}, Reason: "Required preservation rule", AcceptanceOccurrence: "decision-original"}}}
}
func TestRequiredContextFingerprintIncludesAuthorityStateButNotUtility(t *testing.T) {
	original := contextRecord()
	before := MemoryFingerprint(original)
	for name, change := range map[string]func(*model.MemoryRecordV2){
		"acl":           func(r *model.MemoryRecordV2) { r.ACLScope = "another-worker" },
		"revision":      func(r *model.MemoryRecordV2) { r.Revision++ },
		"conflict":      func(r *model.MemoryRecordV2) { r.ConflictIDs = []string{"other"} },
		"superseded":    func(r *model.MemoryRecordV2) { r.SupersededBy = []string{"newer"} },
		"confidence":    func(r *model.MemoryRecordV2) { r.Confidence = model.ConfidenceInferred },
		"last_verified": func(r *model.MemoryRecordV2) { now := original.ObservedAt.Add(time.Second); r.LastVerifiedAt = &now },
		"authority":     func(r *model.MemoryRecordV2) { r.Authority = model.AuthorityAgent },
		"lifecycle":     func(r *model.MemoryRecordV2) { r.Lifecycle = model.MemoryStale },
		"source":        func(r *model.MemoryRecordV2) { r.Source.Reference = "another-owner" },
	} {
		t.Run(name, func(t *testing.T) {
			r := original
			change(&r)
			if MemoryFingerprint(r) == before {
				t.Fatal("required authority mutation did not change state fingerprint")
			}
		})
	}
	original.ExtMeta = map[string]any{"utility": .7, "retrieval_clock": "later"}
	if MemoryFingerprint(original) != before {
		t.Fatal("unrelated utility metadata invalidated required context")
	}
}
func TestRequiredContextFingerprintBindsCanonicalRepositoryFreshnessAnchors(t *testing.T) {
	original := contextRecord()
	for key, value := range map[string]any{"file_path": "internal/store/state.go", "referenced_files": []string{"internal/store/state.go"}, "file_hashes": map[string]string{"internal/store/state.go": "sha256:anchor"}, "symbols": []string{"Store.Save"}, "verified_tests": []string{"TestAtomicSave"}} {
		t.Run(key, func(t *testing.T) {
			anchored := original
			anchored.ExtMeta = map[string]any{key: value}
			if MemoryFingerprint(anchored) == MemoryFingerprint(original) {
				t.Fatal("canonical freshness anchor mutation passed state CAS")
			}
			before := MemoryFingerprint(anchored)
			anchored.ExtMeta["retrieval_clock"] = "later"
			if MemoryFingerprint(anchored) != before {
				t.Fatal("incidental clock changed anchored fingerprint")
			}
		})
	}
}
func TestRequiredContractDigestCanonicalSetsAndRejectsAmbiguity(t *testing.T) {
	c := contextContract()
	c.Invariants = append(c.Invariants, "No unbound actions")
	first, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	reordered := c
	reordered.Invariants = []string{c.Invariants[1], c.Invariants[0]}
	second, err := reordered.CanonicalDigest()
	if err != nil || first != second {
		t.Fatal("set order changed contract", first, second, err)
	}
	if c.Invariants[0] != "Keep accepted history" {
		t.Fatal("digest mutated caller values")
	}
	c.Required = append(c.Required, c.Required[0])
	c.Required[1].Revision++
	if _, err := c.CanonicalDigest(); err == nil {
		t.Fatal("conflicting duplicate ref accepted")
	}
	c = contextContract()
	c.Components[0].Paths = []string{"../other-project"}
	if _, err := c.CanonicalDigest(); err == nil {
		t.Fatal("escaping ownership path accepted")
	}
}
func TestRequiredContextPacketNeverTruncatesAndMeasuresUTF8Wrapper(t *testing.T) {
	c := contextContract()
	c.Purpose = "Loyiha maqsadi ⚡ <keep>"
	digest, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Contract: c, Goal: Goal{ID: "GOAL-1", Revision: 1, Outcome: "Current task", Constraints: []model.Constraint{{ID: "keep", Text: "Keep all facts", IsHard: true}}}, Binding: Binding{Project: c.Project, Contract: ContractRef{ID: c.ID, Revision: 1, Digest: digest}, TaskID: "task", Attempt: 1, Provider: "codex"}, Records: []model.MemoryRecordV2{contextRecord()}}
	p, err := Render(m, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if p.Bytes != len(p.Text) {
		t.Fatal("UTF-8 wrapper byte accounting mismatch")
	}
	exact, err := Render(m, p.Bytes)
	if err != nil || exact.Text != p.Text || exact.Digest != p.Digest {
		t.Fatal("exact cap was not stable", err)
	}
	if _, err := Render(m, p.Bytes-1); !errors.Is(err, ErrBudget) {
		t.Fatal("required overflow was truncated or ignored", err)
	}
	if !strings.Contains(p.Text, "Preserve accepted history.") {
		t.Fatal("essential omitted")
	}
	m.Records = nil
	if _, err := Render(m, 8192); !errors.Is(err, ErrMissing) {
		t.Fatal("missing essential rendered", err)
	}
	m.Records = []model.MemoryRecordV2{contextRecord()}
	m.Records[0].ACLScope = "revoked"
	if _, err := Render(m, 8192); !errors.Is(err, ErrChanged) {
		t.Fatal("state changed behind content digest", err)
	}
}

func TestRequiredContextRefusesMalformedStoredMemoryText(t *testing.T) {
	for _, field := range []string{"title", "body"} {
		for _, value := range []string{string([]byte{'a', 0xff, 'b'}), "text\x00hidden"} {
			c := contextContract()
			r := contextRecord()
			if field == "title" {
				r.Title = value
			} else {
				r.Body = value
			}
			r.ContentDigest = r.CanonicalDigest()
			c.Required[0].ContentDigest = r.ContentDigest
			c.Required[0].StateFingerprint = MemoryFingerprint(r)
			digest, err := c.CanonicalDigest()
			if err != nil {
				t.Fatal(err)
			}
			m := Manifest{Contract: c, Goal: Goal{ID: "GOAL-1", Revision: 1, Outcome: "Preserve exact memory"}, Binding: Binding{Project: c.Project, Contract: ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}}, Records: []model.MemoryRecordV2{r}}
			packet, err := Render(m, 16<<10)
			if !errors.Is(err, ErrInvalid) || packet.Text != "" || packet.Digest != "" || packet.Bytes != 0 {
				t.Fatalf("malformed stored %s rendered with authoritative digests: %+v %v", field, packet, err)
			}
		}
	}
}
func TestRequiredContextApplicabilityAndNilBindingPreserveLegacyJSON(t *testing.T) {
	a := Applicability{Paths: []string{"internal/store"}}
	if !a.Matches(Selection{Paths: []string{"internal/store/memory.go"}}) || a.Matches(Selection{Paths: []string{"internal/storehouse/a.go"}}) {
		t.Fatal("path applicability is not segment-bound")
	}
	legacy := struct {
		Goal    string   `json:"goal"`
		Context *Binding `json:"context,omitempty"`
	}{Goal: "existing"}
	raw, err := json.Marshal(legacy)
	if err != nil || string(raw) != "{\"goal\":\"existing\"}" {
		t.Fatal("nil binding changed Community bytes", string(raw), err)
	}
	if !reflect.DeepEqual(contextContract(), contextContract()) {
		t.Fatal("fixture unstable")
	}
}

func TestRequiredContextLargeGoalAndFullPrefixAreMeasuredWithoutTrimming(t *testing.T) {
	c := contextContract()
	digest, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Contract: c, Goal: Goal{ID: "GOAL-long", Revision: 17, Outcome: strings.Repeat("Accepted current goal ⚡ <facts>\n", 200), DoNotDo: []string{"Preserve every current prohibition"}, SuccessCriteria: []string{"Actual acceptance evidence"}}, Binding: Binding{Project: c.Project, Contract: ContractRef{ID: c.ID, Revision: 1, Digest: digest}}, Records: []model.MemoryRecordV2{contextRecord()}}
	prefix := "Existing execution envelope:\n" + strings.Repeat("provider-specific wrapper ", 20)
	if _, err := RenderWithPrefix(m, prefix, 8<<10); !errors.Is(err, ErrBudget) {
		t.Fatal("undersized cap silently discarded goal", err)
	}
	packet, err := RenderWithPrefix(m, prefix, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Bytes != len(packet.Text) || !strings.HasPrefix(packet.Text, prefix) || !strings.Contains(packet.Text, "Preserve every current prohibition") {
		t.Fatal("complete rendered wrapper/essentials not preserved")
	}
	t.Logf("synthetic complete Go packet=%d UTF-8 bytes including prefix; 8KiB refusal, 16KiB qualified", packet.Bytes)
	if _, err = RenderWithPrefix(m, prefix, MaximumPacketBytes+1); !errors.Is(err, ErrBudget) {
		t.Fatal("unbounded caller cap accepted", err)
	}
}
func TestRequiredContextCanonicalValidationAndEvidenceLifecycles(t *testing.T) {
	for name, change := range map[string]func(*Contract){"version": func(c *Contract) { c.FormatVersion = 2 }, "scope": func(c *Contract) { c.Project.StorageID = "" }, "purpose": func(c *Contract) { c.Purpose = "" }, "component": func(c *Contract) { c.Components[0].Responsibility = "" }, "dependency": func(c *Contract) { c.Components[0].Dependencies = []string{"unknown"} }, "self_dependency": func(c *Contract) { c.Components[0].Dependencies = []string{"store"} }, "invalid_class": func(c *Contract) { c.Required[0].Class = "caller_accepted" }, "mixed_applicability": func(c *Contract) { c.Required[0].Applies.TaskIDs = []string{"task"} }, "empty_applicability": func(c *Contract) { c.Required[0].Applies = Applicability{} }, "foreign_memory": func(c *Contract) { c.Required[0].ProjectID = "other" }, "missing_provenance": func(c *Contract) { c.Required[0].AcceptanceOccurrence = "" }, "negative_age": func(c *Contract) { c.Required[0].MaxVerificationAgeSeconds = -1 }, "bad_component_scope": func(c *Contract) { c.Required[0].Applies = Applicability{Components: []string{"unknown"}} }} {
		t.Run(name, func(t *testing.T) {
			c := contextContract()
			change(&c)
			if _, err := c.CanonicalDigest(); err == nil {
				t.Fatal("invalid authoritative contract accepted")
			}
		})
	}
	now := contextRecord().ValidFrom
	r := contextRecord()
	ref := contextContract().Required[0]
	if err := ValidateRequiredRecord(r, ref, now); err != nil {
		t.Fatal(err)
	}
	r.Lifecycle = model.MemoryCandidate
	r.Authority = model.AuthorityAgent
	r.ContentDigest = r.CanonicalDigest()
	ref.ContentDigest = r.ContentDigest
	ref.StateFingerprint = MemoryFingerprint(r)
	if err := ValidateRequiredRecord(r, ref, now); err == nil {
		t.Fatal("candidate became required policy")
	}
	for name, change := range map[string]func(*model.MemoryRecordV2, *MemoryRef){"future": func(r *model.MemoryRecordV2, ref *MemoryRef) { r.ValidFrom = now.Add(time.Second) }, "expired": func(r *model.MemoryRecordV2, ref *MemoryRef) { r.ValidTo = &now }, "conflict": func(r *model.MemoryRecordV2, ref *MemoryRef) { r.ConflictIDs = []string{"conflict"} }, "superseded": func(r *model.MemoryRecordV2, ref *MemoryRef) { r.SupersededBy = []string{"new"} }, "unverified_evidence": func(r *model.MemoryRecordV2, ref *MemoryRef) { ref.EvidenceRequired = true }, "missing_verification": func(r *model.MemoryRecordV2, ref *MemoryRef) { ref.MaxVerificationAgeSeconds = 10 }, "expired_verification": func(r *model.MemoryRecordV2, ref *MemoryRef) {
		earlier := now.Add(-10 * time.Second)
		r.LastVerifiedAt = &earlier
		ref.MaxVerificationAgeSeconds = 10
	}} {
		t.Run(name, func(t *testing.T) {
			r := contextRecord()
			ref := contextContract().Required[0]
			change(&r, &ref)
			r.ContentDigest = r.CanonicalDigest()
			ref.ContentDigest = r.ContentDigest
			ref.StateFingerprint = MemoryFingerprint(r)
			if err := ValidateRequiredRecord(r, ref, now); err == nil {
				t.Fatal("unusable premise qualified")
			}
		})
	}
	for _, lifecycle := range []model.MemoryLifecycle{model.MemoryCandidate, model.MemoryObserved, model.MemoryRejected, model.MemoryStale, model.MemoryConflicted, model.MemorySuperseded, model.MemoryTombstoned} {
		r := contextRecord()
		r.Lifecycle = lifecycle
		if EligibleAcceptedRecord(r) {
			t.Fatal("nonaccepted lifecycle entered policy discovery", lifecycle)
		}
	}
	if !(Applicability{TaskIDs: []string{"task"}}).Matches(Selection{TaskID: "task"}) || !(Applicability{Components: []string{"store"}}).Matches(Selection{Components: []string{"store"}}) {
		t.Fatal("explicit controlled scopes missed")
	}
}

func TestRequiredContextPlanningSelectionIncludesEveryMandatoryScope(t *testing.T) {
	c := contextContract()
	records := []model.MemoryRecordV2{contextRecord()}
	applications := []Applicability{{TaskIDs: []string{"alpha"}}, {TaskIDs: []string{"beta"}}, {Components: []string{"store"}}, {Paths: []string{"docs"}}}
	for i, applies := range applications {
		r := contextRecord()
		r.ID = "MEM-scoped-" + string(rune('a'+i))
		r.Body = "Required scoped fact " + r.ID
		r.ContentDigest = r.CanonicalDigest()
		records = append(records, r)
		ref := c.Required[0]
		ref.ID = r.ID
		ref.ContentDigest = r.ContentDigest
		ref.StateFingerprint = MemoryFingerprint(r)
		ref.Applies = applies
		c.Required = append(c.Required, ref)
	}
	selection, err := PlanningSelection(c)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Mode != SelectionPlanning || selection.TaskID != "" || len(selection.Paths) != 0 || len(selection.Components) != 0 {
		t.Fatal("planning chosen from caller subset", selection)
	}
	digest, _ := c.CanonicalDigest()
	m := Manifest{Contract: c, Goal: Goal{ID: "GOAL-current", Revision: 7, Outcome: "Actual current task outcome"}, Binding: Binding{Project: c.Project, Contract: ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}}, Selection: selection, Records: records}
	p, err := Render(m, 16384)
	if err != nil || len(p.Loaded) != 5 {
		t.Fatal("planning forgot scoped required facts", p, err)
	}
	for _, r := range records {
		if !strings.Contains(p.Text, r.Body) {
			t.Fatal("essential omitted", r.ID)
		}
	}
	m.Records = records[:len(records)-1]
	if _, err = Render(m, 16384); !errors.Is(err, ErrMissing) {
		t.Fatal("missing scope was silently excluded", err)
	}
	m.Records = records
	m.Selection = Selection{TaskID: "alpha"}
	worker, err := Render(m, 16384)
	if err != nil || len(worker.Loaded) != 2 {
		t.Fatal("ordinary alpha selection widened", worker, err)
	}
	for _, bad := range []Selection{{Mode: "unknown"}, {Mode: SelectionPlanning, TaskID: "alpha"}, {Mode: SelectionPlanning, CanonicalTaskID: "TASK-alpha"}, {Mode: SelectionPlanning, Paths: []string{"docs"}}, {Mode: SelectionPlanning, Components: []string{"store"}}} {
		m.Selection = bad
		if _, err = Render(m, 16384); !errors.Is(err, ErrInvalid) {
			t.Fatal("caller subset/unknown mode qualified", bad, err)
		}
		if _, err = ApplicableContractDigest(c, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("caller subset/unknown mode digested", bad, err)
		}
	}
}

func TestRequiredContextPlanningModePreservesLegacyWorkerBytes(t *testing.T) {
	c := contextContract()
	digest, _ := c.CanonicalDigest()
	s := Selection{TaskID: "alpha", Components: []string{"store"}, Paths: []string{"internal/store"}}
	raw, err := json.Marshal(s)
	if err != nil || string(raw) != `{"CanonicalTaskID":"","TaskID":"alpha","Components":["store"],"Paths":["internal/store"]}` {
		t.Fatal("default worker JSON changed", string(raw), err)
	}
	m := Manifest{Contract: c, Goal: Goal{ID: "GOAL", Revision: 1, Outcome: "Preserve accepted work"}, Binding: Binding{Project: c.Project, Contract: ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}, Principal: "owner", Session: "session"}, Selection: s, Records: []model.MemoryRecordV2{contextRecord()}}
	p, err := Render(m, 16384)
	if err != nil || p.Digest != "sha256:eb95ccd7fabbab12319d04fe82fe8fe0329c648db9e1e1a296a5cb49cbdf1530" || p.ManifestDigest != "sha256:9b399f27a3c7f2b710692afea23079ff7d36092a8abf98096da3e6b7738ec8bf" || p.Bytes != 1379 {
		t.Fatal("legacy worker packet bytes/digests changed", p, err)
	}
	if _, err = PlanningSelection(Contract{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unaccepted malformed contract produced planning selector", err)
	}
}
