package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

// Egress decisions the Security view shows.
const (
	EgressAllowed = "allowed"
	EgressWaiting = "waiting"
	EgressDenied  = "denied"
)

// Read bounds of the Security overview. Each read takes the newest rows of
// one key range and stops; nothing reads a whole table.
const (
	// securityEgressScan is how many of the newest events of each egress
	// event type are looked at for the run's rows.
	securityEgressScan = 200
	// securityEgressRows is how many egress rows the overview keeps.
	securityEgressRows = 20
	// securityLeaseRows bounds the active credential leases read.
	securityLeaseRows = 20
	// securityAuditScan is how many of the newest audit records are read:
	// the newest securityAuditRows show, and honeypot trips are counted in
	// all of them.
	securityAuditScan = 200
	securityAuditRows = 10
)

// SecurityOverview is one Marshal run's security state for the TUI
// Security view: the run's newest egress decisions, the active credential
// leases and the newest audit records.
type SecurityOverview struct {
	RunID string
	// Egress is the run's newest egress decisions, newest first.
	Egress []SecurityEgress
	// Leases is the active bounded credential leases, soonest to end first.
	Leases []SecurityLease
	// Audit is the newest audit records, newest first.
	Audit []SecurityAudit
	// HoneypotTrips counts HONEYPOT_HIT records among the newest audit
	// records; HoneypotPartial is set when there are more records than
	// were read, so older trips may not be counted.
	HoneypotTrips   int
	HoneypotPartial bool
	// Unconfined is the newest lane that ran unconfined on the host; nil
	// before one, and always nil in the default edition, which refuses to.
	Unconfined *SecurityUnconfined
}

// SecurityEgress is one egress decision of a worker in the run.
type SecurityEgress struct {
	Sequence int64
	// Decision is EgressAllowed, EgressWaiting or EgressDenied.
	Decision string
	// Worker is the worker or agent; Task its store task; RunID the worker
	// run whose broker decided, which a pending inbox request names.
	Worker, Task, RunID string
	// Endpoint is host:port.
	Endpoint string
	Reason   string
	At       time.Time
}

// SecurityLease is one active bounded credential lease.
type SecurityLease struct {
	ID, Worker, Task, Purpose string
	// Secret is provider/name of the leased secret.
	Secret  string
	State   string
	Expires time.Time
}

// SecurityAudit is one audit record: its time, type and a short subject.
type SecurityAudit struct {
	At            time.Time
	Type, Subject string
}

// SecurityUnconfined is a lane that ran without the sandbox.
type SecurityUnconfined struct {
	Worker, Task, Cause string
	At                  time.Time
}

// securityEgressTypes are the egress events a decision is read from: the
// broker's attempts, the requests it raised for the operator, and the
// operator's grants and revocations that answer them.
var securityEgressTypes = []events.EventType{
	events.EventTypeNetworkEgressAttempt,
	events.EventTypeNetworkEgressNotification,
	events.EventTypeNetworkEgressGranted,
	events.EventTypeNetworkEgressRevoked,
}

// SecurityOverview reads one run's security state. Every read goes through
// a key or an index: the run by its key; egress events by the (event_type,
// sequence) index, newest first and bounded; leases by the (expires_at,
// revoked_at) index; audit records newest first by rowid, bounded. Nothing
// scans a table with json_extract: event data is decoded in Go.
func (s *Store) SecurityOverview(ctx context.Context, projectID, runID string) (SecurityOverview, error) {
	out := SecurityOverview{RunID: runID}
	if s == nil || s.db == nil {
		return out, fmt.Errorf("store database is not configured")
	}
	if runID == "" {
		return out, fmt.Errorf("%w: missing run", model.ErrInvalid)
	}
	var owner string
	err := s.db.QueryRowContext(ctx, `SELECT project_id FROM marshal_runs WHERE run_id = ?`, runID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && projectID != "" && owner != projectID {
		return out, fmt.Errorf("%w: marshal_runs", model.ErrNotFound)
	}
	if err != nil {
		return out, err
	}
	if out.Egress, err = s.securityEgress(ctx, runID); err != nil {
		return out, err
	}
	if out.Leases, err = s.securityLeases(ctx, time.Now()); err != nil {
		return out, err
	}
	if err = s.securityAudit(ctx, owner, &out); err != nil {
		return out, err
	}
	if out.Unconfined, err = s.securityUnconfined(ctx); err != nil {
		return out, err
	}
	return out, nil
}

type securityEgressEvent struct {
	seq                   int64
	kind                  events.EventType
	worker, task, run, at string
	endpoint, reason      string
	allowed, requestable  bool
}

// securityEgress reads the newest events of each egress type through the
// (event_type, sequence) index and keeps the run's: those of a worker run
// whose parent is the Marshal run, or of the run itself.
func (s *Store) securityEgress(ctx context.Context, runID string) ([]SecurityEgress, error) {
	var evs []securityEgressEvent
	for _, kind := range securityEgressTypes {
		rows, err := s.db.QueryContext(ctx, `SELECT sequence, subject, task_id, run_id, at, data_json FROM structured_events INDEXED BY structured_events_by_type_sequence
			WHERE event_type = ? ORDER BY sequence DESC LIMIT ?`, string(kind), securityEgressScan)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			e := securityEgressEvent{kind: kind}
			var raw string
			if err := rows.Scan(&e.seq, &e.worker, &e.task, &e.run, &e.at, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var data struct {
				Endpoint    string `json:"endpoint"`
				Allowed     bool   `json:"allowed"`
				Reason      string `json:"reason"`
				RuleID      string `json:"rule_id"`
				Requestable bool   `json:"requestable"`
				Parent      string `json:"parent_run_id"`
				Worker      string `json:"worker"`
			}
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				rows.Close()
				return nil, fmt.Errorf("decode egress event %d: %w", e.seq, err)
			}
			if e.run != runID && data.Parent != runID {
				continue
			}
			e.endpoint, e.allowed, e.requestable = data.Endpoint, data.Allowed, data.Requestable
			e.reason = firstNonBlank(data.Reason, data.RuleID)
			if e.worker == "" {
				e.worker = data.Worker
			}
			evs = append(evs, e)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(evs, func(a, b securityEgressEvent) int { return cmp.Compare(a.seq, b.seq) })
	// One row per decision, oldest first: an attempt the broker allowed or
	// refused; a refusal it raised for the operator replaces the refused
	// attempt and waits until the operator's grant or revocation answers it.
	var out []SecurityEgress
	refused := map[string]int{}
	waiting := map[string]int{}
	for _, e := range evs {
		key := e.run + "\x00" + e.endpoint
		row := SecurityEgress{Sequence: e.seq, Worker: e.worker, Task: e.task, RunID: e.run, Endpoint: e.endpoint, Reason: e.reason}
		row.At, _ = time.Parse(time.RFC3339Nano, e.at)
		switch e.kind {
		case events.EventTypeNetworkEgressAttempt:
			row.Decision = EgressDenied
			if e.allowed {
				row.Decision = EgressAllowed
				row.Reason = firstNonBlank(row.Reason, "in the run's allowlist")
			} else {
				row.Reason = firstNonBlank(row.Reason, "not in the run's allowlist")
				refused[key] = len(out)
			}
			out = append(out, row)
		case events.EventTypeNetworkEgressNotification:
			if !e.requestable {
				continue
			}
			row.Decision, row.Reason = EgressWaiting, "outside the run's allowlist · asks the owner"
			if i, ok := refused[key]; ok {
				row.Reason = "outside the run's allowlist: " + out[i].Reason
				out[i] = row
				delete(refused, key)
				waiting[key] = i
			} else {
				waiting[key] = len(out)
				out = append(out, row)
			}
		case events.EventTypeNetworkEgressGranted, events.EventTypeNetworkEgressRevoked:
			i, ok := waiting[key]
			if !ok {
				continue
			}
			delete(waiting, key)
			if e.kind == events.EventTypeNetworkEgressGranted {
				out[i].Decision, out[i].Reason = EgressAllowed, "owner: allow for run"
			} else {
				out[i].Decision, out[i].Reason = EgressDenied, "owner: revoked"
			}
		}
	}
	slices.Reverse(out)
	return out[:min(len(out), securityEgressRows)], nil
}

// securityLeases reads the credential leases that have not ended through
// the (expires_at, revoked_at) index.
func (s *Store) securityLeases(ctx context.Context, now time.Time) ([]SecurityLease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lease_id, subject, task_id, provider, secret_name, purpose, state, expires_at FROM secret_leases INDEXED BY secret_leases_by_expiry
		WHERE expires_at > ? AND revoked_at IS NULL ORDER BY expires_at LIMIT ?`, now.UTC().Format(time.RFC3339), securityLeaseRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecurityLease
	for rows.Next() {
		var l SecurityLease
		var provider, name, expires string
		if err := rows.Scan(&l.ID, &l.Worker, &l.Task, &provider, &name, &l.Purpose, &l.State, &expires); err != nil {
			return nil, err
		}
		if l.State == "revoked" || l.State == "expired" {
			continue
		}
		l.Secret = joinNonBlank("/", provider, name)
		if l.Expires, err = time.Parse(time.RFC3339Nano, expires); err != nil || !l.Expires.After(now) {
			continue
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// securityAudit reads the newest audit records by rowid, keeps the
// project's newest few and counts honeypot trips among all read.
func (s *Store) securityAudit(ctx context.Context, projectID string, out *SecurityOverview) error {
	rows, err := s.db.QueryContext(ctx, `SELECT event_type, COALESCE(project_id, ''), COALESCE(task_id, ''), timestamp, data_json FROM audit_events ORDER BY rowid DESC LIMIT ?`, securityAuditScan)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		var kind, project, task, at, raw string
		if err := rows.Scan(&kind, &project, &task, &at, &raw); err != nil {
			return err
		}
		if project != "" && project != projectID {
			continue
		}
		if kind == "HONEYPOT_HIT" {
			out.HoneypotTrips++
		}
		if len(out.Audit) >= securityAuditRows {
			continue
		}
		a := SecurityAudit{Type: kind, Subject: securityAuditSubject(task, raw)}
		a.At, _ = time.Parse(time.RFC3339Nano, at)
		out.Audit = append(out.Audit, a)
	}
	out.HoneypotPartial = n >= securityAuditScan
	return rows.Err()
}

// securityAuditSubject is a short subject for an audit record: what it was
// about, from the fields most records carry.
func securityAuditSubject(task, raw string) string {
	var data map[string]any
	_ = json.Unmarshal([]byte(raw), &data)
	str := func(k string) string { v, _ := data[k].(string); return v }
	subject := joinNonBlank(" ", str("kind"), firstNonBlank(str("object"), str("endpoint"), str("task_id"), task))
	if allow, ok := data["allow"].(bool); ok {
		subject += map[bool]string{true: " · allowed", false: " · denied"}[allow]
	}
	return strings.TrimSpace(firstNonBlank(subject, str("reason"), str("decision"), str("outcome")))
}

func joinNonBlank(sep string, v ...string) string {
	var parts []string
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, sep)
}
