package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var (
	secretRegexes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(bearer\s+)[a-zA-Z0-9_\-\.]{10,}`),
		regexp.MustCompile(`(?i)(["']?api[_-]?key["']?\s*[:=]\s*["']?)[a-zA-Z0-9_\-\.]{10,}(["']?)`),
		regexp.MustCompile(`(?i)(["']?token["']?\s*[:=]\s*["']?)[a-zA-Z0-9_\-\.]{10,}(["']?)`),
		regexp.MustCompile(`(?i)(["']?password["']?\s*[:=]\s*["']?)[^"',\s]+(["']?)`),
		regexp.MustCompile(`(?i)(["']?secret["']?\s*[:=]\s*["']?)[^"',\s]+(["']?)`),
		regexp.MustCompile(`-----BEGIN [A-Z ]+ PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+ PRIVATE KEY-----`),
	}
)

// RedactSecrets scrubs sensitive strings matching common secret patterns.
func RedactSecrets(s string) string {
	redacted := s
	for _, re := range secretRegexes {
		redacted = re.ReplaceAllString(redacted, "${1}[REDACTED]${2}")
	}
	return redacted
}

// JournalStore provides append-only, tamper-evident event logging for runs.
type JournalStore interface {
	Append(event JournalEvent) (JournalEvent, error)
	GetEvents(runID string) ([]JournalEvent, error)
	VerifyIntegrity(runID string) error
}

// MemoryJournalStore implements an in-memory tamper-evident event journal.
type MemoryJournalStore struct {
	mu           sync.RWMutex
	events       map[string][]JournalEvent // runID -> events
	lastDigest   map[string]string         // runID -> last event digest
	nextEventSeq int64
}

// NewMemoryJournalStore creates a new in-memory journal store.
func NewMemoryJournalStore() *MemoryJournalStore {
	return &MemoryJournalStore{
		events:     make(map[string][]JournalEvent),
		lastDigest: make(map[string]string),
	}
}

// Append validates, redacts, digests, and records an event into the journal.
func (js *MemoryJournalStore) Append(event JournalEvent) (JournalEvent, error) {
	if event.RunID == "" {
		return JournalEvent{}, fmt.Errorf("%w: journal event requires run ID", ErrRunInvalid)
	}

	js.mu.Lock()
	defer js.mu.Unlock()

	js.nextEventSeq++
	event.EventID = js.nextEventSeq
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Scrub secret-bearing payloads
	event.Summary = RedactSecrets(event.Summary)
	if event.PayloadJSON != "" {
		event.PayloadJSON = RedactSecrets(event.PayloadJSON)
	}

	prevDigest := js.lastDigest[event.RunID]
	event.Digest = computeJournalDigest(prevDigest, event)
	js.lastDigest[event.RunID] = event.Digest

	js.events[event.RunID] = append(js.events[event.RunID], event)
	return event, nil
}

// GetEvents returns all events for a run ordered chronologically.
func (js *MemoryJournalStore) GetEvents(runID string) ([]JournalEvent, error) {
	js.mu.RLock()
	defer js.mu.RUnlock()

	evs, exists := js.events[runID]
	if !exists {
		return nil, nil
	}
	copied := make([]JournalEvent, len(evs))
	copy(copied, evs)
	return copied, nil
}

// VerifyIntegrity checks the SHA256 tamper-evident hash chain for a run.
func (js *MemoryJournalStore) VerifyIntegrity(runID string) error {
	js.mu.RLock()
	defer js.mu.RUnlock()

	evs, exists := js.events[runID]
	if !exists || len(evs) == 0 {
		return nil
	}

	var prevDigest string
	for idx, ev := range evs {
		expectedDigest := computeJournalDigest(prevDigest, ev)
		if ev.Digest != expectedDigest {
			return fmt.Errorf("journal tamper detected at event %d (idx %d): got %s, want %s",
				ev.EventID, idx, ev.Digest, expectedDigest)
		}
		prevDigest = ev.Digest
	}

	return nil
}

func computeJournalDigest(prevDigest string, ev JournalEvent) string {
	h := sha256.New()
	fmt.Fprintf(h, "prev:%s\n", prevDigest)
	fmt.Fprintf(h, "id:%d\n", ev.EventID)
	fmt.Fprintf(h, "run:%s\n", ev.RunID)
	fmt.Fprintf(h, "task:%s\n", ev.TaskID)
	fmt.Fprintf(h, "actor:%s\n", ev.Actor)
	fmt.Fprintf(h, "type:%s\n", ev.EventType)
	fmt.Fprintf(h, "time:%s\n", ev.Timestamp.Format(time.RFC3339Nano))
	fmt.Fprintf(h, "before:%s:after:%s\n", ev.StateBefore, ev.StateAfter)
	fmt.Fprintf(h, "action:%s\n", ev.ActionRef)
	fmt.Fprintf(h, "summary:%s\n", ev.Summary)
	fmt.Fprintf(h, "payload:%s\n", ev.PayloadJSON)
	return hex.EncodeToString(h.Sum(nil))
}

// FileJournalStore implements a durable, hash-chained, append-only journal store on disk.
type FileJournalStore struct {
	mu      sync.RWMutex
	baseDir string
	mem     *MemoryJournalStore
}

// NewFileJournalStore creates a durable file-backed journal store.
func NewFileJournalStore(baseDir string) (*FileJournalStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create journal directory: %w", err)
	}
	return &FileJournalStore{
		baseDir: baseDir,
		mem:     NewMemoryJournalStore(),
	}, nil
}

// Append appends a new journal event in memory and to disk.
func (f *FileJournalStore) Append(event JournalEvent) (JournalEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rec, err := f.mem.Append(event)
	if err != nil {
		return JournalEvent{}, err
	}

	filePath := filepath.Join(f.baseDir, fmt.Sprintf("%s.journal.jsonl", event.RunID))
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return JournalEvent{}, fmt.Errorf("failed to open journal file: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(rec)
	if err != nil {
		return JournalEvent{}, fmt.Errorf("failed to serialize journal event: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return JournalEvent{}, fmt.Errorf("failed to write journal event: %w", err)
	}

	return rec, nil
}

// GetEvents retrieves all journal events for a run.
func (f *FileJournalStore) GetEvents(runID string) ([]JournalEvent, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mem.GetEvents(runID)
}

// VerifyIntegrity verifies the tamper-evident hash chain of a run.
func (f *FileJournalStore) VerifyIntegrity(runID string) error {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mem.VerifyIntegrity(runID)
}
