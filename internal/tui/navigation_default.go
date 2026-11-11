//go:build governed

package tui

var buildResolvedGapIDs = []ActionID{}

func buildModelsGap(*Node) bool { return true }

// Community retains its existing dispatch policy.
func (v *NavView) buildActionDispatchRefusal(*Node) string { return "" }

var buildTaskControlOperations = map[ActionID]string{}

func configureBuildIA(*IA) {}

func configureTaskControlBindings(*ControlSource, map[ActionID]Binding) {}

func (v *NavView) buildActionAvailability(action *Node) ActionAvailability {
	return Availability(action)
}
