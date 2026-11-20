//go:build !governed && unix

package proposalmcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The spool carries the same request and response frames as the socket, as
// files in a private 0700 directory: a client writes <id>.tmp and renames it
// to <id>.req; the broker claims it by renaming it to <id>.work, serves it
// with the same checks (capability, namespace, strict JSON) and answers with
// <id>.resp, written as <id>.rtmp and renamed. Every file is 0600 and opened
// without following symlinks. It exists for a client that may write files
// but not connect(2): Codex's sandbox around Marshal's MCP child.

const (
	// spoolPoll is how often the broker looks for new requests.
	spoolPoll = 20 * time.Millisecond
	// spoolReplyPoll is how often a client looks for its answer.
	spoolReplyPoll = 10 * time.Millisecond
)

// newSpoolDir creates the broker's private request directory under parent.
func newSpoolDir(parent string) (string, error) {
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("proposalmcp: create spool parent: %w", err)
	}
	dir, err := os.MkdirTemp(parent, "mcp-")
	if err != nil {
		return "", fmt.Errorf("proposalmcp: create spool: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("proposalmcp: restrict spool: %w", err)
	}
	return dir, nil
}

// sandboxRefusedDial reports whether a dial failed because the caller may
// not connect to the socket at all, rather than because nothing listens.
func sandboxRefusedDial(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

func validSpoolID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func (s *server) spoolLoop() {
	defer close(s.spoolDone)
	t := time.NewTicker(spoolPoll)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.drainSpool()
		}
	}
}

// drainSpool claims every waiting request and serves each on its own
// goroutine, as the socket serves each connection.
func (s *server) drainSpool() {
	entries, err := os.ReadDir(s.spool)
	if err != nil {
		return
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".req")
		if !ok || !validSpoolID(id) || !e.Type().IsRegular() {
			continue
		}
		claimed := filepath.Join(s.spool, id+".work")
		if err := os.Rename(filepath.Join(s.spool, e.Name()), claimed); err != nil {
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleSpooled(id, claimed)
		}()
	}
}

func (s *server) handleSpooled(id, path string) {
	data, err := readSpoolFile(path)
	_ = os.Remove(path)
	var resp responseEnvelope
	if err != nil {
		resp = responseEnvelope{Error: "invalid request: " + err.Error()}
	} else {
		ctx, cancel := context.WithTimeout(s.ctx, transportTimeout)
		resp = s.serve(ctx, data)
		cancel()
	}
	out, err := json.Marshal(resp)
	if err != nil {
		out = []byte(`{"error":"internal transport error"}`)
	}
	_ = writeSpoolFile(s.spool, id, ".rtmp", ".resp", out)
}

// readSpoolFile reads one frame: a regular file, never through a symlink,
// at most maxRequestBytes.
func readSpoolFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRequestBytes {
		return nil, fmt.Errorf("request exceeds %d bytes", maxRequestBytes)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("empty request")
	}
	return data, nil
}

// writeSpoolFile writes data to dir/<id><tmp> (created new, 0600) and
// renames it to dir/<id><final>, so a reader never sees half a frame.
func writeSpoolFile(dir, id, tmp, final string, data []byte) error {
	path := filepath.Join(dir, id+tmp)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := os.Rename(path, filepath.Join(dir, id+final)); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// callSpool sends one encoded request frame through the spool and waits for
// its answer, until ctx ends or transportTimeout passes.
func callSpool(ctx context.Context, dir string, frame []byte) ([]byte, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("proposalmcp: spool id: %w", err)
	}
	id := hex.EncodeToString(raw[:])
	if len(frame) > maxRequestBytes {
		return nil, fmt.Errorf("proposalmcp: request exceeds %d bytes", maxRequestBytes)
	}
	if err := writeSpoolFile(dir, id, ".tmp", ".req", frame); err != nil {
		return nil, fmt.Errorf("proposalmcp: spool request: %w", err)
	}
	deadline := time.Now().Add(transportTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	reply := filepath.Join(dir, id+".resp")
	t := time.NewTicker(spoolReplyPoll)
	defer t.Stop()
	for {
		data, err := readSpoolFile(reply)
		if err == nil {
			_ = os.Remove(reply)
			return data, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(reply)
			return nil, fmt.Errorf("proposalmcp: read spooled response: %w", err)
		}
		if !time.Now().Before(deadline) {
			// An unclaimed request is withdrawn; a claimed one may still
			// be admitted, as a socket call that times out may be.
			_ = os.Remove(filepath.Join(dir, id+".req"))
			return nil, fmt.Errorf("proposalmcp: spooled response: %w", os.ErrDeadlineExceeded)
		}
		select {
		case <-ctx.Done():
			_ = os.Remove(filepath.Join(dir, id+".req"))
			return nil, fmt.Errorf("proposalmcp: spooled response: %w", ctx.Err())
		case <-t.C:
		}
	}
}
