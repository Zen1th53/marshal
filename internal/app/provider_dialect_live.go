package app

import (
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
)

// dialectNeed says where an operation MARSHAL uses shows up in a provider
// CLI's own --help: as a command the help of path lists, or as a flag it
// lists. It names no version: whatever version is installed is asked.
type dialectNeed struct {
	path    []string
	command string
	flag    string
	// terminalOnly marks an operation MARSHAL offers headless only when the
	// CLI has it; without it the interactive terminal remains the way.
	terminalOnly bool
}

// providerDialectNeeds are the operations whose spelling differs per
// provider. Every native command word (providerCommandWords) is also an
// operation, needing that command in the top-level help, and every
// management verb (providerGroupVerbs) needs the verb in its group's help.
var providerDialectNeeds = map[string]map[string]dialectNeed{
	"codex": {
		"new":              {},
		"continue":         {command: "resume"},
		"fork --last":      {path: []string{"fork"}, flag: "--last"},
		"exec fork --last": {path: []string{"exec", "fork"}, flag: "--last", terminalOnly: true},
		"approval":         {flag: "--ask-for-approval"},
		"sandbox":          {flag: "--sandbox"},
		"search":           {flag: "--search"},
	},
	"claude": {
		"new":      {},
		"continue": {flag: "--continue"},
		"resume":   {flag: "--resume"},
		"fork":     {flag: "--fork-session"},
		"exec":     {flag: "--print"},
		"review":   {command: "review"},
		"login":    {path: []string{"auth"}, command: "login"},
		"logout":   {path: []string{"auth"}, command: "logout"},
	},
	"opencode": {
		"new":      {},
		"continue": {flag: "--continue"},
		"resume":   {flag: "--session"},
		"fork":     {flag: "--fork"},
		"review":   {command: "review"},
	},
	"agy": {
		"new":      {},
		"continue": {flag: "--continue"},
		"resume":   {flag: "--conversation"},
		"fork":     {flag: "--fork"},
		"prompt":   {flag: "--print"},
		"review":   {command: "review"},
	},
}

// providerRequiredOperations are what MARSHAL itself runs a provider with. A
// provider is usable when its installed CLI has them, at any version.
var providerRequiredOperations = map[string][]string{
	"codex":    {"exec", "resume"},
	"claude":   {"exec", "resume"},
	"opencode": {"run", "resume"},
	"agy":      {"prompt", "resume"},
}

// providerGroupVerbs is the management vocabulary MARSHAL recognizes under a
// command group, for every provider: which of them a provider has is read
// from that group's help.
var providerGroupVerbs = map[string]string{
	"mcp":       "list,get,add,remove,login,logout,auth,debug,enable,disable,serve",
	"plugin":    "list,get,add,install,remove,uninstall,marketplace,enable,disable,validate,import,link",
	"features":  "list,enable,disable",
	"exec":      "resume,fork,review",
	"auth":      "login,logout,status",
	"providers": "list,login,logout",
}

// ProbeProviderDialect reads the operations a provider CLI offers from its
// own --help output. help returns the CLI's help for a command path ("" for
// a path it could not read). A need the help lists is SUPPORTED; one that a
// help with a command list (or an option list, for flags) does not list is
// UNSUPPORTED, with that help as evidence; one whose help could not be read
// stays UNKNOWN and is passed through.
func ProbeProviderDialect(provider, version string, help func(path ...string) string) ProviderDialect {
	provider = providerKey(provider)
	d := UnprobedProviderDialect(provider, version)
	words := strings.Split(providerCommandWords[provider], ",")
	needs := map[string]dialectNeed{}
	for _, word := range words {
		needs[word] = dialectNeed{command: word}
	}
	for group, verbs := range providerGroupVerbs {
		if !oneOfProvider(group, words...) {
			continue
		}
		for _, verb := range strings.Split(verbs, ",") {
			needs[group+" "+verb] = dialectNeed{path: []string{group}, command: verb}
		}
	}
	for op, need := range providerDialectNeeds[provider] {
		needs[op] = need
	}
	program := provider
	if spec, ok := providerspec.Lookup(provider); ok {
		program = spec.Binary
	}
	pages := readHelpPages(program, help, needs)
	if pages[""] == nil {
		return d
	}
	d.Probed = true
	for op, need := range needs {
		d.operations[op] = judgeDialectNeed(d, pages, need)
	}
	return d
}

// readHelpPages reads the help of every command path a need refers to. A
// path is read only when its parent's help lists it; paths of one depth are
// read in parallel, because a cold node-based CLI takes seconds per start.
func readHelpPages(program string, help func(path ...string) string, needs map[string]dialectNeed) map[string]*helpPage {
	pages := map[string]*helpPage{}
	top := parseProviderHelp(help(), program, nil)
	if !top.hasCommands && !top.hasFlags {
		return pages
	}
	pages[""] = top
	paths := map[string][]string{}
	deepest := 0
	for _, need := range needs {
		for i := 1; i <= len(need.path); i++ {
			paths[strings.Join(need.path[:i], " ")] = need.path[:i]
		}
		deepest = max(deepest, len(need.path))
	}
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var mu sync.Mutex
	for depth := 1; depth <= deepest; depth++ {
		var wg sync.WaitGroup
		for _, key := range keys {
			path := paths[key]
			if len(path) != depth {
				continue
			}
			parent := pages[strings.Join(path[:depth-1], " ")]
			if parent == nil || !parent.commands[path[depth-1]] {
				continue
			}
			wg.Add(1)
			go func(key string, path []string) {
				defer wg.Done()
				page := parseProviderHelp(help(path...), program, path)
				if !page.hasCommands && !page.hasFlags {
					return
				}
				mu.Lock()
				pages[key] = page
				mu.Unlock()
			}(key, path)
		}
		wg.Wait()
	}
	return pages
}

func judgeDialectNeed(d ProviderDialect, pages map[string]*helpPage, need dialectNeed) ProviderOperation {
	helpOf := func(path []string) string {
		return strings.Join(append([]string{d.Provider}, path...), " ") + " --help of " + d.Provider + " " + d.Version
	}
	// Every command on the way must be listed by its parent.
	for i := range need.path {
		parent := pages[strings.Join(need.path[:i], " ")]
		if parent == nil {
			return ProviderOperation{Status: ProviderUnknown}
		}
		if !parent.commands[need.path[i]] {
			if !parent.hasCommands {
				return ProviderOperation{Status: ProviderUnknown}
			}
			return ProviderOperation{Status: ProviderUnsupported, TerminalOnly: need.terminalOnly, Evidence: helpOf(need.path[:i]) + " does not list " + need.path[i]}
		}
	}
	page := pages[strings.Join(need.path, " ")]
	if page == nil {
		return ProviderOperation{Status: ProviderUnknown}
	}
	evidence := helpOf(need.path)
	switch {
	case need.command != "":
		if page.commands[need.command] {
			return ProviderOperation{Status: ProviderSupported, Evidence: evidence + " lists " + need.command}
		}
		if !page.hasCommands {
			return ProviderOperation{Status: ProviderUnknown}
		}
		return ProviderOperation{Status: ProviderUnsupported, TerminalOnly: need.terminalOnly, Evidence: evidence + " does not list " + need.command}
	case need.flag != "":
		if page.flags[need.flag] {
			return ProviderOperation{Status: ProviderSupported, Evidence: evidence + " lists " + need.flag}
		}
		if !page.hasFlags {
			return ProviderOperation{Status: ProviderUnknown}
		}
		return ProviderOperation{Status: ProviderUnsupported, TerminalOnly: need.terminalOnly, Evidence: evidence + " does not list " + need.flag}
	}
	return ProviderOperation{Status: ProviderSupported, Evidence: evidence}
}

// Missing is the operations MARSHAL runs the provider with that the
// installed CLI's help shows it lacks. A provider is usable when nothing is
// missing, whatever its version; an operation whose help could not be read
// is not counted missing.
func (d ProviderDialect) Missing() []string {
	var missing []string
	for _, op := range providerRequiredOperations[d.Provider] {
		if d.Operation(op).Status == ProviderUnsupported {
			missing = append(missing, op)
		}
	}
	return missing
}

// providerHelp runs `<path> <args> --help` with a bound.
func providerHelp(ctx context.Context, path string, args ...string) string {
	helpCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(helpCtx, path, append(args, "--help")...)
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	output := &providerProbeOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	_ = cmd.Run() // some CLIs exit non-zero after printing help
	if helpCtx.Err() != nil {
		return ""
	}
	return string(output.data)
}

// helpPage is what one --help output lists.
type helpPage struct {
	commands    map[string]bool
	flags       map[string]bool
	hasCommands bool
	hasFlags    bool
}

// commandsHeader matches the heading of a help's command list in each layout
// the provider CLIs use: "Commands:" (clap, commander, yargs), "Available
// subcommands:" (Go), "COMMANDS" (OpenCode 2.x).
var commandsHeader = regexp.MustCompile(`(?i)^(?:available\s+)?(?:sub)?commands:?$`)

// usageProgram is the program a help's usage line names ("antigravity" for
// the agy binary).
var usageProgram = regexp.MustCompile(`(?im)^usage(?: of)?:?\s+([a-z][a-z0-9-]*)`)

var helpAliases = regexp.MustCompile(`\[aliases?:\s*([^\]]+)\]`)

var helpFlag = regexp.MustCompile(`--[A-Za-z0-9][A-Za-z0-9-]*`)

var helpCommandWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// parseProviderHelp reads the commands listed under a help's command heading
// and the flags its option lines define. path is the command path the help
// belongs to: yargs repeats the program and path before every command
// ("opencode mcp list   list MCP servers").
func parseProviderHelp(text, program string, path []string) *helpPage {
	page := &helpPage{commands: map[string]bool{}, flags: map[string]bool{}}
	text = strings.ReplaceAll(text, "\r", "")
	programs := map[string]bool{program: true}
	if m := usageProgram.FindStringSubmatch(text); m != nil {
		programs[m[1]] = true
	}
	inCommands := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			inCommands = commandsHeader.MatchString(trimmed)
			page.hasCommands = page.hasCommands || inCommands
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			// An option line: its flags come before the description, which
			// starts after a run of spaces.
			head := trimmed
			if i := strings.Index(head, "  "); i >= 0 {
				head = head[:i]
			}
			for _, flag := range helpFlag.FindAllString(head, -1) {
				page.flags[flag] = true
				page.hasFlags = true
			}
			continue
		}
		if !inCommands || indent > 8 {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) > len(path)+1 && programs[fields[0]] && sameWords(fields[1:1+len(path)], path) {
			fields = fields[1+len(path):]
		}
		for _, name := range strings.FieldsFunc(fields[0], func(r rune) bool { return r == '|' || r == ',' }) {
			if helpCommandWord.MatchString(name) {
				page.commands[name] = true
			}
		}
		for _, m := range helpAliases.FindAllStringSubmatch(trimmed, -1) {
			for _, alias := range strings.Split(m[1], ",") {
				if alias = strings.TrimSpace(alias); helpCommandWord.MatchString(alias) {
					page.commands[alias] = true
				}
			}
		}
	}
	return page
}

func sameWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
