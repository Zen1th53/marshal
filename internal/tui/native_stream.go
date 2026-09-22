package tui

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// One channel, flowing one way.
//
// Cross-agent exchange used to be point to point: a running session copied what
// it did into every other provider's mailbox. Four agents meant four copies of
// one event, a guard against writing it twice, and a table of who posts to
// whom — all of it bookkeeping created by the shape, not by the problem.
//
// There is one stream instead. Every agent drops what it does into it as it
// happens, in order, and nothing already in it is rewritten. What each agent
// sees is decided when it reads, not when someone writes: a reader has a filter
// naming whose entries it takes and a cursor saying how far it has looked.
//
// That the filter is per reader is the point rather than a detail. Models differ in
// what they can hold: one does better seeing everything the others did, and one
// does worse, because more context it cannot use becomes more it can be
// confused by. The operator decides which is which, before the work starts.

const (
	// streamEntryBytes bounds one entry's text, for the same reason tool output
	// is bounded in memory: a single paste must not crowd out everything else a
	// reader is meant to see.
	streamEntryBytes = 1 << 10
	// streamMaxEntries bounds the channel. It is a working record of recent
	// work, not the archive — durable memory is the archive, and an entry that
	// has flowed past is still in it.
	streamMaxEntries = 4096
)

// streamEntry is one thing an agent did.
type streamEntry struct {
	// Seq orders the channel. It is assigned on append and never reused, so a
	// reader's cursor stays meaningful across restarts.
	Seq int64 `json:"seq"`
	// Provider is who did it. A reader's filter is expressed in these.
	Provider string    `json:"provider"`
	Session  string    `json:"session,omitempty"`
	At       time.Time `json:"at"`
	Role     string    `json:"role"`
	Kind     string    `json:"kind,omitempty"`
	Text     string    `json:"text"`
	// Digest identifies the event itself, so the same message arriving from two
	// capture paths is appended once.
	Digest string `json:"digest"`
}

// stream is the append-only channel, shared by every agent in one project.
type stream struct {
	mu   sync.Mutex
	root string
	path string
	next int64
	// digests guards against appending the same event twice, which happens
	// when a session delivers its own work forward and a peer watcher reads
	// the same history.
	digests map[string]bool
}

func streamDir(root string) string  { return filepath.Join(root, ".marshal", "stream") }
func streamPath(root string) string { return filepath.Join(streamDir(root), "events.jsonl") }

// openStream opens the channel, learning where it left off.
func openStream(root string) (*stream, error) {
	if err := os.MkdirAll(streamDir(root), 0700); err != nil {
		return nil, err
	}
	s := &stream{root: root, path: streamPath(root), digests: map[string]bool{}}
	entries, err := s.readAll()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Seq >= s.next {
			s.next = e.Seq + 1
		}
		s.digests[e.Digest] = true
	}
	return s, nil
}

// append drops one message into the channel and reports whether it was new.
func (s *stream) append(provider, session string, message importer.Message) (bool, error) {
	text := strings.TrimSpace(message.Content)
	if text == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	at := message.Timestamp.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	entry := streamEntry{
		Provider: provider,
		Session:  session,
		At:       at,
		Role:     strings.ToLower(strings.TrimSpace(message.Role)),
		Kind:     strings.ToLower(strings.TrimSpace(message.Kind)),
		Text:     truncateStreamText(text),
		Digest:   streamDigest(provider, at, message),
	}
	if s.digests[entry.Digest] {
		return false, nil
	}
	entry.Seq = s.next

	line, err := json.Marshal(entry)
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	s.next++
	s.digests[entry.Digest] = true
	return true, nil
}

// since returns the entries after a cursor, oldest first.
//
// A cursor pointing before the start of a trimmed channel returns what remains
// rather than nothing: a reader that was away longer than the channel is deep
// has missed entries, and showing the rest is more use than showing none.
func (s *stream) since(cursor int64) ([]streamEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readAll()
	if err != nil {
		return nil, err
	}
	var out []streamEntry
	for _, e := range entries {
		if e.Seq > cursor {
			out = append(out, e)
		}
	}
	return out, nil
}

// head reports the newest sequence number, which is where a reader that does
// not want history starts.
func (s *stream) head() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next - 1
}

func (s *stream) readAll() ([]streamEntry, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []streamEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e streamEntry
		// A damaged line is skipped rather than failing the read. The channel
		// is append-only and written by a live process; a torn final line is a
		// normal thing to meet, not a corrupt file.
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// trim keeps the channel bounded, dropping the oldest entries.
//
// Nothing is lost by this: every entry was written to durable memory before it
// reached the channel, and the channel exists so an agent can catch up on
// recent work without searching.
func (s *stream) trim() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readAll()
	if err != nil {
		return err
	}
	if len(entries) <= streamMaxEntries {
		return nil
	}
	kept := entries[len(entries)-streamMaxEntries:]
	var b strings.Builder
	for _, e := range kept {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func streamDigest(provider string, at time.Time, m importer.Message) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		provider, at.Format(time.RFC3339Nano),
		strings.ToLower(m.Role), strings.ToLower(m.Kind), m.Content,
	}, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func truncateStreamText(s string) string {
	if len(s) <= streamEntryBytes {
		return s
	}
	cut := streamEntryBytes
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return fmt.Sprintf("%s\n… truncated (%d of %d bytes)", strings.TrimRight(s[:cut], "\n"), cut, len(s))
}

// cursors records how far each reader has looked.
type cursors map[string]int64

func cursorPath(root string) string { return filepath.Join(streamDir(root), "cursors.json") }

func loadCursors(root string) (cursors, error) {
	data, err := os.ReadFile(cursorPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return cursors{}, nil
	}
	if err != nil {
		return nil, err
	}
	c := cursors{}
	if err := json.Unmarshal(data, &c); err != nil {
		// A cursor file that cannot be read is rebuilt rather than fatal. The
		// cost of losing it is a reader seeing recent entries twice, which is
		// a great deal better than refusing to start an agent.
		return cursors{}, nil
	}
	return c, nil
}

func saveCursors(root string, c cursors) error {
	if err := os.MkdirAll(streamDir(root), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := cursorPath(root) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, cursorPath(root))
}

// readerNames lists the readers with a cursor, for reporting.
func (c cursors) readerNames() []string {
	names := make([]string, 0, len(c))
	for name := range c {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
