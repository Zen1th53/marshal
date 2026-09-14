package model

import "time"

// ExecutionModelPreference is a project-scoped, versioned operator choice for
// a harness.  It is configuration for future governed dispatches only: it
// never rewrites a plan, task, lease, or the model recorded by a past run.
type ExecutionModelPreference struct {
	ProjectID string    `json:"project_id"`
	Adapter   string    `json:"adapter"`
	Model     string    `json:"model"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}
