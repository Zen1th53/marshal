//go:build !governed

package cli

// editionCommandSynopsis lists the hidden Extreme-only commands so help
// resolves them statically like every other command.
const editionCommandSynopsis = `
goal create SESSION-ID --file INPUT.json
goal approve SESSION-ID --revision N
plan draft SESSION-ID [--provider PROVIDER] [--mode standard|ultra] [--codex-model MODEL] [--claude-model MODEL] [--opencode-model MODEL]
memory migrate-legacy --source-kind KIND [--dry-run] [--digest DIGEST] [--initialize-target]
memory rollback-migration --digest DIGEST`

// editionSweepTree adds this edition's children to the help sweep tree.
var editionSweepTree = map[string]string{
	"goal": "create approve", "plan": "draft", "memory": "migrate-legacy rollback-migration",
}
