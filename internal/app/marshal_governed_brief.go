package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// governedBriefBytes bounds what a Marshal run adds to a Process 05 worker's
// provider context beyond the canonical constraint header and approved task
// instructions: the task's approved plan note and why earlier attempts were
// returned. It matches the other optional worker context blocks.
const governedBriefBytes = 4096

var errGovernedBriefTooLarge = errors.New("governed worker brief exceeds its bound")

// governedNoteHeader introduces the approved plan note. Approved instructions
// refer to it as tasks/<id>.md, a file of the private plan pack that the
// worker's worktree never contains, so its text is delivered here instead.
func governedNoteHeader(taskID string) string {
	return fmt.Sprintf("\nApproved plan note tasks/%s.md (the file is not in your worktree; this is its full approved text):\n", taskID)
}

const governedReturnedHeader = "\nEarlier attempts at this task were returned for these reasons. Address each one:\n"

// governedPlanNoteBlock renders the whole approved note or fails closed: a
// note is never cut or dropped, because its required paths and its "leave
// alone" list are what the worker is held to.
func governedPlanNoteBlock(taskID, note string) (string, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return "", nil
	}
	block := governedNoteHeader(taskID) + note + "\n"
	if len(block) > governedBriefBytes {
		return "", fmt.Errorf("%w: approved plan note tasks/%s.md needs %d bytes, a governed worker brief carries at most %d; shorten the note and amend the plan", errGovernedBriefTooLarge, taskID, len(block), governedBriefBytes)
	}
	return block, nil
}

// renderGovernedBrief adds the newest return reasons that fit after the note.
// Older reasons that do not fit are counted, never silently lost.
func renderGovernedBrief(taskID, note string, returned []string) (string, error) {
	block, err := governedPlanNoteBlock(taskID, note)
	if err != nil {
		return "", err
	}
	var reasons []string
	for _, reason := range returned {
		if reason = strings.TrimSpace(reason); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	if len(reasons) == 0 {
		return block, nil
	}
	omittedLine := func(n int) string { return fmt.Sprintf("- (%d older reasons omitted)\n", n) }
	room := governedBriefBytes - len(block) - len(governedReturnedHeader) - len(omittedLine(len(reasons)))
	var kept []string
	used := 0
	for i := len(reasons) - 1; i >= 0; i-- {
		line := "- " + reasons[i] + "\n"
		if used+len(line) > room {
			break
		}
		kept = append([]string{line}, kept...)
		used += len(line)
	}
	if len(kept) == 0 {
		return block, nil
	}
	block += governedReturnedHeader
	if omitted := len(reasons) - len(kept); omitted > 0 {
		block += omittedLine(omitted)
	}
	return block + strings.Join(kept, ""), nil
}

// validateGovernedPlanNotes refuses a draft whose governed task note could
// never be delivered, before anything is approved or dispatched.
func validateGovernedPlanNotes(pack *marshal.PlanPack, tasks []marshal.Task) error {
	if pack == nil {
		return nil
	}
	for _, t := range tasks {
		if t.Mode != marshal.Governed {
			continue
		}
		if _, err := governedPlanNoteBlock(t.PlanTaskID, pack.Tasks[t.PlanTaskID]); err != nil {
			return err
		}
	}
	return nil
}

type process05BriefKey struct{}

type process05Brief struct {
	taskID string
	text   string
}

// withProcess05Brief binds a rendered brief to one plan task of the governed
// dispatch; a provider turn for any other task never receives it.
func withProcess05Brief(ctx context.Context, taskID, text string) context.Context {
	if text == "" {
		return ctx
	}
	return context.WithValue(ctx, process05BriefKey{}, process05Brief{taskID: taskID, text: text})
}

func process05BriefFor(ctx context.Context, task execution.TaskExecution) string {
	brief, ok := ctx.Value(process05BriefKey{}).(process05Brief)
	if !ok || brief.taskID == "" || brief.taskID != task.TaskID {
		return ""
	}
	return brief.text
}

// process05Brief renders the governed brief for a Process 05 dispatch from
// the plan pack stored with the approved run and the task's durable return
// reasons. The pack digest is checked; the note passes the evidence
// sanitizer so a known secret never reaches a provider.
func (r *Runtime) process05Brief(ctx context.Context, s *MarshalService, runID string, run marshal.Run, task marshal.Task) (string, error) {
	pack, err := s.refreshPlanPack(runID, run)
	if err != nil {
		return "", err
	}
	note := ""
	if pack != nil {
		note = pack.Tasks[task.PlanTaskID]
	}
	returned, err := s.taskReturnReasons(ctx, runID, task)
	if err != nil {
		return "", err
	}
	text, err := renderGovernedBrief(task.PlanTaskID, note, returned)
	if err != nil || text == "" {
		return "", err
	}
	clean, err := r.sanitizeProviderOutput(ctx, []byte(text))
	if err != nil {
		return "", fmt.Errorf("process 05: governed brief for task %s rejected by the evidence sanitizer: %w", task.PlanTaskID, err)
	}
	return string(clean), nil
}
