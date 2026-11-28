//go:build !governed

package app

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/architectureworkspace"
)

// ArchitectureWorkspace reads the accepted contract and the existing HEAD check.
func (r *Runtime) ArchitectureWorkspace(ctx context.Context, reviewer ...architectureworkspace.Finding) (architectureworkspace.Snapshot, error) {
	if r == nil || r.store == nil {
		return architectureworkspace.Snapshot{}, errors.New("no runtime")
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return architectureworkspace.Snapshot{}, err
	}
	report, checkErr := r.ArchitectureConformance(ctx, "HEAD")
	observations := architectureworkspace.Conformance(report.Findings)
	for _, file := range report.UnownedSample {
		observations = append(observations, architectureworkspace.Finding{Title: "File has no contract owner", Evidence: file, Source: "conformance", Severity: "warning", Status: "open"})
	}
	for _, part := range report.UnimplementedParts {
		observations = append(observations, architectureworkspace.Finding{Title: "No tracked implementation files for this component", Part: part, Source: "conformance", Severity: "warning", Status: "open"})
	}
	snapshot, err := architectureworkspace.Project(accepted.Contract, append(observations, reviewer...))
	snapshot.Notice = "Accepted contract · HEAD conformance. Evidence without a line number is file-level. Areas follow contract parents. Reviewer observations are retained from the latest task attempts."
	if checkErr != nil {
		snapshot.Notice += " Conformance unavailable."
	} else if report.Truncated || report.MoreFindings > 0 || report.UnownedFiles > len(report.UnownedSample) {
		snapshot.Notice += " Conformance results are partial."
	}
	return snapshot, err
}
