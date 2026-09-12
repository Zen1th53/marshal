package tui

// Memory data: the read bindings behind the Memory section.
//
// Memory is the section most likely to leak. Its records hold whatever an agent
// learned — which can include file contents, command output and, if a provider
// echoed one, a credential. So this layer treats record content as bounded and
// potentially sensitive: it shows identity, provenance and standing freely, and
// content only in bounded excerpts that the copy path refuses.
//
// It also computes no scores. Utility, relevance and confidence belong to
// Process 07; a UI that recomputed them would be a second opinion about what
// MARSHAL knows.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// MemoryReader is the canonical Process 07 state the section displays.
type MemoryReader interface {
	// MemoryStatus reports the service's own health and version.
	MemoryStatus(ctx context.Context) (MemoryStatus, error)
	// RecallRecent lists recently recalled or written records.
	RecallRecent(ctx context.Context, limit int) ([]model.MemoryRecordV2, error)
	// TaskSlots lists the working-memory slots for a task.
	TaskSlots(ctx context.Context, taskID string) ([]MemorySlot, error)
}

// MemoryStatus is the memory service's own report on itself.
type MemoryStatus struct {
	Version   string
	Healthy   bool
	ProjectID string
}

// MemorySlot is one working-memory slot.
type MemorySlot struct {
	Key      string
	Revision int64
	Private  bool
}

// MemoryFeed bundles the readers Memory binds to.
//
// It is not called MemorySource because model.MemorySource is the canonical
// provenance record on a memory row, and two types with one name would make
// every mention of "the memory source" ambiguous.
type MemoryFeed struct {
	Reader    MemoryReader
	SessionID string
	ProjectID string
	Now       func() time.Time
}

func (s *MemoryFeed) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

const memoryBinding = "internal/app/memory_runtime.go, internal/memory"

// MemorySnapshot is everything Memory displays, read at one instant.
type MemorySnapshot struct {
	ServiceVersion Value
	ServiceHealth  Value
	ServiceProject Value

	Records       []MemoryRow
	RecordsStatus Value

	Slots       []SlotRow
	SlotsStatus Value

	ObservedAt time.Time
}

// MemoryRow is one canonical memory record.
//
// Content is deliberately not a field. A record's text is shown only through
// Excerpt, which is bounded and marked, because a memory record can hold
// anything an agent saw.
type MemoryRow struct {
	ID         Value
	Kind       Value
	Scope      Value
	Standing   Value
	Provenance Value
	Excerpt    Value
	When       Value
}

// SlotRow is one working-memory slot.
type SlotRow struct {
	Key      Value
	Revision Value
	Access   Value
}

// ReadMemory gathers Process 07 state at one instant.
func (s *MemoryFeed) ReadMemory(ctx context.Context) MemorySnapshot {
	snap := MemorySnapshot{ObservedAt: s.now()}
	if s == nil || s.Reader == nil {
		unavailable := Unknown(
			"no memory service is attached to this workspace", memoryBinding)
		snap.ServiceVersion, snap.ServiceHealth = unavailable, unavailable
		snap.ServiceProject, snap.RecordsStatus = unavailable, unavailable
		snap.SlotsStatus = unavailable
		return snap
	}

	status, err := s.Reader.MemoryStatus(ctx)
	if err != nil {
		v := Errored(fmt.Sprintf("the memory service could not be read: %s", err), memoryBinding)
		snap.ServiceVersion, snap.ServiceHealth, snap.ServiceProject = v, v, v
	} else {
		snap.ServiceVersion = knownOrEmpty(status.Version, memoryBinding)
		snap.ServiceProject = knownOrEmpty(status.ProjectID, memoryBinding)
		// Health is the service's own answer, restated rather than judged.
		if status.Healthy {
			snap.ServiceHealth = Known("healthy", memoryBinding)
		} else {
			snap.ServiceHealth = Value{
				Text: "unhealthy", Status: TruthKnown,
				Reason: "the memory service reports itself unhealthy; recall may be " +
					"degraded or incomplete",
				Source: memoryBinding,
			}
		}
	}

	s.readRecords(ctx, &snap)
	// Working memory is task-scoped. Until the user selects a task there is no
	// canonical task id to query and an empty list would falsely mean the task
	// has no slots.
	snap.SlotsStatus = NotRun("select a task to list its canonical working-memory slots", memoryBinding)
	return snap
}

func (s *MemoryFeed) readRecords(ctx context.Context, snap *MemorySnapshot) {
	records, err := s.Reader.RecallRecent(ctx, 12)
	switch {
	case err != nil:
		snap.RecordsStatus = Errored(
			fmt.Sprintf("memory records could not be read: %s", err), memoryBinding)
		return
	case len(records) == 0:
		snap.RecordsStatus = Empty(memoryBinding)
		return
	}

	// Newest first, then by id, so the list is stable across refreshes.
	sorted := make([]model.MemoryRecordV2, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID < sorted[j].ID
	})

	at := s.now()
	for _, record := range sorted {
		snap.Records = append(snap.Records, MemoryRow{
			ID:         Known(record.ID, memoryBinding),
			Kind:       knownOrEmpty(string(record.Kind), memoryBinding),
			Scope:      knownOrEmpty(record.ScopeID, memoryBinding),
			Standing:   memoryStanding(record),
			Provenance: memoryProvenance(record),
			// The excerpt is bounded and redacted: a memory record can hold
			// whatever an agent saw, including output a provider echoed.
			Excerpt: memoryExcerpt(record),
			When:    Known(relativeTime(at, record.CreatedAt), memoryBinding),
		})
	}
	snap.RecordsStatus = Known(fmt.Sprintf("%d records", len(sorted)), memoryBinding)
}

// memoryStanding reports where a record stands, without recomputing it.
//
// Process 07 decides whether a record is active, superseded or invalidated.
// This restates that decision; it never infers standing from age or usage.
func memoryStanding(record model.MemoryRecordV2) Value {
	lifecycle := strings.ToUpper(strings.TrimSpace(string(record.Lifecycle)))
	switch lifecycle {
	case "":
		return Unknown("this record carries no lifecycle state", memoryBinding)
	case "VERIFIED", "DURABLE":
		return Known(lifecycle, memoryBinding)
	case "REJECTED", "SUPERSEDED", "STALE", "CONFLICTED":
		// A retired record is a real answer: it says what MARSHAL used to
		// believe and no longer does.
		return Value{
			Text: lifecycle, Status: TruthKnown,
			Reason: "this record has been retired and no longer informs recall",
			Source: memoryBinding,
		}
	case "OBSERVED", "CANDIDATE":
		return NotRun(fmt.Sprintf(
			"recorded as %s: this has not been promoted into governed memory", lifecycle),
			memoryBinding)
	}
	return Unknown(fmt.Sprintf(
		"Process 07 reported lifecycle %q, which this build does not recognise",
		record.Lifecycle), memoryBinding)
}

// memoryProvenance names where a record came from.
//
// A record with no provenance cannot be checked against anything, which is
// worth saying rather than leaving blank.
func memoryProvenance(record model.MemoryRecordV2) Value {
	var parts []string
	if record.Source.Kind != "" {
		parts = append(parts, record.Source.Kind)
	}
	if record.Source.Reference != "" {
		parts = append(parts, record.Source.Reference)
	}
	// Evidence is what makes a record checkable rather than merely asserted.
	if len(record.EvidenceIDs) > 0 {
		parts = append(parts, fmt.Sprintf("(%d evidence)", len(record.EvidenceIDs)))
	}
	if len(parts) == 0 {
		return Unknown(
			"this record records no source, so nothing can be checked against it",
			memoryBinding)
	}
	return Known(strings.Join(parts, " "), memoryBinding)
}

// memoryExcerpt renders a bounded, non-copyable excerpt of a record.
//
// The full content is never rendered and never copyable. A memory record holds
// whatever an agent learned, which can include command output or a credential a
// provider echoed, and a terminal that copies it puts it somewhere MARSHAL no
// longer governs.
func memoryExcerpt(record model.MemoryRecordV2) Value {
	content := strings.TrimSpace(record.Title)
	if body := strings.TrimSpace(record.Body); body != "" {
		if content != "" {
			content += " — "
		}
		content += body
	}
	if content == "" {
		return Empty(memoryBinding)
	}
	// A memory record holds whatever an agent saw, so a credential a provider
	// echoed can be sitting in the body. Bounding the length limits how much
	// leaks, not whether it leaks: a token is shorter than the excerpt. The
	// same pattern redaction the rest of the TUI applies to user-facing text
	// runs here before anything reaches the screen.
	//
	// It runs BEFORE the length bound, not after. The patterns match on
	// minimum lengths -- sk- needs sixteen characters, Bearer ten -- so a
	// truncation that cut a token to twelve characters would leave a fragment
	// the pattern no longer recognises, and the fragment would render in
	// cleartext. Redacting the whole content first means truncation can only
	// ever shorten "[REDACTED]".
	content = RedactContent(content, nil)

	const maxExcerpt = 120
	runes := []rune(content)
	truncated := false
	if len(runes) > maxExcerpt {
		runes = runes[:maxExcerpt]
		truncated = true
	}
	// Newlines are collapsed so one record cannot take over the pane.
	excerpt := strings.Join(strings.Fields(string(runes)), " ")
	if truncated {
		excerpt += "…"
	}
	return Value{
		Text:   excerpt,
		Status: TruthKnown,
		Reason: "a bounded excerpt; the full record is not rendered and cannot be " +
			"copied from here, because memory content may hold anything an agent saw",
		Source: memoryBinding,
		// Redaction is what makes CopyText refuse: the excerpt is for reading,
		// not for moving somewhere MARSHAL does not govern.
		Redacted: true,
	}
}
