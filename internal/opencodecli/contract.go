package opencodecli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Contract is the part of an installed OpenCode CLI that MARSHAL depends on,
// observed from the binary itself rather than assumed from a pinned version.
type Contract struct {
	// Version is the normalized semantic version ("2.1.4"); RawVersion is
	// exactly what `--version` printed ("opencode v2.1.4").
	Version    string
	RawVersion string
	// Run flags MARSHAL passes to `opencode run`.
	Run        bool
	FormatJSON bool
	Model      bool
	Session    bool
	// Standalone is the 2.x flag that runs a private server. Without it a 2.x
	// CLI joins the shared background service, which ignores the per-launch
	// OPENCODE_CONFIG_CONTENT (MCP server, instructions) of the new process.
	Standalone bool
	// Dir is the 1.x `run --dir` flag; 2.x rejects it as unrecognized.
	Dir bool
}

var versionPattern = regexp.MustCompile(`\bv?(\d+)\.(\d+)\.(\d+)([-+][0-9A-Za-z.\-+]*)?\b`)

// NormalizeVersion extracts the semantic version from `opencode --version`
// output. 1.x prints "1.18.16"; 2.x prints "opencode v2.1.4".
func NormalizeVersion(raw string) string {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2] + "." + m[3] + m[4]
}

// Major returns the major component of a normalized version, or -1.
func Major(version string) int {
	head, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(head)
	if err != nil {
		return -1
	}
	return n
}

// ParseRunHelp reads the flags of `opencode run --help`.
func ParseRunHelp(help string) Contract {
	var c Contract
	has := func(flag string) bool {
		for _, line := range strings.Split(help, "\n") {
			for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '=' || r == '[' || r == ']' }) {
				if field == flag {
					return true
				}
			}
		}
		return false
	}
	c.Run = strings.Contains(help, "opencode run") || has("--format")
	c.Model = has("--model") || has("-m")
	c.Session = has("--session") || has("-s")
	c.Standalone = has("--standalone")
	c.Dir = has("--dir")
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "--format") && strings.Contains(line, "json") {
			c.FormatJSON = true
		}
	}
	return c
}

// WorkerError reports why this CLI cannot run MARSHAL's headless worker
// command, or nil when every flag and the JSON event stream it depends on are
// present. A 2.x CLI must offer --standalone so the per-launch configuration
// is honoured.
func (c Contract) WorkerError() error {
	var missing []string
	if !c.Run {
		missing = append(missing, "run subcommand")
	}
	if !c.FormatJSON {
		missing = append(missing, "run --format json")
	}
	if !c.Model {
		missing = append(missing, "run --model")
	}
	if Major(c.Version) >= 2 && !c.Standalone {
		missing = append(missing, "run --standalone (2.x shares one background server otherwise)")
	}
	if c.Version == "" {
		missing = append(missing, "parsable --version")
	}
	if len(missing) > 0 {
		return fmt.Errorf("OpenCode CLI %s lacks %s", orUnknown(c.RawVersion), strings.Join(missing, ", "))
	}
	return nil
}

func orUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unknown version)"
	}
	return strings.TrimSpace(v)
}

// RunArgs adapts the `run` argv for this CLI: --standalone where the CLI
// would otherwise join a shared background server, and no --dir where the
// CLI rejects it (the process working directory already is the worktree).
func (c Contract) RunArgs(args []string) []string {
	out := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		if args[i] == "--dir" && !c.Dir {
			i++
			continue
		}
		out = append(out, args[i])
	}
	if c.Standalone && !hasArg(out, "--standalone", "--server") {
		at := 0
		if len(out) > 0 && out[0] == "run" {
			at = 1
		}
		out = append(out[:at], append([]string{"--standalone"}, out[at:]...)...)
	}
	return out
}

func hasArg(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

type cacheKey struct {
	path  string
	size  int64
	mtime time.Time
}

var (
	contractMu    sync.Mutex
	contractCache = map[cacheKey]Contract{}
)

// ProbeContract runs `<binary> --version` and `<binary> run --help` once per
// binary file and caches the observation. Neither call opens a model session.
func ProbeContract(ctx context.Context, binary string) (Contract, error) {
	info, err := os.Stat(binary)
	if err != nil {
		return Contract{}, err
	}
	key := cacheKey{path: binary, size: info.Size(), mtime: info.ModTime()}
	contractMu.Lock()
	if c, ok := contractCache[key]; ok {
		contractMu.Unlock()
		return c, nil
	}
	contractMu.Unlock()
	run := func(args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, binary, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		cmd.Stdin = nil
		err := cmd.Run()
		return out.String(), err
	}
	rawVersion, err := run("--version")
	if err != nil {
		return Contract{}, fmt.Errorf("OpenCode --version failed: %w", err)
	}
	help, err := run("run", "--help")
	if err != nil && strings.TrimSpace(help) == "" {
		return Contract{}, fmt.Errorf("OpenCode run --help failed: %w", err)
	}
	c := ParseRunHelp(help)
	c.RawVersion = strings.TrimSpace(rawVersion)
	c.Version = NormalizeVersion(rawVersion)
	contractMu.Lock()
	contractCache[key] = c
	contractMu.Unlock()
	return c, nil
}
