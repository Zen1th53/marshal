package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

// WebIntent contains only admitted, canonical settings data and trusted identity.
type WebIntent struct {
	AuthGeneration string `json:"authGeneration"`
	ProjectEpoch   string `json:"projectEpoch"`
	TargetKind     string `json:"targetKind"`
	RequestBinding string `json:"requestBinding,omitempty"`
	RequestID      string `json:"requestId"`
	ProjectID      string `json:"projectId"`
	Owner          string `json:"-"`
	Session        string `json:"-"`
	Digest         string `json:"-"`
	Op             string `json:"op"`
	Key            string `json:"key"`
	Value          string `json:"value"`
	Revision       int64  `json:"revision"`
	RunID          string `json:"runId,omitempty"`
	AttemptID      string `json:"attemptId,omitempty"`
}

type WebReceipt struct {
	RequestID string   `json:"requestId"`
	Status    string   `json:"status"`
	Code      string   `json:"code"`
	Revision  int64    `json:"revision"`
	Effects   []string `json:"effects"`
	Result    string   `json:"result,omitempty"`
}

func init() {
	registerCanonicalMigration(112, ddlMigration(`CREATE TABLE IF NOT EXISTS web_settings_actions (
 project_id TEXT NOT NULL, owner TEXT NOT NULL, request_id TEXT NOT NULL,
 digest TEXT NOT NULL, session TEXT NOT NULL, intent TEXT NOT NULL,
 status TEXT NOT NULL, receipt TEXT NOT NULL, created INTEGER NOT NULL,
 PRIMARY KEY(project_id,owner,request_id));
 CREATE TABLE IF NOT EXISTS ultra_preference_revisions (project_id TEXT PRIMARY KEY, revision INTEGER NOT NULL);
 CREATE TRIGGER IF NOT EXISTS ultra_preference_insert_revision AFTER INSERT ON ultra_execution_preferences BEGIN
 INSERT INTO ultra_preference_revisions VALUES(NEW.project_id,1) ON CONFLICT(project_id) DO UPDATE SET revision=revision+1; END;
 CREATE TRIGGER IF NOT EXISTS ultra_preference_update_revision AFTER UPDATE ON ultra_execution_preferences BEGIN
 INSERT INTO ultra_preference_revisions VALUES(NEW.project_id,1) ON CONFLICT(project_id) DO UPDATE SET revision=revision+1; END;`))
}

func (s *Store) UltraPreferenceRevision(ctx context.Context, project string) (int64, error) {
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM ultra_preference_revisions WHERE project_id=?`, project).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return revision, err
}

// WebReceipt keeps a compact digest tombstone after the receipt retention period.
func (s *Store) WebReceipt(ctx context.Context, project, owner, id, digest string) (WebReceipt, error) {
	var raw, saved string
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT receipt,digest,created FROM web_settings_actions WHERE project_id=? AND owner=? AND request_id=?`, project, owner, id).Scan(&raw, &saved, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return WebReceipt{}, model.ErrNotFound
	}
	if err != nil {
		return WebReceipt{}, err
	}
	if digest != "" && saved != digest {
		return WebReceipt{}, model.ErrConflict
	}
	var r WebReceipt
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return r, err
	}
	if time.Now().Unix()-created > int64((7*24*time.Hour)/time.Second) {
		r = WebReceipt{RequestID: id, Status: "refused", Code: "expired", Effects: []string{}}
		raw, _ := json.Marshal(r)
		// Keep the identity and digest for the project lifetime, without old payloads.
		_, err = s.db.ExecContext(ctx, `UPDATE web_settings_actions SET intent='{}',receipt=?,status='expired' WHERE project_id=? AND owner=? AND request_id=? AND status NOT IN ('queued','applying')`, string(raw), project, owner, id)
		if err != nil {
			return WebReceipt{}, err
		}
	}
	return r, nil
}

// AdmitWebSetting commits a bounded durable queue entry before any input-loop wakeup.
func (s *Store) AdmitWebSetting(ctx context.Context, i WebIntent) (WebReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WebReceipt{}, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_settings_actions WHERE status IN ('queued','applying')`).Scan(&count); err != nil {
		return WebReceipt{}, err
	}
	if count >= 64 {
		return WebReceipt{}, model.ErrConflict
	}
	if i.Op == "lane/nudge" {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_settings_actions WHERE project_id=? AND json_extract(intent,'$.op')=? AND json_extract(intent,'$.key')=? AND created>?`, i.ProjectID, i.Op, i.Key, time.Now().Add(-30*time.Second).Unix()).Scan(&count); err != nil {
			return WebReceipt{}, err
		}
		if count != 0 {
			return WebReceipt{}, model.ErrConflict
		}
	}
	var revision int64
	switch i.Op {
	case "settings/set":
		err = tx.QueryRowContext(ctx, `SELECT revision FROM marshal_settings WHERE project_id=?`, i.ProjectID).Scan(&revision)
	case "settings/ultra.set":
		err = tx.QueryRowContext(ctx, `SELECT revision FROM ultra_preference_revisions WHERE project_id=?`, i.ProjectID).Scan(&revision)
	case "settings/ultra.request", "task/retry", "task/retry.debug", "task/reassign", "task/skip", "task/accept", "task/return", "task/approve-task", "task/model.set", "lane/nudge", "lane/restart", "run/pause", "run/stop", "run/resume", "run/stop-workers", "run/amend.deny", "team/provider.verify", "team/provider.route-around", "team/provider.signin.start", "team/session.import.request", "team/architecture.open", "team/telemetry.choose":
		revision = i.Revision
	default:
		return WebReceipt{}, model.ErrInvalid
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return WebReceipt{}, err
	}
	if revision != i.Revision {
		return WebReceipt{}, model.ErrConflict
	}
	// A queued settings revision can be claimed by only one tab.
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_settings_actions WHERE project_id=? AND status IN ('queued','applying') AND json_extract(intent,'$.op')=? AND json_extract(intent,'$.revision')=? AND json_extract(intent,'$.key')=?`, i.ProjectID, i.Op, i.Revision, i.Key).Scan(&count); err != nil {
		return WebReceipt{}, err
	}
	if count != 0 {
		return WebReceipt{}, model.ErrConflict
	}
	raw, _ := json.Marshal(i)
	r := WebReceipt{RequestID: i.RequestID, Status: "queued", Code: "queued", Revision: i.Revision, Effects: []string{}}
	receipt, _ := json.Marshal(r)
	_, err = tx.ExecContext(ctx, `INSERT INTO web_settings_actions VALUES(?,?,?,?,?,?, 'queued',?,?)`, i.ProjectID, i.Owner, i.RequestID, i.Digest, i.Session, string(raw), string(receipt), time.Now().Unix())
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// FenceWebSettings refuses unstarted work from a previous Board process.
func (s *Store) FenceWebSettings(ctx context.Context, project, session string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE web_settings_actions SET status='refused', receipt=json_set(receipt,'$.status','refused','$.code','session_changed','$.result','The authenticated Board session changed before this operation started.') WHERE project_id=? AND session<>? AND status='queued'`, project, session)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE web_settings_actions SET status='interrupted',receipt=json_set(receipt,'$.status','interrupted','$.code','outcome_unknown','$.result','Request outcome unknown; inspect entitlement request status before trying again.') WHERE project_id=? AND session<>? AND status='applying'`, project, session)
	return err
}

func (s *Store) RefuseWebSetting(ctx context.Context, i WebIntent, code string) (WebReceipt, error) {
	r := WebReceipt{RequestID: i.RequestID, Status: "refused", Code: code, Revision: i.Revision, Effects: []string{}}
	switch code {
	case "entitlement_required":
		r.Result = "ULTRA execution requires current entitlement."
	case "stale_or_locked":
		r.Result = "The setting changed or is locked; refresh and preview again."
	case "authority_changed", "session_changed":
		r.Result = "The project or authenticated session changed before this operation started."
	case "writer_refused":
		r.Result = "The Board could not apply this change; refresh and preview again."
	}
	raw, _ := json.Marshal(r)
	_, err := s.db.ExecContext(ctx, `UPDATE web_settings_actions SET status='refused',receipt=? WHERE project_id=? AND owner=? AND request_id=? AND status='queued'`, string(raw), i.ProjectID, i.Owner, i.RequestID)
	return r, err
}

// ApplyWebSetting commits the local effect, audit facts and terminal receipt together.
func (s *Store) ApplyWebSetting(ctx context.Context, i WebIntent) (WebReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WebReceipt{}, err
	}
	defer tx.Rollback()
	var state, session, digest string
	err = tx.QueryRowContext(ctx, `SELECT status,session,digest FROM web_settings_actions WHERE project_id=? AND owner=? AND request_id=?`, i.ProjectID, i.Owner, i.RequestID).Scan(&state, &session, &digest)
	if err != nil {
		return WebReceipt{}, err
	}
	if state != "queued" || session != i.Session || digest != i.Digest {
		return WebReceipt{}, model.ErrConflict
	}
	var next int64
	switch i.Op {
	case "settings/set":
		old := marshal.DefaultSettings()
		var raw string
		var rev int64
		err = tx.QueryRowContext(ctx, `SELECT data_json,revision FROM marshal_settings WHERE project_id=?`, i.ProjectID).Scan(&raw, &rev)
		if err == nil {
			err = json.Unmarshal([]byte(raw), &old)
		} else if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return WebReceipt{}, err
		}
		if rev != i.Revision {
			return WebReceipt{}, model.ErrConflict
		}
		value, e := settingscatalog.Apply(old, i.Key, i.Value)
		if e != nil {
			return WebReceipt{}, e
		}
		next, _, err = s.commitMarshalSettingsTx(ctx, tx, i.ProjectID, value, i.Revision, "owner")
	case "settings/ultra.set":
		var rev int64
		err = tx.QueryRowContext(ctx, `SELECT revision FROM ultra_preference_revisions WHERE project_id=?`, i.ProjectID).Scan(&rev)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return WebReceipt{}, err
		}
		if rev != i.Revision {
			return WebReceipt{}, model.ErrConflict
		}
		if i.Value != "true" && i.Value != "false" {
			return WebReceipt{}, model.ErrInvalid
		}
		err = s.setUltraPreferenceTx(ctx, tx, i.ProjectID, i.Value == "true")
		next = rev + 1
	default:
		return WebReceipt{}, model.ErrInvalid
	}
	if err != nil {
		return WebReceipt{}, err
	}
	r := WebReceipt{RequestID: i.RequestID, Status: "applied", Code: "applied", Revision: next, Effects: []string{i.Key}, Result: "Saved; applies at the next admission or run."}
	raw, _ := json.Marshal(r)
	_, err = tx.ExecContext(ctx, `UPDATE web_settings_actions SET status='applied',receipt=? WHERE project_id=? AND owner=? AND request_id=?`, string(raw), i.ProjectID, i.Owner, i.RequestID)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// StartWebRequest records the exact external journal binding before network work.
func (s *Store) StartWebRequest(ctx context.Context, i WebIntent, binding string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE web_settings_actions SET status='applying', intent=json_set(intent,'$.requestBinding',?), receipt=json_set(receipt,'$.status','applying','$.code','request_started') WHERE project_id=? AND owner=? AND request_id=? AND session=? AND digest=? AND status='queued'`, binding, i.ProjectID, i.Owner, i.RequestID, i.Session, i.Digest)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return model.ErrConflict
	}
	return err
}

// UnfinishedWebRequests returns trusted journal identities for observation only.
func (s *Store) UnfinishedWebRequests(ctx context.Context, project string) ([]WebIntent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT intent,owner,session,digest FROM web_settings_actions WHERE project_id=? AND status IN ('applying','interrupted')`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebIntent
	for rows.Next() {
		var raw string
		var i WebIntent
		var owner, session, digest string
		if err = rows.Scan(&raw, &owner, &session, &digest); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &i); err != nil {
			return nil, err
		}
		i.Owner, i.Session, i.Digest = owner, session, digest
		out = append(out, i)
	}
	return out, rows.Err()
}

// FinishWebRequest records only observations; an entitlement request grants nothing.
func (s *Store) FinishWebRequest(ctx context.Context, i WebIntent, r WebReceipt) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE web_settings_actions SET status=?,receipt=? WHERE project_id=? AND owner=? AND request_id=? AND session=? AND digest=? AND status IN ('applying','interrupted')`, r.Status, string(raw), i.ProjectID, i.Owner, i.RequestID, i.Session, i.Digest)
	return err
}
