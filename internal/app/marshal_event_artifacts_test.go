package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// A synchronous driver lets the test inspect durable intent at the launch boundary.
type oversizedEventDriver struct {
	driver.Driver
	launch func(driver.Request) error
	cancel func(*driver.Handle) error
}

func (d oversizedEventDriver) Launch(_ context.Context, req driver.Request) (*driver.Handle, error) {
	if err := d.launch(req); err != nil {
		return nil, err
	}
	h := driver.NewHandle(req)
	h.Complete()
	return h, nil
}
func (d oversizedEventDriver) Cancel(h *driver.Handle) error {
	if d.cancel != nil {
		return d.cancel(h)
	}
	return nil
}

func oversizedEventFixture(t *testing.T, databasePath ...string) *MarshalService {
	t.Helper()
	s, _ := marshalFixture(t, 1, databasePath...)
	if err := os.Chmod(filepath.Dir(s.Worktrees), 0700); err != nil {
		t.Fatal(err)
	}
	draft := s.Model.(marshalFakeModel).draft
	draft.Tasks[0].Mode = marshal.Native
	s.Model = marshalFakeModel{draft: draft}
	s.Drivers["worker"] = driver.Native{Provider: "fixture"}
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	return s
}
func oversizedEventBrief(t *testing.T) string {
	t.Helper()
	brief := strings.Repeat("exact context <>&\n", 5000)
	e := events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": brief}}
	if !errors.Is(e.Validate(), events.ErrEventDataInvalid) {
		t.Fatal("fixture does not exceed Event.Validate limit")
	}
	return brief
}

func TestMarshalOversizedEventPreflightDispatch(t *testing.T) {
	for _, name := range []string{"initial", "expanded", "both"} {
		t.Run(name, func(t *testing.T) {
			s := oversizedEventFixture(t)
			brief := oversizedEventBrief(t)
			delivered := brief
			if name != "initial" {
				if name == "expanded" {
					brief = "write"
				}
				delivered = brief + "\n" + oversizedEventBrief(t)
				s.ProjectContext = func(context.Context, string, string) (string, error) { return oversizedEventBrief(t), nil }
			}
			launches := 0
			s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(req driver.Request) error {
				launches++
				if req.Brief != delivered {
					t.Fatal("worker context was truncated or changed")
				}
				record, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
				if err != nil {
					t.Fatal(err)
				}
				if record.Value.Operation == nil {
					t.Fatal("worker launched without durable intent")
				}
				if err := record.Value.Operation.Event.Validate(); err != nil {
					t.Fatalf("unbounded event persisted before worker launch: %v", err)
				}
				sum := sha256.Sum256([]byte(req.Brief))
				if record.Value.Operation.Event.Data["brief_sha256"] != hex.EncodeToString(sum[:]) {
					t.Fatal("pending intent digest does not match the delivered brief")
				}
				full, err := s.fullMarshalEvent(record.Value.Operation.Event)
				if err != nil || full.Data["brief"] != req.Brief {
					t.Fatalf("durable artifact lost worker context: %v", err)
				}
				return nil
			}}
			if _, err := s.Dispatch(t.Context(), "run", "a", brief); err != nil {
				t.Fatalf("oversized dispatch: %v", err)
			}
			if launches != 1 {
				t.Fatalf("launches = %d", launches)
			}
			run, _, err := s.load(t.Context(), "run")
			if err != nil || run.Operation != nil || run.Tasks[0].State != marshal.Dispatched {
				t.Fatalf("completion failed: %+v %v", run, err)
			}
			history, err := s.Store.MarshalDecisions(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range history {
				if err := e.Validate(); err != nil {
					t.Fatal(err)
				}
				if e.Type == events.EventTypeMarshalTaskDispatched {
					full, err := s.fullMarshalEvent(e)
					if err != nil || full.Data["brief"] != delivered {
						t.Fatalf("history artifact lost context: %v", err)
					}
				}
			}
		})
	}
}

func TestMarshalOversizedEventPreflightAbandon(t *testing.T) {
	s := oversizedEventFixture(t)
	s.ProjectContext = func(context.Context, string, string) (string, error) { return oversizedEventBrief(t), nil }
	refused := errors.New("launch refused")
	s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { return refused }}
	if _, err := s.Dispatch(t.Context(), "run", "a", "write"); !errors.Is(err, refused) {
		t.Fatalf("expected launch refusal: %v", err)
	}
	run, _, err := s.load(t.Context(), "run")
	if err != nil || run.Operation != nil || run.Tasks[0].State != marshal.Queued {
		t.Fatalf("failed launch poisoned recovery: %+v %v", run, err)
	}
	if err := s.RecoverPendingOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalOversizedEventArtifactUnsafeDirectory(t *testing.T) {
	for _, kind := range []string{"symlink", "permissions", "read-only", "file"} {
		t.Run(kind, func(t *testing.T) {
			s := oversizedEventFixture(t)
			dir := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(t.TempDir(), dir)
			case "permissions":
				err = os.Mkdir(dir, 0755)
			case "read-only":
				err = os.Mkdir(dir, 0500)
				t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			case "file":
				err = os.WriteFile(dir, []byte("unexpected"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			launches := 0
			s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { launches++; return nil }}
			_, err = s.Dispatch(t.Context(), "run", "a", oversizedEventBrief(t))
			if err == nil || launches != 0 {
				t.Fatalf("unsafe artifact storage reached worker: launches=%d err=%v", launches, err)
			}
			run, _, err := s.load(t.Context(), "run")
			if err != nil || run.Operation != nil {
				t.Fatalf("unsafe artifact storage persisted intent: %v", err)
			}
		})
	}
}

func TestMarshalOversizedEventArtifactDisplacementBoundary(t *testing.T) {
	for _, tc := range []struct {
		name         string
		brief        string
		encodedBytes int
		displaced    bool
	}{
		{"inline-limit", strings.Repeat("x", marshalEventDataLimit-marshalEventHeadroom-12), marshalEventDataLimit - marshalEventHeadroom, false},
		{"inline-limit-plus-one", strings.Repeat("x", marshalEventDataLimit-marshalEventHeadroom-11), marshalEventDataLimit - marshalEventHeadroom + 1, true},
		{"exact-64KiB", strings.Repeat("x", 64*1024-12), 64 * 1024, true},
		{"64KiB-plus-one", strings.Repeat("x", 64*1024-11), 64*1024 + 1, true},
		{"JSON-escaping", strings.Repeat("é<\"", 8000), 80012, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
			event := events.Event{ID: "boundary", Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": tc.brief}}
			data, err := json.Marshal(event.Data)
			if err != nil || len(data) != tc.encodedBytes || len(tc.brief) >= 64*1024 {
				t.Fatalf("invalid boundary fixture: raw=%d serialized=%d err=%v", len(tc.brief), len(data), err)
			}
			original, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.boundMarshalEvent(&event); err != nil {
				t.Fatal(err)
			}
			_, displaced := event.Data["event_artifact"]
			if displaced != tc.displaced {
				t.Fatalf("displaced=%v for %d serialized bytes", displaced, len(data))
			}
			if err := event.Validate(); err != nil {
				t.Fatal(err)
			}
			full, err := s.fullMarshalEvent(event)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := json.Marshal(full)
			if err != nil || !bytes.Equal(original, restored) {
				t.Fatalf("boundary event changed: %v", err)
			}
			if !tc.displaced {
				if _, err := os.Stat(filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("inline event created artifact storage: %v", err)
				}
			}
		})
	}
}

func TestMarshalOversizedEventPreflightAbandonStoredEvent(t *testing.T) {
	s := oversizedEventFixture(t)
	dir := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
	s.ProjectContext = func(context.Context, string, string) (string, error) {
		if err := os.Mkdir(dir, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		return oversizedEventBrief(t), nil
	}
	launches := 0
	s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { launches++; return nil }}
	if _, err := s.Dispatch(t.Context(), "run", "a", "stored brief"); !errors.Is(err, errMarshalEventArtifact) || launches != 0 {
		t.Fatalf("expected pre-launch artifact failure: %v, launches=%d", err, launches)
	}
	run, _, err := s.load(t.Context(), "run")
	if err != nil || run.Operation != nil || run.Tasks[0].State != marshal.Queued {
		t.Fatalf("stored intent was not abandoned: %+v %v", run, err)
	}
	history, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil || len(history) == 0 {
		t.Fatalf("missing escalation: %v", err)
	}
	event := history[len(history)-1]
	if event.Type != events.EventTypeMarshalEscalated || event.Data["brief"] != "stored brief" {
		t.Fatalf("escalation did not use the persisted event: %+v", event)
	}
}

func TestMarshalOversizedEventPreflightAbandonFailure(t *testing.T) {
	for _, kind := range []string{"stored-bounding", "escalated-bounding", "save"} {
		t.Run(kind, func(t *testing.T) {
			s := oversizedEventFixture(t)
			refused := errors.New("launch refused")
			var before marshal.Run
			var revision int64
			s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error {
				record, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
				if err != nil || record.Value.Operation == nil {
					t.Fatalf("missing pending intent: %v", err)
				}
				dir := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
				switch kind {
				case "stored-bounding":
					if err := os.Remove(filepath.Join(dir, artifactReferenceName(t, record.Value.Operation.Event))); err != nil {
						t.Fatal(err)
					}
				case "escalated-bounding":
					// The stored event fits exactly; adding the escalation reason
					// requires an artifact, whose directory is deliberately unsafe.
					record.Value.Operation.Event.Data = map[string]any{"brief": strings.Repeat("x", 64*1024-12)}
					if err := record.Value.Operation.Event.Validate(); err != nil {
						t.Fatal(err)
					}
					if err := s.save(t.Context(), "run", record.Value, record.Revision); err != nil {
						t.Fatal(err)
					}
					record.Revision++
					if err := os.Mkdir(dir, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
				case "save":
					// A duplicate completion ID makes the atomic save fail after
					// bounding, while leaving the pending intent readable.
					if _, err := s.Store.AppendMarshalDecision(t.Context(), record.Value.Operation.Event); err != nil {
						t.Fatal(err)
					}
				}
				before, revision = record.Value, record.Revision
				return refused
			}}
			brief := "write"
			if kind == "stored-bounding" {
				brief = oversizedEventBrief(t)
			}
			_, err := s.Dispatch(t.Context(), "run", "a", brief)
			if !errors.Is(err, refused) {
				t.Fatalf("lost original launch error: %v", err)
			}
			if kind == "save" {
				if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
					t.Fatalf("abandonment save failure was swallowed: %v", err)
				}
			} else if !errors.Is(err, errMarshalEventArtifact) {
				t.Fatalf("abandonment bounding failure was swallowed: %v", err)
			}
			after, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
			if err != nil || after.Revision != revision || !reflect.DeepEqual(after.Value, before) {
				t.Fatalf("failed abandonment changed durable intent: %v", err)
			}
		})
	}
}

func artifactEventFixture(t *testing.T) (*MarshalService, events.Event, events.Event) {
	t.Helper()
	s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
	original := events.Event{ID: "launch", Type: events.EventTypeMarshalTaskDispatched, Subject: "project", RunID: "run", TaskID: "a", Data: map[string]any{"brief": oversizedEventBrief(t), "operation_id": "launch", "operation_kind": "launch", "worker": "worker"}}
	bounded := original
	if err := s.boundMarshalEvent(&bounded); err != nil {
		t.Fatal(err)
	}
	if err := bounded.Validate(); err != nil {
		t.Fatal(err)
	}
	return s, original, bounded
}

func TestMarshalOversizedEventArtifactExactImmutable(t *testing.T) {
	s, original, bounded := artifactEventFixture(t)
	full, err := s.fullMarshalEvent(bounded)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(original)
	got, _ := json.Marshal(full)
	if !bytes.Equal(got, want) {
		t.Fatal("full event changed")
	}
	name := artifactReferenceName(t, bounded)
	path := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts", name)
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatalf("artifact is not private and immutable: %v %v", info, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := original
	if err := s.boundMarshalEvent(&duplicate); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	afterInfo, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !bytes.Equal(before, after) || !os.SameFile(info, afterInfo) {
		t.Fatal("existing artifact replaced")
	}
	if original.Data["brief"] != oversizedEventBrief(t) {
		t.Fatal("bounding mutated caller data")
	}
}

func TestMarshalOversizedEventArtifactRejectReference(t *testing.T) {
	for _, value := range []any{"", "../outside", "/tmp/outside", "nested/file.json", strings.Repeat("A", 64) + ".json", strings.Repeat("0", 64) + ".json", "a\\b", nil, 1, map[string]any{"path": "elsewhere"}} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			s, _, bounded := artifactEventFixture(t)
			bounded.Data["event_artifact"] = value
			if _, err := s.fullMarshalEvent(bounded); err == nil {
				t.Fatal("malformed or missing artifact accepted")
			}
			if err := s.boundMarshalEvent(&bounded); err == nil {
				t.Fatal("unsafe reference was discarded or rebound")
			}
		})
	}
}

func TestMarshalOversizedEventArtifactRejectTampering(t *testing.T) {
	for _, kind := range []string{"contents", "writable", "readable", "directory", "symlink", "hardlink", "fifo", "root-permissions", "root-symlink", "ancestor-symlink", "identity", "metadata", "extra-payload"} {
		t.Run(kind, func(t *testing.T) {
			s, _, bounded := artifactEventFixture(t)
			dir := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
			path := filepath.Join(dir, artifactReferenceName(t, bounded))
			var err error
			switch kind {
			case "contents":
				if err = os.Chmod(path, 0600); err == nil {
					err = os.WriteFile(path, []byte("tampered"), 0600)
				}
				if err == nil {
					err = os.Chmod(path, 0400)
				}
			case "writable":
				err = os.Chmod(path, 0600)
			case "readable":
				err = os.Chmod(path, 0444)
			case "directory", "symlink", "fifo":
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "directory":
					err = os.Mkdir(path, 0700)
				case "symlink":
					target := filepath.Join(t.TempDir(), "target")
					err = os.WriteFile(target, []byte("outside"), 0400)
					if err == nil {
						err = os.Symlink(target, path)
					}
				case "fifo":
					err = syscall.Mkfifo(path, 0400)
				}
			case "hardlink":
				err = os.Link(path, filepath.Join(t.TempDir(), "alias"))
			case "root-permissions":
				err = os.Chmod(dir, 0755)
			case "root-symlink":
				moved := dir + "-moved"
				err = os.Rename(dir, moved)
				if err == nil {
					err = os.Symlink(moved, dir)
				}
			case "ancestor-symlink":
				parent := filepath.Dir(s.Worktrees)
				moved := parent + "-moved"
				err = os.Rename(parent, moved)
				if err == nil {
					err = os.Symlink(moved, parent)
				}
				t.Cleanup(func() { os.Remove(parent); os.Rename(moved, parent) })
			case "identity":
				bounded.ID = "different-operation"
			case "metadata":
				bounded.Data["operation_kind"] = "close"
			case "extra-payload":
				bounded.Data["brief"] = "unverified replacement"
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.fullMarshalEvent(bounded); err == nil {
				t.Fatal("unsafe artifact accepted")
			}
			if err := s.boundMarshalEvent(&bounded); err == nil {
				t.Fatal("unsafe artifact rebound")
			}
		})
	}
}

func TestMarshalOversizedEventArtifactValidationFailsClosed(t *testing.T) {
	for _, kind := range []string{"secret", "type", "encoding"} {
		t.Run(kind, func(t *testing.T) {
			s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
			event := events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": oversizedEventBrief(t)}}
			switch kind {
			case "secret":
				event.Data["api_token"] = "must be rejected"
			case "type":
				event.Type = "invalid"
			case "encoding":
				event.Data["invalid"] = make(chan int)
			}
			if err := s.boundMarshalEvent(&event); err == nil {
				t.Fatal("invalid event was laundered through an artifact")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid event wrote an artifact")
			}
		})
	}
}

func TestMarshalOversizedEventArtifactRejectUnexpectedContents(t *testing.T) {
	for _, kind := range []string{"json", "unknown-field", "nested-reference", "sensitive-field", "different-event", "missing-metadata"} {
		t.Run(kind, func(t *testing.T) {
			s, original, bounded := artifactEventFixture(t)
			if kind == "missing-metadata" {
				delete(bounded.Data, "operation_kind")
			} else {
				data, _ := json.Marshal(original)
				switch kind {
				case "json":
					data = []byte("{invalid json}")
				case "unknown-field":
					data = append([]byte(`{"unexpected":true,`), data[1:]...)
				case "nested-reference":
					original.Data["event_artifact"] = bounded.Data["event_artifact"]
					data, _ = json.Marshal(original)
				case "sensitive-field":
					original.Data["password"] = "rejected"
					data, _ = json.Marshal(original)
				case "different-event":
					original.ID = "other"
					data, _ = json.Marshal(original)
				}
				digest := sha256.Sum256(data)
				name := hex.EncodeToString(digest[:]) + ".json"
				path := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts", name)
				if err := os.WriteFile(path, data, 0400); err != nil {
					t.Fatal(err)
				}
				bounded.Data["event_artifact"] = newMarshalEventReference(name, len(data))
			}
			if _, err := s.fullMarshalEvent(bounded); err == nil {
				t.Fatal("unexpected full artifact accepted")
			}
		})
	}
}

func TestMarshalOversizedEventArtifactExistingTamperNotReplaced(t *testing.T) {
	s, original, bounded := artifactEventFixture(t)
	path := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts", artifactReferenceName(t, bounded))
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := s.boundMarshalEvent(&original); !errors.Is(err, errMarshalEventArtifact) {
		t.Fatalf("existing corrupt artifact replaced: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "tampered" {
		t.Fatalf("existing artifact changed: %v", err)
	}
}

func TestMarshalOversizedEventPreflightLateTampering(t *testing.T) {
	s := oversizedEventFixture(t)
	launches := 0
	s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { launches++; return nil }}
	checks := 0
	s.RequireArchitectureAcceptance = func(context.Context) error {
		checks++
		if checks == 2 {
			record, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
			if err != nil {
				t.Fatal(err)
			}
			name := artifactReferenceName(t, record.Value.Operation.Event)
			if err := os.Remove(filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts", name)); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	if _, err := s.Dispatch(t.Context(), "run", "a", oversizedEventBrief(t)); !errors.Is(err, errMarshalEventArtifact) || launches != 0 {
		t.Fatalf("unsafe artifact reached launch: %v, launches=%d", err, launches)
	}
	if err := s.RecoverPendingOperations(t.Context()); !errors.Is(err, errMarshalEventArtifact) {
		t.Fatalf("unsafe retained reference did not fail closed: %v", err)
	}
}

func artifactReferenceName(t *testing.T, event events.Event) string {
	t.Helper()
	ref, err := parseMarshalEventReference(event.Data["event_artifact"])
	if err != nil {
		t.Fatal(err)
	}
	return ref.SHA256 + ".json"
}

func TestMarshalOversizedEventArtifactConcurrentWriters(t *testing.T) {
	for round := 0; round < 8; round++ {
		s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
		original := events.Event{ID: "same", Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": oversizedEventBrief(t)}}
		start := make(chan struct{})
		results := make(chan error, 12)
		var wg sync.WaitGroup
		for i := 0; i < cap(results); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				event := original
				err := s.boundMarshalEvent(&event)
				if err == nil {
					var full events.Event
					full, err = s.fullMarshalEvent(event)
					if err == nil && full.Data["brief"] != original.Data["brief"] {
						err = errors.New("concurrent writer changed bytes")
					}
				}
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		entries, err := os.ReadDir(filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("publication left temporary files: %v %v", entries, err)
		}
	}
}

func TestMarshalOversizedEventArtifactPublicationWindow(t *testing.T) {
	s, original, bounded := artifactEventFixture(t)
	root, err := s.marshalEventArtifactRoot(false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := lockMarshalEventDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	name := artifactReferenceName(t, bounded)
	if err := root.Link(name, "publishing"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 3)
	for i := 0; i < 2; i++ {
		go func() { event := original; results <- s.boundMarshalEvent(&event) }()
	}
	go func() { _, err := s.fullMarshalEvent(bounded); results <- err }()
	select {
	case err := <-results:
		t.Fatalf("reader/writer observed unfinished publication: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := root.Remove("publishing"); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("publication lock was not released")
		}
	}
}

func TestMarshalOversizedEventArtifactHeadroomAnnotations(t *testing.T) {
	for _, externalized := range []bool{false, true} {
		t.Run(fmt.Sprint(externalized), func(t *testing.T) {
			s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
			event := events.Event{ID: "headroom", Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": strings.Repeat("x", marshalEventDataLimit-marshalEventHeadroom-12)}}
			if externalized {
				event.Data["brief"] = oversizedEventBrief(t)
			}
			if err := s.boundMarshalEvent(&event); err != nil {
				t.Fatal(err)
			}
			event.Data["reason"] = strings.Repeat("<", 300)
			event.Data["launch_outcome"] = "interrupted; driver completion unknown"
			event.Data["model_session_id"] = strings.Repeat("\"", 1000)
			if err := event.Validate(); err != nil {
				t.Fatalf("headroom failed: %v", err)
			}
			if err := s.boundMarshalEvent(&event); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"reason", "launch_outcome", "model_session_id"} {
				if event.Data[key] == nil {
					t.Fatalf("lost %s", key)
				}
			}
			if _, err := s.fullMarshalEvent(event); err != nil {
				t.Fatal(err)
			}
		})
	}
	// These annotations are present at the moment an inline event is displaced.
	s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
	event := events.Event{Type: events.EventTypeMarshalEscalated, Data: map[string]any{"brief": oversizedEventBrief(t), "reason": "refused", "launch_outcome": "unknown", "model_session_id": "conversation"}}
	if err := s.boundMarshalEvent(&event); err != nil {
		t.Fatal(err)
	}
	if event.Data["reason"] != "refused" || event.Data["launch_outcome"] != "unknown" || event.Data["model_session_id"] != "conversation" {
		t.Fatal("externalization stripped annotations")
	}
	event.Data["reason"] = strings.Repeat("<", marshalEventHeadroom)
	before, _ := json.Marshal(event)
	if err := s.boundMarshalEvent(&event); !errors.Is(err, errMarshalEventArtifact) {
		t.Fatalf("unbounded annotations accepted: %v", err)
	}
	after, _ := json.Marshal(event)
	if !bytes.Equal(before, after) {
		t.Fatal("failed annotation bounding mutated the event")
	}
}

func TestMarshalOversizedEventArtifactStructuredReadLimits(t *testing.T) {
	for _, kind := range []string{"version", "kind", "zero", "negative", "too-large", "short-length", "long-length", "unknown", "fraction", "sparse"} {
		t.Run(kind, func(t *testing.T) {
			s, _, bounded := artifactEventFixture(t)
			ref, err := parseMarshalEventReference(bounded.Data["event_artifact"])
			if err != nil {
				t.Fatal(err)
			}
			value := map[string]any{"kind": ref.Kind, "version": ref.Version, "sha256": ref.SHA256, "bytes": ref.Bytes}
			switch kind {
			case "version":
				value["version"] = 2
			case "kind":
				value["kind"] = "worker-output"
			case "zero":
				value["bytes"] = 0
			case "negative":
				value["bytes"] = -1
			case "too-large":
				value["bytes"] = marshalEventArtifactLimit + 1
			case "short-length":
				value["bytes"] = ref.Bytes - 1
			case "long-length":
				value["bytes"] = ref.Bytes + 1
			case "unknown":
				value["path"] = "elsewhere"
			case "fraction":
				value["bytes"] = 1.5
			case "sparse":
				path := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts", ref.SHA256+".json")
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(path, 1<<40); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0400); err != nil {
					t.Fatal(err)
				}
			}
			bounded.Data["event_artifact"] = value
			if _, err := s.fullMarshalEvent(bounded); !errors.Is(err, errMarshalEventArtifact) {
				t.Fatalf("invalid reference/read accepted: %v", err)
			}
		})
	}
}

func TestMarshalOversizedEventArtifactPrivateParent(t *testing.T) {
	s, _, bounded := artifactEventFixture(t)
	parent := filepath.Dir(s.Worktrees)
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0700) })
	if _, err := s.fullMarshalEvent(bounded); !errors.Is(err, errMarshalEventArtifact) {
		t.Fatalf("nonprivate parent accepted: %v", err)
	}
}

func TestMarshalOversizedEventPreflightReservedKey(t *testing.T) {
	s, _, bounded := artifactEventFixture(t)
	for _, value := range []any{bounded.Data["event_artifact"], "caller-value", nil} {
		op := &marshal.LifecycleOperation{Kind: "launch", Event: events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"event_artifact": value}}}
		before, _ := json.Marshal(op)
		if _, err := s.beginOperation(t.Context(), "run", marshal.Run{}, 0, op); !errors.Is(err, errMarshalEventArtifact) {
			t.Fatalf("reserved key accepted: %v", err)
		}
		after, _ := json.Marshal(op)
		if !bytes.Equal(before, after) {
			t.Fatal("rejected inline intent was mutated")
		}
	}
}

func TestMarshalOversizedEventArtifactBriefDigest(t *testing.T) {
	for _, brief := range []string{"small", oversizedEventBrief(t)} {
		for _, value := range []any{strings.Repeat("0", 64), 123, nil} {
			s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
			event := events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": brief, "brief_sha256": value}}
			before, _ := json.Marshal(event)
			if err := s.boundMarshalEvent(&event); !errors.Is(err, errMarshalEventArtifact) {
				t.Fatalf("wrong digest accepted: %v", err)
			}
			after, _ := json.Marshal(event)
			if !bytes.Equal(before, after) {
				t.Fatal("failed bounding mutated the event")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("wrong digest created an artifact")
			}
		}
	}
}

func TestMarshalOversizedEventCompletionNoArtifactAccess(t *testing.T) {
	s := oversizedEventFixture(t)
	dir := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
	launches := 0
	s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error {
		launches++
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		return nil
	}}
	if _, err := s.Dispatch(t.Context(), "run", "a", oversizedEventBrief(t)); err != nil {
		t.Fatalf("completion accessed artifacts after launch: %v", err)
	}
	if launches != 1 {
		t.Fatal("launch count", launches)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completion recreated artifact storage: %v", err)
	}
	run, _, err := s.load(t.Context(), "run")
	if err != nil || run.Operation != nil || run.Tasks[0].State != marshal.Dispatched {
		t.Fatalf("completion not persisted: %+v %v", run, err)
	}
}

func TestMarshalOversizedEventCompletionRejectUnbounded(t *testing.T) {
	s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
	op := &marshal.LifecycleOperation{Next: &marshal.Run{}, Event: events.Event{Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": oversizedEventBrief(t)}}}
	before, _ := json.Marshal(op.Event)
	if err := s.completeOperation(t.Context(), "run", 0, op); !errors.Is(err, events.ErrEventDataInvalid) {
		t.Fatalf("unbounded completion accepted: %v", err)
	}
	after, _ := json.Marshal(op.Event)
	if !bytes.Equal(before, after) {
		t.Fatal("completion bounded the event after the effect")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completion wrote an artifact")
	}
}

func privateEventTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMarshalOversizedEventPreflightExpansionImmutable(t *testing.T) {
	for _, referenced := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("referenced=%v/fail=%v", referenced, fail), func(t *testing.T) {
				s := &MarshalService{Worktrees: filepath.Join(privateEventTempDir(t), "worktrees")}
				brief := "initial"
				if referenced {
					brief = oversizedEventBrief(t)
				}
				sum := sha256.Sum256([]byte(brief))
				original := events.Event{ID: "expand", Type: events.EventTypeMarshalTaskDispatched, Data: map[string]any{"brief": brief, "brief_sha256": hex.EncodeToString(sum[:])}}
				if err := s.boundMarshalEvent(&original); err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(original)
				dir := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
				var oldBytes []byte
				var oldInfo os.FileInfo
				var oldPath string
				if referenced {
					oldPath = filepath.Join(dir, artifactReferenceName(t, original))
					var err error
					oldBytes, err = os.ReadFile(oldPath)
					if err != nil {
						t.Fatal(err)
					}
					oldInfo, err = os.Stat(oldPath)
					if err != nil {
						t.Fatal(err)
					}
				}
				if fail {
					if !referenced {
						if err := os.Mkdir(dir, 0700); err != nil {
							t.Fatal(err)
						}
					}
					// Reject the new write without damaging the first artifact.
					if err := os.Chmod(dir, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
				}
				expandedBrief := brief + "\n" + oversizedEventBrief(t)
				expanded, err := s.marshalEventWithBrief(original, expandedBrief)
				if fail {
					if !errors.Is(err, errMarshalEventArtifact) {
						t.Fatalf("expected bounding failure: %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					full, err := s.fullMarshalEvent(expanded)
					sum := sha256.Sum256([]byte(expandedBrief))
					if err != nil || full.Data["brief"] != expandedBrief || expanded.Data["brief_sha256"] != hex.EncodeToString(sum[:]) {
						t.Fatalf("expanded context/digest lost: %v", err)
					}
					if referenced && artifactReferenceName(t, original) == artifactReferenceName(t, expanded) {
						t.Fatal("expansion reused original reference")
					}
				}
				after, _ := json.Marshal(original)
				if !bytes.Equal(before, after) {
					t.Fatal("expansion mutated original intent before success")
				}
				if referenced {
					afterBytes, err := os.ReadFile(oldPath)
					afterInfo, statErr := os.Stat(oldPath)
					if err != nil || statErr != nil || !bytes.Equal(oldBytes, afterBytes) || !os.SameFile(oldInfo, afterInfo) {
						t.Fatal("first orphaned artifact changed")
					}
				}
			})
		}
	}
}

// A dispatch whose brief is moved to an artifact keeps only a reference
// inline. Review reads the dispatched tier from that event, so it must
// resolve the artifact (a real run stopped with "dispatch tier is missing").
func TestMarshalOversizedDispatchKeepsTier(t *testing.T) {
	s := oversizedEventFixture(t)
	s.Drivers["worker"] = oversizedEventDriver{Driver: s.Drivers["worker"], launch: func(driver.Request) error { return nil }}
	if _, err := s.Dispatch(t.Context(), "run", "a", oversizedEventBrief(t)); err != nil {
		t.Fatalf("oversized dispatch: %v", err)
	}
	history, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	offloaded := false
	for _, e := range history {
		if e.Type == events.EventTypeMarshalTaskDispatched {
			if _, ok := e.Data["event_artifact"]; ok {
				offloaded = true
			}
		}
	}
	if !offloaded {
		t.Fatal("fixture brief did not move the dispatch event to an artifact")
	}
	tier, err := s.dispatchedTier(t.Context(), "run", "a")
	if err != nil {
		t.Fatalf("dispatched tier of an artifact-backed dispatch: %v", err)
	}
	if tier != marshal.Standard && tier != marshal.Ultra {
		t.Fatalf("tier = %q", tier)
	}
}
