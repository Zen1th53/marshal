package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestPlanningAdviceGoesOnlyToMarshalInbox(t *testing.T) {
	root := t.TempDir()
	if err := writeMarshalPlanningAdvice(root, "run", []string{"split independent components"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, statepath.DirName, "inbox", "marshal.md"))
	if err != nil || !strings.Contains(string(data), "split independent components") {
		t.Fatalf("missing Marshal advice: %q %v", data, err)
	}
}

func TestBoardStopKeepsWorkerStderr(t *testing.T) {
	note := marshalOutcomeNote(marshal.Run{}, errors.New("Codex failed; stderr: first: connecting; last: TLS failed"))
	reason, _, _, ok := parseKnownStopNote(note)
	if !ok || !strings.Contains(reason, "first: connecting") || !strings.Contains(reason, "last: TLS failed") {
		t.Fatalf("Board discarded stderr: %q", note)
	}
}
