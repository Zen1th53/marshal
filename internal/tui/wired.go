package tui

// Wired status: what each advanced-menu entry does today.
//
// The source is the e2e walk of the Ctrl+N menu (tools/e2e/tui_walk.py), not
// manifest.json, whose BOUND marker is set on 808 of 810 specs and so cannot
// tell a wired action from one that only explains why it is not. The walk's
// matrix is turned into wired.json by `tui_walk.py wired`; this file only reads
// it. An entry the operator cannot use yet says so, with who owns the missing
// wiring, why, and the phase it lands in, instead of looking like a dead key.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed wired.json
var wiredJSON []byte

// wiredSchema is the version tui_walk.py writes.
const wiredSchema = "marshal.tui.wired.v1"

// WiredState is what selecting an entry does in this build.
type WiredState string

const (
	// WiredLive opens its form, confirmation or screen.
	WiredLive WiredState = "live"
	// WiredBlockedByState is wired and refuses until state allows it (no
	// plan, no goal, nothing awaiting approval).
	WiredBlockedByState WiredState = "blocked_by_state"
	// WiredUnderConstruction exists in the design and is not wired yet, or is
	// wired and fails.
	WiredUnderConstruction WiredState = "under_construction"
)

// WiredStatus is one spec's entry in wired.json.
type WiredStatus struct {
	SpecID string     `json:"spec_id"`
	Status WiredState `json:"status"`
	// Walk is the state the walker recorded (FORM, CONFIRM, RESULT, DISABLED,
	// ERROR, HIDDEN); empty for specs the walk does not visit.
	Walk  string `json:"walk,omitempty"`
	Owner string `json:"owner,omitempty"`
	Cause string `json:"cause,omitempty"`
	Phase string `json:"phase,omitempty"`
	// Task is the task file that will wire the entry; empty until one is
	// filed.
	Task string `json:"task,omitempty"`
}

// UnderConstruction reports whether the entry renders as [WIP].
func (w WiredStatus) UnderConstruction() bool { return w.Status == WiredUnderConstruction }

type wiredFile struct {
	Schema string        `json:"schema"`
	Source string        `json:"source"`
	Specs  []WiredStatus `json:"specs"`
}

var (
	wiredOnce  sync.Once
	wiredTable map[string]WiredStatus
)

// loadWired parses and validates a wired.json document.
func loadWired(data []byte) (map[string]WiredStatus, error) {
	var file wiredFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("wired status: %w", err)
	}
	if file.Schema != wiredSchema {
		return nil, fmt.Errorf("wired status: schema %q, want %q", file.Schema, wiredSchema)
	}
	table := make(map[string]WiredStatus, len(file.Specs))
	for _, w := range file.Specs {
		if _, dup := table[w.SpecID]; dup {
			return nil, fmt.Errorf("wired status: %s listed twice", w.SpecID)
		}
		switch w.Status {
		case WiredLive, WiredBlockedByState:
		case WiredUnderConstruction:
			// An entry that says "not yet" without saying who and when is
			// the dead end this status exists to remove.
			if w.Owner == "" || w.Cause == "" || w.Phase == "" {
				return nil, fmt.Errorf("wired status: %s is under construction without owner, cause and phase", w.SpecID)
			}
		default:
			return nil, fmt.Errorf("wired status: %s has unknown status %q", w.SpecID, w.Status)
		}
		table[w.SpecID] = w
	}
	return table, nil
}

// Wired returns the wired status of a spec. A spec the table does not list,
// or a table that failed to load, reads as live: the menu then behaves as it
// did before wired status existed, and TestWiredCoversManifest catches both.
func Wired(specID string) WiredStatus {
	wiredOnce.Do(func() { wiredTable, _ = loadWired(wiredJSON) })
	if w, ok := wiredTable[specID]; ok {
		return w
	}
	return WiredStatus{SpecID: specID, Status: WiredLive}
}

// showUnwiredEnv makes the menu list entries that are not wired yet, marked
// [WIP]. Only the menu walk sets it (tools/e2e/tui_walk.py), so it can still
// see whether a hidden entry has been wired since; an operator never needs it.
const showUnwiredEnv = "MARSHAL_TUI_SHOW_WIP"

// showUnwired is read once: the menu does not change shape while it runs.
var showUnwired = os.Getenv(showUnwiredEnv) == "1"

// menuHidesSpec reports entries the Ctrl+N menu, its filter and the palette do
// not offer. Owner decision 2026-11-05: no dead or WIP entry is visible, so an
// entry under construction is hidden until it is wired; its owner, cause and
// task stay in wired.json and in its task file, and the next walk brings it
// back once it works. Entries that are wired and wait for state
// (blocked_by_state) stay visible: their refusal is a real answer.
func menuHidesSpec(specID string) bool {
	if editionHidesSpec(specID) {
		return true
	}
	return !showUnwired && Wired(specID).UnderConstruction()
}

// wipMark marks an under-construction entry, which the menu lists only with
// showUnwired (the walk) and otherwise hides. It is plain text in every theme:
// the owner rule is no emoji anywhere, and an ASCII mark keeps every row one
// cell per rune so no width helper needs a special case.
const wipMark = "[WIP]"

// wiredTerms is the text a filter matches for an entry besides its title, so
// "wip", "[WIP]", an owner or a phase finds the entries that are not wired yet.
func wiredTerms(w WiredStatus) string {
	switch w.Status {
	case WiredUnderConstruction:
		return strings.Join([]string{wipMark, "wip under construction", w.Owner, w.Cause, w.Phase}, " ")
	case WiredBlockedByState:
		return "blocked by state " + w.Cause
	}
	return ""
}

// filterMatches reports whether a menu entry, or anything beneath it, matches
// every term. Matching descendants keeps a section visible while it holds
// what the operator is looking for, so a filter typed at the top still leads
// down to the entry.
func filterMatches(n *Node, terms []string) bool {
	text := strings.ToLower(communityLabel(n.Title) + " " + wiredTerms(Wired(n.SpecID)))
	all := true
	for _, t := range terms {
		if !strings.Contains(text, t) {
			all = false
			break
		}
	}
	if all {
		return true
	}
	for _, c := range n.Children {
		if filterMatches(c, terms) {
			return true
		}
	}
	return false
}
