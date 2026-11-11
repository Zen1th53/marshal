package tui

import (
	"fmt"
	"time"
)

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

func roundDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "under 1s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
