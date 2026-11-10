package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestIntakeLanguageOnlyAccepted(t *testing.T) {
	for _, valid := range []string{
		`MARSHAL_PROPOSAL {"action":"intake","language":"Uzbek"}`,
		`MARSHAL_PROPOSAL {"action":"intake","language":"Uzbek","earlier_work":""}`,
		`MARSHAL_PROPOSAL {"action":"intake","language":"Uzbek","earlier_work":"yes"}`,
		`MARSHAL_PROPOSAL {"action":"intake","language":"Uzbek","earlier_work":"no"}`,
	} {
		if _, err := parseMarshalProposal(valid); err != nil {
			t.Fatalf("parseMarshalProposal rejected valid intake %s: %v", valid, err)
		}
	}

	proposal, err := parseMarshalProposal(`MARSHAL_PROPOSAL {"action":"intake","language":"Uzbek"}`)
	if err != nil {
		t.Fatalf("parseMarshalProposal rejected language-only intake: %v", err)
	}
	if proposal.action != "intake" || proposal.language != "Uzbek" || proposal.earlierWork != "" {
		t.Fatalf("parsed proposal mismatch: %+v", proposal)
	}

	w, rt := realControlWorkspace(t, "SESSION-intake-language-only")
	if err := w.saveMarshalIntake(marshalIntake{Language: proposal.language, EarlierWork: proposal.earlierWork}); err != nil {
		t.Fatalf("saveMarshalIntake failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.ProjectRoot(), statepath.DirName, "marshal-intake.json"))
	if err != nil {
		t.Fatalf("reading marshal-intake.json failed: %v", err)
	}
	var saved marshalIntake
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal intake file failed: %v", err)
	}
	if saved.Language != "Uzbek" {
		t.Fatalf("saved language = %q, want Uzbek", saved.Language)
	}
	if saved.EarlierWork != "" {
		t.Fatalf("saved earlier_work = %q, want empty", saved.EarlierWork)
	}

	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		args := marshalKickoffArgs(provider, rt.ProjectRoot())
		if len(args) == 0 || args[len(args)-1] != marshalKickoffContinue {
			t.Fatalf("%s marshalKickoffArgs = %v, want last element %q", provider, args, marshalKickoffContinue)
		}
	}

	// Also verify the proposal file pickup channel works with language-only intake.
	dir := filepath.Join(rt.ProjectRoot(), statepath.DirName, "proposals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lang-only.json"), []byte(`{"action":"intake","language":"French"}`), 0600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())

	data, err = os.ReadFile(filepath.Join(rt.ProjectRoot(), statepath.DirName, "marshal-intake.json"))
	if err != nil {
		t.Fatalf("reading marshal-intake.json after file pickup failed: %v", err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal intake file after file pickup failed: %v", err)
	}
	if saved.Language != "French" {
		t.Fatalf("saved language after file pickup = %q, want French", saved.Language)
	}
}

func TestIntakeLanguageOnlyKeepsEarlierWork(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-intake-keeps-earlier-work")
	if err := w.saveMarshalIntake(marshalIntake{Language: "English", EarlierWork: "yes"}); err != nil {
		t.Fatalf("initial saveMarshalIntake failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.ProjectRoot(), statepath.DirName, "marshal-intake.json"))
	if err != nil {
		t.Fatalf("reading initial marshal-intake.json failed: %v", err)
	}
	var initial marshalIntake
	if err := json.Unmarshal(data, &initial); err != nil {
		t.Fatalf("unmarshal initial intake file failed: %v", err)
	}
	if initial.EarlierWork != "yes" {
		t.Fatalf("initial earlier_work = %q, want yes", initial.EarlierWork)
	}

	proposal, err := parseMarshalProposal(`MARSHAL_PROPOSAL {"action":"intake","language":"Uzbek"}`)
	if err != nil {
		t.Fatalf("parseMarshalProposal rejected language-only intake: %v", err)
	}
	if err := w.saveMarshalIntake(marshalIntake{Language: proposal.language, EarlierWork: proposal.earlierWork}); err != nil {
		t.Fatalf("second saveMarshalIntake failed: %v", err)
	}

	data, err = os.ReadFile(filepath.Join(rt.ProjectRoot(), statepath.DirName, "marshal-intake.json"))
	if err != nil {
		t.Fatalf("reading updated marshal-intake.json failed: %v", err)
	}
	var updated marshalIntake
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatalf("unmarshal updated intake file failed: %v", err)
	}
	if updated.Language != "Uzbek" {
		t.Fatalf("updated language = %q, want Uzbek", updated.Language)
	}
	if updated.EarlierWork != "yes" {
		t.Fatalf("updated earlier_work = %q, want yes", updated.EarlierWork)
	}
}

func TestIntakeRejectsUnknownKey(t *testing.T) {
	for _, bad := range []string{
		`MARSHAL_PROPOSAL {"action":"intake","language":"English","x":"1"}`,
		`MARSHAL_PROPOSAL {"action":"intake","language":"English","earlier_work":"yes","x":"1"}`,
		`MARSHAL_PROPOSAL {"action":"intake","x":"1"}`,
		`MARSHAL_PROPOSAL {"action":"intake"}`,
		`MARSHAL_PROPOSAL {"action":"intake","language":""}`,
		`MARSHAL_PROPOSAL {"action":"intake","language":"   "}`,
		`MARSHAL_PROPOSAL {"action":"intake","language":"English","earlier_work":"maybe"}`,
	} {
		if _, err := parseMarshalProposal(bad); err == nil {
			t.Errorf("parseMarshalProposal accepted invalid intake: %s", bad)
		}
	}
}
