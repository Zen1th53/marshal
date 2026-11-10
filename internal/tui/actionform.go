package tui

// InputSpec is one closed, typed field collected for a canonical action.
// There is deliberately no generic command/shell kind.
type InputSpec struct {
	Key       string
	Label     string
	Required  bool
	Sensitive bool
	Default   string
	Validate  func(string) error
	// Advanced marks an optional field reached with Tab. Enter on the field
	// before it submits the form while every later Advanced field is empty.
	Advanced bool
}
