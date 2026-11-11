package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
)

// Schema 92 is retained as historical data. Schema 93 never backfills authority
// from these tables, JSON approvals, accounts or build flags.
const architectureKnowledgeSchema = `
CREATE TABLE IF NOT EXISTS architecture_targets (
 project_id TEXT PRIMARY KEY REFERENCES projects(project_id),
 root TEXT NOT NULL, store_path TEXT NOT NULL UNIQUE, source_root TEXT NOT NULL,
 protocol TEXT NOT NULL, policy TEXT NOT NULL CHECK(policy IN ('community','private_extreme'))
);
CREATE TABLE IF NOT EXISTS architecture_acceptance_audit (
 receipt_id TEXT PRIMARY KEY, receipt_digest TEXT NOT NULL UNIQUE,
 project_id TEXT NOT NULL REFERENCES architecture_targets(project_id),
 envelope_digest TEXT NOT NULL, origin TEXT NOT NULL CHECK(origin IN ('owner_browser','owner_command'))
);
CREATE TABLE IF NOT EXISTS architecture_acceptances (
 project_id TEXT PRIMARY KEY REFERENCES architecture_targets(project_id),
 architecture_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision > 0),
 digest TEXT NOT NULL, layout_digest TEXT NOT NULL, ui_digest TEXT NOT NULL,
 source_head TEXT NOT NULL, source_tree TEXT NOT NULL,
 receipt_id TEXT NOT NULL UNIQUE REFERENCES architecture_acceptance_audit(receipt_id)
);
CREATE TRIGGER IF NOT EXISTS architecture_targets_no_update BEFORE UPDATE ON architecture_targets BEGIN SELECT RAISE(ABORT,'immutable architecture target'); END;
CREATE TRIGGER IF NOT EXISTS architecture_targets_no_delete BEFORE DELETE ON architecture_targets BEGIN SELECT RAISE(ABORT,'immutable architecture target'); END;
CREATE TRIGGER IF NOT EXISTS architecture_audit_no_update BEFORE UPDATE ON architecture_acceptance_audit BEGIN SELECT RAISE(ABORT,'immutable architecture audit'); END;
CREATE TRIGGER IF NOT EXISTS architecture_audit_no_delete BEFORE DELETE ON architecture_acceptance_audit BEGIN SELECT RAISE(ABORT,'immutable architecture audit'); END;
CREATE TRIGGER IF NOT EXISTS architecture_acceptances_no_update BEFORE UPDATE ON architecture_acceptances BEGIN SELECT RAISE(ABORT,'immutable architecture acceptance'); END;
CREATE TRIGGER IF NOT EXISTS architecture_acceptances_no_delete BEFORE DELETE ON architecture_acceptances BEGIN SELECT RAISE(ABORT,'immutable architecture acceptance'); END;
`

// ArchitectureAcceptanceFor is a read-only joined lookup. Missing policy,
// receipt or audit is denial; there is no migration from JSON approval data.
func (s *Store) ArchitectureAcceptanceFor(ctx context.Context, projectID string) (ArchitectureAcceptance, error) {
	a, err := s.currentArchitectureAcceptance(ctx, projectID)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	source, err := SnapshotArchitectureSource(ctx, a.Root)
	if err != nil || source.GitDirectory != filepath.Join(a.Root, ".git") || source.Dirty != a.SourceDirty || a.Validate(a.ArchitectureTarget, source.Head, source.Tree) != nil {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	return a, nil
}

const architectureOwnerSchema = `
CREATE TABLE IF NOT EXISTS architecture_target_generations (
 project_id TEXT NOT NULL REFERENCES projects(project_id), generation INTEGER NOT NULL CHECK(generation>0),
 enrollment_id TEXT NOT NULL UNIQUE, enrollment BLOB NOT NULL, PRIMARY KEY(project_id,generation)
);
CREATE TABLE IF NOT EXISTS architecture_owner_nonces (
 nonce TEXT PRIMARY KEY, project_id TEXT NOT NULL, generation INTEGER NOT NULL,
 payload_digest TEXT NOT NULL, expires_unix INTEGER NOT NULL, consumed_receipt TEXT,
 FOREIGN KEY(project_id,generation) REFERENCES architecture_target_generations(project_id,generation)
);
CREATE TABLE IF NOT EXISTS architecture_accepted_revisions (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, receipt_id TEXT NOT NULL UNIQUE,
 receipt_digest TEXT NOT NULL UNIQUE, project_id TEXT NOT NULL, generation INTEGER NOT NULL,
 nonce TEXT NOT NULL UNIQUE REFERENCES architecture_owner_nonces(nonce), receipt BLOB NOT NULL,
 FOREIGN KEY(project_id,generation) REFERENCES architecture_target_generations(project_id,generation)
);
CREATE TABLE IF NOT EXISTS architecture_owner_audit (
 receipt_id TEXT PRIMARY KEY REFERENCES architecture_accepted_revisions(receipt_id),
 project_id TEXT NOT NULL, generation INTEGER NOT NULL, message_id TEXT NOT NULL,
 session_id TEXT NOT NULL, origin TEXT NOT NULL CHECK(origin IN ('owner_command','owner_browser')),
 accepted_at TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS architecture_generations_no_update BEFORE UPDATE ON architecture_target_generations BEGIN SELECT RAISE(ABORT,'immutable generation'); END;
CREATE TRIGGER IF NOT EXISTS architecture_generations_no_delete BEFORE DELETE ON architecture_target_generations BEGIN SELECT RAISE(ABORT,'immutable generation'); END;
CREATE TRIGGER IF NOT EXISTS architecture_revisions_no_update BEFORE UPDATE ON architecture_accepted_revisions BEGIN SELECT RAISE(ABORT,'append only acceptance'); END;
CREATE TRIGGER IF NOT EXISTS architecture_revisions_no_delete BEFORE DELETE ON architecture_accepted_revisions BEGIN SELECT RAISE(ABORT,'append only acceptance'); END;
CREATE TRIGGER IF NOT EXISTS architecture_owner_audit_no_update BEFORE UPDATE ON architecture_owner_audit BEGIN SELECT RAISE(ABORT,'append only audit'); END;
CREATE TRIGGER IF NOT EXISTS architecture_owner_audit_no_delete BEFORE DELETE ON architecture_owner_audit BEGIN SELECT RAISE(ABORT,'append only audit'); END;
CREATE TRIGGER IF NOT EXISTS architecture_nonce_update BEFORE UPDATE ON architecture_owner_nonces
 WHEN OLD.consumed_receipt IS NOT NULL OR NEW.consumed_receipt IS NULL OR
 NEW.nonce!=OLD.nonce OR NEW.project_id!=OLD.project_id OR NEW.generation!=OLD.generation OR
 NEW.payload_digest!=OLD.payload_digest OR NEW.expires_unix!=OLD.expires_unix
 BEGIN SELECT RAISE(ABORT,'one use nonce'); END;
CREATE TRIGGER IF NOT EXISTS architecture_nonce_no_delete BEFORE DELETE ON architecture_owner_nonces BEGIN SELECT RAISE(ABORT,'immutable nonce'); END;
`

type architectureQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readArchitectureGeneration(ctx context.Context, q architectureQuery, projectID string) (ArchitectureTargetGeneration, error) {
	var b []byte
	namespace, err := architectureNamespace(ctx, q, projectID)
	if err != nil {
		return ArchitectureTargetGeneration{}, err
	}
	err = q.QueryRowContext(ctx, "SELECT enrollment FROM architecture_target_generations WHERE project_id=? ORDER BY generation DESC LIMIT 1", namespace).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return ArchitectureTargetGeneration{}, model.ErrPolicyDenied
	}
	if err != nil {
		return ArchitectureTargetGeneration{}, err
	}
	var e SignedArchitectureEnrollment
	if json.Unmarshal(b, &e) != nil || e.Target.ProjectID != projectID || e.Verify(ed25519.PublicKey(e.Target.Issuer)) != nil {
		return ArchitectureTargetGeneration{}, model.ErrPolicyDenied
	}
	return e.Target, nil
}
func (s *Store) ArchitectureTargetGenerationFor(ctx context.Context, projectID string) (ArchitectureTargetGeneration, error) {
	if s == nil || s.db == nil {
		return ArchitectureTargetGeneration{}, model.ErrPolicyDenied
	}
	return readArchitectureGeneration(ctx, s.db, projectID)
}
func architectureReceiptDigest(r SignedArchitectureReceipt) (string, []byte, error) {
	b, err := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), b, err
}
func architectureChallengeDigest(c ArchitectureOwnerChallenge) string {
	b, _ := architectureSignedBytes("marshal.architecture.receipt.v1", c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// BeginArchitectureChallenge admits an externally signed pending envelope under
// the compiled edition's origin and target policy. It never supplies a key.
func (s *Store) BeginArchitectureChallenge(ctx context.Context, r SignedArchitectureReceipt) error {
	if !architecturecodec.ArchitectureApprovalOriginAllowed(r.Challenge.Binding) {
		return model.ErrPolicyDenied
	}
	path, err := s.mainDatabasePath(ctx)
	if err != nil || path != r.Challenge.Binding.StorePath || s.validateArchitectureFile(ctx, path) != nil {
		return model.ErrPolicyDenied
	}
	g, err := s.ArchitectureTargetGenerationFor(ctx, r.Challenge.Binding.ProjectID)
	if err != nil {
		return err
	}
	// Authenticate the original envelope before consulting retry state. Use
	// its signed validity interval here; wall-clock expiry is checked below
	// unless the already committed receipt is byte-identical.
	if err = r.Verify(g, time.Unix(r.Challenge.ExpiresUnix-1, 0)); err != nil {
		return err
	}
	var existing []byte
	digest, raw, err := architectureReceiptDigest(r)
	if err != nil {
		return err
	}
	err = s.db.QueryRowContext(ctx, "SELECT receipt FROM architecture_accepted_revisions WHERE receipt_digest=?", digest).Scan(&existing)
	if err == nil && bytes.Equal(existing, raw) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = r.Verify(g, time.Now()); err != nil {
		return err
	}
	c := r.Challenge
	namespace, err := architectureNamespace(ctx, s.db, g.ProjectID)
	if err != nil {
		return err
	}
	var pending string
	err = s.db.QueryRowContext(ctx, "SELECT payload_digest FROM architecture_owner_nonces WHERE nonce=?", c.Nonce).Scan(&pending)
	if err == nil {
		if pending == architectureChallengeDigest(c) {
			return nil
		}
		return model.ErrPolicyDenied
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO architecture_owner_nonces(nonce,project_id,generation,payload_digest,expires_unix) VALUES(?,?,?,?,?)", c.Nonce, namespace, g.Generation, architectureChallengeDigest(c), c.ExpiresUnix)
	return err
}

// AcceptArchitectureReceipt is the sole acceptance writer. current is a fresh
// native observation of the selected review and live source, never receipt
// JSON. Exact retries return the existing result without renewing a nonce.
func (s *Store) AcceptArchitectureReceipt(ctx context.Context, r SignedArchitectureReceipt, current ArchitectureOwnerChallenge) (ArchitectureAcceptance, error) {
	return s.acceptArchitectureReceipt(ctx, r, current, nil)
}

// AcceptArchitectureReceiptWithFacts admits only facts derived from the graph
// whose full canonical digest is already signed by the isolated owner.
func (s *Store) AcceptArchitectureReceiptWithFacts(ctx context.Context, r SignedArchitectureReceipt, current ArchitectureOwnerChallenge, architecture json.RawMessage) (ArchitectureAcceptance, error) {
	digest, err := architecturecodec.CanonicalDigest(architecture)
	if err != nil || digest != r.Challenge.Binding.Digest {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	facts, err := architecturecodec.ArchitectureFactDigests(architecture)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	return s.acceptArchitectureReceipt(ctx, r, current, facts)
}

func (s *Store) acceptArchitectureReceipt(ctx context.Context, r SignedArchitectureReceipt, current ArchitectureOwnerChallenge, facts []string) (ArchitectureAcceptance, error) {
	for attempt := 0; attempt < sqliteBusyRetries; attempt++ {
		a, err := s.acceptArchitectureReceiptOnce(ctx, r, current, facts)
		if !isSQLiteBusy(err) {
			return a, err
		}
		if err = waitSQLiteRetry(ctx, attempt); err != nil {
			return ArchitectureAcceptance{}, err
		}
	}
	return ArchitectureAcceptance{}, model.ErrPolicyDenied
}

func (s *Store) acceptArchitectureReceiptOnce(ctx context.Context, r SignedArchitectureReceipt, current ArchitectureOwnerChallenge, facts []string) (ArchitectureAcceptance, error) {
	deny := func() (ArchitectureAcceptance, error) { return ArchitectureAcceptance{}, model.ErrPolicyDenied }
	c := r.Challenge
	if c.Binding != current.Binding || c.SourceDirty != current.SourceDirty || c.Generation != current.Generation {
		return deny()
	}
	path, err := s.mainDatabasePath(ctx)
	if err != nil || path != c.Binding.StorePath {
		return deny()
	}
	digest, raw, err := architectureReceiptDigest(r)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	defer tx.Rollback()
	if err := s.validateArchitectureTransaction(ctx, tx, path); err != nil {
		return deny()
	}
	g, err := readArchitectureGeneration(ctx, tx, c.Binding.ProjectID)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	namespace, err := architectureNamespace(ctx, tx, g.ProjectID)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	if r.Verify(g, time.Unix(c.ExpiresUnix-1, 0)) != nil {
		return deny()
	}
	// Verify expiry on first acceptance. Existing exact receipts stay idempotent
	// after expiry, but signatures and current generation still must match.
	verifyTime := time.Now()
	var old []byte
	err = tx.QueryRowContext(ctx, "SELECT receipt FROM architecture_accepted_revisions WHERE receipt_id=?", c.Binding.ReceiptID).Scan(&old)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return deny()
		}
		verifyTime = time.Unix(c.ExpiresUnix-1, 0)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ArchitectureAcceptance{}, err
	}
	if r.Verify(g, verifyTime) != nil {
		return deny()
	}
	if old != nil {
		if err := s.validateArchitectureTransaction(ctx, tx, path); err != nil {
			return deny()
		}
		return c.Binding, nil
	}
	// Reject silent renewal and revision rollback even with a fresh signed nonce.
	var latest []byte
	err = tx.QueryRowContext(ctx, "SELECT receipt FROM architecture_accepted_revisions WHERE project_id=? ORDER BY sequence DESC LIMIT 1", namespace).Scan(&latest)
	if err == nil {
		var prior SignedArchitectureReceipt
		if json.Unmarshal(latest, &prior) != nil || prior.Challenge.Binding.ArchitectureID != c.Binding.ArchitectureID || prior.Challenge.Binding.Revision >= c.Binding.Revision {
			return deny()
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ArchitectureAcceptance{}, err
	}
	// Admission, consumption, receipt and audit share the same rollback boundary.
	if err := s.validateArchitectureTransaction(ctx, tx, path); err != nil {
		return deny()
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO architecture_owner_nonces(nonce,project_id,generation,payload_digest,expires_unix) VALUES(?,?,?,?,?) ON CONFLICT(nonce) DO NOTHING", c.Nonce, namespace, g.Generation, architectureChallengeDigest(c), c.ExpiresUnix); err != nil {
		return ArchitectureAcceptance{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE architecture_owner_nonces SET consumed_receipt=? WHERE nonce=? AND project_id=? AND generation=? AND payload_digest=? AND expires_unix>? AND consumed_receipt IS NULL`, c.Binding.ReceiptID, c.Nonce, namespace, g.Generation, architectureChallengeDigest(c), time.Now().Unix())
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return deny()
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO architecture_accepted_revisions(receipt_id,receipt_digest,project_id,generation,nonce,receipt) VALUES(?,?,?,?,?,?)", c.Binding.ReceiptID, digest, namespace, g.Generation, c.Nonce, raw); err != nil {
		return ArchitectureAcceptance{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO architecture_owner_audit VALUES(?,?,?,?,?,?,?)", c.Binding.ReceiptID, namespace, g.Generation, c.MessageID, c.SessionID, c.Binding.Origin, utcNow()); err != nil {
		return ArchitectureAcceptance{}, err
	}
	for _, fact := range facts {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO architecture_fact_bindings VALUES(?,?,?,?,?,?,?)", namespace, fact, c.Binding.ReceiptID, c.Binding.ArchitectureID, c.Binding.Revision, c.Binding.SourceHead, c.Binding.SourceTree); err != nil {
			return ArchitectureAcceptance{}, err
		}
	}
	if err := s.validateArchitectureTransaction(ctx, tx, path); err != nil {
		return deny()
	}
	if err = tx.Commit(); err != nil {
		return ArchitectureAcceptance{}, err
	}
	return c.Binding, nil
}

func (s *Store) currentArchitectureAcceptance(ctx context.Context, projectID string) (ArchitectureAcceptance, error) {
	if s == nil || s.db == nil || projectID == "" {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	g, err := s.ArchitectureTargetGenerationFor(ctx, projectID)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	namespace, err := architectureNamespace(ctx, s.db, g.ProjectID)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	var raw []byte
	var digest, payload, origin, session, message string
	err = s.db.QueryRowContext(ctx, `SELECT r.receipt,r.receipt_digest,n.payload_digest,a.origin,a.session_id,a.message_id
 FROM architecture_accepted_revisions r JOIN architecture_owner_audit a ON a.receipt_id=r.receipt_id AND a.project_id=r.project_id AND a.generation=r.generation
 JOIN architecture_owner_nonces n ON n.nonce=r.nonce AND n.project_id=r.project_id AND n.generation=r.generation AND n.consumed_receipt=r.receipt_id
 WHERE r.project_id=? AND r.generation=? ORDER BY r.sequence DESC LIMIT 1`, namespace, g.Generation).Scan(&raw, &digest, &payload, &origin, &session, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	var r SignedArchitectureReceipt
	if json.Unmarshal(raw, &r) != nil || r.Verify(g, time.Unix(r.Challenge.ExpiresUnix-1, 0)) != nil {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	actual, _, err := architectureReceiptDigest(r)
	if err != nil || actual != digest || payload != architectureChallengeDigest(r.Challenge) || origin != r.Challenge.Binding.Origin || session != r.Challenge.SessionID || message != r.Challenge.MessageID {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	path, err := s.mainDatabasePath(ctx)
	if err != nil || path != g.StorePath || s.validateArchitectureFile(ctx, path) != nil {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	return r.Challenge.Binding, nil
}

type ArchitectureTarget = architecturecodec.ArchitectureTarget
type ArchitectureAcceptance = architecturecodec.ArchitectureAcceptance
type ArchitectureTargetGeneration = architecturecodec.ArchitectureTargetGeneration
type SignedArchitectureEnrollment = architecturecodec.SignedArchitectureEnrollment
type ArchitectureOwnerChallenge = architecturecodec.ArchitectureOwnerChallenge
type SignedArchitectureReceipt = architecturecodec.SignedArchitectureReceipt

func BindArchitectureChallenge(c *ArchitectureOwnerChallenge) {
	architecturecodec.BindArchitectureChallenge(c)
}
func architectureSignedBytes(domain string, value any) ([]byte, error) {
	b, err := json.Marshal(value)
	return append([]byte(domain+"\x00"), b...), err
}

// ArchitectureFactAvailable requires a live source and review binding before
// the canonical fact join. Metadata and historical notes confer no authority.
func (s *Store) ArchitectureFactAvailable(ctx context.Context, projectID string, body json.RawMessage) bool {
	digest, err := architecturecodec.CanonicalDigest(body)
	if err != nil {
		return false
	}
	a, err := s.currentArchitectureAcceptance(ctx, projectID)
	if err != nil {
		return false
	}
	observed, _, err := s.ObserveRegisteredArchitectureOwner(ctx)
	if err != nil || a.Validate(observed.Binding.ArchitectureTarget, observed.Binding.SourceHead, observed.Binding.SourceTree) != nil ||
		a.SourceDirty != observed.SourceDirty ||
		a.MatchesReview(observed.Binding.ArchitectureID, observed.Binding.Revision, observed.Binding.Digest, observed.Binding.LayoutDigest, observed.Binding.UIDigest) != nil {
		return false
	}
	namespace, err := architectureNamespace(ctx, s.db, projectID)
	if err != nil {
		return false
	}
	var n int
	err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM architecture_fact_bindings WHERE project_id=? AND fact_digest=? AND receipt_id=? AND architecture_id=? AND revision=? AND source_head=? AND source_tree=?", namespace, digest, a.ReceiptID, a.ArchitectureID, a.Revision, a.SourceHead, a.SourceTree).Scan(&n)
	return err == nil && n == 1
}

const architectureFactSchema = `
CREATE TABLE IF NOT EXISTS architecture_fact_bindings (
 project_id TEXT NOT NULL, fact_digest TEXT NOT NULL,
 receipt_id TEXT NOT NULL REFERENCES architecture_accepted_revisions(receipt_id),
 architecture_id TEXT NOT NULL, revision INTEGER NOT NULL,
 source_head TEXT NOT NULL, source_tree TEXT NOT NULL,
 PRIMARY KEY(receipt_id,fact_digest)
);
CREATE TRIGGER IF NOT EXISTS architecture_facts_no_update BEFORE UPDATE ON architecture_fact_bindings BEGIN SELECT RAISE(ABORT,'immutable fact binding'); END;
CREATE TRIGGER IF NOT EXISTS architecture_facts_no_delete BEFORE DELETE ON architecture_fact_bindings BEGIN SELECT RAISE(ABORT,'immutable fact binding'); END;
`

const architectureOwnerNamespaceSchema = `
CREATE TABLE IF NOT EXISTS architecture_owner_namespaces (
 canonical_uid TEXT PRIMARY KEY,
 sql_namespace TEXT NOT NULL UNIQUE REFERENCES projects(project_id)
);
CREATE TRIGGER IF NOT EXISTS architecture_namespace_no_update BEFORE UPDATE ON architecture_owner_namespaces BEGIN SELECT RAISE(ABORT,'immutable owner namespace'); END;
CREATE TRIGGER IF NOT EXISTS architecture_namespace_no_delete BEFORE DELETE ON architecture_owner_namespaces BEGIN SELECT RAISE(ABORT,'immutable owner namespace'); END;
`

// Every authority lookup requires the explicit pinned canonical UID mapping.
func architectureNamespace(ctx context.Context, q architectureQuery, id string) (string, error) {
	var namespace string
	err := q.QueryRowContext(ctx, "SELECT sql_namespace FROM architecture_owner_namespaces WHERE canonical_uid=?", id).Scan(&namespace)
	if err != nil || namespace == "" {
		return "", model.ErrPolicyDenied
	}
	return namespace, nil
}

// ArchitectureRegisteredNamespace requires a real pinned mapping. It provides
// no legacy PROJECT-local alias fallback and creates no project rows.
func (s *Store) ArchitectureRegisteredNamespace(ctx context.Context, uid string) (string, error) {
	if s == nil || s.db == nil {
		return "", model.ErrPolicyDenied
	}
	var namespace string
	err := s.db.QueryRowContext(ctx, "SELECT sql_namespace FROM architecture_owner_namespaces WHERE canonical_uid=?", uid).Scan(&namespace)
	if err != nil || namespace == "" {
		return "", model.ErrPolicyDenied
	}
	return namespace, nil
}

// CheckArchitectureNamespace requires the existing canonical UID binding.
func (s *Store) CheckArchitectureNamespace(ctx context.Context, uid, expected string) error {
	namespace, err := architectureNamespace(ctx, s.db, uid)
	if err != nil {
		return err
	}
	if namespace != expected {
		return model.ErrPolicyDenied
	}
	return nil
}
