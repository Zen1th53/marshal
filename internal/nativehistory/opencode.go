package nativehistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

type openCodeSession struct {
	ID        string `json:"id"`
	Updated   int64  `json:"updated"`
	Directory string `json:"directory"`
}

func newOpenCodeHistoryWatch(binary, root string) *Capture {
	watch := newNativeHistoryWatch("", root)
	watch.captureTools = true
	if dbPath, err := openCodeDBPath(); err == nil {
		watch.openCodeDB = dbPath
		watch.dir = filepath.Dir(dbPath)
	}
	watch.binary = binary
	watch.runTimeout = 30 * time.Second
	return watch
}

func (w *Capture) configureOpenCodeRunner(binary string) {
	if binary == "" {
		binary = "opencode"
	}
	w.binary = binary
	if w.runTimeout <= 0 {
		w.runTimeout = 30 * time.Second
	}
	w.openCodeRun = func(ctx context.Context, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, w.runTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.WaitDelay = 100 * time.Millisecond
		cmd.Dir = w.root
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return nil, fmt.Errorf("opencode %v: %s", args, exitErr.Stderr)
			}
			return nil, err
		}
		return out, nil
	}
}

func (w *Capture) syncOpenCode(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sessions, err := w.listOpenCodeSessions(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		sessionFailed := false
		cwd := session.Directory
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = resolved
		}
		if session.ID == "" || filepath.Clean(cwd) != w.root {
			continue
		}
		stamp := strconv.FormatInt(session.Updated, 10)
		if w.seen[session.ID] == stamp {
			continue
		}
		exported, err := w.openCodeRun(ctx, "export", session.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("export %s: %w", session.ID, err))
			continue
		}
		tr, err := (importer.OpenCodeExportAdapter{CaptureTools: w.captureTools}).Decode(exported)
		if err != nil {
			// OpenCode can finish replacing a large export just after the command
			// returns. Retry once so a transient partial JSON document does not
			// leave an otherwise healthy session permanently behind.
			exported, retryErr := w.openCodeRun(ctx, "export", session.ID)
			if retryErr != nil {
				failures = append(failures, fmt.Errorf("retry export %s: %w", session.ID, retryErr))
				continue
			}
			tr, err = (importer.OpenCodeExportAdapter{CaptureTools: w.captureTools}).Decode(exported)
			if err != nil {
				failures = append(failures, fmt.Errorf("decode %s: %w", session.ID, err))
				continue
			}
		}
		for _, message := range tr.Messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			one := tr
			one.Messages = []importer.Message{message}
			if err := w.consume(one); err != nil {
				failures = append(failures, fmt.Errorf("import %s: %w", session.ID, err))
				sessionFailed = true
				break
			}
		}
		if !sessionFailed {
			w.seen[session.ID] = stamp
			w.indexDirty = true
		}
		if len(failures) >= 3 {
			break
		}
	}
	return errors.Join(append(failures, w.saveIndexIfChanged())...)
}

func (w *Capture) primeOpenCode(ctx context.Context) error {
	if w.authorized != nil && !w.authorized(w.dir) {
		return nil
	}
	sessions, err := w.listOpenCodeSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		cwd := session.Directory
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = resolved
		}
		if session.ID != "" && filepath.Clean(cwd) == w.root {
			w.seen[session.ID] = strconv.FormatInt(session.Updated, 10)
			w.indexDirty = true
		}
	}
	return w.saveIndexIfChanged()
}

func (w *Capture) listOpenCodeSessions(ctx context.Context) ([]openCodeSession, error) {
	list, err := w.openCodeRun(ctx, "session", "list", "--format", "json", "--max-count", "100")
	if err != nil {
		return nil, err
	}
	var sessions []openCodeSession
	if err := json.Unmarshal(list, &sessions); err != nil {
		return nil, fmt.Errorf("decode OpenCode session list: %w", err)
	}
	return sessions, nil
}
