package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// contractNotGiven is shown where the page needs text the contract does not
// give. Nothing else on a contract-built page is written by Marshal code.
const contractNotGiven = "not given"

// contractReviewSource is the project contract a browser review was built
// from, stored as the architecture's source_contract.
type contractReviewSource struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

// contractReviewSeedFor builds the browser review of a pending project
// contract from the contract alone: its id and purpose, each component's
// responsibility, paths and dependencies, its invariants and prohibited
// changes. Dependencies become connections. A connection has no label in a
// contract, so its label is shown as "not given". language is the owner's
// approved language; the page's own labels stay in English because no model
// turn may translate them before the contract is accepted.
func contractReviewSeedFor(c projectcontext.Contract, digest, language string) (*contractReviewSeed, error) {
	if c.ID == "" || c.Revision < 1 || digest == "" {
		return nil, errors.New("the project summary is incomplete")
	}
	components := make([]any, 0, len(c.Components))
	connections := []any{}
	uiComponents, uiConnections := []any{}, []any{}
	known := map[string]bool{}
	for _, v := range c.Components {
		known[v.ID] = true
	}
	for _, v := range c.Components {
		paths := append([]string{}, v.Paths...)
		deps := append([]string{}, v.Dependencies...)
		components = append(components, map[string]any{"id": v.ID, "name": v.ID, "responsibility": v.Responsibility, "technology": "", "files": paths, "dependencies": deps})
		uiComponents = append(uiComponents, map[string]any{"id": v.ID, "name": v.ID, "responsibility": orNotGiven(v.Responsibility)})
		for _, dep := range v.Dependencies {
			if !known[dep] || dep == v.ID {
				continue
			}
			id := v.ID + "--" + dep
			connections = append(connections, map[string]any{"id": id, "source": v.ID, "target": dep, "label": "", "protocol": ""})
			uiConnections = append(uiConnections, map[string]any{"id": id, "label": contractNotGiven, "caption": contractNotGiven})
		}
	}
	source := contractReviewSource{ID: c.ID, Revision: c.Revision, Digest: digest}
	graph := map[string]any{"title": c.ID, "purpose": c.Purpose, "components": components, "connections": connections, "constraints": append([]string{}, c.Invariants...), "source_contract": source}
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
	texts := []any{}
	for key, text := range architecturereview.EnglishUIStrings() {
		texts = append(texts, map[string]any{"key": key, "text": text})
	}
	ui, err := json.Marshal(map[string]any{"language": language, "strings": texts, "components": uiComponents, "connections": uiConnections})
	if err != nil {
		return nil, err
	}
	if _, err := architecturereview.ValidateUI(ui, language, architecture); err != nil {
		return nil, fmt.Errorf("the project summary cannot be drawn: %w", err)
	}
	return &contractReviewSeed{Architecture: architecture, UI: ui, Source: source}, nil
}

func orNotGiven(text string) string {
	if text == "" {
		return contractNotGiven
	}
	return text
}

// replaceSupersededContractReview sets aside a review that was built from an
// older pending contract than seed, so the owner sees and accepts the
// contract the Marshal submitted last. A model-drafted review (no
// source_contract) and a review of the same contract are kept.
func replaceSupersededContractReview(directory string, seed *contractReviewSeed) error {
	data, err := readArchitecturePrivateFile(directory, "state.json", architectureContextLimit)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state struct {
		Architecture struct {
			Source *contractReviewSource `json:"source_contract"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Architecture.Source == nil || *state.Architecture.Source == seed.Source {
		return nil
	}
	return os.Rename(filepath.Join(directory, "state.json"), filepath.Join(directory, fmt.Sprintf("superseded-%d.json", time.Now().UnixNano())))
}

// modelDraftedReviewExists reports a stored review that was not built from a
// project contract; it keeps its model-drafted path.
func modelDraftedReviewExists(directory string) (bool, error) {
	data, err := readArchitecturePrivateFile(directory, "state.json", architectureContextLimit)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state struct {
		Architecture struct {
			Source *contractReviewSource `json:"source_contract"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false, err
	}
	return state.Architecture.Source == nil, nil
}
