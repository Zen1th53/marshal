//go:build !governed

package tui

// configureBuildBindings applies this edition's own binding changes: the
// Marshal cancels only a pending goal through CTUI-0048.
func configureBuildBindings(source *ControlSource, bindings map[ActionID]Binding) {
	configureGoalControlBindings(source, bindings)
}
