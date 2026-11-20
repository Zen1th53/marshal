//go:build linux && !governed

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// An agy Marshal whose project has no MCP file of its own gets MARSHAL's
// server through .agents/mcp_config.json and a proposal lease in its
// environment, which agy passes to that server.
func TestAntigravityMarshalGetsAProposalLease(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-agy-lease", false)
	root := w.runtime.ProjectRoot()
	args, env, dir, err := prepareMarshalLaunch("antigravity", root, nil, "MARSHAL PROTOCOL\nTest launch", false, "")
	if err != nil {
		t.Fatal(err)
	}
	args, env, dir, err = w.prepareProposalMCP("antigravity", root, args, env, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dir.remove)
	if dir.workspaceMCP == "" {
		t.Fatal("no workspace MCP file for the agy Marshal")
	}
	data, err := os.ReadFile(filepath.Join(root, ".agents", "mcp_config.json"))
	if err != nil || !strings.Contains(string(data), `"--role"`) {
		t.Fatalf("workspace file %q (%v): the Marshal server must run with its marshal role", data, err)
	}
	var path string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG="); ok {
			path = v
		}
	}
	if path == "" {
		t.Fatalf("no proposal lease in the agy environment: %q", env)
	}
	if _, err := proposalmcp.LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "INTAKE ONLY") {
		t.Fatal("a leased agy Marshal was told it is intake only")
	}
}

// `marshal agy` starts the person's own agy session before the Marshal
// chat; that session writes the workspace file first. The Marshal chat must
// still recognise the file as MARSHAL's and get its lease (it used to treat
// it as the person's file and stay intake-only).
func TestAntigravityMarshalLeaseAfterAPlainSessionWroteTheFile(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-agy-lease-second", false)
	root := w.runtime.ProjectRoot()
	_, _, plain, err := configureMarshalMCP("antigravity", root, []string{"--print", "x"}, nil, nil, "session", "S-1")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.remove()
	if plain.workspaceMCP == "" {
		t.Fatal("the first session did not create the workspace file")
	}
	args, env, dir, err := prepareMarshalLaunch("antigravity", root, nil, "MARSHAL PROTOCOL\nTest launch", false, "")
	if err != nil {
		t.Fatal(err)
	}
	_, env, dir, err = w.prepareProposalMCP("antigravity", root, args, env, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dir.remove)
	if dir.workspaceMCP != "" {
		t.Fatal("the Marshal chat took over a file another session created")
	}
	leased := false
	for _, e := range env {
		leased = leased || strings.HasPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG=")
	}
	if !leased {
		t.Fatal("the Marshal chat got no proposal lease")
	}
}
