package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The Web Board's run details are read from what the run retained: the
// pause, the MCP failure, each task's latest hand-in and review, and the
// project's recent conversation. Nothing here runs a command or touches Git;
// a diff is the one the hand-in kept, paged by pageTaskDiff. The page's run
// scope is the run id; a diff is bound to its task and result commit.

// maxDetailAttempts bounds the hand-in attempts read per task.
const maxDetailAttempts = 32

func (w *Workspace) webDetailsSource() webboard.DetailsSource {
	return webboard.DetailsSource{
		Scope:    func(ctx context.Context) string { _, id, _ := w.webDetailRun(ctx); return id },
		Details:  w.webRunDetails,
		Diff:     w.webTaskDiff,
		Messages: w.webRetainedMessages,
	}
}

// webDetailRun is the Board's run now, with the service that holds it.
func (w *Workspace) webDetailRun(ctx context.Context) (*app.MarshalService, string, marshal.Run) {
	if w.runtime == nil {
		return nil, "", marshal.Run{}
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID, service := m.runID, m.service
	m.mu.Unlock()
	if service == nil {
		service = w.runtime.Marshal()
	}
	if service == nil || runID == "" {
		return nil, "", marshal.Run{}
	}
	record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
	if err != nil {
		return nil, "", marshal.Run{}
	}
	return service, runID, record.Value
}

// latestAttempt is the newest stored hand-in of a task and its attempt.
func latestAttempt(ctx context.Context, service *app.MarshalService, runID string, t marshal.Task) (marshal.HandIn, int, bool) {
	var found marshal.HandIn
	n := 0
	for attempt := t.EvidenceAttemptBase + 1; attempt <= t.EvidenceAttemptBase+maxDetailAttempts; attempt++ {
		stored, err := service.Store.GetMarshalHandIn(ctx, runID, t.PlanTaskID, attempt)
		if err != nil {
			break
		}
		found, n = stored.Value, attempt
	}
	return found, n, n > 0
}

func limitLines(limits []model.Limit) []string {
	var out []string
	for _, l := range limits {
		out = append(out, strings.TrimSpace(l.Subject+": kept "+fmt.Sprint(l.KeptBytes)+" of "+fmt.Sprint(l.ActualBytes)+" bytes. "+l.Action))
	}
	return out
}

// webRunDetails is the run's details; with no run it still carries the MCP
// failure.
func (w *Workspace) webRunDetails(ctx context.Context) webboard.RunDetails {
	d := webboard.RunDetails{MCP: w.mcpFailure()}
	service, runID, run := w.webDetailRun(ctx)
	if service == nil {
		return d
	}
	d.Run, d.Scope = runID, runID
	if p := run.Pause; p != nil && run.State == marshal.AwaitingUser {
		d.Paused = &webboard.PausedDetail{Kind: p.Kind, Code: p.Code, Reason: p.Reason, Raw: p.Detail, Resolutions: p.Resolutions}
		if p.Limit != nil {
			d.Paused.Raw = strings.TrimSpace(d.Paused.Raw + "\n" + strings.Join(limitLines([]model.Limit{*p.Limit}), "\n"))
		}
	}
	for _, t := range run.Tasks {
		e := webboard.TaskEvidence{Task: t.PlanTaskID, Title: t.Title, State: string(t.State), Scope: t.Files}
		for _, c := range t.Checks {
			e.Checks = append(e.Checks, c.Command)
		}
		if h, attempt, ok := latestAttempt(ctx, service, runID, t); ok {
			e.Changed, e.Base, e.Result = h.FilesTouched, h.BaseCommit, h.ResultCommit
			e.Limits = limitLines(h.Limits)
			e.Checks = e.Checks[:0]
			for _, c := range h.CheckResults {
				verdict := "failed"
				if c.Passed {
					verdict = "passed"
				}
				e.Checks = append(e.Checks, c.Command+" — "+verdict)
			}
			if h.ResultCommit != "" {
				e.DiffSubject = t.PlanTaskID + "@" + h.ResultCommit
			}
			if r, err := service.Store.GetMarshalReview(ctx, runID, t.PlanTaskID, attempt); err == nil {
				e.Verdict, e.Reviewer, e.Reasons = string(r.Value.Verdict), r.Value.Reviewer, r.Value.Reasons
				if ind := r.Value.Independent; ind != nil {
					e.Reasons = append(e.Reasons, "Independent review ("+string(ind.Verdict)+"): "+strings.Join(ind.Reasons, "; "))
				}
			}
		}
		d.Tasks = append(d.Tasks, e)
	}
	return d
}

// webTaskDiff is one page of the retained diff of subject ("task@commit"),
// still the task's latest reviewed result.
func (w *Workspace) webTaskDiff(ctx context.Context, subject, cursor string) (webboard.DiffPart, error) {
	task, commit, ok := strings.Cut(subject, "@")
	service, runID, run := w.webDetailRun(ctx)
	if !ok || service == nil {
		return webboard.DiffPart{}, webboard.ErrStaleDetails
	}
	for _, t := range run.Tasks {
		if t.PlanTaskID != task {
			continue
		}
		h, _, found := latestAttempt(ctx, service, runID, t)
		if !found || h.ResultCommit != commit {
			return webboard.DiffPart{}, webboard.ErrStaleDetails
		}
		if h.Diff == "" {
			return webboard.DiffPart{Note: "The diff was not retained; inspect commit " + commit + " in the repository."}, nil
		}
		text, limit, err := pageTaskDiff(task, commit, h.Diff, cursor)
		if err != nil {
			return webboard.DiffPart{}, errors.Join(webboard.ErrStaleDetails, err)
		}
		part := webboard.DiffPart{Text: text}
		if limit != nil {
			part.Next, part.Note = limit.Cursor, limit.Action
		}
		return part, nil
	}
	return webboard.DiffPart{}, webboard.ErrStaleDetails
}

// webRetainedMessages is the project conversation the workspace keeps,
// oldest first. Earlier history is not retained, and the page says so.
func (w *Workspace) webRetainedMessages(context.Context) webboard.Retained {
	state := w.GetUIState()
	r := webboard.Retained{Session: state.SessionID, Unavailable: "Only the most recent messages are retained here; earlier conversation is not available."}
	for _, m := range state.RecentMessages {
		if r.Base == "" {
			r.Base = m.ID
		}
		r.Messages = append(r.Messages, webboard.Message{Role: m.From.String(), Text: m.Content, At: m.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return r
}
