package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestNativeTranscriptPublicationUsesProducer(t *testing.T) {
	root := t.TempDir()
	stream, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	transcript := importer.SessionTranscript{Provider: "openai", SessionID: "producer-session", Messages: []importer.Message{{Role: "assistant", Content: "OWN_WORK", Timestamp: time.Now()}}}
	if err := publishNativeTranscript(stream, "opencode", transcript); err != nil {
		t.Fatal(err)
	}
	if err := publishNativeTranscript(stream, "opencode", transcript); err != nil {
		t.Fatal(err)
	}
	entries, err := stream.since(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Provider != "opencode" || entries[0].Session != "producer-session" {
		t.Fatalf("wrong authorship: %+v", entries)
	}
	for _, reader := range knownProviders {
		view, err := openInboxView(root, reader, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg := newChannelConfig()
		if reader == "claude" {
			cfg.sees[reader] = nil
		}
		if _, err := view.deliver(entries, cfg); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(view.path)
		if err != nil {
			t.Fatal(err)
		}
		want := reader != "opencode" && reader != "claude"
		if strings.Contains(string(data), "OWN_WORK") != want {
			t.Fatalf("reader %s visibility wrong: %s", reader, data)
		}
	}
}

func TestConcurrentChannelObserversDeduplicateProducer(t *testing.T) {
	root := t.TempDir()
	a, _ := openStream(root)
	b, _ := openStream(root)
	tr := importer.SessionTranscript{Provider: "codex", SessionID: "same", Messages: []importer.Message{{Role: "assistant", Content: "one", Timestamp: time.Now()}}}
	if err := publishNativeTranscript(a, "codex", tr); err != nil {
		t.Fatal(err)
	}
	if err := publishNativeTranscript(b, "codex", tr); err != nil {
		t.Fatal(err)
	}
	entries, _ := a.since(-1)
	if len(entries) != 1 {
		t.Fatalf("same producer duplicated by observers: %+v", entries)
	}
}

func TestHarnessVerifyCommandSyntax(t *testing.T) {
	if usage := configCommandUsage([]string{"/harness", "verify", "opencode", "opencode/nemotron-3-ultra-free"}); usage != "" {
		t.Fatal(usage)
	}
	for _, args := range [][]string{{"/harness", "verify"}, {"/harness", "verify", "opencode", "model", "extra"}} {
		if configCommandUsage(args) == "" {
			t.Fatalf("invalid verify syntax accepted: %v", args)
		}
	}
}

func TestNoneRemovesPreviouslyVisiblePeerWork(t *testing.T) {
	root := t.TempDir()
	s, _ := openStream(root)
	if _, err := s.append("codex", "s", msg("OLD_PEER_WORK", baseTime)); err != nil {
		t.Fatal(err)
	}
	view, _ := openInboxView(root, "claude", false)
	entries, _ := s.since(-1)
	if _, err := view.deliver(entries, newChannelConfig()); err != nil {
		t.Fatal(err)
	}
	cfg := newChannelConfig()
	cfg.sees["claude"] = nil
	if err := saveChannelConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(view.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "OLD_PEER_WORK") {
		t.Fatal("none leaves previously visible peer work in the inbox")
	}
}
