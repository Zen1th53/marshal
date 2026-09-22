package tui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// Reading OpenCode while it runs.
//
// MARSHAL reached OpenCode's history through its public CLI export, on the
// reasoning that the export is a supported interface and its database is not.
// That reasoning was sound while the export was the only option, and it stopped
// being the whole story once Antigravity's private store was read directly for
// exactly the same purpose. Holding one provider to a rule the next one is
// exempt from is not a principle, it is an inconsistency — and the cost fell on
// the operator, whose OpenCode work reached the others late for no reason they
// could see.
//
// So the store is read directly, and the CLI export becomes the fallback rather
// than the only path: it is what runs when the schema has moved out from under
// this reader.
//
// Three things keep the schema coupling honest:
//
//   - The database is opened read-only and never written to.
//   - The shape is checked before it is read. A schema that has moved makes
//     this path stand down and hand back to the CLI export, rather than
//     importing something it has misunderstood. The export cannot run
//     mid-session, so that session's work arrives at exit instead of as it
//     happens — later than it should be, never wrong and never missing.
//   - What is read is assembled into the same export document the CLI produces
//     and handed to the same adapter. The live path cannot select different
//     fields from the export path, because it does not select fields at all.
//     In particular reasoning parts are excluded there, once, for both.

// openCodeDBPath is where OpenCode keeps its store.
func openCodeDBPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("MARSHAL_OPENCODE_DB")); override != "" {
		return override, nil
	}
	if share := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); share != "" {
		return filepath.Join(share, "opencode", "opencode.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}

// errOpenCodeSchemaMoved marks the one failure that is not a failure: OpenCode
// owns this database and may change it. When it has, MARSHAL stops reading it
// and falls back to the supported CLI export, which cannot run mid-session and
// so delivers at exit — late, but never wrong.
var errOpenCodeSchemaMoved = errors.New("OpenCode store is not the shape MARSHAL reads")

// openCodeSchema is the shape this reader understands.
//
// Checked rather than assumed: OpenCode owns this database and may change it,
// and a reader that guessed wrong would put something it had misread into every
// other agent's view.
var openCodeSchema = map[string][]string{
	"session": {"id", "directory", "time_updated"},
	"message": {"id", "session_id", "time_created", "data"},
	"part":    {"id", "message_id", "time_created", "data"},
}

// checkOpenCodeSchema reports what is missing, or nil when the shape holds.
func checkOpenCodeSchema(db *sql.DB) error {
	for table, columns := range openCodeSchema {
		rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return fmt.Errorf("read %s columns: %w", table, err)
		}
		present := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			present[name] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(present) == 0 {
			return fmt.Errorf("table %q is missing", table)
		}
		for _, column := range columns {
			if !present[column] {
				return fmt.Errorf("%s.%s is missing", table, column)
			}
		}
	}
	return nil
}

// openCodeLiveSession is one session's export document, rebuilt from the store.
type openCodeLiveSession struct {
	id      string
	updated int64
	// document is the same JSON the CLI export emits, so the same adapter reads
	// it. Building the document rather than the transcript is the point: field
	// selection stays in one place.
	document []byte
}

// readOpenCodeLive returns the project's sessions as export documents.
//
// It reads, it does not import: the caller decides what is new and hands the
// documents to the adapter.
func readOpenCodeLive(dbPath, root string) ([]openCodeLiveSession, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	db, err := openReadOnlySQLite(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := checkOpenCodeSchema(db); err != nil {
		return nil, fmt.Errorf("%w: %v", errOpenCodeSchemaMoved, err)
	}

	rows, err := db.Query(`SELECT id, directory, time_updated FROM session`)
	if err != nil {
		return nil, err
	}
	type sessionRow struct {
		id        string
		directory string
		updated   int64
	}
	var candidates []sessionRow
	for rows.Next() {
		var r sessionRow
		var directory sql.NullString
		var updated sql.NullInt64
		if err := rows.Scan(&r.id, &directory, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		r.directory, r.updated = directory.String, updated.Int64
		candidates = append(candidates, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []openCodeLiveSession
	for _, candidate := range candidates {
		directory := candidate.directory
		if resolved, err := filepath.EvalSymlinks(directory); err == nil {
			directory = resolved
		}
		if candidate.id == "" || filepath.Clean(directory) != root {
			continue
		}
		document, err := buildOpenCodeExport(db, candidate.id, candidate.directory, candidate.updated)
		if err != nil {
			return nil, err
		}
		out = append(out, openCodeLiveSession{
			id: candidate.id, updated: candidate.updated, document: document,
		})
	}
	return out, nil
}

// buildOpenCodeExport assembles one session into the CLI export's shape.
func buildOpenCodeExport(db *sql.DB, sessionID, directory string, updated int64) ([]byte, error) {
	type exportPart struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		Tool  string          `json:"tool,omitempty"`
		State json.RawMessage `json:"state,omitempty"`
	}
	type exportMessage struct {
		Info struct {
			SessionID string `json:"sessionID"`
			Role      string `json:"role"`
			Time      struct {
				Created int64 `json:"created"`
			} `json:"time"`
		} `json:"info"`
		Parts []exportPart `json:"parts"`
	}
	var doc struct {
		Info struct {
			ID        string `json:"id"`
			Directory string `json:"directory"`
			Time      struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			} `json:"time"`
		} `json:"info"`
		Messages []exportMessage `json:"messages"`
	}
	doc.Info.ID = sessionID
	doc.Info.Directory = directory
	doc.Info.Time.Updated = updated

	rows, err := db.Query(
		`SELECT id, time_created, data FROM message WHERE session_id = ? ORDER BY time_created, id`,
		sessionID)
	if err != nil {
		return nil, err
	}
	type messageRow struct {
		id      string
		created int64
		data    []byte
	}
	var messages []messageRow
	for rows.Next() {
		var m messageRow
		var created sql.NullInt64
		if err := rows.Scan(&m.id, &created, &m.data); err != nil {
			rows.Close()
			return nil, err
		}
		m.created = created.Int64
		messages = append(messages, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, m := range messages {
		var info struct {
			Role string `json:"role"`
		}
		// A message whose payload cannot be parsed is skipped rather than
		// failing the session: one malformed row must not stop every other
		// agent hearing about the rest of the work.
		if err := json.Unmarshal(m.data, &info); err != nil {
			continue
		}
		var out exportMessage
		out.Info.SessionID = sessionID
		out.Info.Role = info.Role
		out.Info.Time.Created = m.created

		partRows, err := db.Query(
			`SELECT data FROM part WHERE message_id = ? ORDER BY time_created, id`, m.id)
		if err != nil {
			return nil, err
		}
		for partRows.Next() {
			var data []byte
			if err := partRows.Scan(&data); err != nil {
				partRows.Close()
				return nil, err
			}
			var part exportPart
			if err := json.Unmarshal(data, &part); err != nil {
				continue
			}
			out.Parts = append(out.Parts, part)
		}
		partRows.Close()
		if err := partRows.Err(); err != nil {
			return nil, err
		}
		doc.Messages = append(doc.Messages, out)
	}
	return json.Marshal(doc)
}

// syncOpenCodeLive reads the store and hands new sessions to the adapter.
//
// It mirrors syncOpenCode's bookkeeping — the same seen-index keyed on the
// session's update stamp — so switching between the live read and the export
// does not re-import anything.
func (w *nativeHistoryWatch) syncOpenCodeLive() error {
	sessions, err := readOpenCodeLive(w.openCodeDB, w.root)
	if err != nil {
		return err
	}
	var failures []error
	for _, session := range sessions {
		stamp := strconv.FormatInt(session.updated, 10)
		if w.seen[session.id] == stamp {
			continue
		}
		tr, err := w.decodeOpenCodeExport(session.document)
		if err != nil {
			if len(failures) < 3 {
				failures = append(failures, fmt.Errorf("%s: %w", session.id, err))
			}
			continue
		}
		if len(tr.Messages) > 0 {
			if err := w.consume(tr); err != nil {
				if len(failures) < 3 {
					failures = append(failures, fmt.Errorf("%s: %w", session.id, err))
				}
				continue
			}
		}
		w.seen[session.id] = stamp
	}
	return errors.Join(append(failures, w.saveIndex())...)
}

// decodeOpenCodeExport runs the export document through the adapter the CLI
// path uses, so both select the same fields and exclude the same ones.
func (w *nativeHistoryWatch) decodeOpenCodeExport(document []byte) (importer.SessionTranscript, error) {
	return (importer.OpenCodeExportAdapter{CaptureTools: w.captureTools}).Decode(document)
}
