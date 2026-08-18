package budget

import (
	"context"
	"sort"
)

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Allocate(ctx context.Context, b Budget, sections []SectionPriority) (*Decision, error) {
	if b.MaxTokens <= 0 {
		return nil, ErrTooLarge
	}

	mandatoryTokens := 0
	for _, sec := range sections {
		if sec.Mandatory {
			mandatoryTokens += sec.MinTokens
		}
	}

	avail := b.MaxTokens - b.ReserveTokens
	if mandatoryTokens > avail {
		return nil, ErrMandatoryOverflow
	}

	// Sort sections by priority
	sort.Slice(sections, func(i, j int) bool {
		return sections[i].Priority > sections[j].Priority
	})

	totalUsed := 0
	dropped := []string{}
	compacted := []string{}

	for _, sec := range sections {
		if totalUsed+sec.MinTokens <= avail {
			totalUsed += sec.MinTokens
			if !sec.Mandatory {
				compacted = append(compacted, sec.Kind)
			}
		} else if !sec.Mandatory {
			dropped = append(dropped, sec.Kind)
		}
	}

	return &Decision{
		Action:          "ALLOCATE_OK",
		Dropped:         dropped,
		Compacted:       compacted,
		EstimatedTokens: totalUsed,
	}, nil
}
