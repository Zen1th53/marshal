//go:build linux && !governed

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Finding 76: the Codex Marshal chat could not resume or amend the run, its
// sandbox refused Marshal's own control socket ("proposalmcp: dial: dial
// unix /var/tmp/mx/proposal-*/broker.sock: connect: operation not
// permitted"). A Codex lease's broker also serves a spool in
// .marshal/proposals, which the Marshal chat's sandbox already lets it
// write, and a refused dial reaches the live broker through it.
func TestCodexMarshalMCPReachesTheBrokerWhenTheSocketIsRefused(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-live-mcp-spool", false)
	cfg := bindWorkspaceMCP(t, w)
	root := w.runtime.ProjectRoot()
	if cfg.Spool == "" || filepath.Dir(cfg.Spool) != statepath.Path(root, "proposals") {
		t.Fatalf("codex lease spool = %q, want a directory in %s", cfg.Spool, statepath.Path(root, "proposals"))
	}
	inside := false
	for _, d := range marshalWritableDirs {
		inside = inside || strings.HasPrefix(cfg.Spool, statepath.Path(root, d)+string(filepath.Separator))
	}
	if !inside {
		t.Fatalf("spool %q is outside the Marshal chat's writable directories", cfg.Spool)
	}
	// The socket's directory may not be searched: the dial is refused, as
	// the sandbox refuses it.
	dir := filepath.Dir(cfg.Endpoint)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	r := submitMCP(t, cfg, `{"submission_id":"spooled-1","proposal": {"action": "setting", "key": "acceptance-mode", "value": "marshal"}}`)
	if r.State != "admitted" || r.OccurrenceID == "" {
		t.Fatalf("spooled submit receipt = %+v", r)
	}
	if batch := w.permissions.queue.Take(false); len(batch) != 1 {
		t.Fatalf("spooled proposal did not reach the inbox: %+v", batch)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	w.closeProposalMCP()
	if _, err := os.Stat(cfg.Spool); !os.IsNotExist(err) {
		t.Fatalf("closed broker left its spool: %v", err)
	}
}

// Only Codex gets a spool: the other providers do not sandbox the MCP child
// that way, and their brokers stay socket-only.
func TestProposalMCPSpoolIsForCodexOnly(t *testing.T) {
	root := t.TempDir()
	if got := proposalMCPListenOptions("codex", root); got.SpoolParent != statepath.Path(root, "proposals") {
		t.Fatalf("codex spool parent = %q", got.SpoolParent)
	}
	for _, p := range []string{"claude", "opencode", "agy"} {
		if got := proposalMCPListenOptions(p, root); got != (proposalmcp.Options{}) {
			t.Fatalf("%s got a spool: %+v", p, got)
		}
	}
}
