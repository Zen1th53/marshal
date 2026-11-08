package tui

import "github.com/Zen1th53/marshal/internal/nativehistory"

// Keep the package-local names used by the TUI's tests and launch glue while
// the database reader itself lives in nativehistory.
type openCodeLiveSession struct {
	sessionID string
	updated   int64
	document  []byte
}

var errOpenCodeSchemaMoved = nativehistory.ErrOpenCodeSchemaMoved

func openCodeDBPath() (string, error) { return nativehistory.OpenCodeDBPath() }

func readOpenCodeLive(dbPath, root string) ([]openCodeLiveSession, error) {
	sessions, err := nativehistory.ReadOpenCodeLive(dbPath, root)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	out := make([]openCodeLiveSession, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, openCodeLiveSession{sessionID: session.SessionID, updated: session.Updated, document: session.Document})
	}
	return out, nil
}
