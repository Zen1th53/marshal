package store

import (
	"context"
	"database/sql"
	"net/url"
)

// OpenReadOnly opens existing state for diagnostics without configuring WAL,
// migrating, creating files, or acquiring runtime ownership.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(1000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
