package environment

import (
	"context"
	"strings"
	"sync"
	"time"
)

type ExperienceKind string

const (
	ExpStaticObservation  ExperienceKind = "STATIC_OBSERVATION"
	ExpDynamicEvent        ExperienceKind = "DYNAMIC_EVENT"
	ExpWorkflowNote        ExperienceKind = "WORKFLOW_NOTE"
	ExpGotchaFailureMode   ExperienceKind = "GOTCHA_FAILURE_MODE"
	ExpPremiseConstraint  ExperienceKind = "PREMISE_CONSTRAINT"
)

type Signature struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
	Toolchain string `json:"toolchain"`
}

func (s Signature) Matches(other Signature) bool {
	if s.OS != "" && other.OS != "" && s.OS != other.OS {
		return false
	}
	if s.Arch != "" && other.Arch != "" && s.Arch != other.Arch {
		return false
	}
	return true
}

type Experience struct {
	ID          string         `json:"id"`
	Kind        ExperienceKind `json:"kind"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	Environment Signature      `json:"environment"`
	CreatedAt   time.Time      `json:"created_at"`
}

type ExperienceManager struct {
	mu          sync.RWMutex
	experiences []Experience
}

func NewExperienceManager() *ExperienceManager {
	return &ExperienceManager{}
}

// RecordExperience registers an environment-bound technical experience record.
func (m *ExperienceManager) RecordExperience(ctx context.Context, exp Experience) (Experience, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if exp.CreatedAt.IsZero() {
		exp.CreatedAt = time.Now().UTC()
	}
	m.experiences = append(m.experiences, exp)
	return exp, nil
}

// RecallMatchingExperience retrieves environment experience records matching the caller's environment signature.
func (m *ExperienceManager) RecallMatchingExperience(ctx context.Context, currentEnv Signature, query string) ([]Experience, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []Experience
	queryLower := strings.ToLower(query)

	for _, exp := range m.experiences {
		if !exp.Environment.Matches(currentEnv) {
			continue // Environmental mismatch filter
		}

		fullText := strings.ToLower(exp.Title + " " + exp.Body)
		if queryLower == "" || strings.Contains(fullText, queryLower) {
			results = append(results, exp)
		}
	}

	return results, nil
}
