package marshal

import "testing"

func TestReservedGovernancePaths(t *testing.T) {
	for _, name := range []string{".marshal/state.db", ".github/workflows/test.yml", "AGENTS.md", "nested/CLAUDE.md", "policy/network.json", "policies/local.yaml", ".agents/skills/foo/SKILL.md", "x/chat.instructions.md", ".gitlab-ci.yml"} {
		if !ReservedPath(name) {
			t.Errorf("not reserved: %s", name)
		}
	}
	for _, name := range []string{"internal/main.go", "README.md", "agents.md.txt", "src/policy_test.go"} {
		if ReservedPath(name) {
			t.Errorf("reserved ordinary file: %s", name)
		}
	}
}
