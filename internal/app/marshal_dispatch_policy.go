package app

// OperatorCanMoveQueuedTask reports whether the operator may move a task that
// has not started yet to another model. It reads the edition's
// governedProviderSwitchAllowed (true in the shipped build, false with
// -tags governed) through one untagged accessor so the TUI never duplicates
// the constant.
func OperatorCanMoveQueuedTask() bool { return governedProviderSwitchAllowed }
