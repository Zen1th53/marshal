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
	transcript := importer.SessionTranscript{Provider: "opencode", SessionID: "producer-session", Messages: []importer.Message{{Role: "assistant", Content: "OWN_WORK", Timestamp: time.Now()}}}
	if err := publishNativeTranscript(stream, transcript); err != nil {
		t.Fatal(err)
	}
	if err := publishNativeTranscript(stream, transcript); err != nil {
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
