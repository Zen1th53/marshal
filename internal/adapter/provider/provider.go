// Package provider describes each provider CLI Marshal can drive by what it
// is rather than by name checks scattered through the application: its
// canonical identifier, the other names it is known by, its executable and
// where it keeps its own state on this machine.
//
// Nothing here runs a process or names a model or a CLI version. The
// capabilities that ask the installed CLI (model catalog, quota) are in
// package providers, which builds on these specs.
package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Getenv reads one environment variable; os.Getenv is the usual one.
type Getenv func(string) string

// Spec is one provider's static identity and on-disk layout.
type Spec struct {
	// ID is the canonical identifier used for workers, settings and records.
	ID string
	// Name is the label shown to the operator.
	Name string
	// Binary is the executable looked up on PATH.
	Binary string
	// Harness is the key of the provider's harness profile.
	Harness string
	// Aliases are other spellings an operator or a record may use.
	Aliases []string
	// StateEnv, when set in the environment, is the directory the CLI keeps
	// its state under (joined with StateEnvSubdir); "" when the CLI reads
	// no such variable.
	StateEnv       string
	StateEnvSubdir string
	// StateDefault is the state directory relative to the user's home when
	// StateEnv is unset.
	StateDefault string
	// HistorySubdir is the directory under the state directory holding the
	// CLI's JSONL session transcripts; "" when the CLI keeps its sessions in
	// some other form (a database, summaries) directly in its state.
	HistorySubdir string
	// CredentialsFile is the file in the state directory where the CLI keeps
	// its sign-in; "" when it is not known to keep one there.
	CredentialsFile string
}

var specs = []Spec{
	{ID: "codex", Name: "Codex", Binary: "codex", Harness: "codex", StateEnv: "CODEX_HOME", StateDefault: ".codex", HistorySubdir: "sessions", CredentialsFile: "auth.json"},
	{ID: "claude", Name: "Claude", Binary: "claude", Harness: "claude-code", StateEnv: "CLAUDE_CONFIG_DIR", StateDefault: ".claude", HistorySubdir: "projects", CredentialsFile: ".credentials.json"},
	{ID: "opencode", Name: "OpenCode", Binary: "opencode", Harness: "opencode", StateEnv: "XDG_DATA_HOME", StateEnvSubdir: "opencode", StateDefault: filepath.Join(".local", "share", "opencode")},
	{ID: "agy", Name: "Antigravity", Binary: "agy", Harness: "antigravity", Aliases: []string{"antigravity"}, StateDefault: filepath.Join(".gemini", "antigravity-cli"), CredentialsFile: "antigravity-oauth-token"},
}

// All is every registered provider, in the order they are named to the
// operator. The order carries no preference.
func All() []Spec {
	out := make([]Spec, len(specs))
	for i, s := range specs {
		out[i] = s
		out[i].Aliases = append([]string(nil), s.Aliases...)
	}
	return out
}

// IDs is the canonical identifier of every registered provider, in All order.
func IDs() []string {
	ids := make([]string, len(specs))
	for i, s := range specs {
		ids[i] = s.ID
	}
	return ids
}

func clean(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Lookup finds a provider by its ID or an alias, ignoring case and
// surrounding space. Harness profile keys are not accepted; see ByHarness.
func Lookup(name string) (Spec, bool) {
	name = clean(name)
	for _, s := range specs {
		if s.ID == name {
			return s, true
		}
		for _, a := range s.Aliases {
			if a == name {
				return s, true
			}
		}
	}
	return Spec{}, false
}

// ByHarness finds a provider by its ID, an alias or its harness profile key.
func ByHarness(name string) (Spec, bool) {
	if s, ok := Lookup(name); ok {
		return s, true
	}
	name = clean(name)
	for _, s := range specs {
		if s.Harness == name {
			return s, true
		}
	}
	return Spec{}, false
}

// Canonical is the ID of the provider name refers to, or name lowercased and
// trimmed when it names no registered provider.
func Canonical(name string) string {
	if s, ok := Lookup(name); ok {
		return s.ID
	}
	return clean(name)
}

// HarnessName is the harness profile key of the provider name refers to, or
// name unchanged when it names no registered provider.
func HarnessName(name string) string {
	if s, ok := Lookup(name); ok {
		return s.Harness
	}
	return name
}

// StateEnvs lists every environment variable a registered provider reads
// for its own state directory, in All order.
func StateEnvs() []string {
	var out []string
	for _, s := range specs {
		if s.StateEnv != "" {
			out = append(out, s.StateEnv)
		}
	}
	return out
}

// ErrNoHistoryDir reports a provider that keeps no JSONL transcript
// directory under its state directory.
var ErrNoHistoryDir = errors.New("provider keeps no JSONL history directory")

// StateDir is where the provider keeps its own state. When the provider's
// variable is set (to anything but space), its trimmed value is used as
// given, possibly relative, and overridden is true; otherwise the default
// under the user's home is used.
func (s Spec) StateDir(getenv Getenv) (dir string, overridden bool, err error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if s.StateEnv != "" {
		if v := strings.TrimSpace(getenv(s.StateEnv)); v != "" {
			if s.StateEnvSubdir != "" {
				v = filepath.Join(v, s.StateEnvSubdir)
			}
			return v, true, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	if home == "" {
		return "", false, errors.New("user home directory is unknown")
	}
	return filepath.Join(home, s.StateDefault), false, nil
}

// StateDirIn is StateDir with a relative override resolved against root.
func (s Spec) StateDirIn(getenv Getenv, root string) (string, error) {
	dir, _, err := s.StateDir(getenv)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir, nil
}

// HistoryDir is the directory holding the provider's session transcripts,
// with a relative state override resolved against root.
func (s Spec) HistoryDir(getenv Getenv, root string) (string, error) {
	if s.HistorySubdir == "" {
		return "", ErrNoHistoryDir
	}
	dir, err := s.StateDirIn(getenv, root)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, s.HistorySubdir), nil
}

// EnvSlice is a Getenv over a command environment: the last assignment of a
// variable in env wins, and a variable env does not assign falls back to
// the process environment.
func EnvSlice(env []string) Getenv {
	return func(key string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(env[i], key+"="); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return os.Getenv(key)
	}
}

// Must returns the registered spec for id and panics when there is none; it
// is for package-level references to a provider's own spec.
func Must(id string) Spec {
	s, ok := Lookup(id)
	if !ok {
		panic("provider: unregistered provider " + id)
	}
	return s
}
