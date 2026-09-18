package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// Antigravity's CLI is `agy`. It keeps each conversation as its own SQLite
// database under ~/.gemini/antigravity-cli/conversations, with a summaries
// database beside it that names the workspace a conversation ran in.
//
// Like OpenCode, the history is a live database rather than an append-only
// log, so it is read once the agent exits rather than polled while it writes.
// Reading is read-only: MARSHAL never writes to agy's files.

const antigravityBinary = "agy"

// StartWithNativeAntigravity opens agy after MARSHAL owns the terminal.
func (w *Workspace) StartWithNativeAntigravity(args []string) {
	w.StartWithNativeCodex(args)
	w.nativeStartupProvider = "antigravity"
}

// antigravityHome is where agy keeps its CLI state.
func antigravityHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli"), nil
}

func newAntigravityHistoryWatch(root string) (*nativeHistoryWatch, error) {
	home, err := antigravityHome()
	if err != nil {
		return nil, err
	}
	watch := newNativeHistoryWatch(filepath.Join(home, "conversations"), root)
	watch.antigravity = true
	watch.antigravitySummaries = filepath.Join(home, "conversation_summaries.db")
	return watch, nil
}

// antigravityConversation is one conversation database on disk.
type antigravityConversation struct {
	id    string
	path  string
	stamp string
}

// listAntigravityConversations finds the conversation databases and stamps
// each with its size and modification times. The write-ahead log is included,
// because a conversation's newest steps can live there before a checkpoint.
func (w *nativeHistoryWatch) listAntigravityConversations() ([]antigravityConversation, error) {
	entries, err := os.ReadDir(w.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var conversations []antigravityConversation
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(name, ".db") {
			continue
		}
		path := filepath.Join(w.dir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stamp := fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
		if wal, err := os.Stat(path + "-wal"); err == nil {
			stamp += fmt.Sprintf(":%d:%d", wal.ModTime().UnixNano(), wal.Size())
		}
		conversations = append(conversations, antigravityConversation{
			id: strings.TrimSuffix(name, ".db"), path: path, stamp: stamp,
		})
	}
	return conversations, nil
}

// primeAntigravity records every conversation that already exists, so the exit
// sync imports only what this launch created or continued rather than every
// agy conversation ever held on the machine.
func (w *nativeHistoryWatch) primeAntigravity() error {
	conversations, err := w.listAntigravityConversations()
	if err != nil {
		return err
	}
	for _, conversation := range conversations {
		w.seen[conversation.id] = conversation.stamp
	}
	return w.saveIndex()
}

func (w *nativeHistoryWatch) syncAntigravity() error {
	conversations, err := w.listAntigravityConversations()
	if err != nil {
		return err
	}
	workspaces := w.antigravityWorkspaces()

	var failures []error
	for _, conversation := range conversations {
		if w.seen[conversation.id] == conversation.stamp {
			continue
		}
		// A conversation agy recorded against another workspace belongs to
		// that project. One with no recorded workspace is attributed by the
		// baseline instead: it changed while this project's session ran.
		if uris, known := workspaces[conversation.id]; known && len(uris) > 0 && !w.antigravityWorkspaceMatches(uris) {
			w.seen[conversation.id] = conversation.stamp
			continue
		}
		steps, err := readAntigravitySteps(conversation.path)
		if err != nil {
			failures = append(failures, fmt.Errorf("read %s: %w", conversation.id, err))
			continue
		}
		tr, err := importer.DecodeAntigravityConversation(conversation.id, steps, w.captureTools)
		if err != nil {
			failures = append(failures, fmt.Errorf("decode %s: %w", conversation.id, err))
			continue
		}
		tr.CWD = w.root
		failed := false
		for _, message := range tr.Messages {
			one := tr
			one.Messages = []importer.Message{message}
			if err := w.consume(one); err != nil {
				failures = append(failures, fmt.Errorf("import %s: %w", conversation.id, err))
				failed = true
				break
			}
		}
		if !failed {
			w.seen[conversation.id] = conversation.stamp
		}
		if len(failures) >= 3 {
			break
		}
	}
	return errors.Join(append(failures, w.saveIndex())...)
}

// antigravityWorkspaces reads which workspace each conversation ran in. A
// summaries database that cannot be read leaves every conversation to the
// baseline rule rather than failing the sync.
func (w *nativeHistoryWatch) antigravityWorkspaces() map[string][]string {
	workspaces := map[string][]string{}
	if w.antigravitySummaries == "" {
		return workspaces
	}
	if _, err := os.Stat(w.antigravitySummaries); err != nil {
		return workspaces
	}
	db, err := openReadOnlySQLite(w.antigravitySummaries)
	if err != nil {
		return workspaces
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT conversation_id, workspace_uris FROM conversation_summaries`)
	if err != nil {
		return workspaces
	}
	defer rows.Close()
	for rows.Next() {
		var id, uris string
		if err := rows.Scan(&id, &uris); err != nil {
			continue
		}
		workspaces[id] = splitWorkspaceURIs(uris)
	}
	return workspaces
}

// splitWorkspaceURIs reads the summaries column. agy writes it as a JSON array
// of file URIs; anything that is not one is split on separators instead, so a
// change of encoding degrades to a looser match rather than to none.
func splitWorkspaceURIs(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var uris []string
	if err := json.Unmarshal([]byte(value), &uris); err == nil {
		return uris
	}
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';' ||
			r == '[' || r == ']' || r == '"'
	})
}

func (w *nativeHistoryWatch) antigravityWorkspaceMatches(uris []string) bool {
	for _, raw := range uris {
		path := raw
		if parsed, err := url.Parse(raw); err == nil && parsed.Scheme == "file" {
			path = parsed.Path
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		if filepath.Clean(path) == w.root {
			return true
		}
	}
	return false
}

// readAntigravitySteps reads a conversation's steps without writing to it.
func readAntigravitySteps(path string) ([]importer.AntigravityStep, error) {
	db, err := openReadOnlySQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT idx, step_type, metadata, step_payload FROM steps ORDER BY idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []importer.AntigravityStep
	for rows.Next() {
		var step importer.AntigravityStep
		if err := rows.Scan(&step.Index, &step.Type, &step.Metadata, &step.Payload); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func openReadOnlySQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
