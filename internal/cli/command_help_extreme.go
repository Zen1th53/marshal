//go:build !governed

package cli

// editionCommandSynopsis lists the hidden Extreme-only commands so help
// resolves them statically like every other command.
const editionCommandSynopsis = `
memory migrate-legacy --source-kind KIND [--dry-run] [--digest DIGEST] [--initialize-target]
memory rollback-migration --digest DIGEST`

// editionSweepTree adds this edition's children to the help sweep tree.
var editionSweepTree = map[string]string{
	"memory": "migrate-legacy rollback-migration",
}
