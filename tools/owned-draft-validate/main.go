// owned-draft-validate parses a proposal file with MarshalService.DraftFromProposal and
// prints a JSON summary. It performs no Runtime, Store, admission or provider calls.
// Usage: owned-draft-validate <proposal.json> <project-id>
// Manual operator tool; not part of the release or install path.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Zen1th53/marshal/internal/app"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <proposal.json> <project-id>\n", os.Args[0])
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	s := &app.MarshalService{ProjectID: os.Args[2]}
	d, err := s.DraftFromProposal(b, "codex")
	if err != nil {
		panic(err)
	}
	result := map[string]any{"parser_pass": true, "task_count": len(d.Tasks), "canonical_state_created": false, "scope": "pure DraftFromProposal parse only, no Runtime/Store/admission/provider call"}
	b, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}
