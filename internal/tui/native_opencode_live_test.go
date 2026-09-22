package tui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// writeOpenCodeStore builds a store in the shape OpenCode keeps one.
func writeOpenCodeStore(t *testing.T, dir, directory string) string {
	t.Helper()
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT, directory TEXT, time_updated INTEGER)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO session VALUES('ses_1','p',?,1700000000000)`, directory); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO message VALUES('m1','ses_1',1700000000000,'{"role":"assistant"}')`); err != nil {
		t.Fatal(err)
	}
	parts := []string{
		`{"type":"text","text":"rewrote the watcher"}`,
		`{"type":"reasoning","text":"the operator probably wants me to guess here"}`,
		`{"type":"step-start"}`,
		`{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"go test ./..."},"output":"ok"}}`,
	}
	for i, p := range parts {
		if _, err := db.Exec(`INSERT INTO part VALUES(?,'m1','ses_1',?,?)`,
			"p"+string(rune('a'+i)), 1700000000000+int64(i), p); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// OpenCode's work is read while it runs, from the store, and decoded by the
// same adapter the CLI export goes through.
func TestOpenCodeLiveReadsTheStore(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	path := writeOpenCodeStore(t, dir, root)

	sessions, err := readOpenCodeLive(path, root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("found %d sessions, want 1", len(sessions))
	}
	tr, err := (importer.OpenCodeExportAdapter{CaptureTools: true}).Decode(sessions[0].document)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var joined strings.Builder
	for _, m := range tr.Messages {
		joined.WriteString(m.Content)
		joined.WriteString("\n")
	}
	if !strings.Contains(joined.String(), "rewrote the watcher") {
		t.Errorf("visible text did not survive:\n%s", joined.String())
	}
	if !strings.Contains(joined.String(), "go test") {
		t.Errorf("tool evidence did not survive:\n%s", joined.String())
	}
}

// The model's hidden reasoning must never travel between agents, any more than
// it reaches memory.
func TestOpenCodeLiveExcludesReasoning(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	path := writeOpenCodeStore(t, dir, root)

	sessions, err := readOpenCodeLive(path, root)
	if err != nil {
		t.Fatal(err)
	}
	// The reasoning is in the document, because the document is the store's
	// own shape — the adapter is what refuses it, once, for every path.
	if !strings.Contains(string(sessions[0].document), "probably wants me to guess") {
		t.Fatal("the fixture no longer carries a reasoning part; the test proves nothing")
	}
	tr, err := (importer.OpenCodeExportAdapter{CaptureTools: true}).Decode(sessions[0].document)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range tr.Messages {
		if strings.Contains(m.Content, "probably wants me to guess") {
			t.Errorf("hidden reasoning reached the transcript: %q", m.Content)
		}
	}
}

// Only this project's sessions are read.
func TestOpenCodeLiveIgnoresOtherProjects(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	path := writeOpenCodeStore(t, dir, "/somewhere/else")

	sessions, err := readOpenCodeLive(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("read %d sessions belonging to another project", len(sessions))
	}
}

// A store whose shape has moved is not read at all. OpenCode owns this
// database; a reader that guessed wrong would put something it had misread into
// every other agent's view.
func TestOpenCodeLiveStandsDownOnAMovedSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, folder TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = readOpenCodeLive(path, t.TempDir())
	if !errors.Is(err, errOpenCodeSchemaMoved) {
		t.Fatalf("a moved schema returned %v, want errOpenCodeSchemaMoved", err)
	}
}

// A missing store is not an error: OpenCode may simply never have run here.
func TestOpenCodeLiveToleratesNoStore(t *testing.T) {
	sessions, err := readOpenCodeLive(filepath.Join(t.TempDir(), "absent.db"), t.TempDir())
	if err != nil || sessions != nil {
		t.Errorf("absent store gave %d sessions, err=%v", len(sessions), err)
	}
}

// One malformed row must not stop every other agent hearing about the rest.
func TestOpenCodeLiveSkipsAMalformedRow(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	path := writeOpenCodeStore(t, dir, root)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO message VALUES('m2','ses_1',1700000001000,'{not json')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	sessions, err := readOpenCodeLive(path, root)
	if err != nil {
		t.Fatalf("a malformed row failed the whole read: %v", err)
	}
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(sessions[0].document, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 1 {
		t.Errorf("document carries %d messages, want the one good row", len(doc.Messages))
	}
}

// The live read and the CLI prime agree on what counts as already seen, so
// switching between them re-imports nothing. Both key on the session's update
// stamp, and the values were verified equal against a real store.
func TestOpenCodeLiveSharesTheSeenIndexWithTheExport(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	path := writeOpenCodeStore(t, dir, root)

	w := newNativeHistoryWatch(dir, root)
	w.openCodeDB = path
	w.captureTools = true
	w.indexPath = filepath.Join(t.TempDir(), "index.json")
	imported := 0
	w.consume = func(tr importer.SessionTranscript) error {
		imported += len(tr.Messages)
		return nil
	}
	if err := w.sync(); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if imported == 0 {
		t.Fatal("the first sync imported nothing")
	}
	before := imported
	if err := w.sync(); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if imported != before {
		t.Errorf("a second sync re-imported %d message(s)", imported-before)
	}
	if got := w.seen["ses_1"]; got != "1700000000000" {
		t.Errorf("seen stamp = %q, want the session's update time", got)
	}
}

func TestOpenCodeDBPathHonoursOverrides(t *testing.T) {
	t.Setenv("MARSHAL_OPENCODE_DB", "/tmp/explicit.db")
	if got, _ := openCodeDBPath(); got != "/tmp/explicit.db" {
		t.Errorf("explicit override ignored: %s", got)
	}
	t.Setenv("MARSHAL_OPENCODE_DB", "")
	t.Setenv("XDG_DATA_HOME", "/tmp/share")
	if got, _ := openCodeDBPath(); got != "/tmp/share/opencode/opencode.db" {
		t.Errorf("XDG_DATA_HOME ignored: %s", got)
	}
	_ = os.Unsetenv("XDG_DATA_HOME")
}
