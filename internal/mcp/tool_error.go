package mcp

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// toolErrorCode is the JSON-RPC code every failed Marshal tool call carries.
const toolErrorCode = -32000

// rpcErrData keeps the stable machine code of a translated tool error, so a
// caller that parses codes still can while the message stays a plain sentence.
type rpcErrData struct {
	Code string `json:"code"`
}

// toolError turns an error from one of Marshal's own tools into the error the
// Marshal model receives. The model's chat shows this message to the owner, so
// a known internal code becomes a plain sentence that says what to do next.
// Live proposal errors cross the parent transport as text, so each code is
// matched by its sentinel's text as well as by errors.Is.
func toolError(tool string, err error) *rpcErr {
	msg := err.Error()
	for _, known := range []struct {
		sentinel error
		text     string
	}{
		{projectcontext.ErrReviewPending, "The project context is not ready yet: the architecture or a required project memory still waits for the owner's review. Carry on with the intake, submit the architecture if it is not submitted yet, and read marshal_project_contract_context again after the owner accepts it."},
		{projectcontext.ErrMissing, "Part of the required project context is missing, so it cannot be read now. Read marshal_project_contract_inventory and submit an updated architecture, or ask the owner."},
		{projectcontext.ErrChanged, "The project changed while this was being read (the Goal, the architecture or the project files). Read marshal_project_contract_context again, then retry."},
		{projectcontext.ErrBudget, "The reply would be too large to send. Ask for a smaller page (a lower limit) or send a shorter proposal."},
		{projectcontext.ErrInvalid, "The project context did not pass its checks. Read marshal_project_contract_context again; if this repeats, tell the owner."},
	} {
		code := known.sentinel.Error()
		if errors.Is(err, known.sentinel) || strings.Contains(msg, code) {
			text := known.text
			if detail := contextDetail(msg, code); detail != "" {
				switch {
				case strings.HasPrefix(detail, projectcontext.RequiredMemoryDetail):
					// One named reference refused: say which and what to do,
					// in place of the general advice that cannot fix it.
					text = "The project contract was not accepted: " + detail + "."
				case known.sentinel == projectcontext.ErrInvalid:
					// The runtime's own explanation of a refused draft.
					text = "The project context did not pass its checks: " + detail + ". Fix that and submit again with a new submission_id."
				}
			}
			return &rpcErr{Code: toolErrorCode, Message: text, Data: &rpcErrData{Code: code}}
		}
	}
	if errors.Is(err, sql.ErrNoRows) || strings.Contains(msg, sql.ErrNoRows.Error()) {
		text := "Nothing matches that ID. Check the ID and try again."
		if strings.HasSuffix(tool, "_receipt") {
			text = "Nothing was submitted with that submission_id in this chat. Pass the same submission_id you used when you submitted."
		}
		return &rpcErr{Code: toolErrorCode, Message: text, Data: &rpcErrData{Code: "not_found"}}
	}
	if detail, ok := sentinelDetail(err, msg, model.ErrNotFound); ok {
		text := "Nothing was found. Check the ID and try again."
		if detail != "" {
			text = "Nothing was found for " + detail + ". Check the ID and try again."
		}
		if strings.HasPrefix(tool, "task") {
			text = strings.TrimSuffix(text, " Check the ID and try again.") + " Call tasks_list to see the current task IDs."
		}
		return &rpcErr{Code: toolErrorCode, Message: text, Data: &rpcErrData{Code: "not_found"}}
	}
	if detail, ok := sentinelDetail(err, msg, model.ErrInvalid); ok {
		text := "The arguments for " + tool + " are not valid. Check them against the tool's description and try again."
		if detail != "" {
			text = "The arguments for " + tool + " are not valid: " + detail + "."
		}
		return &rpcErr{Code: toolErrorCode, Message: text, Data: &rpcErrData{Code: "invalid_input"}}
	}
	return &rpcErr{Code: toolErrorCode, Message: msg}
}

// contextDetail is the text after "code: " in msg, without a trailing period,
// or "" when the code carries no detail.
func contextDetail(msg, code string) string {
	i := strings.Index(msg, code+": ")
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSpace(msg[i+len(code)+2:]), ".")
}

// sentinelDetail reports whether msg is the sentinel's text, alone or followed
// by ": detail", or wraps it, and returns the detail without a trailing period.
func sentinelDetail(err error, msg string, sentinel error) (string, bool) {
	code := sentinel.Error()
	switch {
	case msg == code:
		return "", true
	case strings.HasPrefix(msg, code+": "):
		return strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(msg, code+": ")), "."), true
	case errors.Is(err, sentinel):
		return strings.TrimSuffix(strings.TrimSuffix(msg, ": "+code), "."), true
	}
	return "", false
}
