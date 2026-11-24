package marshal

import "errors"

// SlotState is where a worker reservation is in its lifecycle.
type SlotState string

const (
	// SlotReserved is committed together with the launch intent.
	SlotReserved SlotState = "reserved"
	// SlotRunning names a worker the launch receipt proves started.
	SlotRunning SlotState = "running"
	// SlotStaged holds a proved exit whose result is not yet committed.
	SlotStaged SlotState = "staged"
	// SlotHeld keeps capacity for evidence that needs inspection.
	SlotHeld SlotState = "held"
	// SlotReleased frees capacity after a proved exit or proved absence.
	SlotReleased SlotState = "released"
)

// Exit proofs: only these release a slot.
const (
	ExitProofExited = "exited"
	ExitProofAbsent = "absent"
)

// WorkerSlot is one reserved worker slot, bound to the exact launch and
// attempt it was reserved for so a restart can correlate it again.
type WorkerSlot struct {
	LaunchKey   string
	TaskID      string
	Attempt     int
	PlanID      string
	PlanVersion int64
	// Lease is the execution that reserved the slot.
	Lease string `json:",omitempty"`
	// Revision is the run revision the reservation was committed against.
	Revision    int64
	State       SlotState
	Exit        *StagedExit `json:",omitempty"`
	Quarantined bool        `json:",omitempty"`
	Reason      string      `json:",omitempty"`
	// Acked records that the scheduler was told about the release.
	Acked bool `json:",omitempty"`
}

// StagedExit is a worker's bounded exit record: what proved the exit, digests
// of the evidence and exit, and the hand-in it refers to.
type StagedExit struct {
	Proof          string
	EvidenceDigest string `json:",omitempty"`
	ExitDigest     string
	HandInRef      string `json:",omitempty"`
	HandInValid    bool   `json:",omitempty"`
	// Receipt names the operation that applied the exit, once applied.
	Receipt string `json:",omitempty"`
}

// Active reports whether the slot still counts against capacity.
func (s WorkerSlot) Active() bool { return s.State != SlotReleased }

// SlotIndex returns the slot reserved under key, or -1.
func (r Run) SlotIndex(key string) int {
	if key == "" {
		return -1
	}
	for i := range r.Slots {
		if r.Slots[i].LaunchKey == key {
			return i
		}
	}
	return -1
}

// ActiveSlots counts reservations that still hold capacity.
func (r Run) ActiveSlots() int {
	n := 0
	for _, s := range r.Slots {
		if s.Active() {
			n++
		}
	}
	return n
}

// ValidateSlots refuses slot accounting a worker could have asserted itself:
// a release or staged exit without an exit proof, or a key reserved twice.
func (r Run) ValidateSlots() error {
	seen := map[string]bool{}
	for _, s := range r.Slots {
		if s.LaunchKey == "" || s.TaskID == "" {
			return errors.New("worker slot has no launch key or task")
		}
		if seen[s.LaunchKey] {
			return errors.New("worker slot reserved twice for one launch key")
		}
		seen[s.LaunchKey] = true
		switch s.State {
		case SlotReserved, SlotRunning, SlotHeld:
		case SlotStaged, SlotReleased:
			if s.Exit == nil || (s.Exit.Proof != ExitProofExited && s.Exit.Proof != ExitProofAbsent) || s.Exit.ExitDigest == "" {
				return errors.New("worker slot " + string(s.State) + " without a proved exit")
			}
		default:
			return errors.New("worker slot has unknown state")
		}
	}
	return nil
}
