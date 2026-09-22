package tui

import (
	"os"
	"strings"
	"testing"
)

// With no file, every provider that can be told is told. A missing preference
// is not a decision to stop delivering.
func TestLivePeersDefaultsToEveryEligibleSender(t *testing.T) {
	matrix, problems := loadLivePeers(t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("an absent file reported problems: %v", problems)
	}
	if got := matrix.sendersFor("claude"); len(got) != 1 || got[0] != "codex" {
		t.Errorf("claude receives from %v, want [codex]", got)
	}
	if got := matrix.receiversFor("claude"); len(got) != 3 {
		t.Errorf("claude delivers to %v, want codex, opencode and antigravity", got)
	}
	if matrix.delivers("claude", "claude") {
		t.Error("a provider was told about its own work")
	}
}

// Receiving and sending are separate capabilities. OpenCode can be told what
// the others did; its own history cannot be read while it runs, so it cannot
// tell them.
func TestLivePeersOpenCodeReceivesButCannotSend(t *testing.T) {
	matrix := livePeerMatrix{}
	if !matrix.delivers("codex", "opencode") {
		t.Error("opencode was not offered codex's work")
	}
	if matrix.delivers("opencode", "codex") {
		t.Error("opencode was accepted as a live sender")
	}
	if _, bad := parseLiveSenders("codex", "opencode"); len(bad) != 1 || bad[0] != "opencode" {
		t.Errorf("configuring opencode as a sender was not refused, bad = %v", bad)
	}
}

// The two directions are set separately and are allowed to disagree.
func TestLivePeersDirectionsAreIndependent(t *testing.T) {
	matrix, problems := parseLivePeers("claude: codex\ncodex: none\n")
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if !matrix.delivers("codex", "claude") {
		t.Error("claude should receive codex")
	}
	if matrix.delivers("claude", "codex") {
		t.Error("codex asked for none and was delivered to anyway")
	}
}

func TestLivePeersRoundTripsThroughTheFile(t *testing.T) {
	root := t.TempDir()
	if err := saveLivePeers(root, livePeerMatrix{"codex": nil}); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(livePeerPath(root))
	if err != nil {
		t.Fatal(err)
	}
	// Every receiver is spelled out, so the file shows the whole decision
	// rather than the part that differs from a default the reader must know.
	for _, receiver := range liveReceivers {
		if !strings.Contains(string(data), receiver+":") {
			t.Errorf("%s is missing from the saved matrix:\n%s", receiver, data)
		}
	}
	if !strings.Contains(string(data), "codex: none") {
		t.Errorf("an empty sender list was not written as none:\n%s", data)
	}

	reloaded, problems := loadLivePeers(root)
	if len(problems) != 0 {
		t.Fatalf("reload reported problems: %v", problems)
	}
	if reloaded.delivers("claude", "codex") {
		t.Error("codex received after being set to none")
	}
	if !reloaded.delivers("codex", "claude") {
		t.Error("claude stopped receiving codex, which was never asked for")
	}
}

// A typo must not stop an agent starting. The line is skipped, the rest of the
// file still applies, and the caller is told what was ignored.
func TestLivePeersReportsBadLinesWithoutFailing(t *testing.T) {
	matrix, problems := parseLivePeers("claude: codex\nnonsense\nsonnet: codex\nclaude: gemini\n")
	if len(problems) != 3 {
		t.Fatalf("problems = %v, want three", problems)
	}
	if !matrix.delivers("codex", "claude") {
		t.Error("a good line was lost with the bad ones")
	}
}

func TestLiveStatusRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := writeLiveStatus(root, "claude", liveStatus{Imported: 12, Delivered: 3}); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, err := readLiveStatus(root, "claude")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if status.Provider != "claude" || status.Imported != 12 || status.Delivered != 3 {
		t.Errorf("status = %+v", status)
	}
}
