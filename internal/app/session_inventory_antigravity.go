package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func readAntigravityInventory(ctx context.Context, root string, add func(string, string, string, time.Time, bool)) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, ".gemini", "antigravity-cli")
	path := filepath.Join(base, "conversation_summaries.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT conversation_id, workspace_uris FROM conversation_summaries`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var uris []string
		if json.Unmarshal([]byte(raw), &uris) != nil {
			continue
		}
		for _, uri := range uris {
			path := uri
			if u, err := url.Parse(uri); err == nil && u.Scheme == "file" {
				path = u.Path
			}
			if canonicalSessionRoot(path) != canonicalSessionRoot(root) {
				continue
			}
			// A summary must refer to a real conversation, and IDs cannot escape
			// the provider's conversation directory.
			if filepath.Base(id) != id || id == "." || id == ".." {
				continue
			}
			info, err := os.Stat(filepath.Join(base, "conversations", id+".db"))
			if err != nil {
				continue
			}
			add("antigravity", id, "provider history index", info.ModTime(), false)
			break
		}
	}
	return rows.Err()
}
