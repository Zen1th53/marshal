package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// StartWithNativeOpenCode opens OpenCode after MARSHAL owns the terminal.
func (w *Workspace) StartWithNativeOpenCode(args []string) {
	w.StartWithNativeCodex(args)
	w.nativeStartupProvider = "opencode"
}

func openCodeUsesModelPreference(args []string) bool {
	for _, arg := range args {
		if arg == "-m" || arg == "--model" || strings.HasPrefix(arg, "--model=") {
			return false
		}
	}
	return len(args) == 0 || args[0] == "--continue" || args[0] == "--session" || args[0] == "--fork"
}

type openCodeSession struct {
	ID        string `json:"id"`
	Updated   int64  `json:"updated"`
	Directory string `json:"directory"`
}

func newOpenCodeHistoryWatch(binary, root string) *nativeHistoryWatch {
	watch := newNativeHistoryWatch("", root)
	watch.captureTools = true
	watch.openCodeRun = func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = root
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
	return watch
}

func (w *nativeHistoryWatch) syncOpenCode() error {
	sessions, err := w.listOpenCodeSessions()
	if err != nil {
		return err
	}
	var failures []error
	for _, session := range sessions {
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
		exported, err := w.openCodeRun("export", session.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("export %s: %w", session.ID, err))
			continue
		}
		tr, err := (importer.OpenCodeExportAdapter{CaptureTools: w.captureTools}).Decode(exported)
		if err != nil {
			// OpenCode can finish replacing a large export just after the command
			// returns. Retry once so a transient partial JSON document does not
			// leave an otherwise healthy session permanently behind.
			exported, retryErr := w.openCodeRun("export", session.ID)
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
		}
		if len(failures) >= 3 {
			break
		}
	}
	return errors.Join(append(failures, w.saveIndex())...)
}

func (w *nativeHistoryWatch) primeOpenCode() error {
	sessions, err := w.listOpenCodeSessions()
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
		}
	}
	return w.saveIndex()
}

func (w *nativeHistoryWatch) listOpenCodeSessions() ([]openCodeSession, error) {
	list, err := w.openCodeRun("session", "list", "--format", "json", "--max-count", "100")
	if err != nil {
		return nil, err
	}
	var sessions []openCodeSession
	if err := json.Unmarshal(list, &sessions); err != nil {
		return nil, fmt.Errorf("decode OpenCode session list: %w", err)
	}
	return sessions, nil
}
