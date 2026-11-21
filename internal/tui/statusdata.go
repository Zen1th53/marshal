package tui

// RuntimeStatus adapts app.Status to the deliberately small System reader shape.
type RuntimeStatus struct {
	ProjectID     string
	ProjectName   string
	SchemaVersion int
	AgentCount    int
	SessionCount  int
	TaskCount     int
	LeaseCount    int
	Honeypot      string
}
