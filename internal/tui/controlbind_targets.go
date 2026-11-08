package tui

// Control bindings: session targets read before operations actions.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
)

func (s *ControlSource) sessionTarget(kind, digest, summary string) Target {
	return Target{
		Kind: kind, ID: s.SessionID, Digest: digest,
		Scope:   fmt.Sprintf("session %s in project %s", s.SessionID, s.ProjectID),
		Summary: summary,
	}
}

func digestTarget(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// prepareAgentRoster binds registration to the exact roster reviewed. A
// concurrent registration therefore moves the target instead of being
// mistaken for evidence that this action succeeded.
func (s *ControlSource) prepareAgentRoster(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	agents, err := s.Authority.Agents(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read agent roster for registration binding: %w", err)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	digest, err := digestTarget(agents)
	if err != nil {
		return Target{}, fmt.Errorf("digest agent roster: %w", err)
	}
	return s.sessionTarget("agent-roster", digest,
		fmt.Sprintf("session %s; %d registered agent(s)", s.SessionID, len(agents))), nil
}

func publicTokenMetadata(records []auth.TokenRecord) []struct {
	ID           string
	Name         string
	Kind         auth.PrincipalKind
	CreatedAt    time.Time
	Revoked      bool
	Capabilities []string
} {
	public := make([]struct {
		ID           string
		Name         string
		Kind         auth.PrincipalKind
		CreatedAt    time.Time
		Revoked      bool
		Capabilities []string
	}, 0, len(records))
	for _, record := range records {
		public = append(public, struct {
			ID           string
			Name         string
			Kind         auth.PrincipalKind
			CreatedAt    time.Time
			Revoked      bool
			Capabilities []string
		}{record.ID, record.Name, record.Kind, record.CreatedAt, record.Revoked, append([]string(nil), record.Capabilities...)})
	}
	sort.Slice(public, func(i, j int) bool { return public[i].ID < public[j].ID })
	return public
}

func (s *ControlSource) prepareTokenRoster(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	records, err := s.Authority.TokenMetadata(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read token metadata: %w", err)
	}
	digest, err := digestTarget(publicTokenMetadata(records))
	if err != nil {
		return Target{}, err
	}
	return s.sessionTarget("access-token-roster", digest,
		fmt.Sprintf("%d local access token record(s)", len(records))), nil
}

func (s *ControlSource) prepareWorkspaceExit(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	if s.Authority.WorkspaceExitRequested() {
		return Target{}, errors.New("workspace exit is already requested")
	}
	return s.sessionTarget("workspace-session", "running",
		"exit the local terminal workspace; durable session state is preserved"), nil
}

func (s *ControlSource) prepareTokenRevoke(ctx context.Context, req ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	id := strings.TrimSpace(req.Inputs["token_id"])
	records, err := s.Authority.TokenMetadata(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read token metadata: %w", err)
	}
	for _, record := range records {
		if record.ID != id {
			continue
		}
		if record.Revoked {
			return Target{}, fmt.Errorf("token %s is already revoked", id)
		}
		digest, digestErr := digestTarget(publicTokenMetadata([]auth.TokenRecord{record}))
		if digestErr != nil {
			return Target{}, digestErr
		}
		return Target{Kind: "access-token", ID: id, Revision: record.CreatedAt.UnixNano(),
			Digest: digest, Scope: strings.Join(record.Capabilities, ","),
			Summary: fmt.Sprintf("%s (%s)", record.Name, record.Kind)}, nil
	}
	return Target{}, fmt.Errorf("token %s was not found", id)
}

func (s *ControlSource) prepareWorktreeGC(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	count, err := s.Authority.GCWorktrees(ctx, true)
	if err != nil {
		return Target{}, fmt.Errorf("read eligible worktrees: %w", err)
	}
	digest, _ := digestTarget(struct {
		Session, Project string
		Eligible         int
	}{s.SessionID, s.ProjectID, count})
	return s.sessionTarget("worktree-set", digest,
		fmt.Sprintf("session %s; %d eligible worktree(s)", s.SessionID, count)), nil
}

func (s *ControlSource) prepareArtifactGC(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	count, err := s.Authority.GCArtifacts(ctx, true)
	if err != nil {
		return Target{}, fmt.Errorf("read eligible artifacts: %w", err)
	}
	digest, _ := digestTarget(struct {
		Session, Project string
		Eligible         int
	}{s.SessionID, s.ProjectID, count})
	return s.sessionTarget("artifact-set", digest,
		fmt.Sprintf("session %s; %d eligible artifact(s)", s.SessionID, count)), nil
}

// prepareMemoryLedger binds a write to the exact project-scoped memory ledger
// the operator reviewed. Typed title/body are part of the idempotency key;
// the ledger digest catches a concurrent change before the write is submitted.
func (s *ControlSource) prepareMemoryLedger(ctx context.Context, _ ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	records, err := s.Authority.RecallRecent(ctx, 128)
	if err != nil {
		return Target{}, fmt.Errorf("read memory ledger: %w", err)
	}
	type row struct {
		ID       string
		Revision int64
		Digest   string
	}
	rows := make([]row, 0, len(records))
	for _, record := range records {
		rows = append(rows, row{record.ID, record.Revision, record.ContentDigest})
	}
	digest, err := digestTarget(rows)
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "memory-ledger", ID: s.ProjectID, Digest: digest,
		Scope:   fmt.Sprintf("project memory for %s", s.ProjectID),
		Summary: fmt.Sprintf("%d visible project memory record(s)", len(rows))}, nil
}

func (s *ControlSource) prepareOutcomeCapture(ctx context.Context, req ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	runID, err := s.Authority.CurrentRunID(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read current run: %w", err)
	}
	if runID == "" {
		return Target{}, errors.New("no Process 05 run is active for outcome capture")
	}
	run, err := s.Authority.GetRun(ctx, runID)
	if err != nil {
		return Target{}, fmt.Errorf("read current run: %w", err)
	}
	taskID := strings.TrimSpace(req.Inputs["task_id"])
	task, ok := run.Tasks[taskID]
	if !ok {
		return Target{}, fmt.Errorf("task %s is not part of current run %s", taskID, runID)
	}
	digest, err := digestTarget(struct {
		Run     string
		Version int64
		Phase   string
		Task    string
		State   string
	}{run.RunID, run.Version, string(run.CurrentPhase), task.TaskID, string(task.State)})
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "run-task-outcome", ID: task.TaskID, Revision: run.Version, Digest: digest,
		Scope:   fmt.Sprintf("task %s in run %s", task.TaskID, run.RunID),
		Summary: fmt.Sprintf("%s at run version %d (%s)", task.TaskID, run.Version, task.State)}, nil
}

// A backup is append-only and does not overwrite the live store. It is bound
// to the canonical schema and object inventory visible at confirmation time;
// the resulting database digest is the post-operation proof.
func (s *ControlSource) prepareBackupState(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	schema, err := s.Authority.StoreSchemaVersion(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read store schema for backup: %w", err)
	}
	counts := map[string]int{}
	for _, table := range []string{"projects", "sessions", "tasks", "artifacts", "memory_records_v2"} {
		n, countErr := s.Authority.ObjectCount(ctx, table)
		if countErr != nil {
			return Target{}, fmt.Errorf("read %s count for backup: %w", table, countErr)
		}
		counts[table] = n
	}
	digest, err := digestTarget(struct {
		Schema int
		Counts map[string]int
	}{schema, counts})
	if err != nil {
		return Target{}, err
	}
	return s.sessionTarget("store-backup", digest,
		fmt.Sprintf("project %s store schema %d", s.ProjectID, schema)), nil
}

// prepareRestoreState verifies the selected backup before opening a destructive
// confirmation. The path stays a sensitive form input; the confirmation is
// bound to the backup's content digest and never renders the host path.
func (s *ControlSource) prepareRestoreState(ctx context.Context, req ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	backupPath := strings.TrimSpace(req.Inputs["backup_path"])
	proof, err := s.Authority.VerifyStateBackup(ctx, backupPath)
	if err != nil {
		return Target{}, fmt.Errorf("verify selected backup: %w", err)
	}
	if proof.Digest == "" || proof.SchemaVersion <= 0 {
		return Target{}, fmt.Errorf("selected backup has no canonical digest or schema version")
	}
	return Target{Kind: "state-backup", ID: proof.Digest, Revision: int64(proof.SchemaVersion),
		Digest: proof.Digest, Scope: fmt.Sprintf("project %s state database", s.ProjectID),
		Summary: fmt.Sprintf("verified backup at schema v%d", proof.SchemaVersion)}, nil
}

func (s *ControlSource) prepareProjectionRebuild(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	count, err := s.Authority.ObjectCount(ctx, "memory_records_v2")
	if err != nil {
		return Target{}, fmt.Errorf("read canonical memory inventory: %w", err)
	}
	digest, _ := digestTarget(struct {
		Project string
		Records int
	}{s.ProjectID, count})
	return s.sessionTarget("memory-projections", digest,
		fmt.Sprintf("project %s; %d canonical memory record(s)", s.ProjectID, count)), nil
}
