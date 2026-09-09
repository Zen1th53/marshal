package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The privacy guarantee is structural: there is no field on Event that could
// hold a prompt, a path or a project name. This test states that as an
// assertion, so adding such a field fails the build rather than shipping.
func TestEventCarriesNoFreeFormField(t *testing.T) {
	allowed := map[string]string{
		"Kind":           "cloud.EventKind",
		"InstallationID": "string",
		"SessionID":      "string",
		"ClientVersion":  "string",
		"OS":             "string",
		"Arch":           "string",
		"Mode":           "cloud.TelemetryMode",
		"ErrorClass":     "cloud.ErrorClass",
		"OccurredAt":     "time.Time",
	}

	typ := reflect.TypeOf(Event{})
	if typ.NumField() != len(allowed) {
		t.Fatalf("Event has %d fields, expected %d; a new field needs a privacy review",
			typ.NumField(), len(allowed))
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		want, ok := allowed[f.Name]
		if !ok {
			t.Fatalf("Event has an unreviewed field %q of type %s", f.Name, f.Type)
		}
		if got := f.Type.String(); got != want {
			t.Fatalf("field %s is %s, expected %s", f.Name, got, want)
		}
		// A map or a slice of anything is how free-form data arrives.
		switch f.Type.Kind() {
		case reflect.Map, reflect.Slice, reflect.Interface:
			t.Fatalf("field %s is a %s, which can carry arbitrary content", f.Name, f.Type.Kind())
		}
	}
}

// Recording must never let a caller attach content. The API takes enums only,
// so this checks the values that actually reach the wire.
func TestRecordedEventsCarryOnlyClosedValues(t *testing.T) {
	r := NewReporter(nil, testInstallation, testSession, "1.0.0", nil)

	r.Record(KindAppStart, TelemetryStandard, "")
	r.Record(KindErrorClass, TelemetryUltra, ErrorNetwork)
	r.Record(KindHeartbeat, TelemetryUltra, "")

	r.mu.Lock()
	queued := append([]Event(nil), r.queue...)
	r.mu.Unlock()

	if len(queued) != 3 {
		t.Fatalf("expected 3 events, got %d", len(queued))
	}
	for _, e := range queued {
		if !validKind(e.Kind) {
			t.Fatalf("event kind %q is outside the closed set", e.Kind)
		}
		if e.Mode != "" && e.Mode != TelemetryStandard && e.Mode != TelemetryUltra {
			t.Fatalf("mode %q is outside the closed set", e.Mode)
		}
		if e.ErrorClass != "" && !validErrorClass(e.ErrorClass) {
			t.Fatalf("error class %q is outside the closed set", e.ErrorClass)
		}
		if e.InstallationID != testInstallation || e.SessionID != testSession {
			t.Fatal("identifiers were altered")
		}
		// OS and Arch describe the binary, not the machine.
		if e.OS == "" || e.Arch == "" {
			t.Fatal("platform fields are empty")
		}
	}
}

func validKind(k EventKind) bool {
	switch k {
	case KindRegistration, KindAppStart, KindHeartbeat, KindModeChange,
		KindActivationOK, KindActivationFail, KindRenewalOK, KindRenewalFail,
		KindErrorClass:
		return true
	}
	return false
}

func validErrorClass(c ErrorClass) bool {
	switch c {
	case ErrorNetwork, ErrorAuth, ErrorLeaseExpired, ErrorLeaseInvalid,
		ErrorRateLimited, ErrorServer, ErrorClient, ErrorSandbox, ErrorUnknown:
		return true
	}
	return false
}

// The serialised payload is what actually leaves the machine, so it is worth
// checking directly rather than trusting the struct definition alone.
func TestSerialisedPayloadCarriesNothingSensitive(t *testing.T) {
	r := NewReporter(nil, testInstallation, testSession, "1.0.0", nil)
	r.Record(KindErrorClass, TelemetryUltra, ErrorLeaseExpired)

	r.mu.Lock()
	payload, err := json.Marshal(r.queue)
	r.mu.Unlock()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Nothing about this machine, this project, or this user may appear.
	body := strings.ToLower(string(payload))
	for _, forbidden := range machineIdentifiers(t) {
		if len(forbidden) < 4 {
			continue
		}
		if strings.Contains(body, strings.ToLower(forbidden)) {
			t.Fatalf("payload contains %q", forbidden)
		}
	}
	for _, forbidden := range []string{"/home/", "/users/", "c:\\", ".go", "prompt", "goal", "command"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("payload contains %q", forbidden)
		}
	}
}

// ClassifyError must reduce an error to a category and never carry its text.
func TestClassifyErrorLosesDetail(t *testing.T) {
	cases := []struct {
		err  error
		want ErrorClass
	}{
		{nil, ""},
		{ErrUnreachable, ErrorNetwork},
		{ErrRefused, ErrorAuth},
		{ErrLeaseExpired, ErrorLeaseExpired},
		{ErrLeaseSignature, ErrorLeaseInvalid},
		{ErrLeaseBinding, ErrorLeaseInvalid},
		{ErrUnknownKey, ErrorLeaseInvalid},
		{ErrState, ErrorClient},
		{errors.New("failed to open /home/someone/secret-project/main.go"), ErrorUnknown},
	}
	for _, c := range cases {
		if got := ClassifyError(c.err); got != c.want {
			t.Fatalf("ClassifyError(%v) = %q, want %q", c.err, got, c.want)
		}
	}

	// The wrapped-path case is the one that matters: the class must not carry
	// any part of the message.
	class := ClassifyError(errors.New("/home/someone/private/repo: permission denied"))
	if strings.Contains(string(class), "/") || strings.Contains(string(class), "home") {
		t.Fatalf("error class leaked path detail: %q", class)
	}
}

// A queue that grew without limit would turn an outage into a memory leak.
func TestQueueIsBounded(t *testing.T) {
	r := NewReporter(nil, testInstallation, testSession, "1.0.0", nil)
	for i := 0; i < maxQueue*3; i++ {
		r.Record(KindHeartbeat, TelemetryUltra, "")
	}
	if q := r.Queued(); q != maxQueue {
		t.Fatalf("queue holds %d, want the bound %d", q, maxQueue)
	}
	if r.Dropped() != maxQueue*2 {
		t.Fatalf("dropped %d, expected %d", r.Dropped(), maxQueue*2)
	}
}

// A nil Reporter must accept and discard, so call sites need no nil checks.
func TestNilReporterIsSafe(t *testing.T) {
	var r *Reporter
	r.Record(KindAppStart, TelemetryStandard, "")
	if r.Queued() != 0 || r.Dropped() != 0 {
		t.Fatal("a nil reporter accumulated state")
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("nil flush: %v", err)
	}
	r.Run(context.Background())
	r.Close(context.Background())
}

// telemetrySink captures batches the client sends.
type telemetrySink struct {
	mu      sync.Mutex
	batches [][]Event
	status  int
}

func newTelemetrySink(t *testing.T) (*telemetrySink, *Client) {
	t.Helper()
	sink := &telemetrySink{status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink.mu.Lock()
		status := sink.status
		sink.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		var batch []Event
		json.NewDecoder(r.Body).Decode(&batch)
		sink.mu.Lock()
		sink.batches = append(sink.batches, batch)
		sink.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"accepted": len(batch), "submitted": len(batch)})
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "1.0.0")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return sink, client
}

func (s *telemetrySink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b)
	}
	return n
}

func TestFlushSendsQueuedEvents(t *testing.T) {
	sink, client := newTelemetrySink(t)
	r := NewReporter(client, testInstallation, testSession, "1.0.0", nil)

	for i := 0; i < 10; i++ {
		r.Record(KindHeartbeat, TelemetryUltra, "")
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sink.count() != 10 {
		t.Fatalf("server received %d events, want 10", sink.count())
	}
	if r.Queued() != 0 {
		t.Fatalf("queue not drained: %d left", r.Queued())
	}
}

// The server refuses batches over 128, so the client must split them.
func TestFlushSplitsOversizedBatches(t *testing.T) {
	sink, client := newTelemetrySink(t)
	r := NewReporter(client, testInstallation, testSession, "1.0.0", nil)

	for i := 0; i < maxQueue; i++ {
		r.Record(KindHeartbeat, TelemetryUltra, "")
	}
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.batches) < 2 {
		t.Fatalf("expected the queue to be split, got %d batch(es)", len(sink.batches))
	}
	for i, b := range sink.batches {
		if len(b) > maxBatch {
			t.Fatalf("batch %d has %d events, over the server's limit of %d", i, len(b), maxBatch)
		}
	}
}

// A failed send must return the events to the queue, so a brief outage costs
// nothing — but the bound must still hold afterwards.
func TestFailedFlushRequeuesWithinTheBound(t *testing.T) {
	sink, client := newTelemetrySink(t)
	r := NewReporter(client, testInstallation, testSession, "1.0.0", nil)

	for i := 0; i < 20; i++ {
		r.Record(KindHeartbeat, TelemetryUltra, "")
	}
	sink.mu.Lock()
	sink.status = http.StatusServiceUnavailable
	sink.mu.Unlock()

	if err := r.Flush(context.Background()); err == nil {
		t.Fatal("a failing server produced a successful flush")
	}
	if r.Queued() != 20 {
		t.Fatalf("events were lost on failure: %d queued, want 20", r.Queued())
	}

	// Recovery sends them.
	sink.mu.Lock()
	sink.status = http.StatusOK
	sink.mu.Unlock()
	if err := r.Flush(context.Background()); err != nil {
		t.Fatalf("flush after recovery: %v", err)
	}
	if sink.count() != 20 {
		t.Fatalf("server received %d after recovery, want 20", sink.count())
	}
}

// An error class on a non-error event, or its absence on one, is a caller
// mistake the server would silently drop. The client normalises instead.
func TestRecordNormalisesErrorClass(t *testing.T) {
	r := NewReporter(nil, testInstallation, testSession, "1.0.0", nil)
	r.Record(KindHeartbeat, TelemetryUltra, ErrorNetwork) // class on a non-error
	r.Record(KindErrorClass, TelemetryUltra, "")          // no class on an error

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.queue[0].ErrorClass != "" {
		t.Fatalf("error class survived on a %s event", r.queue[0].Kind)
	}
	if r.queue[1].ErrorClass != ErrorUnknown {
		t.Fatalf("missing error class was not defaulted, got %q", r.queue[1].ErrorClass)
	}
}

func TestReporterConcurrentUse(t *testing.T) {
	sink, client := newTelemetrySink(t)
	_ = sink
	r := NewReporter(client, testInstallation, testSession, "1.0.0", nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Record(KindHeartbeat, TelemetryUltra, "")
				_ = r.Queued()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			_ = r.Flush(context.Background())
		}
	}()
	wg.Wait()
}

// Close must flush and must not hang, since MARSHAL exits through it.
func TestCloseFlushesAndReturns(t *testing.T) {
	sink, client := newTelemetrySink(t)
	r := NewReporter(client, testInstallation, testSession, "1.0.0", nil)
	r.Record(KindAppStart, TelemetryStandard, "")

	go r.Run(context.Background())

	done := make(chan struct{})
	go func() { r.Close(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	if sink.count() != 1 {
		t.Fatalf("Close did not flush: server got %d events", sink.count())
	}
	// A second Close must be safe.
	r.Close(context.Background())
}
