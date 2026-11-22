//go:build !governed

package cli

// editionCommandSynopsis lists the hidden Extreme-only commands so help
// resolves them statically like every other command. None is left.
const editionCommandSynopsis = ""

// editionSweepTree adds this edition's children to the help sweep tree.
var editionSweepTree = map[string]string{}
