package store

import (
	"context"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
)

// The architecture receiver tables (schemas 92 and 104-107) stay registered so
// existing databases keep their history and new databases keep the same
// migration ledger. No product code ever enrolled an owner namespace or target
// generation, so no receipt could be accepted through them; the working
// acceptance is the project contract path.

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

// ArchitectureAcceptanceFor reports that no receiver acceptance exists. The
// receiver was never enrolled by any product path, so this lookup has always
// been denied; callers fall back to the project contract.
func (s *Store) ArchitectureAcceptanceFor(context.Context, string) (ArchitectureAcceptance, error) {
	return ArchitectureAcceptance{}, model.ErrPolicyDenied
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

type ArchitectureAcceptance = architecturecodec.ArchitectureAcceptance

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
