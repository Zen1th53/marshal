package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Each accepted paste has a separate intent so queued owner text survives a
// restart. Recovery is explicit: terminal observation cannot prove acceptance.
func savePasteIntent(root string, turn chattransport.Turn) (string, error) {
	path := statepath.Path(root, filepath.Join("chat-paste-outbox", turn.ID+".json"))
	if err := statepath.CheckPath(root, path); err != nil {
		return "", err
	}
	d, err := chattransport.Persisted(nil, path)
	if err != nil {
		return "", err
	}
	defer d.Close()
	if err := d.Prepare(turn); err != nil {
		return "", err
	}
	return path, nil
}
func completePasteIntent(item sayItem) error {
	if item.outboxPath == "" {
		return nil
	}
	d, err := chattransport.Persisted(nil, item.outboxPath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Resolve(item.messageID)
}
func pendingPasteIntents(root string) ([]chattransport.Turn, error) {
	dir := statepath.Path(root, "chat-paste-outbox")
	if err := statepath.CheckPath(root, dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var turns []chattransport.Turn
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := statepath.CheckPath(root, path); err != nil {
			return nil, err
		}
		d, err := chattransport.Persisted(nil, path)
		if err != nil {
			return nil, err
		}
		if pending := d.Pending(); pending != nil {
			turns = append(turns, *pending)
		}
		d.Close()
	}
	return turns, nil
}
