package nativehistory

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
	_ "modernc.org/sqlite"
)

// antigravityHome is where agy keeps its CLI state.
func antigravityHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli"), nil
}

func newAntigravityHistoryWatch(root string) (*Capture, error) {
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
func (w *Capture) listAntigravityConversations(ctx context.Context) ([]antigravityConversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(w.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var conversations []antigravityConversation
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
func (w *Capture) primeAntigravity(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.authorized != nil && !w.authorized(w.dir) {
		return nil
	}
	conversations, err := w.listAntigravityConversations(ctx)
	if err != nil {
		return err
	}
	for _, conversation := range conversations {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.seen[conversation.id] = conversation.stamp
		w.indexDirty = true
	}
	return w.saveIndexIfChanged()
}

func (w *Capture) syncAntigravity(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	conversations, err := w.listAntigravityConversations(ctx)
	if err != nil {
		return err
	}
	workspaces := w.antigravityWorkspaces(ctx)

	var failures []error
	for _, conversation := range conversations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if w.chatFile != nil && !w.chatFile(conversation.path) {
			continue
		}
		if w.seen[conversation.id] == conversation.stamp {
			continue
		}
		// A conversation agy recorded against another workspace belongs to
		// that project. Only conversations positively confirmed to match this
		// project root are imported; unknown or absent workspaces are skipped
		// so concurrent sessions in other projects are never attributed here,
		// unless this specific run launched the session.
		uris, known := workspaces[conversation.id]
		if known && len(uris) > 0 {
			if !w.antigravityWorkspaceMatches(uris) {
				w.seen[conversation.id] = conversation.stamp
				w.indexDirty = true
				continue
			}
		} else {
			if w.chatFile == nil || !w.chatFile(conversation.path) {
				continue
			}
		}
		steps, err := readAntigravitySteps(ctx, conversation.path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failures = append(failures, fmt.Errorf("read %s: %w", conversation.id, err))
			continue
		}
		tr, err := importer.DecodeAntigravityConversation(conversation.id, steps, w.captureTools)
		if err != nil {
			failures = append(failures, fmt.Errorf("decode %s: %w", conversation.id, err))
			continue
		}
		tr.CWD = w.root
		if w.observeSession != nil {
			if err := w.observeSession(tr); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		failed := false
		for _, message := range tr.Messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			one := tr
			one.Messages = []importer.Message{message}
			if err := w.consume(one); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failures = append(failures, fmt.Errorf("import %s: %w", conversation.id, err))
				failed = true
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !failed {
			w.seen[conversation.id] = conversation.stamp
			w.indexDirty = true
		}
		if len(failures) >= 3 {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(append(failures, w.saveIndexIfChanged())...)
}

// antigravityWorkspaces reads which workspace each conversation ran in. A
// summaries database that cannot be read leaves conversations without a
// confirmed workspace match, so they are safely skipped.
func (w *Capture) antigravityWorkspaces(ctx context.Context) map[string][]string {
	workspaces := map[string][]string{}
	if err := ctx.Err(); err != nil {
		return workspaces
	}
	summariesPath := w.antigravitySummaries
	if summariesPath == "" && w.dir != "" {
		summariesPath = filepath.Join(filepath.Dir(w.dir), "conversation_summaries.db")
	}
	if summariesPath == "" {
		return workspaces
	}
	if _, err := os.Stat(summariesPath); err != nil {
		return workspaces
	}
	db, err := openReadOnlySQLite(summariesPath)
	if err != nil {
		return workspaces
	}
	defer db.Close()
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT conversation_id, workspace_uris FROM conversation_summaries`)
	if err != nil {
		return workspaces
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return workspaces
		}
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

func (w *Capture) antigravityWorkspaceMatches(uris []string) bool {
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
func readAntigravitySteps(ctx context.Context, path string) ([]importer.AntigravityStep, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := openReadOnlySQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT idx, step_type, metadata, step_payload FROM steps ORDER BY idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []importer.AntigravityStep
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var step importer.AntigravityStep
		if err := rows.Scan(&step.Index, &step.Type, &step.Metadata, &step.Payload); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return steps, nil
}

func openReadOnlySQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
