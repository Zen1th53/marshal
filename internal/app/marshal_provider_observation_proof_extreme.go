//go:build !governed

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

const providerObservationRecordPrefix = "process05 provider-observation "
const providerObservationRecordSuffix = "\nRuntime lifecycle observation; paid inference intervals are not recorded."

func decodeProviderObservationRecord(record marshal.CommandRecord) (*execution.ProviderObservation, []byte, error) {
	if record.ExitCode != 0 || len(record.Output) > 4096+len(providerObservationRecordSuffix) || !strings.HasSuffix(record.Output, providerObservationRecordSuffix) {
		return nil, nil, errors.New("runtime provider observation record malformed or oversized")
	}
	raw := []byte(strings.TrimSuffix(record.Output, providerObservationRecordSuffix))
	var observed execution.ProviderObservation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&observed); err != nil {
		return nil, nil, err
	}
	if err := execution.ValidateProviderObservation(&observed); err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(&observed)
	// The runtime encoder emits this exact canonical form. Strict equality also
	// refuses duplicate fields, trailing JSON, noncanonical aliases and unknowns.
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, nil, errors.New("runtime provider observation is not canonical typed JSON")
	}
	return &observed, canonical, nil
}

func (s *MarshalService) runtimeProviderObservationProof(ctx context.Context, run marshal.Run) ([]verification.RuntimeProviderObservationProof, error) {
	var proofs []verification.RuntimeProviderObservationProof
	seen := map[string]bool{}
	total := 0
	for _, task := range run.Tasks {
		if task.Mode != marshal.Governed || s.Store == nil {
			continue
		}
		attempt := 1 + task.EvidenceAttemptBase
		attempt += taskReturns(task)
		retained, err := s.Store.GetMarshalHandIn(ctx, marshalRunID(run), task.PlanTaskID, attempt)
		if errors.Is(err, model.ErrNotFound) {
			continue
		} // Historical missing evidence stays unknown.
		if err != nil {
			return nil, err
		}
		handin := retained.Value
		hasObservations := false
		for _, record := range handin.RuntimeObserved {
			if strings.HasPrefix(record.Command, providerObservationRecordPrefix) {
				hasObservations = true
				break
			}
		}
		if !hasObservations {
			continue
		}
		if !fullProofObject(handin.ResultCommit) || !fullProofObject(task.ResultCommit) {
			return nil, errors.New("runtime provider observation requires full hand-in result identity")
		}
		executionRunID := ""
		providerResultCommit := ""
		for _, record := range handin.RuntimeObserved {
			if !strings.HasPrefix(record.Command, "process05 ") || strings.HasPrefix(record.Command, providerObservationRecordPrefix) {
				continue
			}
			if record.Command == "process05 "+task.PlanTaskID && record.ExitCode == 0 && record.Output == "" {
				continue // The dispatch envelope is not the provider import anchor.
			}
			if executionRunID != "" || record.ExitCode != 0 || !strings.HasPrefix(record.Output, "imported ") || !fullProofObject(strings.TrimPrefix(record.Output, "imported ")) {
				return nil, errors.New("runtime provider observation import anchor malformed")
			}
			executionRunID = strings.TrimPrefix(record.Command, "process05 ")
			providerResultCommit = strings.TrimPrefix(record.Output, "imported ")
		}
		if executionRunID == "" {
			return nil, errors.New("runtime provider observation import anchor missing")
		}
		if _, err := runtimeProofGit(ctx, s.Repository, "merge-base", "--is-ancestor", providerResultCommit, handin.ResultCommit); err != nil {
			return nil, errors.New("runtime provider result is not an ancestor of the exact collected hand-in")
		}
		if _, err := runtimeProofGit(ctx, s.Repository, "merge-base", "--is-ancestor", handin.ResultCommit, task.ResultCommit); err != nil {
			return nil, errors.New("runtime provider hand-in is not an ancestor of the integrated task result")
		}
		for _, record := range handin.RuntimeObserved {
			if !strings.HasPrefix(record.Command, providerObservationRecordPrefix) {
				continue
			}
			observation, raw, err := decodeProviderObservationRecord(record)
			if err != nil {
				return nil, err
			}
			evidenceID := strings.TrimPrefix(record.Command, providerObservationRecordPrefix)
			provider := strings.TrimSuffix(strings.TrimSuffix(observation.Provider, "-app-server"), "-stream")
			if evidenceID == "" || len(evidenceID) > 256 || strings.ContainsAny(evidenceID, " \n\r\t") || seen[evidenceID] || handin.Mode != marshal.Governed || executionRunID == "" || observation.RunID != executionRunID || observation.TaskID != task.PlanTaskID || observation.CanonicalTaskID != canonicalPlanTaskID(run.PlanID, run.PlanVersion, task.PlanTaskID) || observation.Attempt < 1 || provider != handin.Provider {
				return nil, errors.New("runtime provider observation differs from exact accepted hand-in")
			}
			total += len(raw)
			if total > providerObservationProofBytes || len(proofs) >= providerObservationProofCount {
				return nil, errors.New("runtime verifier provider observations exceed bound")
			}
			seen[evidenceID] = true
			proofs = append(proofs, verification.RuntimeProviderObservationProof{Task: task.PlanTaskID, HandInAttempt: attempt, ResultCommit: handin.ResultCommit, ProviderResultCommit: providerResultCommit, IntegratedTaskResultCommit: task.ResultCommit, EvidenceID: evidenceID, Observation: raw})
		}
	}
	return proofs, nil
}

// The independent verifier reads provider observations from its read-only
// proof file, not its prompt, so a large plan's hand-ins (r57: 52 governed
// tasks) are admitted. Each observation stays a bounded typed record.
const (
	providerObservationProofBytes = 2 << 20
	providerObservationProofCount = 1024
)
