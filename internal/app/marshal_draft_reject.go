package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/plan"
)

// DraftRejection is a refused Marshal draft with a reason the runtime wrote
// itself: a field path built from task and check positions and a fixed rule.
// It never carries draft text (ids, titles, criteria, commands, unknown field
// names), because the reason is handed back to the Marshal chat and shown to
// the owner. Error keeps the full underlying error for logs and callers that
// already match on it; Reason is the only part that leaves the runtime.
type DraftRejection struct {
	Path, Rule string
	err        error
}

func (e *DraftRejection) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return e.Reason()
}

func (e *DraftRejection) Unwrap() error { return e.err }

// Reason is "tasks[2].mode: <rule>": safe to repeat to the Marshal.
func (e *DraftRejection) Reason() string {
	if e.Path == "" {
		return e.Rule
	}
	return e.Path + ": " + e.Rule
}

func rejectDraft(err error, path, rule string, args ...any) error {
	if err == nil {
		err = errors.New(path + ": " + fmt.Sprintf(rule, args...))
	}
	return &DraftRejection{Path: path, Rule: fmt.Sprintf(rule, args...), err: err}
}

// draftGenericRule is the reason for a refusal the runtime cannot place on a
// field. It names nothing from the draft.
const draftGenericRule = "draft: refused by a runtime rule; check it against the draft form in your briefing"

// draftSchemaField matches a JSON path the decoder builds from the draft
// struct's own field names ("tasks.checks.command"), never from draft data.
var draftSchemaField = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)*$`)

// DraftRejectionReason is the safe reason for a draft refusal: the field path
// and rule a DraftRejection carries, a fixed description of a JSON error, or
// a generic rule. It never returns err.Error() of an unknown error, which can
// quote draft content.
func DraftRejectionReason(err error) string {
	if err == nil {
		return ""
	}
	var rejection *DraftRejection
	if errors.As(err, &rejection) {
		return rejection.Reason()
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("draft: not valid JSON (at byte %d)", syntax.Offset)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if draftSchemaField.MatchString(typeErr.Field) {
			return fmt.Sprintf("%s: wrong JSON type (want %s)", typeErr.Field, jsonKind(typeErr.Type.String()))
		}
		return "draft: a field has the wrong JSON type"
	}
	if errors.Is(err, plan.ErrPlanInvalid) {
		return "tasks: the task graph is invalid (an unsafe or repeated id, or a dependency on a missing task, on itself or in a cycle)"
	}
	return draftGenericRule
}

func jsonKind(goType string) string {
	switch {
	case strings.HasPrefix(goType, "[]"):
		return "array"
	case goType == "string" || strings.HasSuffix(goType, "Mode") || strings.HasSuffix(goType, "Type"):
		return "string"
	case strings.HasPrefix(goType, "app."), strings.HasPrefix(goType, "struct"):
		return "object"
	}
	return "another type"
}

// decodeDraftRejection describes a draft the JSON decoder refused. The
// decoder's own message quotes an unknown field's name, so it is replaced.
func decodeDraftRejection(err error) error {
	wrapped := fmt.Errorf("invalid Marshal draft JSON: %w", err)
	var syntax *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax), errors.As(err, &typeErr):
		return &DraftRejection{Rule: DraftRejectionReason(err), err: wrapped}
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return rejectDraft(wrapped, "draft", "has a field that is not in the draft form (only the fields shown in your briefing are allowed)")
	}
	return rejectDraft(wrapped, "draft", "not a JSON object of the draft form")
}

// knownWorkerName is worker when it is one of the providers the runtime
// knows, so a reason may name it; "" for anything else.
func knownWorkerName(worker string) string {
	if slices.Contains([]string{"codex", "claude", "agy", "opencode"}, worker) {
		return worker
	}
	return ""
}

// placeDraftViolation keeps err and adds the field it concerns when the
// locator finds one; otherwise DraftRejectionReason falls back to a fixed
// rule.
func placeDraftViolation(err error, proposal marshalTaskProposal) error {
	if path, rule, ok := locateDraftViolation(proposal); ok {
		return &DraftRejection{Path: path, Rule: rule, err: err}
	}
	return err
}

// locateDraftViolation places a refusal from the plan graph or the draft
// check on one field, by the same rules, without quoting the draft.
func locateDraftViolation(proposal marshalTaskProposal) (string, string, bool) {
	seen := make(map[string]int, len(proposal.Tasks))
	for i, item := range proposal.Tasks {
		if !plan.SafeTaskID(item.ID) {
			return fmt.Sprintf("tasks[%d].id", i), "not a safe task id (short letters, digits and dashes)", true
		}
		if j, ok := seen[item.ID]; ok {
			return fmt.Sprintf("tasks[%d].id", i), fmt.Sprintf("repeats the id of tasks[%d]", j), true
		}
		seen[item.ID] = i
	}
	for i, item := range proposal.Tasks {
		for k, dependency := range item.DependsOn {
			if dependency == item.ID {
				return fmt.Sprintf("tasks[%d].depends_on[%d]", i, k), "a task cannot depend on itself", true
			}
			if _, ok := seen[dependency]; !ok {
				return fmt.Sprintf("tasks[%d].depends_on[%d]", i, k), "names no task in this draft", true
			}
		}
		for j, check := range item.Checks {
			if strings.TrimSpace(check.Command) == "" {
				return fmt.Sprintf("tasks[%d].checks[%d].command", i, j), "required", true
			}
			if len(check.Criteria) == 0 {
				return fmt.Sprintf("tasks[%d].checks[%d].criteria", i, j), "required: name the task criterion this check proves", true
			}
			for k, criterion := range check.Criteria {
				if strings.TrimSpace(criterion) == "" || !slices.Contains(item.Criteria, criterion) {
					return fmt.Sprintf("tasks[%d].checks[%d].criteria[%d]", i, j, k), "not one of the task's criteria: copy it exactly from tasks[" + fmt.Sprint(i) + "].criteria", true
				}
			}
		}
	}
	return "", "", false
}
