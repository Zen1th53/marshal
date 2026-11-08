package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// The mandatory project context and the result schema share the 64 KiB model
// envelope with this packet. Keep a deterministic reserve for both.
const marshalAmendPacketBytes = 12 << 10

func proposalFromRun(run marshal.Run) marshalTaskProposal {
	var p marshalTaskProposal
	for _, task := range run.Tasks {
		item := marshalProposedTask{ID: task.PlanTaskID, Title: task.Title, Criteria: task.Criteria, Paths: task.Files, DependsOn: task.DependsOn, Worker: task.Worker, Mode: task.Mode, Type: task.EffectiveType(), Instructions: task.Instructions, ExpectedOutput: task.ExpectedOutput}
		for _, check := range task.Checks {
			item.Checks = append(item.Checks, marshalProposedCheck{Command: check.Command, Criteria: check.Criteria})
		}
		p.Tasks = append(p.Tasks, item)
	}
	return p
}

// marshalAmendPacket exposes one task to the model. The runtime reconstructs
// the complete draft from the original run after the model responds.
func marshalAmendPacket(run marshal.Run, reason string) ([]byte, int, error) {
	if len(run.Tasks) == 0 {
		// Preserve the headless fixture's legacy empty-run behavior.
		data, err := json.Marshal(run)
		return data, -1, err
	}
	target := -1
	first, _, _ := strings.Cut(strings.TrimSpace(reason), " ")
	first = strings.Trim(first, "`\"':,; ")
	for i, task := range run.Tasks {
		if task.PlanTaskID == first {
			target = i
			break
		}
	}
	if target < 0 && len(run.Tasks) == 1 {
		target = 0
	}
	if target < 0 {
		return nil, -1, fmt.Errorf("amendment needs a target task ID as the first word")
	}
	item := proposalFromRun(marshal.Run{Tasks: []marshal.Task{run.Tasks[target]}}).Tasks[0]
	packet := struct {
		PlanID      string              `json:"plan_id"`
		PlanVersion int64               `json:"plan_version"`
		Target      marshalProposedTask `json:"target_task"`
		PlanSummary []struct {
			ID    string `json:"id"`
			Title string `json:"title,omitempty"`
		} `json:"plan_summary"`
		Amendment string `json:"amendment"`
	}{PlanID: run.PlanID, PlanVersion: run.PlanVersion, Target: item, Amendment: reason}
	for _, task := range run.Tasks {
		packet.PlanSummary = append(packet.PlanSummary, struct {
			ID    string `json:"id"`
			Title string `json:"title,omitempty"`
		}{ID: task.PlanTaskID, Title: task.Title})
	}
	encode := func() ([]byte, error) { return json.Marshal(packet) }
	data, err := encode()
	if err != nil {
		return nil, -1, err
	}
	// Titles are useful orientation, but are never needed to amend the target.
	for i := len(packet.PlanSummary) - 1; len(data) > marshalAmendPacketBytes && i >= 0; i-- {
		packet.PlanSummary[i].Title = ""
		data, err = encode()
		if err != nil {
			return nil, -1, err
		}
	}
	if len(data) > marshalAmendPacketBytes {
		parts := []struct {
			name  string
			value any
		}{
			{"amendment text", reason},
			{"target task criteria", item.Criteria},
			{"target task scope", item.Paths},
			{"target task checks", item.Checks},
			{"target task instructions", item.Instructions},
			{"target task expected output", item.ExpectedOutput},
			{"plan summary task IDs", packet.PlanSummary},
		}
		field, largest := "required amendment fields together", 0
		for _, part := range parts {
			encoded, err := json.Marshal(part.value)
			if err != nil {
				return nil, -1, err
			}
			if len(encoded) > largest {
				field, largest = part.name, len(encoded)
			}
		}
		return nil, -1, fmt.Errorf("%w: %s for task %q is too large (%d bytes; amendment packet cap %d)", projectcontext.ErrBudget, field, item.ID, len(data), marshalAmendPacketBytes)
	}
	return data, target, nil
}
