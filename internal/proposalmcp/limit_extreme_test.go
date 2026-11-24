//go:build !governed && unix

package proposalmcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// recorded is every request a test broker served.
type recorded struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorded) handler(result string, err error) Handler {
	return func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		r.mu.Lock()
		r.calls = append(r.calls, method+" "+string(raw))
		r.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return json.RawMessage(result), nil
	}
}

func (r *recorded) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// argsOfFrame is a submission whose encoded request frame is exactly size
// bytes. Its text mixes characters that grow when escaped (quotes, a
// backslash, a line separator, angle brackets) with multibyte ones.
func argsOfFrame(t *testing.T, cfg Config, size int) json.RawMessage {
	t.Helper()
	mixed := `"\` + " <>&é漢"
	for pad := 0; pad < size; pad++ {
		text, _ := json.Marshal(mixed + strings.Repeat("x", pad))
		args := json.RawMessage(`{"submission_id":"s-1","proposal":{"action":"intake","text":` + string(text) + `}}`)
		frame, err := encodeRequest(cfg, "submit", args)
		if err != nil {
			t.Fatal(err)
		}
		if len(frame) == size {
			return args
		}
		if len(frame) > size {
			break
		}
	}
	t.Fatalf("no submission encodes to %d bytes", size)
	return nil
}

// refuseSocket makes the socket refuse the dial as a sandbox does, so Call
// takes the spool.
func refuseSocket(t *testing.T, cfg Config) {
	t.Helper()
	dir := filepath.Dir(cfg.Endpoint)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

// The whole encoded frame is bounded at 16384 bytes on both transports: a
// submission at the cap is served whole; one byte more is never sent, the
// caller gets the typed frame limit, and the broker receives only the
// compact notice, which carries none of the submission and no capability.
func TestLiveProposalFrameLimitAtCap(t *testing.T) {
	for _, spool := range []bool{false, true} {
		name := "socket"
		if spool {
			name = "spool"
		}
		t.Run(name, func(t *testing.T) {
			var rec recorded
			cfg, cleanup, err := ListenWith(rec.handler(`{"recorded":true}`, nil), Options{SpoolParent: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if spool {
				refuseSocket(t, cfg)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			atCap := argsOfFrame(t, cfg, MaxFrameBytes)
			if _, err := Call(ctx, cfg, "submit", atCap); err != nil {
				t.Fatalf("frame at the cap refused: %v", err)
			}
			if calls := rec.list(); len(calls) != 1 || calls[0] != "submit "+string(atCap) {
				t.Fatalf("frame at the cap not served whole: %d calls", len(calls))
			}
			over := argsOfFrame(t, cfg, MaxFrameBytes+1)
			_, err = Call(ctx, cfg, "submit", over)
			limit, ok := model.LimitOf(err)
			if !ok || limit.Kind != LimitKindFrame || limit.ActualBytes != MaxFrameBytes+1 || limit.CapBytes != MaxFrameBytes || limit.Action != FrameLimitAction {
				t.Fatalf("over the cap: %v %+v", err, limit)
			}
			if !strings.Contains(limit.Subject, "proposal.text") || !strings.Contains(err.Error(), "the Board was told") {
				t.Fatalf("refusal names %q: %v", limit.Subject, err)
			}
			calls := rec.list()
			if len(calls) != 2 {
				t.Fatalf("calls after the refusal: %v", calls)
			}
			want := NoticeMethod + ` {"method":"submit","submission_id":"s-1","frame_bytes":16385}`
			if calls[1] != want {
				t.Fatalf("notice = %q, want %q", calls[1], want)
			}
			frame, _ := encodeRequest(cfg, NoticeMethod, json.RawMessage(strings.TrimPrefix(calls[1], NoticeMethod+" ")))
			if len(frame) > MaxNoticeFrameBytes || strings.Contains(calls[1], "intake") || strings.Contains(calls[1], cfg.Capability) {
				t.Fatalf("notice not compact: %d bytes %s", len(frame), calls[1])
			}
			if spool {
				if left, _ := os.ReadDir(cfg.Spool); len(left) != 0 {
					t.Fatalf("spool not emptied: %v", left)
				}
			}
		})
	}
}

// A submission without a valid ID is reported by a fixed-size opaque token,
// the same for the same request. A refused read sends no notice.
func TestLiveProposalFrameNoticeCorrelation(t *testing.T) {
	var rec recorded
	cfg, cleanup, err := Listen(rec.handler(`{"recorded":true}`, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	big := json.RawMessage(`{"submission_id":"bad id!","proposal":{"text":"` + strings.Repeat("y", MaxFrameBytes) + `"}}`)
	for range 2 {
		if _, err := Call(t.Context(), cfg, "submit", big); err == nil {
			t.Fatal("oversized frame sent")
		}
	}
	calls := rec.list()
	if len(calls) != 2 || calls[0] != calls[1] {
		t.Fatalf("notices %v", calls)
	}
	var n LimitNotice
	if err := DecodeStrict([]byte(strings.TrimPrefix(calls[0], NoticeMethod+" ")), &n); err != nil || n.SubmissionID != "" || !ValidCorrelation(n.Correlation) || strings.Contains(calls[0], "bad id") {
		t.Fatalf("notice %q: %v", calls[0], err)
	}
	if _, err := Call(t.Context(), cfg, "project_contract_read", big); err == nil {
		t.Fatal("oversized read sent")
	}
	if len(rec.list()) != 2 {
		t.Fatal("a read refusal sent a notice")
	}
}

// With the broker gone or refusing the notice, the caller still gets the
// typed limit, and the refusal says the notice was not confirmed.
func TestLiveProposalFrameNoticeUnconfirmed(t *testing.T) {
	var rec recorded
	cfg, cleanup, err := Listen(rec.handler("", errors.New("proposal launch is inactive")))
	if err != nil {
		t.Fatal(err)
	}
	big := json.RawMessage(`{"submission_id":"s-2","proposal":{"text":"` + strings.Repeat("z", MaxFrameBytes) + `"}}`)
	for _, stage := range []string{"refused", "gone"} {
		if stage == "gone" {
			cleanup()
		}
		_, err := Call(t.Context(), cfg, "submit", big)
		if limit, ok := model.LimitOf(err); !ok || limit.Kind != LimitKindFrame || !strings.Contains(err.Error(), "unconfirmed") {
			t.Fatalf("%s: %v", stage, err)
		}
	}
	// The refused notice is retried only on a transport failure, so the
	// broker saw it once.
	if calls := rec.list(); len(calls) != 1 || !strings.HasPrefix(calls[0], NoticeMethod+" ") {
		t.Fatalf("calls %v", calls)
	}
	cleanup()
}

// A handler's typed limit crosses the transport as that limit, and a reply
// that would not fit its frame becomes a compact typed refusal.
func TestLiveProposalResponseCarriesLimit(t *testing.T) {
	inner := model.NewLimitError(errors.New("invalid proposal submission"), model.Limit{Kind: LimitKindProposal, Subject: "proposal", ActualBytes: 4097, CapBytes: MaxProposalBytes, Action: ProposalLimitAction})
	var rec recorded
	cfg, cleanup, err := Listen(rec.handler("", inner))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, err = Call(t.Context(), cfg, "submit", json.RawMessage(`{}`))
	if limit, ok := model.LimitOf(err); !ok || limit.Kind != LimitKindProposal || limit.ActualBytes != 4097 || err.Error() != "invalid proposal submission" {
		t.Fatalf("inner limit: %v %+v", err, limit)
	}
	big := `{"page":"` + strings.Repeat("p", MaxFrameBytes) + `"}`
	cfg2, cleanup2, err := ListenWith(rec.handler(big, nil), Options{SpoolParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	for _, spool := range []bool{false, true} {
		if spool {
			refuseSocket(t, cfg2)
		}
		_, err = Call(t.Context(), cfg2, "project_contract_read", json.RawMessage(`{}`))
		if limit, ok := model.LimitOf(err); !ok || limit.Kind != LimitKindResponse || limit.CapBytes != MaxFrameBytes {
			t.Fatalf("spool=%v oversized reply: %v", spool, err)
		}
	}
}

// Both readers take a frame body of exactly the cap and refuse one byte
// more; the socket's newline is not counted.
func TestFrameReadersAgreeAtCap(t *testing.T) {
	var rec recorded
	cfg, cleanup, err := Listen(rec.handler(`{}`, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, size := range []int{MaxFrameBytes, MaxFrameBytes + 1} {
		frame, err := encodeRequest(cfg, "submit", argsOfFrame(t, cfg, size))
		if err != nil {
			t.Fatal(err)
		}
		resp := lenientRequest(t, cfg.Endpoint, string(frame)+"\n")
		if served := resp != "" && !hasError(t, resp); served != (size == MaxFrameBytes) {
			t.Fatalf("socket frame of %d bytes: %q", size, resp)
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "f")
		if err := os.WriteFile(path, frame, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = readSpoolFile(path)
		if (err == nil) != (size == MaxFrameBytes) {
			t.Fatalf("spool frame of %d bytes: %v", size, err)
		}
	}
	if len(rec.list()) != 1 {
		t.Fatalf("calls %v", rec.list())
	}
}

// lenientRequest is rawRequest for a frame the broker may refuse by closing
// the connection: a reset reads as no response.
func lenientRequest(t *testing.T, endpoint, frame string) string {
	t.Helper()
	conn, err := net.Dial("unix", endpoint)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte(frame))
	data, _ := io.ReadAll(conn)
	return strings.TrimSpace(string(data))
}
