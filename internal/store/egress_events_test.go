package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func TestEgressControlEventsOrderedFilteredAndIncremental(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var grant events.Event
	for _, input := range []struct {
		id, run string
		kind    events.EventType
	}{
		{"scope", "RUN-a", events.EventTypeNetworkEgressRequested},
		{"attempt", "RUN-a", events.EventTypeNetworkEgressAttempt},
		{"grant", "RUN-a", events.EventTypeNetworkEgressGranted},
		{"other", "RUN-b", events.EventTypeNetworkEgressNotification},
		{"revoke", "RUN-a", events.EventTypeNetworkEgressRevoked},
	} {
		stored, err := s.Append(t.Context(), events.Event{ID: input.id, RunID: input.run, Type: input.kind, At: time.Now().UTC(), Data: map[string]any{"scope_socket": "private-proxy", "endpoint": "example.com:443"}})
		if err != nil {
			t.Fatal(err)
		}
		if input.id == "grant" {
			grant = stored
		}
	}
	selected, err := s.EgressControlEvents(t.Context(), "RUN-a", 0)
	if err != nil || len(selected) != 3 {
		t.Fatalf("filtered history: %+v %v", selected, err)
	}
	for i, id := range []string{"scope", "grant", "revoke"} {
		if selected[i].ID != id || selected[i].Data["scope_socket"] != "private-proxy" || selected[i].At.IsZero() {
			t.Fatalf("projection metadata/order: %+v", selected[i])
		}
	}
	incremental, err := s.EgressControlEvents(t.Context(), "RUN-a", grant.Sequence)
	if err != nil || len(incremental) != 1 || incremental[0].ID != "revoke" {
		t.Fatalf("incremental cursor: %+v %v", incremental, err)
	}
	all, err := s.EgressControlEvents(t.Context(), "", 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("all scopes: %+v %v", all, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EgressControlEvents(t.Context(), "RUN-a", 0); err == nil {
		t.Fatal("closed store read did not fail")
	}
}

func TestCredentialDecisionEventsCommittedScopeOrderAndErrors(t *testing.T) {
	cases := []struct {
		name, raw string
		want      *bool
		bad       bool
	}{
		{"object_current_last", `{"kind":"credential","object":"claude","object":"codex","allow":false}`, boolPtr(false), false},
		{"object_other_last", `{"kind":"credential","object":"codex","object":"claude","allow":false}`, nil, false},
		{"kind_credential_last", `{"kind":"network","kind":"credential","object":"codex","allow":false}`, boolPtr(false), false},
		{"kind_other_last", `{"kind":"credential","kind":"network","object":"codex","allow":false}`, nil, false},
		{"allow_false_last", `{"kind":"credential","object":"codex","allow":true,"allow":false}`, boolPtr(false), false},
		{"allow_true_last", `{"kind":"credential","object":"codex","allow":false,"allow":true}`, boolPtr(true), false},
		{"allow_invalid_last", `{"kind":"credential","object":"codex","allow":false,"allow":"invalid"}`, nil, true},
		{"allow_valid_last", `{"kind":"credential","object":"codex","allow":"invalid","allow":false}`, boolPtr(false), false},
		{"object_invalid_last", `{"kind":"credential","object":"codex","object":["codex"],"allow":false}`, nil, true},
		{"object_valid_last", `{"kind":"credential","object":["claude"],"object":"codex","allow":false}`, boolPtr(false), false},
		{"escaped_object_current_last", `{"kind":"credential","object":"claude","obj\u0065ct":"codex","allow":false}`, boolPtr(false), false},
		{"allow_null", `{"kind":"credential","object":"codex","allow":null}`, nil, true},
		{"allow_missing", `{"kind":"credential","object":"codex"}`, nil, true},
		{"allow_numeric", `{"kind":"credential","object":"codex","allow":0}`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(t.Context(), `INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES('p','/repo','main','1','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			at := "2026-01-01T00:00:00.000000101Z"
			if _, err := s.db.ExecContext(t.Context(), `INSERT INTO audit_events(event_id,event_type,project_id,aggregate_revision,timestamp,data_json) VALUES(?, 'PERMISSION_DECIDED','p',0,?,?)`, tc.name, at, tc.raw); err != nil {
				t.Fatal(err)
			}
			start, _ := time.Parse(time.RFC3339Nano, "2026-01-01T00:00:00.000000100Z")
			got, err := s.CredentialDecisionEvents(t.Context(), "p", "codex", start)
			if tc.bad {
				if err == nil {
					t.Fatalf("malformed relevant decision returned %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("unrelated decision returned %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].ID != tc.name || got[0].Data["allow"] != *tc.want {
				t.Fatalf("decision: %+v", got)
			}
		})
	}
	t.Run("project_time_order_and_unrelated_history", func(t *testing.T) {
		s, err := Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, project := range []string{"p", "other"} {
			if _, err := s.db.ExecContext(t.Context(), `INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES(?,?,'main','1','2026-01-01T00:00:00Z')`, project, "/"+project); err != nil {
				t.Fatal(err)
			}
		}
		start, _ := time.Parse(time.RFC3339Nano, "2026-01-01T00:00:00.000000100Z")
		rows := []struct{ id, project, kind, at, raw string }{
			{"before", "p", "PERMISSION_DECIDED", "2026-01-01T00:00:00.000000099Z", `{"kind":"credential","object":"codex","allow":false}`},
			{"at", "p", "PERMISSION_DECIDED", "2026-01-01T05:00:00.000000100+05:00", `{"kind":"credential","object":"codex","allow":false}`},
			{"after-a", "p", "PERMISSION_DECIDED", "2026-01-01T00:00:00.000000101Z", `{"kind":"credential","object":"codex","allow":false}`},
			{"after-b", "p", "PERMISSION_DECIDED", "2026-01-01T00:00:00.000000101Z", `{"kind":"credential","object":"codex","allow":true}`},
			{"wrong-project", "other", "PERMISSION_DECIDED", "2026-01-01T00:00:00.000000102Z", `bad`},
			{"wrong-type", "p", "UNRELATED", "2026-01-01T00:00:00.000000102Z", `bad`},
		}
		for _, row := range rows {
			if _, err := s.db.ExecContext(t.Context(), `INSERT INTO audit_events(event_id,event_type,project_id,aggregate_revision,timestamp,data_json) VALUES(?,?,?,0,?,?)`, row.id, row.kind, row.project, row.at, row.raw); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.CredentialDecisionEvents(t.Context(), "p", "codex", start)
		if err != nil {
			t.Fatal(err)
		}
		// SQL preserves ListEvents' timestamp/event-ID ordering. Parsed time
		// determines the boundary even when an offset sorts differently.
		if len(got) != 3 || got[0].ID != "after-a" || got[1].ID != "after-b" || got[2].ID != "at" || got[0].Data["allow"] != false || got[1].Data["allow"] != true {
			t.Fatalf("decisions/order: %+v", got)
		}
	})
}

func boolPtr(value bool) *bool { return &value }

// The watcher asks many times a second; later calls read only new rows but
// still see a revoke committed after the first call, and a malformed relevant
// row committed later fails every call from then on.
func TestCredentialDecisionEventsIncrementalStillSeesNewRevokes(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES('p','/repo','main','1','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, at, raw string) {
		t.Helper()
		if _, err := s.db.ExecContext(t.Context(), `INSERT INTO audit_events(event_id,event_type,project_id,aggregate_revision,timestamp,data_json) VALUES(?, 'PERMISSION_DECIDED','p',0,?,?)`, id, at, raw); err != nil {
			t.Fatal(err)
		}
	}
	start, _ := time.Parse(time.RFC3339Nano, "2026-01-01T00:00:00Z")
	insert("allow", "2026-01-01T00:00:01Z", `{"kind":"credential","object":"codex","allow":true}`)
	got, err := s.CredentialDecisionEvents(t.Context(), "p", "codex", start)
	if err != nil || len(got) != 1 || got[0].ID != "allow" {
		t.Fatalf("first read: %+v %v", got, err)
	}
	insert("revoke", "2026-01-01T00:00:02Z", `{"kind":"credential","object":"codex","allow":false}`)
	got, err = s.CredentialDecisionEvents(t.Context(), "p", "codex", start)
	if err != nil || len(got) != 2 || got[1].ID != "revoke" || got[1].Data["allow"] != false {
		t.Fatalf("revoke after the first read missed: %+v %v", got, err)
	}
	insert("broken", "2026-01-01T00:00:03Z", `{"kind":"credential","object":"codex","allow":"yes"}`)
	for i := 0; i < 2; i++ {
		if _, err := s.CredentialDecisionEvents(t.Context(), "p", "codex", start); err == nil {
			t.Fatalf("call %d accepted a malformed relevant decision", i)
		}
	}
}
