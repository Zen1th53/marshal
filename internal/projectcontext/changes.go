package projectcontext

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

type Change string

const (
	ChangeExisting Change = "existing"
	ChangeNew      Change = "new"
	ChangeChanged  Change = "changed"
	ChangeRemoved  Change = "removed"
)

type ComponentChange struct {
	Component
	Change Change     `json:"change"`
	Before *Component `json:"before,omitempty"`
}

// ContractConnection is the drawing's compatibility view of a connection.
type ContractConnection struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Carries string `json:"carries"`
}

func (c ContractConnection) ID() string { return c.From + "--" + c.To }

type ConnectionChange struct {
	ContractConnection
	Change Change              `json:"change"`
	Before *ContractConnection `json:"before,omitempty"`
}

type Changes struct {
	BaseRevision int64              `json:"base_revision"`
	Components   []ComponentChange  `json:"components"`
	Connections  []ConnectionChange `json:"connections"`
}

// ContractConnections is the integration hook for Contract.Connections. Read
// its wire shape so the connections worker can add the typed field separately.
// Until then dependencies remain visible, with an explicitly undescribed flow.
func ContractConnections(c Contract) []ContractConnection {
	var wire struct {
		Connections []ContractConnection `json:"connections"`
	}
	raw, _ := json.Marshal(c)
	_ = json.Unmarshal(raw, &wire)
	byID := map[string]ContractConnection{}
	for _, edge := range wire.Connections {
		byID[edge.ID()] = edge
	}
	for _, part := range c.Components {
		if part.Removed {
			continue
		}
		for _, dep := range part.Dependencies {
			edge := ContractConnection{From: part.ID, To: dep}
			if _, found := byID[edge.ID()]; !found {
				byID[edge.ID()] = edge
			}
		}
	}
	result := make([]ContractConnection, 0, len(byID))
	for _, edge := range byID {
		result = append(result, edge)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID() < result[j].ID() })
	return result
}

// ContractChanges compares whole project revisions. Absent entries are kept
// in the result so the browser can draw removals against the accepted project.
// A zero accepted contract is the baseline; all its proposed parts are new.
func ContractChanges(accepted, proposed Contract) Changes {
	result := Changes{BaseRevision: accepted.Revision, Components: []ComponentChange{}, Connections: []ConnectionChange{}}
	oldParts, nextParts := map[string]Component{}, map[string]Component{}
	for _, part := range accepted.Components {
		oldParts[part.ID] = part
	}
	for _, part := range proposed.Components {
		nextParts[part.ID] = part
		entry := ComponentChange{Component: part, Change: ChangeNew}
		if before, found := oldParts[part.ID]; found && !before.Removed {
			entry.Change = ChangeExisting
			if part.Responsibility != before.Responsibility || part.Parent != before.Parent || !samePaths(part.Paths, before.Paths) {
				entry.Change, entry.Before = ChangeChanged, &before
			}
		}
		if part.Removed {
			entry.Change = ChangeRemoved
			if before, found := oldParts[part.ID]; found {
				entry.Before = &before
			}
		}
		result.Components = append(result.Components, entry)
	}
	for id, part := range oldParts {
		if _, found := nextParts[id]; !found && !part.Removed {
			result.Components = append(result.Components, ComponentChange{Component: part, Change: ChangeRemoved, Before: &part})
		}
	}
	oldEdges, nextEdges := map[string]ContractConnection{}, map[string]bool{}
	for _, edge := range ContractConnections(accepted) {
		oldEdges[edge.ID()] = edge
	}
	for _, edge := range ContractConnections(proposed) {
		// Connections to removed parts disappear with those parts.
		if nextParts[edge.From].Removed || nextParts[edge.To].Removed {
			continue
		}
		nextEdges[edge.ID()] = true
		entry := ConnectionChange{ContractConnection: edge, Change: ChangeNew}
		if before, found := oldEdges[edge.ID()]; found {
			entry.Change = ChangeExisting
			if before.Carries != edge.Carries {
				entry.Change, entry.Before = ChangeChanged, &before
			}
		}
		result.Connections = append(result.Connections, entry)
	}
	for id, edge := range oldEdges {
		if !nextEdges[id] {
			result.Connections = append(result.Connections, ConnectionChange{ContractConnection: edge, Change: ChangeRemoved, Before: &edge})
		}
	}
	sort.Slice(result.Components, func(i, j int) bool { return result.Components[i].ID < result.Components[j].ID })
	sort.Slice(result.Connections, func(i, j int) bool { return result.Connections[i].ID() < result.Connections[j].ID() })
	return result
}

func samePaths(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// ValidateContractRevision prevents a goal-sized fragment replacing the project.
// A removed component must be retained with removed:true for owner review.
func ValidateContractRevision(accepted, proposed Contract) error {
	if accepted.ID != proposed.ID || proposed.Revision != accepted.Revision+1 {
		return fmt.Errorf("%w: submit the next revision of project contract %s", ErrInvalid, accepted.ID)
	}
	kept := map[string]bool{}
	for _, part := range proposed.Components {
		kept[part.ID] = true
	}
	for _, part := range accepted.Components {
		if !part.Removed && !kept[part.ID] {
			return fmt.Errorf("%w: keep accepted component %s; to remove it, include it with removed:true", ErrInvalid, part.ID)
		}
	}
	removed := map[string]bool{}
	for _, part := range proposed.Components {
		removed[part.ID] = part.Removed
	}
	for _, part := range proposed.Components {
		if !part.Removed && removed[part.Parent] {
			return fmt.Errorf("%w: component %s still belongs to removed component %s; move or remove it explicitly", ErrInvalid, part.ID, part.Parent)
		}
	}
	return nil
}
