//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// architectureNotesRef carries the same bindings inside the repository, so a
// clone elsewhere still knows which architecture each commit was made under.
const architectureNotesRef = "marshal-architecture"

func (r *Runtime) recordArchitectureCommitEdition(ctx context.Context, dir, commit, event, task string) {
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return
	}
	if commit == "" {
		if commit, err = gitMarshal(ctx, dir, "rev-parse", "HEAD"); err != nil {
			return
		}
	}
	tree, err := gitMarshal(ctx, dir, "rev-parse", commit+"^{tree}")
	if err != nil {
		return
	}
	ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
	if err = r.store.RecordArchitectureCommit(ctx, r.ProjectContractBinding(), store.ArchitectureCommit{Commit: commit, Tree: tree, Contract: ref, Event: event, Task: task}); err != nil {
		return
	}
	bindings, err := r.store.ArchitectureCommitsFor(ctx, r.ProjectContractBinding(), []string{commit})
	if err != nil {
		return
	}
	note, err := json.Marshal(map[string]any{"marshal_architecture": bindings[commit]})
	if err != nil {
		return
	}
	_, _ = gitMarshal(ctx, dir, "notes", "--ref="+architectureNotesRef, "add", "-f", "-m", string(note), commit)
}

// ArchitectureAt is the architecture that applied at a commit: the binding
// of the commit itself, or of its nearest first-parent ancestor that has one.
type ArchitectureAt struct {
	Requested   string                     `json:"requested"`
	Commit      string                     `json:"commit"`
	Bound       bool                       `json:"bound"`
	Exact       bool                       `json:"exact"`
	BoundCommit string                     `json:"bound_commit,omitempty"`
	Distance    int                        `json:"commits_since_binding"`
	Bindings    []store.ArchitectureCommit `json:"bindings,omitempty"`
	Contract    *projectcontext.Contract   `json:"contract,omitempty"`
	Reason      string                     `json:"reason,omitempty"`
}

var commitArgument = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._/~^@{}-]{0,199}$`)

// architectureSearchDepth bounds the first-parent walk.
const architectureSearchDepth = 5000

// ArchitectureAtCommit answers "which architecture applied at this commit"
// from recorded bindings only; it never infers one. component, when set,
// keeps only that part and the parts inside it.
func (r *Runtime) ArchitectureAtCommit(ctx context.Context, commit, component string) (ArchitectureAt, error) {
	out := ArchitectureAt{Requested: commit}
	if r == nil || r.store == nil {
		return out, errors.New("no project runtime")
	}
	if commit == "" {
		commit = "HEAD"
	}
	if !commitArgument.MatchString(commit) || strings.Contains(commit, "..") {
		return out, errors.New("commit must be a commit id, branch, tag or HEAD expression")
	}
	root := r.ProjectRoot()
	sha, err := gitMarshal(ctx, root, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return out, errors.New("no such commit in this repository")
	}
	out.Commit = sha
	list, err := gitMarshal(ctx, root, "rev-list", "--first-parent", "--max-count=5000", sha)
	if err != nil {
		return out, err
	}
	ancestors := strings.Fields(list)
	found, err := r.store.ArchitectureCommitsFor(ctx, r.ProjectContractBinding(), ancestors)
	if err != nil {
		return out, err
	}
	for i, c := range ancestors {
		bindings := found[c]
		if len(bindings) == 0 {
			continue
		}
		newest := bindings[len(bindings)-1]
		contract, err := r.store.ReadProjectContractRevision(ctx, r.ProjectContractBinding(), newest.Contract)
		if err != nil {
			return out, err
		}
		if component != "" {
			if contract, err = componentSubtree(contract, component); err != nil {
				return out, err
			}
		}
		out.Bound, out.Exact, out.BoundCommit, out.Distance, out.Bindings, out.Contract = true, i == 0, c, i, bindings, &contract
		return out, nil
	}
	out.Reason = "No accepted architecture is recorded at or before this commit: it was made before Marshal kept the record, or outside a Marshal run."
	if len(ancestors) == architectureSearchDepth {
		out.Reason = "No accepted architecture is recorded in the last 5000 first-parent commits before this one."
	}
	return out, nil
}

// componentSubtree keeps one component and every part inside it.
func componentSubtree(c projectcontext.Contract, id string) (projectcontext.Contract, error) {
	inside := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, v := range c.Components {
			if !inside[v.ID] && inside[v.Parent] {
				inside[v.ID], changed = true, true
			}
		}
	}
	kept := []projectcontext.Component{}
	for _, v := range c.Components {
		if inside[v.ID] {
			kept = append(kept, v)
		}
	}
	if len(kept) == 0 {
		return c, errors.New("this architecture revision has no component " + id)
	}
	c.Components = kept
	return c, nil
}

// ArchitectureHistoryEntry is one recorded binding; for a component, its
// entry in that revision and whether it differs from the previous entry.
type ArchitectureHistoryEntry struct {
	store.ArchitectureCommit
	Component *projectcontext.Component `json:"component,omitempty"`
	Changed   bool                      `json:"component_changed,omitempty"`
}

// ArchitectureHistory lists recorded bindings, newest first.
func (r *Runtime) ArchitectureHistory(ctx context.Context, component string, limit int) ([]ArchitectureHistoryEntry, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("no project runtime")
	}
	list, err := r.store.ArchitectureCommitHistory(ctx, r.ProjectContractBinding(), limit)
	if err != nil {
		return nil, err
	}
	entries := make([]ArchitectureHistoryEntry, len(list))
	contracts := map[projectcontext.ContractRef]projectcontext.Contract{}
	for i, b := range list {
		entries[i].ArchitectureCommit = b
		if component == "" {
			continue
		}
		c, ok := contracts[b.Contract]
		if !ok {
			if c, err = r.store.ReadProjectContractRevision(ctx, r.ProjectContractBinding(), b.Contract); err != nil {
				return nil, err
			}
			contracts[b.Contract] = c
		}
		for _, v := range c.Components {
			if v.ID == component {
				v := v
				entries[i].Component = &v
			}
		}
	}
	// Oldest to newest, mark where the component's entry changed.
	for i := len(entries) - 2; i >= 0 && component != ""; i-- {
		before, after := entries[i+1].Component, entries[i].Component
		b, _ := json.Marshal(before)
		a, _ := json.Marshal(after)
		entries[i].Changed = string(a) != string(b)
	}
	return entries, nil
}
