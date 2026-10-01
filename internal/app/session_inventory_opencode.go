package app

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// The existing live importer reads these same identity columns. A private
// history index establishes ownership, never public resume availability.
func readOpenCodeInventory(ctx context.Context, root string, add func(string, string, string, time.Time, bool)) error {
	path := os.Getenv("MARSHAL_OPENCODE_DB")
	if path == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			base = filepath.Join(home, ".local", "share")
		}
		path = filepath.Join(base, "opencode", "opencode.db")
	}
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
	rows, err := db.QueryContext(ctx, `SELECT id, directory, time_updated FROM session`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, dir string
		var updated int64
		if err := rows.Scan(&id, &dir, &updated); err != nil {
			return err
		}
		if canonicalSessionRoot(dir) == canonicalSessionRoot(root) {
			add("opencode", id, "provider history index", time.UnixMilli(updated), false)
		}
	}
	return rows.Err()
}
