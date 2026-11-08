package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func requestFixture(t *testing.T) (*RequestStore, RequestKey) {
	t.Helper()
	return NewRequestStore(t.TempDir()), RequestKey{"http://127.0.0.1:18743", "installation", "session"}
}

func TestEntitlementRequestStatePersistsAcrossRestart(t *testing.T) {
	s, key := requestFixture(t)
	ctx := context.Background()
	attempt, err := s.Begin(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.LocalAttemptID == "" || attempt.ServerRequestID != "" {
		t.Fatal("local ID fabricated a server ID")
	}
	finished, err := s.Finish(ctx, attempt, EntitlementResponse{Status: "pending"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := (&RequestStore{dir: s.dir}).Read(ctx, key)
	if err != nil || !found || got.LocalAttemptID != finished.LocalAttemptID || got.State != "pending" || got.Generation != 2 {
		t.Fatalf("reopen: %+v %v %v", got, found, err)
	}
	for _, other := range []RequestKey{{"http://127.0.0.1:18744", key.InstallationID, key.SessionID}, {key.Endpoint, "other-installation", key.SessionID}, {key.Endpoint, key.InstallationID, "other-session"}} {
		if _, found, err := s.Read(ctx, other); err != nil || found {
			t.Fatalf("identity isolation: %v %v", found, err)
		}
	}
}

func TestEntitlementRequestStateNeverGrantsLease(t *testing.T) {
	s, key := requestFixture(t)
	gate := NewGate(key.InstallationID, key.SessionID, nil, nil)
	a, err := s.Begin(context.Background(), key, 0)
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.Finish(context.Background(), a, EntitlementResponse{Status: "active", RequestID: "observed-server-1"}, nil)
	if err != nil || o.State != "active" {
		t.Fatal(err)
	}
	if gate.Entitled() {
		t.Fatal("request response authorized ULTRA without a signed lease")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(s.dir), "cloud_state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("request state mutated identity or lease state")
	}
}

func TestEntitlementRequestIDsDistinguishLocalAndServer(t *testing.T) {
	for _, id := range []string{"", "server-42"} {
		t.Run(id, func(t *testing.T) {
			s, key := requestFixture(t)
			a, err := s.Begin(context.Background(), key, 0)
			if err != nil {
				t.Fatal(err)
			}
			o, err := s.Finish(context.Background(), a, EntitlementResponse{Status: "pending", RequestID: id}, nil)
			if err != nil || o.ServerRequestID != id || o.LocalAttemptID == id {
				t.Fatalf("IDs: %+v %v", o, err)
			}
		})
	}
}

func TestEntitlementRequestAmbiguousTimeout(t *testing.T) {
	s, key := requestFixture(t)
	a, err := s.Begin(context.Background(), key, 0)
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.Finish(context.Background(), a, EntitlementResponse{}, context.DeadlineExceeded)
	if err != nil || o.State != "unknown" || o.ErrorCode != "request_failed_unknown_outcome" {
		t.Fatalf("timeout: %+v %v", o, err)
	}
	if _, err = s.Finish(context.Background(), a, EntitlementResponse{Status: "pending"}, nil); !errors.Is(err, ErrRequestConflict) {
		t.Fatal("stale finish changed observed outcome")
	}
}

func TestEntitlementRequestConcurrentWriters(t *testing.T) {
	s, key := requestFixture(t)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := &RequestStore{dir: s.dir}
			_, err := other.Begin(context.Background(), key, 0)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrRequestConflict) {
				t.Errorf("concurrent begin: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("CAS winners=%d", wins.Load())
	}
}

func TestEntitlementRequestConcurrentWriterProcess(t *testing.T) {
	dir := os.Getenv("MARSHAL_REQUEST_TEST_DIR")
	if dir == "" {
		return
	}
	s := &RequestStore{dir: dir}
	_, err := s.Begin(context.Background(), RequestKey{"http://127.0.0.1:18743", "installation", "session"}, 0)
	if err == nil {
		os.Stdout.WriteString("won\n")
		os.Exit(0)
	}
	if errors.Is(err, ErrRequestConflict) {
		os.Stdout.WriteString("conflict\n")
		os.Exit(0)
	}
	os.Exit(3)
}

func TestEntitlementRequestTwoProcesses(t *testing.T) {
	s, _ := requestFixture(t)
	commands := make([]*exec.Cmd, 2)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestEntitlementRequestConcurrentWriterProcess$")
		commands[i].Env = append(os.Environ(), "MARSHAL_REQUEST_TEST_DIR="+s.dir)
	}
	var output [2][]byte
	var errs [2]error
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func(i int) { defer wg.Done(); output[i], errs[i] = commands[i].CombinedOutput() }(i)
	}
	wg.Wait()
	wins := 0
	for i := range output {
		if errs[i] != nil {
			t.Fatalf("process: %v %s", errs[i], output[i])
		}
		if string(output[i]) == "won\n" {
			wins++
		} else if string(output[i]) != "conflict\n" {
			t.Fatalf("unexpected process output %q", output[i])
		}
	}
	if wins != 1 {
		t.Fatalf("process CAS winners=%d", wins)
	}
}

func TestEntitlementRequestRejectsInsecureCorruptAndSymlinkState(t *testing.T) {
	for _, kind := range []string{"insecure", "corrupt", "symlink", "future-version", "unknown-field"} {
		t.Run(kind, func(t *testing.T) {
			s, key := requestFixture(t)
			if _, err := s.Begin(context.Background(), key, 0); err != nil {
				t.Fatal(err)
			}
			path, _ := s.path(key)
			switch kind {
			case "insecure":
				os.Chmod(path, 0644)
			case "corrupt":
				os.WriteFile(path, []byte("{"), 0600)
			case "symlink":
				os.Remove(path)
				os.Symlink(filepath.Join(t.TempDir(), "absent"), path)
			case "future-version", "unknown-field":
				data, _ := os.ReadFile(path)
				var obj map[string]any
				json.Unmarshal(data, &obj)
				if kind == "future-version" {
					obj["version"] = 2
				} else {
					obj["lease"] = "forged"
				}
				data, _ = json.Marshal(obj)
				os.WriteFile(path, data, 0600)
			}
			if _, _, err := s.Read(context.Background(), key); !errors.Is(err, ErrState) {
				t.Fatalf("unsafe state accepted: %v", err)
			}
		})
	}
}

type requestRoundTrip func(*http.Request) (*http.Response, error)

func (f requestRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEntitlementRequestTrackedPersistsBeforeNetworkAndReplayDoesNotResend(t *testing.T) {
	s, key := requestFixture(t)
	st, err := NewInstallation()
	if err != nil {
		t.Fatal(err)
	}
	key.InstallationID = st.InstallationID
	c, err := NewClient(key.Endpoint, "test")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.http = &http.Client{Transport: requestRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		o, found, e := s.Read(r.Context(), key)
		if e != nil || !found || o.State != "sending" {
			t.Fatalf("network preceded durable attempt: %+v %v %v", o, found, e)
		}
		body := `{"nonce":"test-nonce"}`
		if strings.HasSuffix(r.URL.Path, "/request") {
			body = `{"status":"pending","tier":"ultra"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	o, err := c.RequestEntitlementTrackedCommand(context.Background(), st, key.SessionID, s, 0, "exact-command-digest")
	if err != nil || o.State != "pending" || o.ServerRequestID != "" || calls != 2 {
		t.Fatalf("typed status: %+v calls=%d %v", o, calls, err)
	}
	replay, err := c.RequestEntitlementTrackedCommand(context.Background(), st, key.SessionID, s, 0, "exact-command-digest")
	if err != nil || replay.LocalAttemptID != o.LocalAttemptID || calls != 2 {
		t.Fatalf("replay resent: %v calls=%d", err, calls)
	}
}

func TestEntitlementRequestInterruptedAttemptNeverResends(t *testing.T) {
	s, key := requestFixture(t)
	a, _, err := s.begin(context.Background(), key, 0, "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	b, replay, err := (&RequestStore{dir: s.dir}).begin(context.Background(), key, 0, "interrupted")
	if err != nil || !replay || b.LocalAttemptID != a.LocalAttemptID || b.State != "sending" {
		t.Fatalf("interrupted replay: %+v %v %v", b, replay, err)
	}
}

func TestEntitlementRequestPersistenceFailurePreventsNetwork(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	c, _ := NewClient("http://127.0.0.1:18743", "test")
	c.http = &http.Client{Transport: requestRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("network request after persistence failure")
		return nil, nil
	})}
	st, _ := NewInstallation()
	if _, err := c.RequestEntitlementTracked(context.Background(), st, "session", NewRequestStore(blocked), 0); err == nil {
		t.Fatal("persistence failure hidden")
	}
}

func TestEntitlementRequestReadHasNoPersistenceSideEffects(t *testing.T) {
	s, key := requestFixture(t)
	if _, found, err := s.Read(context.Background(), key); err != nil || found {
		t.Fatalf("absent read: %v %v", found, err)
	}
	if _, err := os.Lstat(s.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only request observation created state files")
	}
}
