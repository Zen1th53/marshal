package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

var errMarshalEventArtifact = errors.New("invalid Marshal event artifact")

const (
	marshalEventDataLimit = 64 * 1024
	// Lifecycle annotations share this budget, including their JSON escaping.
	marshalEventHeadroom = 4 * 1024
	// Full event headers and data together must fit this storage/read ceiling.
	marshalEventArtifactLimit = 16 * 1024 * 1024
)

type marshalEventReference struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

func newMarshalEventReference(name string, size int) marshalEventReference {
	return marshalEventReference{Kind: "marshal-event", Version: 1, SHA256: strings.TrimSuffix(name, ".json"), Bytes: int64(size)}
}

func parseMarshalEventReference(value any) (marshalEventReference, error) {
	var ref marshalEventReference
	data, err := json.Marshal(value)
	if err != nil {
		return ref, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil {
		return ref, err
	}
	if ref.Kind != "marshal-event" || ref.Version != 1 || ref.Bytes <= 0 || ref.Bytes > marshalEventArtifactLimit || !validMarshalEventArtifactName(ref.SHA256+".json") {
		return ref, errors.New("malformed or unsupported reference")
	}
	return ref, nil
}

func validateMarshalEventAnnotations(event events.Event) error {
	annotations := map[string]any{}
	for _, key := range []string{"reason", "launch_outcome", "model_session_id"} {
		if value, ok := event.Data[key]; ok {
			annotations[key] = value
		}
	}
	encoded, err := json.Marshal(annotations)
	if err != nil || len(encoded) > marshalEventHeadroom {
		return fmt.Errorf("%w: lifecycle annotations exceed headroom", errMarshalEventArtifact)
	}
	return nil
}

func validateMarshalEventBrief(event events.Event) error {
	value, present := event.Data["brief_sha256"]
	if !present {
		return nil
	}
	brief, ok := event.Data["brief"].(string)
	digest, digestOK := value.(string)
	sum := sha256.Sum256([]byte(brief))
	if !ok || !digestOK || digest != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: brief digest mismatch", errMarshalEventArtifact)
	}
	return nil
}

// boundMarshalEvent runs before persisting an intent or launching its worker.
// The full event is immutable; later lifecycle annotations stay in the bounded
// event. Never turn an invalid existing reference into a new, trusted artifact.
func (s *MarshalService) boundMarshalEvent(event *events.Event) error {
	if err := validateMarshalEventAnnotations(*event); err != nil {
		return err
	}
	if _, referenced := event.Data["event_artifact"]; referenced {
		_, err := s.fullMarshalEvent(*event)
		return err
	}
	if err := validateMarshalEventBrief(*event); err != nil {
		return err
	}
	err := event.Validate()
	if err != nil && !errors.Is(err, events.ErrEventDataInvalid) {
		return err
	}
	encoded, encodeErr := json.Marshal(event.Data)
	if encodeErr != nil {
		return encodeErr
	}
	if err == nil && len(encoded) <= marshalEventDataLimit-marshalEventHeadroom {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	// Validate field names separately: externalization must not hide a forbidden
	// field behind the event's size error or bypass any other validation rule.
	probe := *event
	probe.Data = make(map[string]any, len(event.Data))
	for key := range event.Data {
		probe.Data[key] = nil
	}
	if err := probe.Validate(); err != nil {
		return err
	}
	if len(data) > marshalEventArtifactLimit {
		return fmt.Errorf("%w: full event exceeds artifact limit", errMarshalEventArtifact)
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + ".json"
	bounded := *event
	bounded.Data = map[string]any{"event_artifact": newMarshalEventReference(name, len(data))}
	for _, key := range []string{"operation_id", "operation_kind", "brief_sha256", "model_session_id", "reason", "launch_outcome"} {
		if value, ok := event.Data[key]; ok {
			bounded.Data[key] = value
		}
	}
	if err := bounded.Validate(); err != nil {
		return err
	}
	if err := s.writeMarshalEventArtifact(name, data); err != nil {
		return err
	}
	*event = bounded
	return nil
}

// marshalEventWithBrief constructs a replacement without changing the durable
// intent's in-memory event or its shared data if verification or bounding fails.
func (s *MarshalService) marshalEventWithBrief(event events.Event, brief string) (events.Event, error) {
	full, err := s.fullMarshalEvent(event)
	if err != nil {
		return events.Event{}, err
	}
	full.Data = copyMarshalEventData(full.Data)
	full.Data["brief"] = brief
	sum := sha256.Sum256([]byte(brief))
	full.Data["brief_sha256"] = hex.EncodeToString(sum[:])
	if err := s.boundMarshalEvent(&full); err != nil {
		return events.Event{}, err
	}
	return full, nil
}

// fullMarshalEvent resolves only canonical hash names in the private store,
// verifies the bytes and event identity, and rejects unverified payload fields.
func (s *MarshalService) fullMarshalEvent(event events.Event) (events.Event, error) {
	value, referenced := event.Data["event_artifact"]
	if !referenced {
		return event, nil
	}
	fail := func(err error) (events.Event, error) {
		return events.Event{}, fmt.Errorf("%w: %w", errMarshalEventArtifact, err)
	}
	if err := event.Validate(); err != nil {
		return fail(err)
	}
	if err := validateMarshalEventAnnotations(event); err != nil {
		return fail(err)
	}
	ref, err := parseMarshalEventReference(value)
	if err != nil {
		return fail(err)
	}
	name := ref.SHA256 + ".json"
	digest, _ := hex.DecodeString(ref.SHA256)
	root, err := s.marshalEventArtifactRoot(false)
	if err != nil {
		return fail(err)
	}
	defer root.Close()
	data, err := readMarshalEventArtifact(root, name, ref.Bytes)
	if err != nil {
		return fail(err)
	}
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], digest) {
		return fail(errors.New("content digest mismatch"))
	}
	var full events.Event
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&full); err != nil {
		return fail(err)
	}
	canonical, err := json.Marshal(full)
	if err != nil || !bytes.Equal(canonical, data) {
		return fail(errors.New("noncanonical full event"))
	}
	if err := validateMarshalEventBrief(full); err != nil {
		return fail(err)
	}
	if _, nested := full.Data["event_artifact"]; nested {
		return fail(errors.New("nested reference"))
	}
	// An escalation may annotate the original dispatch intent; it cannot change
	// the identity, subject, run, task, timestamp or any other event header.
	originalHeader, currentHeader := full, event
	originalHeader.Data, currentHeader.Data = nil, nil
	// The store assigns Sequence when appending the bounded event.
	currentHeader.Sequence = originalHeader.Sequence
	if event.Type == events.EventTypeMarshalEscalated {
		currentHeader.Type = full.Type
	}
	originalBytes, _ := json.Marshal(originalHeader)
	currentBytes, _ := json.Marshal(currentHeader)
	if !bytes.Equal(originalBytes, currentBytes) {
		return fail(errors.New("event identity mismatch"))
	}
	probe := full
	probe.Data = make(map[string]any, len(full.Data))
	for key := range full.Data {
		probe.Data[key] = nil
	}
	if err := probe.Validate(); err != nil {
		return fail(err)
	}
	for _, key := range []string{"operation_id", "operation_kind", "brief_sha256"} {
		_, originalHas := full.Data[key]
		_, referenceHas := event.Data[key]
		if originalHas != referenceHas {
			return fail(errors.New("event metadata missing or added"))
		}
	}
	for key, value := range event.Data {
		switch key {
		case "event_artifact", "reason", "launch_outcome", "model_session_id":
		case "operation_id", "operation_kind", "brief_sha256":
			expected, _ := json.Marshal(full.Data[key])
			actual, err := json.Marshal(value)
			if err != nil || !bytes.Equal(expected, actual) {
				return fail(errors.New("event metadata mismatch"))
			}
		default:
			return fail(errors.New("unexpected reference field"))
		}
	}
	return full, nil
}

// marshalEventArtifactRoot pins and checks every path component, including
// ancestors. Existing permissions are never repaired: unsafe storage fails
// closed. Only the final, private directory may be created.
func (s *MarshalService) marshalEventArtifactRoot(create bool) (*os.Root, error) {
	if !filepath.IsAbs(s.Worktrees) || filepath.Clean(s.Worktrees) != s.Worktrees || strings.ContainsAny(s.Worktrees, "\\\x00") || filepath.Dir(s.Worktrees) == s.Worktrees {
		return nil, fmt.Errorf("%w: malformed worktree path", errMarshalEventArtifact)
	}
	path := filepath.Join(filepath.Dir(s.Worktrees), "event-artifacts")
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	rootStat, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok {
		root.Close()
		return nil, fmt.Errorf("%w: unavailable directory ownership", errMarshalEventArtifact)
	}
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	for i, part := range parts {
		last := i == len(parts)-1
		info, err := root.Lstat(part)
		if errors.Is(err, os.ErrNotExist) && last && create {
			if err = root.Mkdir(part, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				root.Close()
				return nil, err
			}
			if err := syncMarshalEventDirectory(root); err != nil {
				root.Close()
				return nil, err
			}
			info, err = root.Lstat(part)
		}
		if err != nil {
			root.Close()
			return nil, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		safe := ok && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && (stat.Uid == uint32(os.Geteuid()) || stat.Uid == rootStat.Uid)
		// Shared sticky ancestors (such as /tmp) are safe to traverse with pinned
		// descriptors. The store's immediate parent must not be shared or writable.
		if info.Mode().Perm()&0022 != 0 && (info.Mode()&os.ModeSticky == 0 || i >= len(parts)-2) {
			safe = false
		}
		if i == len(parts)-2 && (info.Mode() != os.ModeDir|0700 || !ok || stat.Uid != uint32(os.Geteuid())) {
			safe = false
		}
		if last && (info.Mode() != os.ModeDir|0700 || !ok || stat.Uid != uint32(os.Geteuid())) {
			safe = false
		}
		if !safe {
			root.Close()
			return nil, fmt.Errorf("%w: unsafe directory component %q", errMarshalEventArtifact, part)
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			root.Close()
			return nil, err
		}
		actual, err := next.Stat(".")
		root.Close()
		if err != nil || !os.SameFile(info, actual) || info.Mode() != actual.Mode() {
			next.Close()
			return nil, fmt.Errorf("%w: directory changed", errMarshalEventArtifact)
		}
		root = next
	}
	return root, nil
}

func validMarshalEventArtifactName(name string) bool {
	if len(name) != 69 || !strings.HasSuffix(name, ".json") {
		return false
	}
	digest, err := hex.DecodeString(name[:64])
	return err == nil && hex.EncodeToString(digest) == name[:64]
}

// Directory locks cover both readers and publishers, so no reader can observe
// the transient second hard link between publication and temporary cleanup.
func lockMarshalEventDirectory(root *os.Root, exclusive bool) (*os.File, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(dir.Fd()), mode); err != nil {
		dir.Close()
		return nil, err
	}
	return dir, nil
}

func readMarshalEventArtifact(root *os.Root, name string, size int64) ([]byte, error) {
	lock, err := lockMarshalEventDirectory(root, false)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	data, err := readLockedMarshalEventArtifact(root, name, size)
	if err != nil {
		return nil, err
	}
	return data, syncMarshalEventDirectory(root)
}

func readLockedMarshalEventArtifact(root *os.Root, name string, size int64) ([]byte, error) {
	if !validMarshalEventArtifactName(name) || size <= 0 || size > marshalEventArtifactLimit {
		return nil, errors.New("malformed artifact name")
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !privateMarshalEventFile(info) || info.Size() != size {
		return nil, errors.New("unsafe artifact file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) || !privateMarshalEventFile(actual) || actual.Size() != size {
		return nil, errors.New("artifact file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, errors.New("artifact size changed")
	}
	// Reused files need the same durability guarantee as newly written files.
	return data, file.Sync()
}

func privateMarshalEventFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode() == 0400 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

func (s *MarshalService) writeMarshalEventArtifact(name string, data []byte) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: %w", errMarshalEventArtifact, err)
		}
	}()
	if !validMarshalEventArtifactName(name) || len(data) == 0 || len(data) > marshalEventArtifactLimit {
		return errors.New("malformed artifact name or size")
	}
	root, err := s.marshalEventArtifactRoot(true)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := lockMarshalEventDirectory(root, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := root.Lstat(name); err == nil {
		existing, err := readLockedMarshalEventArtifact(root, name, int64(len(data)))
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, data) {
			return errors.New("existing artifact differs")
		}
		return syncMarshalEventDirectory(root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := model.NewID("event-")
	if err != nil {
		return err
	}
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Chmod(0400)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	// Linking publishes the complete read-only file without ever replacing an
	// existing hash. A concurrent publisher must have produced identical bytes.
	if err := root.Link(temporary, name); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := root.Remove(temporary); err != nil {
		return err
	}
	existing, err := readLockedMarshalEventArtifact(root, name, int64(len(data)))
	if err != nil {
		return err
	}
	if !bytes.Equal(existing, data) {
		return errors.New("published artifact differs")
	}
	return syncMarshalEventDirectory(root)
}

func syncMarshalEventDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// marshalDecisionsResolved returns the run's Marshal decisions with every
// artifact-backed event replaced by its verified full payload. A large event
// (a dispatch carrying its brief, a return with its reasons) keeps only a
// reference inline, so readers of its data fields must resolve it first.
func (s *MarshalService) marshalDecisionsResolved(ctx context.Context, runID string) ([]events.Event, error) {
	history, err := s.Store.MarshalDecisions(ctx, runID)
	if err != nil {
		return nil, err
	}
	for i := range history {
		if _, referenced := history[i].Data["event_artifact"]; !referenced {
			continue
		}
		full, err := s.fullMarshalEvent(history[i])
		if err != nil {
			return nil, err
		}
		history[i] = full
	}
	return history, nil
}
