package inspector

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// codeEvidenceLines is the inspector's code evidence part of the EVIDENCE
// section: the latest validation of the task's evidence receipts (holds,
// invalidated by other merged work, unknown) and whether the result was
// advisory or returned the task. Nothing shows for a task without receipts,
// and Community records none.
func codeEvidenceLines(b model.Board, taskID string) []string {
	receipts := 0
	var last *events.Event
	for i := range b.Events {
		e := &b.Events[i]
		if e.TaskID != taskID {
			continue
		}
		switch e.Type {
		case events.EventTypeMarshalEvidenceReceipt:
			receipts++
		case events.EventTypeMarshalEvidenceValidated:
			last = e
		}
	}
	if receipts == 0 && last == nil {
		return nil
	}
	if last == nil {
		return []string{fmt.Sprintf("  code evidence: %d receipts · not validated yet", receipts)}
	}
	phase, _ := last.Data["phase"].(string)
	policy, _ := last.Data["policy"].(string)
	if policy == "" {
		policy = "advisory"
	}
	line := fmt.Sprintf("  code evidence (%s, %s): %d holds · %d invalidated · %d unknown", phase, policy, count(last.Data["holds"]), count(last.Data["invalidated"]), count(last.Data["unknown"]))
	out := []string{line}
	if own := count(last.Data["own"]); own > 0 {
		out = append(out, fmt.Sprintf("    %d changed by the task itself", own))
	}
	if paths, ok := last.Data["paths"].([]any); ok && len(paths) > 0 {
		names := make([]string, 0, len(paths))
		for _, p := range paths {
			if s, ok := p.(string); ok {
				names = append(names, s)
			}
		}
		out = append(out, "    changed by other work: "+displayList(names))
	} else if paths, ok := last.Data["paths"].([]string); ok && len(paths) > 0 {
		out = append(out, "    changed by other work: "+displayList(paths))
	}
	if msg, _ := last.Data["error"].(string); msg != "" {
		out = append(out, "    validation unavailable: "+msg)
	}
	return out
}

func count(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
