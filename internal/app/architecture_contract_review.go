package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// contractNotGiven is shown where a component has no description in an older
// contract. Display text otherwise comes from the contract itself; the drawing's
// change metadata is computed against the accepted contract.
const contractNotGiven = "not given"

// contractConnectionNotDescribed labels a dependency that has no connection
// saying what it carries. The submit quality check refuses new ones, so only
// an older stored contract shows it.
const contractConnectionNotDescribed = "not described"

// contractReviewSource is the project contract a browser review was built
// from, stored as the architecture's source_contract.
type contractReviewSource struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

// contractReviewSeedFor draws the whole proposed project against its accepted
// revision (base, absent for a baseline). Removed entries stay in the graph so
// the owner can review deletions. Each component carries its responsibility,
// paths, dependencies and coverage; each connection is labeled with what its
// c.Connections entry carries, and a dependency with no matching connection
// (an older stored contract) displays "not described". Every component and
// connection carries its change status. language is the owner's approved
// language; the page's own labels stay in English because no model turn may
// translate them before the contract is accepted.
func contractReviewSeedFor(c projectcontext.Contract, digest, language string, base ...projectcontext.Contract) (*contractReviewSeed, error) {
	if c.ID == "" || c.Revision < 1 || digest == "" {
		return nil, errors.New("the project summary is incomplete")
	}
	var accepted projectcontext.Contract
	if len(base) > 0 {
		accepted = base[0]
	}
	changes := projectcontext.ContractChanges(accepted, c)
	components, connections := []any{}, []any{}
	known := map[string]bool{}
	for _, v := range changes.Components {
		known[v.ID] = true
		component := contractReviewComponent(v.Component)
		component["change"] = v.Change
		if v.Before != nil {
			component["before"] = contractReviewComponent(*v.Before)
		}
		components = append(components, component)
	}
	for _, edge := range changes.Connections {
		if !known[edge.From] || !known[edge.To] || edge.From == edge.To {
			continue
		}
		label := edge.Carries
		if label == "" {
			label = contractConnectionNotDescribed
		}
		connection := map[string]any{"id": edge.ID(), "source": edge.From, "target": edge.To, "label": label, "protocol": "", "change": edge.Change}
		if edge.Before != nil {
			connection["before"] = edge.Before
		}
		connections = append(connections, connection)
	}

	source := contractReviewSource{ID: c.ID, Revision: c.Revision, Digest: digest}
	graph := map[string]any{"title": c.ID, "purpose": c.Purpose, "components": components, "connections": connections, "constraints": append([]string{}, c.Invariants...), "source_contract": source, "base_revision": changes.BaseRevision}
	if len(c.ProhibitedChanges) > 0 {
		graph["prohibited_changes"] = append([]string{}, c.ProhibitedChanges...)
	}
	architecture, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	if err := architecturereview.ValidateArchitecture(architecture); err != nil {
		return nil, fmt.Errorf("the project summary cannot be drawn: %w", err)
	}
	ui, err := contractUIFor(architecture, language)
	if err != nil {
		return nil, err
	}
	return &contractReviewSeed{Architecture: architecture, UI: ui, Source: source}, nil
}

func contractReviewComponent(v projectcontext.Component) map[string]any {
	component := map[string]any{"id": v.ID, "name": v.ID, "responsibility": v.Responsibility, "technology": "", "files": append([]string{}, v.Paths...), "dependencies": append([]string{}, v.Dependencies...)}
	if v.Parent != "" {
		component["parent"] = v.Parent
	}
	if len(v.Covers) > 0 {
		component["covers"] = append([]string{}, v.Covers...)
	}
	return component
}

// contractUIFor is the display overlay of a contract-built architecture: the
// page's English labels and each part's own id and responsibility. No model
// turn writes any of it.
func contractUIFor(architecture json.RawMessage, language string) (json.RawMessage, error) {
	var graph struct {
		Components []struct {
			ID             string `json:"id"`
			Responsibility string `json:"responsibility"`
		} `json:"components"`
		Connections []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(architecture, &graph); err != nil {
		return nil, err
	}
	uiComponents, uiConnections := []any{}, []any{}
	for _, c := range graph.Components {
		uiComponents = append(uiComponents, map[string]any{"id": c.ID, "name": c.ID, "responsibility": orNotGiven(c.Responsibility)})
	}
	for _, c := range graph.Connections {
		label := c.Label
		if label == "" {
			label = contractConnectionNotDescribed
		}
		uiConnections = append(uiConnections, map[string]any{"id": c.ID, "label": label, "caption": shortConnectionCaption(label)})
	}
	strings := architecturereview.EnglishUIStrings()
	keys := make([]string, 0, len(strings))
	for key := range strings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	texts := []any{}
	for _, key := range keys {
		texts = append(texts, map[string]any{"key": key, "text": strings[key]})
	}
	ui, err := json.Marshal(map[string]any{"language": language, "strings": texts, "components": uiComponents, "connections": uiConnections})
	if err != nil {
		return nil, err
	}
	if _, err := architecturereview.ValidateUI(ui, language, architecture); err != nil {
		return nil, fmt.Errorf("the project summary cannot be drawn: %w", err)
	}
	return ui, nil
}

func orNotGiven(text string) string {
	if text == "" {
		return contractNotGiven
	}
	return text
}

// shortConnectionCaption keeps the edge caption within the architecture review
// UI's 28-rune cap, matching architecturereview.englishOverlay.
func shortConnectionCaption(label string) string {
	runes := []rune(label)
	if len(runes) > 28 {
		return string(append(runes[:27], '…'))
	}
	return label
}

// replaceSupersededContractReview sets aside a review that was built from an
// older pending contract than seed, so the owner sees and accepts the
// contract the Marshal submitted last. The same contract's view is retained
// only when its graph already includes the current revision's change metadata.
func replaceSupersededContractReview(directory string, seed *contractReviewSeed) error {
	data, err := readArchitecturePrivateFile(directory, "state.json", architectureContextLimit)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state struct {
		Architecture json.RawMessage `json:"architecture"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	var existing, proposed map[string]any
	if err := json.Unmarshal(state.Architecture, &existing); err != nil {
		return err
	}
	if err := json.Unmarshal(seed.Architecture, &proposed); err != nil {
		return err
	}
	if reflect.DeepEqual(existing, proposed) {
		return nil
	}
	return os.Rename(filepath.Join(directory, "state.json"), filepath.Join(directory, fmt.Sprintf("superseded-%d.json", time.Now().UnixNano())))
}

// Replacing a pending contract must advance the browser draft sequence too,
// including revisions made before the first contract has been accepted.
func nextContractReviewRevision(directory string) (int64, error) {
	files, err := filepath.Glob(filepath.Join(directory, "superseded-*.json"))
	if err != nil {
		return 0, err
	}
	next := int64(1)
	for _, file := range files {
		data, err := readArchitecturePrivateFile(directory, filepath.Base(file), architectureContextLimit)
		if err != nil {
			return 0, err
		}
		var state struct {
			Revision int64 `json:"revision"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return 0, err
		}
		if state.Revision >= next {
			next = state.Revision + 1
		}
	}
	return next, nil
}
