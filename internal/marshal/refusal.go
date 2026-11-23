package marshal

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// OperationHolder names the operation holding the session lock or the
// runtime lifecycle operation journal.
type OperationHolder struct {
	ID        string    `json:"id"`
	RunID     string    `json:"runId,omitempty"`
	TaskID    string    `json:"taskId,omitempty"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	StartedAt time.Time `json:"startedAt,omitempty"`
}

// RefusalEffects describes whether a side effect may have started before the
// operation was refused. Only pre-effect blockers may claim EffectsNone.
type RefusalEffects string

const (
	EffectsNone    RefusalEffects = "none"
	EffectsPartial RefusalEffects = "partial"
	EffectsUnknown RefusalEffects = "unknown"
)

// Stable machine codes for operation and proposal refusals.
const (
	RefusalCodeBusy               = "operation_busy"
	RefusalCodePausedByOwner      = "paused_by_owner"
	RefusalCodeInterruptedOp      = "interrupted_operation"
	RefusalCodeParseInvalid       = "invalid_proposal"
	RefusalCodeUnauthorized       = "unauthorized"
	RefusalCodeStaleBinding       = "stale_binding"
	RefusalCodeStaleBase          = "stale_base"
	RefusalCodeRepeatedFailure    = "repeated_failure_escalated"
	RefusalCodeCommandBusy        = "command_busy"
	RefusalCodePreconditionFailed = "precondition_failed"
)

// RefusalReceipt carries the structured details of a refused operation or
// proposal across command responses, proposal receipts, inbox notices,
// Web replies, MCP tool errors, and Activity.
type RefusalReceipt struct {
	Code         string           `json:"code"`
	Blocker      *OperationHolder `json:"blocker,omitempty"`
	Effects      RefusalEffects   `json:"effects"`
	NextCommands []string         `json:"nextCommands,omitempty"`
	EscalationID string           `json:"escalationId,omitempty"`
	Attempt      int              `json:"attempt,omitempty"`
	Message      string           `json:"message,omitempty"`
}

const maxRefusalTextBytes = 512

// SanitizeRefusalText collapses control characters and whitespace into a
// bounded single-line string safe for logs, receipts, and UI notices.
func SanitizeRefusalText(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len(cleaned) > maxRefusalTextBytes {
		cut := maxRefusalTextBytes
		for cut > 0 && !utf8.RuneStart(cleaned[cut]) {
			cut--
		}
		cleaned = strings.TrimSpace(cleaned[:cut])
	}
	return cleaned
}

// Clone returns an independent copy of the holder.
func (h *OperationHolder) Clone() *OperationHolder {
	if h == nil {
		return nil
	}
	cp := *h
	return &cp
}

// Sanitized returns a sanitized copy of the holder.
func (h *OperationHolder) Sanitized() *OperationHolder {
	if h == nil {
		return nil
	}
	return &OperationHolder{
		ID:        SanitizeRefusalText(h.ID),
		RunID:     SanitizeRefusalText(h.RunID),
		TaskID:    SanitizeRefusalText(h.TaskID),
		Kind:      SanitizeRefusalText(h.Kind),
		Title:     SanitizeRefusalText(h.Title),
		StartedAt: h.StartedAt.UTC(),
	}
}

// Label formats the blocking operation as a concise human-readable identifier.
func (h *OperationHolder) Label() string {
	if h == nil {
		return ""
	}
	title := SanitizeRefusalText(h.Title)
	kind := SanitizeRefusalText(h.Kind)
	if title == "" {
		title = kind
	}
	if title == "" {
		title = "Marshal operation"
	}
	var meta []string
	if kind != "" {
		meta = append(meta, "kind="+kind)
	}
	if id := SanitizeRefusalText(h.ID); id != "" {
		meta = append(meta, "id="+id)
	}
	if runID := SanitizeRefusalText(h.RunID); runID != "" {
		meta = append(meta, "run="+runID)
	}
	if taskID := SanitizeRefusalText(h.TaskID); taskID != "" {
		meta = append(meta, "task="+taskID)
	}
	if len(meta) == 0 {
		return title
	}
	return fmt.Sprintf("%s (%s)", title, strings.Join(meta, ", "))
}

// Clone returns a deep copy of the receipt.
func (r RefusalReceipt) Clone() RefusalReceipt {
	cp := r
	cp.Blocker = r.Blocker.Clone()
	if len(r.NextCommands) > 0 {
		cp.NextCommands = append([]string(nil), r.NextCommands...)
	}
	return cp
}

// Sanitized returns a copy of the receipt with all text fields sanitized and
// a safe default for Effects (never claiming none unless explicitly set).
func (r RefusalReceipt) Sanitized() RefusalReceipt {
	cp := RefusalReceipt{
		Code:         SanitizeRefusalText(r.Code),
		Blocker:      r.Blocker.Sanitized(),
		Effects:      r.Effects,
		EscalationID: SanitizeRefusalText(r.EscalationID),
		Attempt:      r.Attempt,
		Message:      SanitizeRefusalText(r.Message),
	}
	switch cp.Effects {
	case EffectsNone, EffectsPartial, EffectsUnknown:
	default:
		cp.Effects = EffectsUnknown
	}
	if len(r.NextCommands) > 0 {
		cp.NextCommands = make([]string, 0, len(r.NextCommands))
		for _, cmd := range r.NextCommands {
			if c := SanitizeRefusalText(cmd); c != "" {
				cp.NextCommands = append(cp.NextCommands, c)
			}
		}
	}
	return cp
}

// EffectsText describes the receipt's effect guarantee in plain words.
func (r RefusalReceipt) EffectsText() string {
	switch r.Effects {
	case EffectsNone:
		return "no effects applied"
	case EffectsPartial:
		return "partial effects may have applied"
	default:
		return "effects unknown"
	}
}

// Summary formats the receipt into one sanitized human-readable line naming
// the cause, blocker, effects, attempt/escalation, and exact next commands.
func (r RefusalReceipt) Summary() string {
	s := r.Sanitized()
	var parts []string
	head := s.Message
	if s.Blocker != nil {
		label := s.Blocker.Label()
		switch {
		case head == "":
			head = label
		case !strings.Contains(head, label):
			head = head + ": " + label
		}
	}
	if head != "" {
		parts = append(parts, head)
	}
	parts = append(parts, s.EffectsText())
	if s.Attempt > 0 {
		parts = append(parts, fmt.Sprintf("attempt %d", s.Attempt))
	}
	if s.EscalationID != "" {
		parts = append(parts, "escalation "+s.EscalationID)
	}
	if len(s.NextCommands) > 0 {
		parts = append(parts, "next: "+strings.Join(s.NextCommands, ", "))
	}
	return strings.Join(parts, "; ")
}

// RefusalError wraps an underlying error with a typed RefusalReceipt while
// preserving errors.Is and errors.As matching against the wrapped cause.
type RefusalError struct {
	Receipt  RefusalReceipt
	Cause    error
	Replayed bool
}

// NewRefusalError constructs a sanitized RefusalError wrapping cause.
func NewRefusalError(receipt RefusalReceipt, cause error) *RefusalError {
	if receipt.Message == "" && cause != nil {
		receipt.Message = cause.Error()
	}
	return &RefusalError{
		Receipt: receipt.Sanitized(),
		Cause:   cause,
	}
}

func (e *RefusalError) Error() string {
	if e == nil {
		return ""
	}
	return e.Receipt.Summary()
}

func (e *RefusalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// RefusalOf extracts the typed RefusalReceipt from err if it carries one.
func RefusalOf(err error) (RefusalReceipt, bool) {
	var ref *RefusalError
	if errors.As(err, &ref) && ref != nil {
		return ref.Receipt.Clone(), true
	}
	return RefusalReceipt{}, false
}

// IsReplayedEscalation reports whether err represents a replayed terminal
// escalation receipt whose chat/escalation notice was already emitted.
func IsReplayedEscalation(err error) bool {
	var ref *RefusalError
	return errors.As(err, &ref) && ref != nil && ref.Replayed
}

// ParseRefusalSummary reconstructs a RefusalReceipt from a refusal error that
// crossed a text-only process boundary.
func ParseRefusalSummary(msg string) (RefusalReceipt, bool) {
	clean := SanitizeRefusalText(msg)
	if clean == "" {
		return RefusalReceipt{}, false
	}
	var r RefusalReceipt
	switch {
	case strings.Contains(clean, "automatic retries stopped after "):
		r.Code = RefusalCodeRepeatedFailure
		r.Effects = EffectsUnknown
	case strings.Contains(clean, "the run is paused by you"):
		r.Code = RefusalCodePausedByOwner
		r.Effects = EffectsNone
		r.NextCommands = []string{"/resume"}
	case strings.Contains(clean, "a Marshal operation is already running"):
		r.Code = RefusalCodeBusy
		r.Effects = EffectsNone
		r.NextCommands = []string{"/marshal status", "/marshal stop"}
	case strings.Contains(clean, "unfinished lifecycle operation"),
		strings.Contains(clean, "unfinished merge "),
		strings.Contains(clean, "unfinished close"):
		r.Code = RefusalCodeInterruptedOp
		r.Effects = EffectsUnknown
		r.NextCommands = []string{"/marshal resume"}
	case strings.Contains(clean, "invalid or non-allow-listed Marshal proposal"),
		strings.Contains(clean, "invalid proposal submission"),
		strings.Contains(clean, "invalid decision:"),
		strings.Contains(clean, "invalid submission id"):
		r.Code = RefusalCodeParseInvalid
		r.Effects = EffectsNone
		r.NextCommands = []string{"/marshal help"}
	case strings.Contains(clean, "proposal launch is inactive"),
		strings.Contains(clean, "proposal launch replaced"),
		strings.Contains(clean, "proposal permission belongs to inactive launch"),
		strings.Contains(clean, "architecture auto-open requires"),
		strings.Contains(clean, "broker memory requires"):
		r.Code = RefusalCodeUnauthorized
		r.Effects = EffectsNone
		r.NextCommands = []string{"/marshal chat"}
	case strings.Contains(clean, "stale_base"),
		strings.Contains(clean, "approved delivery base changed"):
		r.Code = RefusalCodeStaleBase
		r.Effects = EffectsNone
		r.NextCommands = []string{"/marshal amend", "/marshal use-plan"}
	case strings.Contains(clean, "Marshal proposal expired"),
		strings.Contains(clean, "reserved merge approval expired"),
		strings.Contains(clean, "that question is no longer open"),
		strings.Contains(clean, "decision no longer pending"),
		strings.Contains(clean, "submission id binds another payload"),
		strings.Contains(clean, "submission key already binds different payload"),
		strings.Contains(clean, "plan is not awaiting approval"),
		strings.Contains(clean, "no matching active plan"),
		strings.Contains(clean, "task is not in the active plan"),
		strings.Contains(clean, "approval does not belong to the active Marshal plan"),
		strings.Contains(clean, "memory candidate does not exist"),
		strings.Contains(clean, "proposal receipt is not pending"):
		r.Code = RefusalCodeStaleBinding
		r.Effects = EffectsNone
		r.NextCommands = []string{"/marshal status"}
	default:
		return RefusalReceipt{}, false
	}

	r.Message = clean
	for _, seg := range strings.Split(clean, "; ") {
		seg = strings.TrimSpace(seg)
		switch {
		case seg == "no effects applied":
			r.Effects = EffectsNone
		case seg == "partial effects may have applied":
			r.Effects = EffectsPartial
		case seg == "effects unknown":
			r.Effects = EffectsUnknown
		case strings.HasPrefix(seg, "attempt "):
			if n, err := strconv.Atoi(strings.TrimPrefix(seg, "attempt ")); err == nil && n > 0 {
				r.Attempt = n
			}
		case strings.HasPrefix(seg, "escalation "):
			r.EscalationID = strings.TrimSpace(strings.TrimPrefix(seg, "escalation "))
		case strings.HasPrefix(seg, "next: "):
			raw := strings.TrimPrefix(seg, "next: ")
			var cmds []string
			for _, part := range strings.Split(raw, ",") {
				if c := strings.TrimSpace(part); c != "" {
					cmds = append(cmds, c)
				}
			}
			if len(cmds) > 0 {
				r.NextCommands = cmds
			}
		}
	}
	if holder := parseHolderFromSummary(clean); holder != nil {
		r.Blocker = holder
	}
	return r.Sanitized(), true
}

func parseHolderFromSummary(s string) *OperationHolder {
	idx := strings.Index(s, "(kind=")
	if idx < 0 {
		return nil
	}
	end := strings.Index(s[idx:], ")")
	if end < 0 {
		return nil
	}
	meta := s[idx+1 : idx+end]
	h := &OperationHolder{}
	for _, item := range strings.Split(meta, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok {
			continue
		}
		switch k {
		case "kind":
			h.Kind = v
		case "id":
			h.ID = v
		case "run":
			h.RunID = v
		case "task":
			h.TaskID = v
		}
	}
	prefix := strings.TrimSpace(s[:idx])
	if colon := strings.LastIndex(prefix, ": "); colon >= 0 {
		h.Title = strings.TrimSpace(prefix[colon+2:])
	}
	if h.ID == "" && h.Kind == "" {
		return nil
	}
	return h
}
