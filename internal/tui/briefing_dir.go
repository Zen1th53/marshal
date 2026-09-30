package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// briefingDir is a per-launch directory under .marshal/briefing/ holding the
// briefings for a provider that reads instructions from files. It keeps them
// out of the project's own files, such as AGENTS.md, and is removed when the
// session ends.
type briefingDir struct {
	provider string
	path     string
	size     int
}

// newBriefingDir creates the directory. The briefing directory ignores itself
// in git, so a project that does not ignore .marshal still never commits one.
func newBriefingDir(root, provider string) (*briefingDir, error) {
	base := filepath.Join(root, ".marshal", "briefing")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte("*\n"), 0o600); err != nil {
		return nil, err
	}
	path, err := os.MkdirTemp(base, provider+"-")
	if err != nil {
		return nil, err
	}
	return &briefingDir{provider: provider, path: path}, nil
}

// file is the one instructions file both providers read: Antigravity loads
// AGENTS.md from every workspace directory, and OpenCode is pointed at it.
func (d *briefingDir) file() string { return filepath.Join(d.path, "AGENTS.md") }

// add appends one briefing to the directory's instructions file.
func (d *briefingDir) add(briefing string) (string, error) {
	briefing = strings.TrimSpace(briefing)
	if briefing == "" {
		return "", nil
	}
	f, err := os.OpenFile(d.file(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if d.size > 0 {
		briefing = "\n" + briefing
	}
	n, err := f.WriteString(briefing + "\n")
	d.size += n
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Briefing delivered through MARSHAL's briefing directory (%d bytes).", len(briefing)), nil
}

// launch returns the arguments and environment that point the agent at the
// directory. With nothing written it changes nothing.
func (d *briefingDir) launch(args []string) ([]string, []string, error) {
	if d == nil || d.size == 0 {
		return args, nil, nil
	}
	switch d.provider {
	case "opencode":
		env, err := opencodeInstructionsEnv(os.Getenv("OPENCODE_CONFIG_CONTENT"), d.file())
		if err != nil {
			return args, nil, err
		}
		return args, []string{env}, nil
	case "antigravity":
		return append([]string{"--add-dir", d.path}, args...), nil, nil
	}
	return args, nil, fmt.Errorf("%s cannot read a briefing directory", providerDisplayName(d.provider))
}

// remove deletes the directory once the session is over.
func (d *briefingDir) remove() {
	if d != nil {
		_ = os.RemoveAll(d.path)
	}
}

// opencodeInstructionsEnv adds file to OpenCode's instructions for this launch
// only. OpenCode merges OPENCODE_CONFIG_CONTENT over the person's own
// configuration, so their instructions stay and none of their files change; a
// value the person already set is extended rather than replaced.
func opencodeInstructionsEnv(existing, file string) (string, error) {
	config := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil {
			return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT is not a JSON object: %w", err)
		}
	}
	var instructions []any
	if prior, ok := config["instructions"].([]any); ok {
		instructions = prior
	}
	config["instructions"] = append(instructions, file)
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return "OPENCODE_CONFIG_CONTENT=" + string(data), nil
}
