package app

import (
	"context"
	"fmt"
	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/project"
)

type ProviderSupport string

const (
	ProviderSupported   ProviderSupport = "SUPPORTED"
	ProviderUnknown     ProviderSupport = "UNKNOWN"
	ProviderUnsupported ProviderSupport = "UNSUPPORTED"
)

// ProviderOperation is a grammar qualification, not execution authority or
// evidence that authentication, isolation, or a provider session succeeded.
type ProviderOperation struct {
	Status       ProviderSupport
	TerminalOnly bool
	Evidence     string
}

type ProviderDialect struct {
	Provider string
	Version  string
	Binary   string
	// Probed is set when the operations were read from the installed CLI's
	// own help; an unprobed dialect knows nothing and refuses nothing.
	Probed     bool
	operations map[string]ProviderOperation
}

func providerKey(provider string) string {
	if spec, ok := providerspec.Lookup(provider); ok {
		return spec.ID
	}
	return provider
}

// providerVersion reads the version a CLI prints for --version, whatever the
// surrounding words ("codex-cli 0.169.0", "2.1.329 (Claude Code)",
// "opencode v2.1.4", "1.5.10").
var providerVersion = regexp.MustCompile(`\bv?([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]*)?)\b`)

// NormalizeProviderVersion is the version in a CLI's --version output, or the
// trimmed output itself when it names none.
func NormalizeProviderVersion(output string) string {
	output = strings.TrimSpace(output)
	if m := providerVersion.FindStringSubmatch(output); m != nil {
		return m[1]
	}
	return output
}

// UnprobedProviderDialect is a dialect nothing has been learned for: every
// operation is UNKNOWN and is passed through to the CLI, which decides.
func UnprobedProviderDialect(provider, version string) ProviderDialect {
	return ProviderDialect{Provider: providerKey(provider), Version: version, operations: map[string]ProviderOperation{}}
}

func (d ProviderDialect) Operation(op string) ProviderOperation {
	if c, ok := d.operations[op]; ok {
		return c
	}
	return ProviderOperation{Status: ProviderUnknown}
}

// WrapperOperation distinguishes MARSHAL services from provider grammar and
// terminal-only wrappers from dialects the provider itself supports in print
// mode. It never enables a new batch execution route.
func (d ProviderDialect) WrapperOperation(op string, terminal bool) ProviderOperation {
	args := NormalizeProviderArgs(d.Provider, strings.Fields(op))
	op = strings.Join(args, " ")
	local := "status,info,health,help,sessions,runs,history"
	if d.Provider == "codex" || d.Provider == "claude" {
		local += ",models,model,select,run,dispatch"
	}
	if d.Provider == "codex" {
		local += ",skills,skill,diff"
	}
	for _, name := range strings.Split(local, ",") {
		if op == name {
			return ProviderOperation{Status: ProviderSupported, Evidence: "MARSHAL application service"}
		}
	}
	if oneOfProvider(op, "cli", "interactive", "chat", "open", "tui") {
		return ProviderOperation{Status: ProviderSupported, TerminalOnly: true, Evidence: "MARSHAL unqualified pass-through; provider arguments remain vendor-owned"}
	}
	if d.Provider == "codex" && !terminal && (oneOfProvider(op, "resume", "fork") || op == "fork --last") {
		op = "exec " + op
	}
	c := d.Operation(op)
	c.TerminalOnly = c.TerminalOnly || op == "fork --last" || (d.Provider == "opencode" || d.Provider == "agy") || (d.Provider == "claude" && !oneOfProvider(op, "exec", "doctor")) || oneOfProvider(op, "new", "continue", "login", "logout")
	return c
}

func oneOfProvider(value string, candidates ...string) bool {
	for _, c := range candidates {
		if value == c {
			return true
		}
	}
	return false
}

func (d ProviderDialect) Check(op string, terminal bool) error {
	c := d.Operation(op)
	if c.Status == ProviderUnsupported {
		if c.TerminalOnly {
			return fmt.Errorf("Nothing was run. %s %s is UNSUPPORTED headless; latest fork is terminal-only. Use /codex fork --last in an interactive terminal, or supply a session ID for headless fork", d.Provider, op)
		}
		return fmt.Errorf("Nothing was run. %s %s is UNSUPPORTED for %s", d.Provider, op, d.Version)
	}
	if c.TerminalOnly && !terminal {
		return fmt.Errorf("Nothing was run. %s %s is terminal-only", d.Provider, op)
	}
	return nil
}

func (c ProviderOperation) Label() string {
	label := string(c.Status)
	if c.Status == ProviderUnknown {
		label += " — unqualified pass-through via cli"
	}
	if c.TerminalOnly {
		label += " — terminal-only in MARSHAL"
	}
	if strings.Contains(c.Evidence, "unqualified pass-through") {
		label += " — unqualified pass-through"
	}
	return label
}

func (d ProviderDialect) Help(operations []string, terminal bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Provider dialect: %s; detected version: %s\n", d.Provider, d.Version)
	if d.Probed {
		b.WriteString("Grammar read from the installed CLI's own --help (no session compatibility claim)\n")
		if missing := d.Missing(); len(missing) > 0 {
			fmt.Fprintf(&b, "Not usable by MARSHAL: the installed CLI lacks %s\n", strings.Join(missing, ", "))
		}
	}
	for _, op := range operations {
		c := d.WrapperOperation(op, terminal)
		fmt.Fprintf(&b, "  /%s %s [%s]\n", d.Provider, op, c.Label())
	}
	return b.String()
}

// One table serves native and governed launch paths. Argument values are
// copied verbatim; only command positions are normalized. Unknown grammar is
// passed through, never guessed or promoted to SUPPORTED.
var providerAliases = map[string]map[string]string{
	"codex":    {"plugins": "plugin", "mcp rm": "mcp remove", "mcp delete": "mcp remove", "plugin install": "plugin add", "plugin rm": "plugin remove", "plugin uninstall": "plugin remove"},
	"claude":   {"plugins": "plugin", "mcp rm": "mcp remove", "mcp delete": "mcp remove", "plugin add": "plugin install", "plugin rm": "plugin uninstall", "plugin remove": "plugin uninstall"},
	"opencode": {"auth": "providers", "mcp ls": "mcp list"},
	"agy":      {"plugins": "plugin", "mcp rm": "mcp remove", "mcp delete": "mcp remove", "plugin add": "plugin install", "plugin rm": "plugin uninstall", "plugin remove": "plugin uninstall"},
}

var providerCommandWords = map[string]string{
	"codex":    "agents,exec,review,login,logout,mcp,plugin,app-server,remote-control,completion,update,doctor,sandbox,debug,apply,resume,queue,archive,delete,migrate-rollouts,unarchive,fork,cloud,exec-server,features,help",
	"claude":   "agents,attach,auth,doctor,mcp,plugin,help,install,update",
	"opencode": "completion,acp,mcp,attach,run,debug,providers,agent,upgrade,uninstall,serve,web,models,stats,export,import,github,pr,session,plugin,db",
	"agy":      "agent,agents,changelog,help,install,mcp,mic-serve,models,plugin,remote-control,update",
}

var providerVerbWords = map[string]map[string]string{
	"codex":    {"mcp": "list,get,add,remove,login,logout,help", "plugin": "list,add,remove,marketplace,help", "features": "list,enable,disable", "exec": "resume,fork,review"},
	"claude":   {"mcp": "list,get,add,remove,login,logout,serve,help", "plugin": "list,install,uninstall,enable,disable,marketplace,help", "auth": "login,logout,status"},
	"opencode": {"mcp": "list,add,auth,logout,debug", "providers": "list,login,logout"},
	"agy":      {"mcp": "list,add,remove,enable,disable", "plugin": "list,install,uninstall,enable,disable,validate,import,link,help"},
}

// Only fixed-arity global options can be traversed safely. In particular,
// Claude's variadic --add-dir leaves the command position unqualified.
var providerGlobalValueOptions = map[string]string{
	"codex":    "-c,--config,-m,--model,--cd,-C,--sandbox,-s,--ask-for-approval,-a,--profile,-p,--add-dir",
	"claude":   "--model,--append-system-prompt,--permission-mode,--output-format,--input-format,--effort",
	"opencode": "--model,-m,--agent,--port,--hostname,--log-level",
	"agy":      "--model,--add-dir,--agent,--project,--effort,--mode,--output-format,--input-format",
}

func providerOptionTail(provider string, args []string) (int, bool) {
	i := 0
	for i < len(args) && oneOfProvider(args[i], strings.Split(providerGlobalValueOptions[providerKey(provider)], ",")...) {
		if i+1 >= len(args) {
			return i, false
		}
		i += 2
	}
	return i, true
}

func NormalizeProviderArgs(provider string, args []string) []string {
	out := append([]string(nil), args...)
	start, valid := providerOptionTail(provider, out)
	if !valid || start == len(out) || strings.HasPrefix(out[start], "-") {
		return out
	}
	tail := out[start:]
	table := providerAliases[providerKey(provider)]
	word := strings.ToLower(tail[0])
	if alias, ok := table[word]; ok {
		tail[0] = alias
	} else if oneOfProvider(tail[0], strings.Split(providerCommandWords[providerKey(provider)], ",")...) {
		// Exact native command spelling. Uppercase positional prompts remain
		// values; slash dispatchers already normalize their selected wrapper.
	} else {
		return out // A native positional prompt is an argument value.
	}
	verbs := providerVerbWords[providerKey(provider)][tail[0]]
	if len(tail) > 1 && verbs != "" && !strings.HasPrefix(tail[1], "-") {
		// Exec's first argument can be a prompt. Only its known subcommands
		// are command positions; preserve prompt spelling and case.
		if tail[0] != "exec" || oneOfProvider(tail[1], "resume", "fork", "review") {
			verb := strings.ToLower(tail[1])
			if alias, ok := table[tail[0]+" "+verb]; ok {
				copy(tail[:2], strings.Fields(alias))
			} else if oneOfProvider(verb, strings.Split(verbs, ",")...) {
				tail[1] = verb
			}
		}
	}
	return out
}

// ProviderArgOperation identifies the provider command, including headless
// Codex subcommands and the session flags used by other providers.
func ProviderArgOperation(provider string, args []string) string {
	args = NormalizeProviderArgs(provider, args)
	// Global options may precede a provider command. Inspect their command
	// tail as well, so `cli --model X mcp enable` cannot bypass a refusal.
	start, valid := providerOptionTail(provider, args)
	if !valid {
		return "UNKNOWN"
	}
	args = args[start:]
	args = NormalizeProviderArgs(provider, args)
	if len(args) == 0 {
		return "new"
	}
	if !strings.HasPrefix(args[0], "-") {
		// A provider's default positional argument can be a prompt or project
		// directory, including words such as "review". Only recognized native
		// command positions may trigger negative operation qualification.
		if !oneOfProvider(args[0], strings.Split(providerCommandWords[providerKey(provider)], ",")...) {
			return "cli"
		}
		if len(args) > 1 && args[0] == "exec" && args[1] == "fork" {
			for _, arg := range args[2:] {
				if arg == "--" {
					break
				}
				if arg == "--last" {
					return "exec fork --last"
				}
			}
		}
		if len(args) > 1 && oneOfProvider(args[0], "mcp", "plugin", "exec") && !strings.HasPrefix(args[1], "-") {
			if args[0] != "exec" || oneOfProvider(args[1], "resume", "fork", "review") {
				return args[0] + " " + args[1]
			}
		}
		return args[0]
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--fork" || arg == "--fork-session" {
			return "fork"
		}
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if oneOfProvider(arg, "--resume", "--session", "--conversation") {
			return "resume"
		}
		if arg == "--continue" {
			return "continue"
		}
	}
	return "new"
}

// ObserveProviderDialect asks the installed CLI for its version and, once per
// installed binary and version, reads which operations it offers from its own
// --help. Time and output are bounded; no stdin, session, account state,
// model discovery, or network request is involved.
func ObserveProviderDialect(ctx context.Context, provider string) ProviderDialect {
	provider = providerKey(provider)
	d := UnprobedProviderDialect(provider, "UNKNOWN")
	if ctx.Err() != nil {
		return d
	}
	path, err := project.FindBinary(provider)
	if err != nil {
		return d
	}
	info, err := os.Stat(path)
	if err != nil {
		return d
	}
	providerDialectCache.Lock()
	key := provider + "\x00" + path
	if cached, ok := providerDialectCache.rows[key]; ok && sameProviderBinary(info, cached.info) && time.Since(cached.checked) < providerVersionRecheck {
		providerDialectCache.Unlock()
		return cached.dialect
	}
	providerDialectCache.Unlock()
	// A cold start of some CLIs (Claude Code, node-based ones) takes several
	// seconds; a short bound reported their version as UNKNOWN.
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, "--version")
	cmd.WaitDelay = 100 * time.Millisecond
	output := &providerProbeOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil || output.truncated {
		d.Binary = path
		return d // a CLI that does not answer --version would not answer --help
	}
	version := NormalizeProviderVersion(string(output.data))
	// The grammar is read once per binary and version: the same version
	// offers the same commands, and a new version is read afresh.
	grammarKey := key + "\x00" + version
	providerDialectCache.Lock()
	learned, known := providerDialectCache.grammar[grammarKey]
	providerDialectCache.Unlock()
	if known {
		d = learned
	} else {
		d = ProbeProviderDialect(provider, version, func(args ...string) string { return providerHelp(ctx, path, args...) })
		if ctx.Err() != nil {
			d.Binary = path
			return d // an interrupted probe is not what this version offers
		}
	}
	d.Binary = path
	// Re-check the executable identity after the probe; never cache a result
	// across replacement during discovery. The bound avoids process-lifetime
	// growth when many isolated projects use different provider doubles.
	if after, err := os.Stat(path); err == nil && sameProviderBinary(info, after) {
		providerDialectCache.Lock()
		defer providerDialectCache.Unlock()
		if len(providerDialectCache.rows) >= 64 {
			providerDialectCache.rows = map[string]providerDialectCacheRow{}
		}
		if len(providerDialectCache.grammar) >= 64 {
			providerDialectCache.grammar = map[string]ProviderDialect{}
		}
		providerDialectCache.rows[key] = providerDialectCacheRow{info: info, checked: time.Now(), dialect: d}
		providerDialectCache.grammar[grammarKey] = d
	}
	return d
}

// providerVersionRecheck bounds how long an unchanged executable is trusted
// to report the same version. A launcher script (an npm shim, a version
// manager) keeps its identity while the CLI it starts is upgraded, so the
// version is asked again after this long; the grammar is read again only
// when the answer differs.
const providerVersionRecheck = 10 * time.Minute

func sameProviderBinary(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.ModTime() == b.ModTime() && a.Size() == b.Size() && a.Mode() == b.Mode()
}

type providerDialectCacheRow struct {
	info    os.FileInfo
	checked time.Time
	dialect ProviderDialect
}

var providerDialectCache = struct {
	sync.Mutex
	rows    map[string]providerDialectCacheRow
	grammar map[string]ProviderDialect
}{rows: map[string]providerDialectCacheRow{}, grammar: map[string]ProviderDialect{}}

type providerProbeOutput struct {
	data      []byte
	truncated bool
}

func (b *providerProbeOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := 64*1024 - len(b.data)
	if len(p) > room {
		p = p[:room]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}
