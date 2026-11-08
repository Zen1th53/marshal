package cloud

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// EntitlementResponse contains observations, never authorization. Older servers
// supply only status/tier; empty ID and nil timestamps explicitly mean unknown.
type EntitlementResponse struct {
	Status    string     `json:"status"`
	Tier      string     `json:"tier"`
	RequestID string     `json:"request_id,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type RequestKey struct {
	Endpoint       string `json:"endpoint"`
	InstallationID string `json:"installation_id"`
	SessionID      string `json:"session_id"`
}

// RequestObservation is deliberately independent of cloud.State and Lease.
// ErrorCode is a bounded classification; raw transport/server errors can carry
// secrets and are never persisted. "unknown" includes ambiguous timeouts.
type RequestObservation struct {
	Key             RequestKey `json:"key"`
	Generation      int64      `json:"generation"`
	LocalAttemptID  string     `json:"local_attempt_id"`
	ServerRequestID string     `json:"server_request_id,omitempty"`
	State           string     `json:"state"`
	AttemptedAt     time.Time  `json:"attempted_at"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
	ServerCreatedAt *time.Time `json:"server_created_at,omitempty"`
	ServerUpdatedAt *time.Time `json:"server_updated_at,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	CommandDigest   string     `json:"command_digest,omitempty"`
}

var ErrRequestConflict = errors.New("cloud: request state conflict")

// RequestStore uses one private versioned file per endpoint/installation/session
// and an inode-stable advisory lock shared by all processes. Renamed data files
// are never themselves used as locks.
type RequestStore struct{ dir string }

func NewRequestStore(runtimeDir string) *RequestStore {
	return &RequestStore{dir: filepath.Join(runtimeDir, "entitlement_requests")}
}

func (s *RequestStore) path(key RequestKey) (string, error) {
	if key.Endpoint == "" || key.InstallationID == "" || key.SessionID == "" || len(key.Endpoint) > 2048 || len(key.InstallationID) > 256 || len(key.SessionID) > 256 {
		return "", ErrState
	}
	u, err := NewClient(key.Endpoint, "")
	if err != nil || u.endpoint != key.Endpoint || strings.Contains(key.Endpoint, "@") || strings.ContainsAny(key.Endpoint, "?#") {
		return "", ErrState
	}
	b, _ := json.Marshal(key)
	sum := sha256.Sum256(b)
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json"), nil
}

func privateRequestFile(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("%w: request file unavailable", ErrState)
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		f.Close()
		return nil, fmt.Errorf("%w: request file must be regular and 0600", ErrState)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		f.Close()
		return nil, ErrState
	}
	return f, nil
}

func (s *RequestStore) locked(ctx context.Context, key RequestKey, fn func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("%w: request directory unavailable", ErrState)
	}
	info, err := os.Lstat(s.dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrState
	}
	f, err := privateRequestFile(path+".lock", syscall.O_CREAT|syscall.O_RDWR)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrState
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn(path)
}

type requestStateFile struct {
	Version     int                `json:"version"`
	Observation RequestObservation `json:"observation"`
}

func readRequest(path string, key RequestKey) (RequestObservation, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return RequestObservation{}, false, nil
	}
	f, err := privateRequestFile(path, syscall.O_RDONLY)
	if err != nil {
		return RequestObservation{}, false, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	dec.DisallowUnknownFields()
	var data requestStateFile
	if err = dec.Decode(&data); err != nil {
		return RequestObservation{}, false, ErrState
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || data.Version != 1 || data.Observation.Key != key || data.Observation.Generation < 1 || data.Observation.LocalAttemptID == "" || data.Observation.AttemptedAt.IsZero() || !validRequestState(data.Observation.State) {
		return RequestObservation{}, false, ErrState
	}
	return data.Observation, true, nil
}

func validRequestState(s string) bool {
	switch s {
	case "sending", "pending", "accepted", "active", "refused", "denied", "unknown":
		return true
	}
	return false
}

func writeRequest(path string, o RequestObservation) error {
	b, err := json.Marshal(requestStateFile{1, o})
	if err != nil {
		return ErrState
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".request-*")
	if err != nil {
		return ErrState
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("%w: persist request state", ErrState)
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrState
	}
	defer d.Close()
	return d.Sync()
}

func (s *RequestStore) Read(ctx context.Context, key RequestKey) (out RequestObservation, found bool, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	path, e := s.path(key)
	if e != nil {
		return RequestObservation{}, false, e
	}
	info, e := os.Lstat(s.dir)
	if errors.Is(e, os.ErrNotExist) {
		return RequestObservation{}, false, nil
	}
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return RequestObservation{}, false, ErrState
	}
	// Atomic rename gives readers a complete old or new revision. Reading an
	// absent request must not create directories or lock files on Status refresh.
	return readRequest(path, key)
}

// Begin must succeed durably before any network request. Expected generation
// binds the reviewed previous attempt. A crash after Begin remains "sending":
// the UI must explain that the server outcome is unknown, never retry silently.
func (s *RequestStore) Begin(ctx context.Context, key RequestKey, expected int64) (out RequestObservation, err error) {
	out, _, err = s.begin(ctx, key, expected, "")
	return
}

func (s *RequestStore) begin(ctx context.Context, key RequestKey, expected int64, commandDigest string) (out RequestObservation, replay bool, err error) {
	err = s.locked(ctx, key, func(path string) error {
		old, found, e := readRequest(path, key)
		if e != nil {
			return e
		}
		if found && commandDigest != "" && old.CommandDigest == commandDigest {
			out = old
			replay = true
			return nil
		}
		if expected < 0 || expected > math.MaxInt64-2 || (!found && expected != 0) || (found && old.Generation != expected) {
			return ErrRequestConflict
		}
		var raw [16]byte
		if _, e = rand.Read(raw[:]); e != nil {
			return e
		}
		out = RequestObservation{Key: key, Generation: expected + 1, LocalAttemptID: "local-" + hex.EncodeToString(raw[:]), State: "sending", AttemptedAt: time.Now().UTC(), CommandDigest: commandDigest}
		return writeRequest(path, out)
	})
	return
}

func (s *RequestStore) Finish(ctx context.Context, attempt RequestObservation, reply EntitlementResponse, requestErr error) (out RequestObservation, err error) {
	err = s.locked(ctx, attempt.Key, func(path string) error {
		old, found, e := readRequest(path, attempt.Key)
		if e != nil {
			return e
		}
		if !found || old.Generation != attempt.Generation || old.LocalAttemptID != attempt.LocalAttemptID || old.State != "sending" {
			return ErrRequestConflict
		}
		now := time.Now().UTC()
		old.ObservedAt = &now
		old.State = "unknown"
		if requestErr != nil {
			old.ErrorCode = "request_failed_unknown_outcome"
			var refusal *RefusalError
			if errors.As(requestErr, &refusal) && refusal.StatusCode >= 400 && refusal.StatusCode < 500 {
				old.State = "refused"
				old.ErrorCode = "server_refused"
			}
		} else {
			if validRequestState(reply.Status) && reply.Status != "sending" {
				old.State = reply.Status
			} else {
				old.ErrorCode = "server_status_unrecognized"
			}
			if len(reply.RequestID) > 256 || strings.ContainsAny(reply.RequestID, "\r\n\x1b") {
				return ErrState
			}
			old.ServerRequestID = reply.RequestID
			old.ServerCreatedAt = reply.CreatedAt
			old.ServerUpdatedAt = reply.UpdatedAt
		}
		old.Generation++
		out = old
		return writeRequest(path, out)
	})
	return
}

// RequestEntitlementTracked records the attempt before using the existing PoP
// protocol. There is deliberately no invented server polling endpoint.
func (c *Client) RequestEntitlementTracked(ctx context.Context, st State, session string, s *RequestStore, expected int64) (RequestObservation, error) {
	return c.RequestEntitlementTrackedCommand(ctx, st, session, s, expected, "")
}

// RequestEntitlementTrackedCommand binds retries to a digest minted at the
// application boundary. A replay of an interrupted sending attempt only reads
// its unknown outcome; it must never silently send a second server request.
func (c *Client) RequestEntitlementTrackedCommand(ctx context.Context, st State, session string, s *RequestStore, expected int64, commandDigest string) (RequestObservation, error) {
	if s == nil {
		return RequestObservation{}, ErrState
	}
	a, replay, err := s.begin(ctx, RequestKey{c.endpoint, st.InstallationID, session}, expected, commandDigest)
	if err != nil {
		return RequestObservation{}, err
	}
	if replay {
		return a, nil
	}
	reply, callErr := c.RequestEntitlementResponse(ctx, st, session)
	// Cancellation of the HTTP call must not discard its durable ambiguous result.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	out, saveErr := s.Finish(persistCtx, a, reply, callErr)
	if saveErr != nil {
		return a, errors.Join(callErr, saveErr)
	}
	return out, callErr
}
