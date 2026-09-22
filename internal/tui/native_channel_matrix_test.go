package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every author/reader pair, driven through the real channel.
//
// Sixteen pairs, each asserted twice: that the configuration says what it is
// expected to say, and that the file the reader actually gets matches. The
// second half is the one that matters — a filter that is right in the config
// and wrong in the rendering would leak work to a model the operator kept it
// from on purpose.
func TestChannelEveryAuthorReaderPair(t *testing.T) {
	type row struct {
		reader, author string
		visible        bool
	}
	var rows []row

	for _, reader := range knownProviders {
		for _, author := range knownProviders {
			root := t.TempDir()
			cfg, problems := loadChannelConfig(root)
			if len(problems) != 0 {
				t.Fatalf("default configuration reported problems: %v", problems)
			}
			want := cfg.canSee(reader, author)

			s, err := openStream(root)
			if err != nil {
				t.Fatal(err)
			}
			marker := author + " touched internal/store/sqlite.go"
			if _, err := s.append(author, "s", msg(marker, baseTime)); err != nil {
				t.Fatal(err)
			}
			entries, err := s.since(-1)
			if err != nil {
				t.Fatal(err)
			}
			view, err := openInboxView(root, reader, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := view.deliver(entries, cfg); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(inboxPath(root, reader))
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(string(data), marker)
			if got != want {
				t.Errorf("%s reading %s: rendered=%v, configuration says %v", reader, author, got, want)
			}
			rows = append(rows, row{reader, author, want})
		}
	}

	var b strings.Builder
	b.WriteString("\nCHANNEL VISIBILITY (default: everyone sees everyone, including themselves)\n")
	fmt.Fprintf(&b, "%-14s %-14s %s\n", "READER", "AUTHOR", "SEES")
	for _, r := range rows {
		mark := "no"
		if r.visible {
			mark = "yes"
		}
		fmt.Fprintf(&b, "%-14s %-14s %s\n", r.reader, r.author, mark)
	}
	b.WriteString("\nCAPTURE TIMING\n")
	for _, p := range knownProviders {
		when := "on exit"
		if capturesLive(p) {
			when = "live, while it runs"
		}
		fmt.Fprintf(&b, "  %-14s %s\n", p, when)
	}
	t.Log(b.String())
}

// The arrangement the operator described: a strong model sees everything, a
// weaker one is kept to a narrow slice so it is not drowned in context it
// cannot use.
func TestChannelHonoursAnAsymmetricArrangement(t *testing.T) {
	root := t.TempDir()
	cfg, problems := parseChannelConfig(`
participants: claude, codex, opencode, agy
agy: all
claude: all
codex: opencode, agy
opencode: none
`)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}

	// agy and claude see every other agent. Never themselves: an agent already
	// knows what it did, so the channel does not hand it back.
	for _, reader := range []string{"antigravity", "claude"} {
		for _, author := range knownProviders {
			want := author != reader
			if got := cfg.canSee(reader, author); got != want {
				t.Errorf("%s sees %s = %v, want %v", reader, author, got, want)
			}
		}
	}
	// codex sees only opencode and agy.
	for author, want := range map[string]bool{
		"opencode": true, "antigravity": true, "claude": false, "codex": false,
	} {
		if got := cfg.canSee("codex", author); got != want {
			t.Errorf("codex sees %s = %v, want %v", author, got, want)
		}
	}
	// opencode contributes but reads nothing.
	if !cfg.joins("opencode") {
		t.Error("opencode stopped contributing when it was set to read nothing")
	}
	for _, author := range knownProviders {
		if cfg.canSee("opencode", author) {
			t.Errorf("opencode sees %s despite being set to none", author)
		}
	}

	// Now render it, because a filter that is only right in the config is not
	// right.
	s, _ := openStream(root)
	for i, author := range knownProviders {
		if _, err := s.append(author, "s", msg(author+" wrote a line", baseTime.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := s.since(-1)

	for reader, wantCount := range map[string]int{
		"antigravity": 3, "claude": 3, "codex": 2, "opencode": 0,
	} {
		view, err := openInboxView(root, reader, true)
		if err != nil {
			t.Fatal(err)
		}
		shown, err := view.deliver(entries, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if shown != wantCount {
			t.Errorf("%s was shown %d entries, want %d", reader, shown, wantCount)
		}
	}
	data, _ := os.ReadFile(inboxPath(root, "codex"))
	if strings.Contains(string(data), "claude wrote a line") {
		t.Error("codex was shown claude's work, which this arrangement withholds")
	}
}

// Leaving the channel stops an agent's work being shared, without changing
// what anyone reads.
func TestChannelParticipantsControlContribution(t *testing.T) {
	cfg, problems := parseChannelConfig("participants: claude, codex\n")
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if cfg.joins("opencode") || cfg.joins("antigravity") {
		t.Error("an agent outside the participants list still contributes")
	}
	if !cfg.joins("claude") || !cfg.joins("codex") {
		t.Error("a listed participant does not contribute")
	}
	// A reader cannot see an author that never joined, whatever its own filter
	// says, because there is nothing of theirs in the channel.
	if cfg.canSee("claude", "opencode") {
		t.Error("an agent outside the channel was visible in it")
	}
}

// "agy" is what the operator types; "antigravity" is what MARSHAL records.
func TestChannelAcceptsTheCommandName(t *testing.T) {
	cfg, problems := parseChannelConfig("agy: claude\n")
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if !cfg.canSee("antigravity", "claude") {
		t.Error("a line written as agy did not configure antigravity")
	}
	if cfg.canSee("antigravity", "codex") {
		t.Error("the agy line did not restrict what antigravity sees")
	}
}

// A typo must not stop an agent starting, and must not quietly become a
// decision to read nothing.
func TestChannelReportsBadLinesWithoutLosingGoodOnes(t *testing.T) {
	cfg, problems := parseChannelConfig("claude: codex\nnonsense\nsonnet: codex\nclaude: gemini\n")
	if len(problems) != 3 {
		t.Fatalf("problems = %v, want three", problems)
	}
	if !cfg.canSee("claude", "codex") {
		t.Error("a good line was lost with the bad ones")
	}
}

// Every agent MARSHAL runs reaches the channel while it works.
//
// There is no exempt provider left. Claude and Codex write append-only history;
// Antigravity and OpenCode keep SQLite, opened read-only and served to readers
// under WAL. Holding one of them to a rule the others are exempt from was an
// inconsistency, and the cost of it fell on the operator.
func TestChannelEveryAgentCapturesLive(t *testing.T) {
	for _, p := range knownProviders {
		if !capturesLive(p) {
			t.Errorf("%s does not reach the channel while it runs", p)
		}
	}
}

// A peer watcher is built for the history the provider actually keeps: files
// under a directory for claude and codex, a database per conversation for agy.
func TestPeerHistoryWatchMatchesTheProviderStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude-home"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex-home"))

	for _, peer := range []string{"claude", "codex"} {
		w, err := newPeerHistoryWatch(peer, root)
		if err != nil {
			t.Fatalf("%s: %v", peer, err)
		}
		if w.antigravity {
			t.Errorf("%s was given the antigravity reader", peer)
		}
		if (peer == "claude") != w.claude {
			t.Errorf("%s claude flag = %v", peer, w.claude)
		}
	}

	w, err := newPeerHistoryWatch("antigravity", root)
	if err != nil {
		t.Fatalf("antigravity: %v", err)
	}
	if !w.antigravity {
		t.Error("antigravity was not given its own reader")
	}
	if w.antigravitySummaries == "" {
		t.Error("the antigravity watcher has no summaries database")
	}

	oc, err := newPeerHistoryWatch("opencode", root)
	if err != nil {
		t.Fatalf("opencode: %v", err)
	}
	if oc.openCodeDB == "" {
		t.Error("the opencode watcher has no store to read")
	}

	// A provider MARSHAL cannot read mid-session gets no watcher at all, rather
	// than one that would quietly read nothing.
	if _, err := newPeerHistoryWatch("gemini", root); err == nil {
		t.Error("a peer watcher was built for a provider that is not read while it runs")
	}
}
