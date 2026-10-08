package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Static usage is resolved before dispatch, so help never opens a runtime or
// invokes a handler. Parent commands list their own children, including aliases.
const commandSynopsis = `init
doctor [--probe-providers] [--deep]
status
agent register --name NAME --role ROLE [--provider PROVIDER] [--model MODEL]
agents
tasks
task import tasks.json [--dry-run]
task show TASK-ID
task claim TASK-ID --agent AGENT-ID [--revision N]
task release TASK-ID
run TASK-ID [--adapter ADAPTER] [--model MODEL] [--agent AGENT-ID] [--revision N]
logs TASK-ID
cancel TASK-ID
adapters
adapter probe NAME
mcp serve [--listen ADDR] [--insecure]
mcp status
a2a serve [--listen ADDR] [--insecure]
a2a status
events
artifacts
verify [-- command args...]
reconcile --file-state STATE.json
memory status
memory recall QUERY
memory show MEM-ID
memory list
memory promote MEM-ID [--dry-run]
memory tombstone MEM-ID [--dry-run]
memory audit
memory remember TITLE BODY [--kind=KIND]
memory write TITLE BODY [--kind=KIND]
policy test SUITE-FILE
legal audit
legal export --output PATH
setup
setup status
update
update check
update status
update install
update apply
goal REQUEST
goal explain REQUEST
plan create SESSION-ID --file INPUT.json
plan show PROJECT-ID
plan approve PROJECT-ID
plan cancel PROJECT-ID
plan handoff SESSION-ID PROJECT-ID
exec start --session SESSION-ID --project PROJECT-ID
exec run RUN-ID
exec status RUN-ID
exec approve APPROVAL-ID [--decider USER] [--reason TEXT]
exec rollback CHECKPOINT-ID
exec handoff RUN-ID
review start SESSION.json
review status VERIFICATION-ID
review evaluate VERIFICATION-ID
review attest VERIFICATION-ID --bundle ENVELOPE.json --provenance TEXT
learning commit INPUT.json
learning show MEMORY-COMMIT-ID
learning item ITEM-ID
learning history ITEM-ID
learning search --project ID [--general] [--terms A,B] [--stale]
learning context --project ID [--general] [--terms A,B] [--constraint TEXT]
learning invalidate INPUT.json
learning trust [TASK-CLASS]
learning fingerprints --project ID
learning playbooks --project ID
learning replays [RUN-ID]
learning benchmarks [NAME]
learning export --project ID [--general]
learning restore BUNDLE.json --project ID [--general]
optimization start INPUT.json
optimization show CYCLE-ID
optimization candidates CYCLE-ID
optimization counterfactuals CYCLE-ID
optimization manifests CYCLE-ID
optimization canaries CYCLE-ID
help [TOPIC]
help why
constitution version
constitution invariants
constitution decisions SESSION-ID
constitution violations SESSION-ID
auth token create --name NAME [--kind=KIND] [--capabilities LIST]
auth token list
auth token revoke --id ID
gc worktrees [--dry-run] [--ttl DURATION]
gc artifacts [--dry-run] [--ttl DURATION] [--max-budget BYTES]
state backup --output PATH
state verify-backup PATH
state restore PATH
tui [SESSION-ID] [--theme THEME] [--no-color] [--no-animation]
codex [NATIVE-CODEX-ARGUMENTS...]
claude [NATIVE-CLAUDE-ARGUMENTS...]
opencode [NATIVE-OPENCODE-ARGUMENTS...]
agy [NATIVE-AGY-ARGUMENTS...]
daemon
version`

var commandHelp = buildCommandHelp()

func buildCommandHelp() map[string]string {
	help := map[string]string{}
	lines := strings.Split(commandSynopsis, "\n")
	for _, alias := range [][2]string{{"exec", "execution"}, {"review", "verification"}, {"agy", "antigravity"}} {
		for _, line := range strings.Split(commandSynopsis, "\n") {
			if line == alias[0] || strings.HasPrefix(line, alias[0]+" ") {
				lines = append(lines, alias[1]+strings.TrimPrefix(line, alias[0]))
			}
		}
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		n := 0
		for n < len(fields) && fields[n][0] >= 'a' && fields[n][0] <= 'z' && !strings.Contains(fields[n], ".") {
			n++
		}
		path := strings.Join(fields[:n], " ")
		help[path] = "Usage: marshal " + line + "\n"
		for i := 1; i < n; i++ {
			parent := strings.Join(fields[:i], " ")
			if _, ok := help[parent]; !ok {
				help[parent] = "Usage: marshal " + parent + " <command>\n"
			}
		}
	}
	keys := make([]string, 0, len(help))
	for key := range help {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, parent := range keys {
		var children []string
		for _, key := range keys {
			if strings.HasPrefix(key, parent+" ") && !strings.Contains(strings.TrimPrefix(key, parent+" "), " ") {
				children = append(children, strings.TrimPrefix(help[key], "Usage: marshal "))
			}
		}
		if len(children) > 0 {
			help[parent] += "\nCommands:\n  " + strings.Join(children, "  ")
		}
		help[parent] += "\nOptions: -h, --help (usage); --json (structured output)\n"
	}
	return help
}

func dispatcherHelp(args []string, out io.Writer) bool {
	requested := false
	tokens := []string{}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-h" || arg == "--help" {
			requested = true
			continue
		}
		if arg == "--json" {
			continue
		}
		tokens = append(tokens, arg)
	}
	// Retain the existing help topics and `help why`; command help is static.
	if len(tokens) > 1 && tokens[0] == "help" {
		if _, ok := commandHelp[tokens[1]]; ok {
			requested = true
			tokens = tokens[1:]
		}
	}
	if !requested {
		return false
	}
	// Native provider commands pass their arguments, help included, to the
	// provider's own CLI.
	if len(tokens) > 0 {
		switch tokens[0] {
		case "codex", "claude", "opencode", "agy", "antigravity":
			return false
		}
	}
	if len(tokens) == 0 {
		fmt.Fprint(out, usage)
		return true
	}
	path := ""
	for _, token := range tokens {
		next := strings.TrimSpace(path + " " + token)
		if _, ok := commandHelp[next]; !ok {
			break
		}
		path = next
	}
	if path == "" {
		return false
	}
	fmt.Fprint(out, commandHelp[path])
	return true
}

func nativeCommand(name string) bool {
	switch name {
	case "codex", "claude", "opencode", "agy", "antigravity":
		return true
	}
	return false
}

// Global flags stop at -- and at native-provider arguments. Work on a copy so
// callers can safely reuse their argument slice.
func globalJSON(args []string) ([]string, bool) {
	filtered := make([]string, 0, len(args))
	enabled := false
	passthrough := false
	for _, arg := range args {
		if !passthrough && arg == "--json" {
			enabled = true
			continue
		}
		filtered = append(filtered, arg)
		if arg == "--" || (len(filtered) == 1 && nativeCommand(arg)) {
			passthrough = true
		}
	}
	return filtered, enabled
}
