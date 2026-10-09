package marshal

import (
	"path"
	"strings"
)

// ReservedPath identifies governance, policy, CI and agent instruction files.
// These files need separate operator consent even when included in task scope.
func ReservedPath(name string) bool {
	name = strings.ToLower(name)
	for _, dir := range []string{".marshal", ".github", ".agents", ".claude", ".codex", ".gemini", ".cursor", "policy", "policies", ".circleci", ".buildkite"} {
		if name == dir || strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	base := path.Base(name)
	switch base {
	case "agents.md", "claude.md", "gemini.md", "instructions.md", "skill.md", "copilot-instructions.md", ".cursorrules", ".clinerules", ".gitlab-ci.yml", "jenkinsfile", "azure-pipelines.yml", ".travis.yml":
		return true
	}
	return strings.HasSuffix(base, ".instructions.md") || strings.HasPrefix(base, "policy.")
}
