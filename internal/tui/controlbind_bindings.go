package tui

// Control bindings: the Bindings inventory of every Control action.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

// Bindings returns the one action the composer runs through a binding:
// CTUI-0048, goal cancellation. The shipped build applies its pending-goal
// override through configureBuildBindings.
func (s *ControlSource) Bindings() map[ActionID]Binding {
	b := map[ActionID]Binding{"CTUI-0048": goalCancelBinding(s)}
	configureBuildBindings(s, b)
	return b
}
