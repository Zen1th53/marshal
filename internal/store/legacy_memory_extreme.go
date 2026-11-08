//go:build !governed

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

const LegacyPrivateMemorySource = "private-extreme-legacy"
const legacyMemoryMaxRecords = 4096
const legacyMemoryMaxBytes = 32 << 20

// LegacyMemoryPlan exposes identity and digests only, never memory bodies or
// launch credentials. The explicit source declaration is operator intent; a
// directory name alone cannot prove that a legacy database was private.
type LegacyMemoryPlan struct {
	Status          string   `json:"status"`
	SourceKind      string   `json:"source_kind"`
	Root            string   `json:"root"`
	ProjectID       string   `json:"project_id"`
	ProjectIdentity string   `json:"project_identity"`
	SourceSchema    int      `json:"source_schema"`
	Digest          string   `json:"digest"`
	MemoryIDs       []string `json:"memory_ids"`
	Records         int      `json:"records"`
	Bytes           int      `json:"bytes"`
	BackupPath      string   `json:"backup_path,omitempty"`
	IndexStatus     string   `json:"index_status,omitempty"`
}

type legacyMemoryAudit struct{ State, Reason, At string }
type legacyMemoryReceipt struct {
	Occurrence, Launch, Owner, Provider, Submission, ProposalDigest, ImportKey, PayloadDigest, State string
	Audit                                                                                            []legacyMemoryAudit
}
type legacyMemoryFact struct {
	Record  model.MemoryRecordV2
	Receipt legacyMemoryReceipt
}
type legacyMemoryBundle struct {
	Format     string
	SourceKind string
	Root       string
	ProjectID  string
	Project    model.Project
	Binding    projectid.Binding
	Schema     int
	Facts      []legacyMemoryFact
}

func legacyDigest(value any) (string, []byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), raw, nil
}

func legacyCanonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if abs != canonical {
		return "", fmt.Errorf("%w: migration requires the canonical root without symlink aliases", model.ErrInvalid)
	}
	return canonical, nil
}

// legacySourcePath checks precisely the files consumed by the read-only source
// connection, not unrelated provider profiles or arbitrary legacy artifacts.
func legacySourcePath(root string) (string, projectid.Binding, error) {
	dir := filepath.Join(root, statepath.OtherDirName)
	for _, name := range []string{"", "state.db", "state.db-wal", "state.db-shm", projectid.BindingFileName} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && (name == "state.db-wal" || name == "state.db-shm") {
			continue
		}
		if err != nil {
			return "", projectid.Binding{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (name == "" && !info.IsDir()) || (name != "" && !info.Mode().IsRegular()) || legacyMultipleLinks(info) {
			return "", projectid.Binding{}, fmt.Errorf("%w: aliased legacy memory source refused", model.ErrInvalid)
		}
	}
	binding, found := projectid.LoadBinding(dir)
	if !found || binding.RecordedRoot != root {
		return "", binding, fmt.Errorf("%w: legacy identity does not bind the canonical project root", model.ErrInvalid)
	}
	return filepath.Join(dir, "state.db"), binding, nil
}

func legacyReadBundle(ctx context.Context, root, kind string) (legacyMemoryBundle, LegacyMemoryPlan, error) {
	b := legacyMemoryBundle{Format: "marshal-private-memory-preservation-v1", SourceKind: kind}
	if kind != LegacyPrivateMemorySource {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: explicitly declare --source-kind %s; Community state is not adopted", model.ErrInvalid, LegacyPrivateMemorySource)
	}
	canonical, err := legacyCanonicalRoot(root)
	if err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	path, binding, err := legacySourcePath(canonical)
	if err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	b.Root, b.Binding = canonical, binding
	if comparison := projectid.Compare(binding, projectid.CollectEvidence(ctx, nil, canonical), canonical); comparison.Verdict != projectid.VerdictSame {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: legacy identity does not match current repository evidence", model.ErrInvalid)
	}
	u := &url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro")
	if err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Every source row below comes from one SQLite read transaction. No WAL
	// checkpoint, schema initialization or source PRAGMA mutation is performed.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	defer tx.Rollback()
	var repository string
	var projects int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&projects); err != nil || projects != 1 {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: source must contain exactly one project", model.ErrInvalid)
	}
	if err = tx.QueryRowContext(ctx, "SELECT project_id,repository,default_branch,pack_version FROM projects").Scan(&b.ProjectID, &repository, &b.Project.DefaultBranch, &b.Project.PackVersion); err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	if repository != canonical {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: source repository root differs", model.ErrInvalid)
	}
	b.Project.ID, b.Project.Repository = b.ProjectID, repository
	if err = tx.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&b.Schema); err != nil || b.Schema != LatestSchemaVersion {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: unsupported legacy memory schema", model.ErrInvalid)
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.memory_id,c.record_json,c.occurrence_id,c.import_key,c.payload_digest,p.launch_id,p.owner_id,p.provider,p.submission_id,p.digest,p.state,substr(p.payload,1,1025) FROM extreme_mcp_memory_candidates c JOIN extreme_mcp_proposals p ON p.project_id=c.project_id AND p.occurrence_id=c.occurrence_id WHERE c.project_id=? AND p.state IN ('committed','applied') ORDER BY c.memory_id LIMIT ?`, b.ProjectID, legacyMemoryMaxRecords+1)
	if err != nil {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: source lacks private accepted-memory journals", model.ErrInvalid)
	}
	var admitted []model.MemoryRecordV2
	admittedBytes := 0
	for rows.Next() {
		var f legacyMemoryFact
		var raw, id, payload string
		err = rows.Scan(&id, &raw, &f.Receipt.Occurrence, &f.Receipt.ImportKey, &f.Receipt.PayloadDigest, &f.Receipt.Launch, &f.Receipt.Owner, &f.Receipt.Provider, &f.Receipt.Submission, &f.Receipt.ProposalDigest, &f.Receipt.State, &payload)
		if err != nil {
			rows.Close()
			return b, LegacyMemoryPlan{}, err
		}
		admittedBytes += len(raw)
		if len(raw) > 1<<20 || admittedBytes > legacyMemoryMaxBytes || len(b.Facts) >= legacyMemoryMaxRecords {
			rows.Close()
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: source exceeds bounded memory migration limits", model.ErrInvalid)
		}
		var candidate model.MemoryRecordV2
		if err = json.Unmarshal([]byte(raw), &candidate); err != nil || candidate.ID != id {
			rows.Close()
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: malformed accepted memory snapshot", model.ErrInvalid)
		}
		var proposal struct{ Action, ID string }
		proposalHash := sha256.Sum256([]byte(payload))
		if len(payload) > 1024 || json.Unmarshal([]byte(payload), &proposal) != nil || proposal.Action != "memory" || proposal.ID != id || hex.EncodeToString(proposalHash[:]) != f.Receipt.ProposalDigest || f.Receipt.Occurrence == "" || f.Receipt.Launch == "" || f.Receipt.Owner == "" || f.Receipt.Provider == "" || f.Receipt.Submission != f.Receipt.ImportKey || f.Receipt.PayloadDigest == "" {
			rows.Close()
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: accepted memory proposal identity is invalid", model.ErrInvalid)
		}
		if candidate.Source.Kind != "live_mcp" || candidate.Source.Reference != f.Receipt.Owner || candidate.SessionID == "" || candidate.ExtMeta["launch_id"] != f.Receipt.Launch || candidate.ExtMeta["provider"] != f.Receipt.Provider || candidate.ExtMeta["canonical_project_root"] != canonical || candidate.ExtMeta["payload_digest"] != f.Receipt.PayloadDigest {
			rows.Close()
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: original private memory source binding differs", model.ErrInvalid)
		}
		f.Record.ID = id
		b.Facts = append(b.Facts, f)
		admitted = append(admitted, candidate)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	if len(b.Facts) == 0 {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: no accepted private memory to preserve", model.ErrNotFound)
	}
	for i := range b.Facts {
		f := &b.Facts[i]
		var recordBytes int
		if err = tx.QueryRowContext(ctx, "SELECT length(CAST(body AS BLOB))+length(CAST(title AS BLOB))+length(CAST(ext_meta_json AS BLOB))+length(CAST(source_json AS BLOB)) FROM memory_records_v2 WHERE project_id=? AND memory_id=?", b.ProjectID, f.Record.ID).Scan(&recordBytes); err != nil || recordBytes > 1<<20 {
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: source memory exceeds record bound or is missing", model.ErrInvalid)
		}
		rec, readErr := getMemoryV2(ctx, tx, b.ProjectID, f.Record.ID)
		if readErr != nil {
			return b, LegacyMemoryPlan{}, readErr
		}
		if rec.ProjectID != b.ProjectID || rec.Scope != string(model.ScopeProject) || rec.ScopeID != b.ProjectID || rec.Authority != model.AuthorityAgent || (rec.Lifecycle != model.MemoryCandidate && rec.Lifecycle != model.MemoryDurable) || rec.ACLScope != "" || rec.ContentDigest != rec.CanonicalDigest() {
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: unsupported accepted memory classification", model.ErrInvalid)
		}
		candidate := admitted[i]
		candidate.ContentDigest = rec.ContentDigest
		// An approved memory is retained as durable; the admitted snapshot is the
		// same record before the operator's decision.
		candidate.Lifecycle = rec.Lifecycle
		candidateDigest, _, _ := legacyDigest(candidate)
		recordDigest, _, _ := legacyDigest(rec)
		if candidateDigest != recordDigest {
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: canonical memory differs from original admitted snapshot", model.ErrConflict)
		}
		if err = rec.Validate(); err != nil {
			return b, LegacyMemoryPlan{}, err
		}
		if err = security.NewFirewall(security.FirewallConfig{}).ScanRecord(ctx, rec); err != nil {
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: unsafe source memory refused", model.ErrInvalid)
		}
		f.Record = rec
		audits, auditErr := tx.QueryContext(ctx, "SELECT state,reason,at FROM extreme_mcp_proposal_audit WHERE project_id=? AND occurrence_id=? ORDER BY sequence LIMIT 129", b.ProjectID, f.Receipt.Occurrence)
		if auditErr != nil {
			return b, LegacyMemoryPlan{}, auditErr
		}
		committed := false
		for audits.Next() {
			var a legacyMemoryAudit
			if err = audits.Scan(&a.State, &a.Reason, &a.At); err != nil {
				audits.Close()
				return b, LegacyMemoryPlan{}, err
			}
			if len(f.Receipt.Audit) >= 128 {
				audits.Close()
				return b, LegacyMemoryPlan{}, model.ErrInvalid
			}
			if a.State == "committed" {
				committed = true
			}
			f.Receipt.Audit = append(f.Receipt.Audit, a)
		}
		err = audits.Err()
		audits.Close()
		if err != nil {
			return b, LegacyMemoryPlan{}, err
		}
		if !committed {
			return b, LegacyMemoryPlan{}, fmt.Errorf("%w: source lacks an original committed receipt", model.ErrInvalid)
		}
	}
	digest, raw, err := legacyDigest(b)
	if err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	if len(raw) > legacyMemoryMaxBytes {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: source exceeds preservation bundle limit", model.ErrInvalid)
	}
	plan := LegacyMemoryPlan{Status: "DRY_RUN", SourceKind: kind, Root: canonical, ProjectID: b.ProjectID, ProjectIdentity: binding.ID.String(), SourceSchema: b.Schema, Digest: digest, Records: len(b.Facts), Bytes: len(raw), MemoryIDs: make([]string, 0, len(b.Facts))}
	for _, f := range b.Facts {
		plan.MemoryIDs = append(plan.MemoryIDs, f.Record.ID)
	}
	if err = tx.Commit(); err != nil {
		return b, LegacyMemoryPlan{}, err
	}
	// Recheck source path/identity after the snapshot; entrypoint checks are not
	// OS confinement against another process running as the same user.
	_, after, err := legacySourcePath(canonical)
	if err != nil || after != binding {
		return b, LegacyMemoryPlan{}, fmt.Errorf("%w: legacy source identity changed", model.ErrConflict)
	}
	return b, plan, nil
}

// PlanLegacyMemoryMigration is read-only, including when the target namespace
// has never been initialized. It does not print or copy any memory payload.
func PlanLegacyMemoryMigration(ctx context.Context, root, kind string) (LegacyMemoryPlan, error) {
	_, p, err := legacyReadBundle(ctx, root, kind)
	return p, err
}

func legacyTarget(ctx context.Context, root string) (*Store, error) {
	if !DatabaseInUseCheckSupported() {
		return nil, fmt.Errorf("%w: offline migration requires open-file inspection", model.ErrUnavailable)
	}
	if err := statepath.Validate(root); err != nil {
		return nil, err
	}
	path := statepath.Path(root, "state.db")
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: initialize the separate Marshal target first", model.ErrInvalid)
	}
	holders, err := processesHolding(path, path+"-wal", path+"-shm")
	if err != nil {
		return nil, err
	}
	if len(holders) != 0 {
		return nil, fmt.Errorf("%w: target database is open; close its Marshal windows and daemon first", model.ErrConflict)
	}
	s, err := Open(ctx, path)
	if err != nil {
		return nil, err
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil || version != LatestSchemaVersion {
		s.Close()
		return nil, model.ErrInvalid
	}
	p, err := s.Project(ctx)
	if err != nil || p.Repository != root {
		s.Close()
		return nil, fmt.Errorf("%w: target is not this canonical project", model.ErrInvalid)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 1 {
		s.Close()
		return nil, model.ErrInvalid
	}
	return s, nil
}

func legacyArchiveSchema(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS extreme_legacy_memory_batches(project_id TEXT NOT NULL,digest TEXT NOT NULL,source_root TEXT NOT NULL,source_identity TEXT NOT NULL,backup_path TEXT NOT NULL,status TEXT NOT NULL,at TEXT NOT NULL,PRIMARY KEY(project_id,digest))`,
		`CREATE TABLE IF NOT EXISTS extreme_legacy_memory_archive(project_id TEXT NOT NULL,memory_id TEXT NOT NULL,digest TEXT NOT NULL,record_json TEXT NOT NULL,receipt_json TEXT NOT NULL,PRIMARY KEY(project_id,memory_id))`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func legacySaveBundle(root, digest string, raw []byte) (string, error) {
	actual := sha256.Sum256(raw)
	if hex.EncodeToString(actual[:]) != digest {
		return "", model.ErrConflict
	}
	dir := statepath.Path(root, "migrations")
	if err := statepath.CheckPath(root, dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "private-memory-"+digest+".json")
	if err := statepath.CheckPath(root, path); err != nil {
		return "", err
	}
	if old, err := os.ReadFile(path); err == nil {
		if string(old) != string(raw) {
			return "", model.ErrConflict
		}
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".preservation-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	d, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

// ApplyLegacyMemoryMigration preserves only already accepted project facts.
// The archive is inert source evidence; it is never a live broker launch,
// permission sink, task grant, session or capability. All target facts, outbox
// pointers and provenance become visible in the same FULL-synced transaction.
func ApplyLegacyMemoryMigration(ctx context.Context, root, kind, expectedDigest string) (LegacyMemoryPlan, error) {
	b, plan, err := legacyReadBundle(ctx, root, kind)
	if err != nil {
		return plan, err
	}
	if expectedDigest == "" || expectedDigest != plan.Digest {
		return plan, fmt.Errorf("%w: migration must match the exact dry-run digest", model.ErrConflict)
	}
	s, err := legacyTarget(ctx, plan.Root)
	if err != nil {
		return plan, err
	}
	defer s.Close()
	target, err := s.Project(ctx)
	if err != nil || target.ID != b.ProjectID {
		return plan, model.ErrInvalid
	}
	if binding, found := projectid.LoadBinding(statepath.Path(plan.Root)); !found || binding.ID != b.Binding.ID || binding.RecordedRoot != plan.Root {
		return plan, fmt.Errorf("%w: target project identity differs", model.ErrInvalid)
	}
	if _, err = s.db.ExecContext(ctx, "PRAGMA synchronous = FULL"); err != nil {
		return plan, err
	}
	_, raw, _ := legacyDigest(b)
	backup, err := legacySaveBundle(plan.Root, plan.Digest, raw)
	if err != nil {
		return plan, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	if err = legacyArchiveSchema(ctx, tx); err != nil {
		return plan, err
	}
	var oldStatus string
	err = tx.QueryRowContext(ctx, "SELECT status FROM extreme_legacy_memory_batches WHERE project_id=? AND digest=?", b.ProjectID, plan.Digest).Scan(&oldStatus)
	if err == nil {
		return plan, fmt.Errorf("%w: preservation batch already exists (%s); do not replay it", model.ErrConflict, oldStatus)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return plan, err
	}
	for _, f := range b.Facts {
		if _, err = getMemoryV2(ctx, tx, b.ProjectID, f.Record.ID); !errors.Is(err, model.ErrNotFound) {
			return plan, fmt.Errorf("%w: target memory ID exists or cannot be verified", model.ErrConflict)
		}
		if err = s.writeMemoryV2Tx(ctx, tx, f.Record); err != nil {
			return plan, err
		}
		_, recordJSON, _ := legacyDigest(f.Record)
		_, receiptJSON, _ := legacyDigest(f.Receipt)
		if _, err = tx.ExecContext(ctx, "INSERT INTO extreme_legacy_memory_archive(project_id,memory_id,digest,record_json,receipt_json) VALUES(?,?,?,?,?)", b.ProjectID, f.Record.ID, plan.Digest, string(recordJSON), string(receiptJSON)); err != nil {
			return plan, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO extreme_legacy_memory_batches(project_id,digest,source_root,source_identity,backup_path,status,at) VALUES(?,?,?,?,?,'migrated',?)", b.ProjectID, plan.Digest, b.Root, b.Binding.ID.String(), backup, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return plan, err
	}
	if err = tx.Commit(); err != nil {
		return plan, err
	}
	plan.Status, plan.BackupPath, plan.IndexStatus = "MIGRATED", backup, "REBUILD_ON_OPEN"
	return plan, nil
}

// InitializeAndApplyLegacyMemoryMigration creates only an absent Extreme
// namespace, preserving the checked legacy project's inert identity. A normal
// fresh init mints a different nonce and must not be overwritten. Staging is
// FULL-synced and published with a no-replace rename; interruption before
// publication leaves only an inert staging directory, which never blocks a
// retry. Interruption after publication can be resumed by ordinary exact-digest
// apply, without --initialize-target. No old role or capability is adopted.
func InitializeAndApplyLegacyMemoryMigration(ctx context.Context, root, kind, expectedDigest string) (LegacyMemoryPlan, error) {
	b, plan, err := legacyReadBundle(ctx, root, kind)
	if err != nil {
		return plan, err
	}
	if expectedDigest == "" || expectedDigest != plan.Digest {
		return plan, model.ErrConflict
	}
	if !DatabaseInUseCheckSupported() {
		return plan, model.ErrUnavailable
	}
	target := statepath.Path(plan.Root)
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return plan, fmt.Errorf("%w: target namespace already exists; explicit initialization never overwrites it", model.ErrConflict)
	}
	stage, err := os.MkdirTemp(plan.Root, ".marshal-extreme-memory-init-*")
	if err != nil {
		return plan, err
	}
	defer os.RemoveAll(stage)
	if err = os.Chmod(stage, 0o700); err != nil {
		return plan, err
	}
	if err = projectid.SaveBinding(stage, b.Binding); err != nil {
		return plan, err
	}
	s, err := Open(ctx, filepath.Join(stage, "state.db"))
	if err != nil {
		return plan, err
	}
	if _, err = s.db.ExecContext(ctx, "PRAGMA synchronous = FULL"); err == nil {
		err = s.Migrate(ctx)
	}
	if err == nil {
		err = s.InitProject(ctx, b.Project)
	}
	closeErr := s.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return plan, err
	}
	if err = os.Chmod(filepath.Join(stage, "state.db"), 0o600); err != nil {
		return plan, err
	}
	for _, path := range []string{filepath.Join(stage, "state.db"), filepath.Join(stage, projectid.BindingFileName), stage} {
		f, openErr := os.Open(path)
		if openErr != nil {
			return plan, openErr
		}
		syncErr := f.Sync()
		closeErr = f.Close()
		if syncErr != nil {
			return plan, syncErr
		}
		if closeErr != nil {
			return plan, closeErr
		}
	}
	if err = legacyPublishNamespace(stage, target); err != nil {
		return plan, fmt.Errorf("publish initialized private namespace: %w", err)
	}
	d, err := os.Open(plan.Root)
	if err != nil {
		return plan, err
	}
	err = d.Sync()
	closeErr = d.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return plan, err
	}
	// A fresh snapshot rechecks the expected source digest after initialization.
	return ApplyLegacyMemoryMigration(ctx, plan.Root, kind, expectedDigest)
}

// RollbackLegacyMemoryMigration removes only unchanged records from this exact
// batch. Subsequent lifecycle/content/revision changes refuse the entire
// rollback. Original legacy data and inert receipt archives are retained.
func RollbackLegacyMemoryMigration(ctx context.Context, root, digest string) (LegacyMemoryPlan, error) {
	canonical, err := legacyCanonicalRoot(root)
	if err != nil {
		return LegacyMemoryPlan{}, err
	}
	s, err := legacyTarget(ctx, canonical)
	if err != nil {
		return LegacyMemoryPlan{}, err
	}
	defer s.Close()
	p, err := s.Project(ctx)
	if err != nil {
		return LegacyMemoryPlan{}, err
	}
	if digest == "" {
		return LegacyMemoryPlan{}, model.ErrInvalid
	}
	if _, err = s.db.ExecContext(ctx, "PRAGMA synchronous = FULL"); err != nil {
		return LegacyMemoryPlan{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyMemoryPlan{}, err
	}
	defer tx.Rollback()
	var status, sourceRoot, backup string
	if err = tx.QueryRowContext(ctx, "SELECT status,source_root,backup_path FROM extreme_legacy_memory_batches WHERE project_id=? AND digest=?", p.ID, digest).Scan(&status, &sourceRoot, &backup); err != nil {
		return LegacyMemoryPlan{}, err
	}
	if status != "migrated" || sourceRoot != canonical {
		return LegacyMemoryPlan{}, model.ErrConflict
	}
	rows, err := tx.QueryContext(ctx, "SELECT memory_id,record_json FROM extreme_legacy_memory_archive WHERE project_id=? AND digest=? ORDER BY memory_id", p.ID, digest)
	if err != nil {
		return LegacyMemoryPlan{}, err
	}
	type snapshot struct{ id, raw string }
	var saved []snapshot
	for rows.Next() {
		var item snapshot
		if err = rows.Scan(&item.id, &item.raw); err != nil {
			rows.Close()
			return LegacyMemoryPlan{}, err
		}
		saved = append(saved, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return LegacyMemoryPlan{}, err
	}
	if len(saved) == 0 {
		return LegacyMemoryPlan{}, model.ErrConflict
	}
	for _, item := range saved {
		rec, e := getMemoryV2(ctx, tx, p.ID, item.id)
		if e != nil {
			return LegacyMemoryPlan{}, e
		}
		_, raw, e := legacyDigest(rec)
		if e != nil || string(raw) != item.raw {
			return LegacyMemoryPlan{}, fmt.Errorf("%w: migrated memory changed; rollback refused", model.ErrConflict)
		}
		var dependents int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_records_v2 r WHERE r.project_id=? AND NOT EXISTS(SELECT 1 FROM extreme_legacy_memory_archive a WHERE a.project_id=r.project_id AND a.memory_id=r.memory_id AND a.digest=?) AND (instr(','||r.evidence_ids||',',','||?||',')>0 OR instr(','||r.supersedes||',',','||?||',')>0 OR instr(','||r.superseded_by||',',','||?||',')>0 OR instr(','||r.conflict_ids||',',','||?||',')>0)`, p.ID, digest, item.id, item.id, item.id, item.id).Scan(&dependents); e != nil {
			return LegacyMemoryPlan{}, e
		}
		if dependents != 0 {
			return LegacyMemoryPlan{}, fmt.Errorf("%w: later memory depends on migrated facts; rollback refused", model.ErrConflict)
		}
	}
	plan := LegacyMemoryPlan{Status: "ROLLED_BACK", SourceKind: LegacyPrivateMemorySource, Root: canonical, ProjectID: p.ID, Digest: digest, Records: len(saved), BackupPath: backup, IndexStatus: "REBUILD_ON_OPEN", MemoryIDs: make([]string, 0, len(saved))}
	for _, item := range saved {
		if _, err = tx.ExecContext(ctx, "DELETE FROM memory_records_v2 WHERE project_id=? AND memory_id=?", p.ID, item.id); err != nil {
			return LegacyMemoryPlan{}, err
		}
		if err = insertMemoryOutboxTx(ctx, tx, p.ID, item.id, "memory.migration_rolled_back", map[string]string{"memory_id": item.id, "migration_digest": digest}); err != nil {
			return LegacyMemoryPlan{}, err
		}
		plan.MemoryIDs = append(plan.MemoryIDs, item.id)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE extreme_legacy_memory_batches SET status='rolled_back' WHERE project_id=? AND digest=? AND status='migrated'", p.ID, digest); err != nil {
		return LegacyMemoryPlan{}, err
	}
	return plan, tx.Commit()
}

// legacyMemoryOrigin preserves the classification used to block generic
// permission re-admission of an original broker ID. Its receipt is historical;
// no live proposal or launch is created in the target database.
func (s *Store) legacyMemoryOrigin(ctx context.Context, project, memoryID string) (MCPMemoryCandidate, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='extreme_legacy_memory_archive')").Scan(&exists); err != nil {
		return MCPMemoryCandidate{}, err
	}
	if !exists {
		return MCPMemoryCandidate{}, sql.ErrNoRows
	}
	var recordJSON, receiptJSON string
	if err := s.db.QueryRowContext(ctx, "SELECT record_json,receipt_json FROM extreme_legacy_memory_archive WHERE project_id=? AND memory_id=?", project, memoryID).Scan(&recordJSON, &receiptJSON); err != nil {
		return MCPMemoryCandidate{}, err
	}
	var rec model.MemoryRecordV2
	var receipt legacyMemoryReceipt
	if err := json.Unmarshal([]byte(recordJSON), &rec); err != nil {
		return MCPMemoryCandidate{}, err
	}
	if err := json.Unmarshal([]byte(receiptJSON), &receipt); err != nil {
		return MCPMemoryCandidate{}, err
	}
	return MCPMemoryCandidate{Occurrence: receipt.Occurrence, ImportKey: receipt.ImportKey, PayloadDigest: receipt.PayloadDigest, Record: rec, State: "historical_migration"}, nil
}
