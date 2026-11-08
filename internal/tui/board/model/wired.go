package model

import "fmt"

// WiredState says whether an advanced-menu action works today.
type WiredState string

const (
	WiredLive              WiredState = "live"
	WiredBlockedByState    WiredState = "blocked_by_state"
	WiredUnderConstruction WiredState = "under_construction"
)

// WiredStatus is one advanced-menu spec as the e2e walk found it. The walk,
// not manifest.json, is the source: the manifest marks almost every spec as
// bound and so cannot tell a live action from a refusal.
type WiredStatus struct {
	SpecID string
	State  WiredState
	Cause  string // the walk's detail; empty for a live action
}

// walkStates maps the action matrix's State column. FORM and CONFIRM opened
// the action; RESULT resolved at once to a verdict, usually BLOCKED by the
// current state; the rest refused or broke.
var walkStates = map[string]WiredState{
	"FORM":        WiredLive,
	"CONFIRM":     WiredLive,
	"RESULT":      WiredBlockedByState,
	"BLOCKED":     WiredBlockedByState,
	"DISABLED":    WiredUnderConstruction,
	"ERROR":       WiredUnderConstruction,
	"DEAD":        WiredUnderConstruction,
	"UNREACHABLE": WiredUnderConstruction,
	"CHANGED":     WiredUnderConstruction,
}

// NewWiredStatus projects one row of the walk's action matrix. A state the
// matrix does not define is an error: guessing would show a broken action as
// live.
func NewWiredStatus(specID, walkState, detail string) (WiredStatus, error) {
	state, ok := walkStates[walkState]
	if !ok {
		return WiredStatus{}, fmt.Errorf("board: spec %s has unknown walk state %q", specID, walkState)
	}
	if state == WiredLive {
		detail = ""
	}
	return WiredStatus{SpecID: specID, State: state, Cause: detail}, nil
}
