package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// A routing amendment ("exclude agy and opencode from all remaining roles")
// came back from the Marshal model with the existing task's check criterion
// restated and a new final verification task whose check named its single
// criterion in other words. The runtime refused it with "check criterion
// differs for slug-final-verify". The criterion text the runtime already has
// now wins, and only a genuinely unmapped criterion is still refused.
func TestAmendBindsRestatedCheckCriteria(t *testing.T) {
	usage := "README.md contains a Usage section showing one accurate slugify(text) example."
	existing := []marshal.Task{{
		PlanTaskID: "slug-usage", Worker: "claude", Mode: marshal.Governed, State: marshal.HandedIn,
		Criteria: []string{usage}, Files: []string{"README.md"},
		Checks: []marshal.Check{{Command: "python3 check_usage.py", Criteria: []string{usage}}},
	}}
	var proposal marshalTaskProposal
	raw := `{"tasks":[
	 {"id":"slug-usage","title":"Document usage","worker":"codex","mode":"governed","criteria":["README.md contains a Usage section showing one accurate slugify(text) example"],"paths":["README.md"],
	  "checks":[{"command":"python3 check_usage.py","criteria":["The README usage example is accurate"]}]},
	 {"id":"slug-final-verify","title":"Verify integrated example","worker":"codex","mode":"governed","type":"verification","criteria":["The documented example runs against the integrated slug.py and prints hello-world."],"paths":["README.md"],"depends_on":["slug-usage"],
	  "checks":[{"command":"python3 -c 'from slug import slugify; assert slugify(\"Hello World\") == \"hello-world\"'","criteria":["documented example prints hello-world against integrated slug.py"]}]}
	]}`
	if err := json.Unmarshal([]byte(raw), &proposal); err != nil {
		t.Fatal(err)
	}
	bindAmendCriteria(&proposal, existing)
	m := &MarshalCLI{ProjectID: "PROJECT-0123456789abcdef0123456789abcdef"}
	draft, err := m.materialize(proposal, "PLAN-x", 2, []string{"codex", "claude"})
	if err != nil {
		t.Fatalf("routing amendment refused: %v", err)
	}
	if got := draft.Tasks[0]; got.Worker != "codex" || got.Criteria[0] != usage || got.Checks[0].Criteria[0] != usage {
		t.Fatalf("existing task did not keep its approved criteria: %+v", got)
	}
	if got := draft.Tasks[1].Checks[0].Criteria; len(got) != 1 || got[0] != draft.Tasks[1].Criteria[0] {
		t.Fatalf("new single-criterion check not bound to its criterion: %q", got)
	}

	// A check of a task with several criteria that names none of them is
	// still refused: the runtime never guesses which one it evidences.
	if err := json.Unmarshal([]byte(`{"tasks":[{"id":"two","title":"two","worker":"codex","mode":"governed","criteria":["a exists","b exists"],"paths":["a"],"checks":[{"command":"true","criteria":["something else"]}]}]}`), &proposal); err != nil {
		t.Fatal(err)
	}
	bindAmendCriteria(&proposal, nil)
	if _, err := m.materialize(proposal, "PLAN-x", 2, []string{"codex"}); err == nil || !strings.Contains(err.Error(), "check criterion differs for two") {
		t.Fatalf("unmapped criterion accepted: %v", err)
	}
}
