package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/project"
)

// StartWithNativeCodex opens the native session after terminal ownership has
// been established and before the workspace starts reading keys.
func (w *Workspace) StartWithNativeCodex(args []string) {
	copyArgs := append([]string(nil), args...)
	w.nativeOnStart = &copyArgs
}

// nativeArgs parses argv, never shell code. Quoted image paths, configuration
// values and prompts must reach Codex as the operator entered them.
func nativeArgs(s string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range s {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, started = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, started = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("unfinished quote or escape in Codex arguments")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

// Native mode intentionally uses the operator's real Codex environment. The
// config-free task harness is a separate workflow with different guarantees.
func (w *Workspace) runNativeCodex(ctx context.Context, args []string) (string, error) {
	if w.terminal == nil || !w.terminal.IsTerminal() {
		return "", errors.New("native Codex requires an interactive terminal; use /codex exec for batch tasks")
	}
	binary, err := project.FindBinary("codex")
	if err != nil {
		return "", err
	}
	root := w.workDir
	if w.runtime != nil {
		root = w.runtime.ProjectRoot()
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".codex")
	}
	watch := newNativeHistoryWatch(filepath.Join(home, "sessions"), root)
	var syncErr error
	watch.indexPath = filepath.Join(root, ".marshal", "codex", "history-index.json")
	if err := watch.loadIndex(); err != nil {
		syncErr = err
	}
	imported := 0
	if source := w.controlSource(); source != nil {
		if authority, ok := source.Authority.(*runtimeControlAuthority); ok {
			if nativeUsesModelPreference(args) {
				if selected, err := authority.SelectedCodexModel(ctx); err == nil && selected != "" {
					args = append([]string{"--model", selected}, args...)
				}
			}
			watch.consume = func(tr importer.SessionTranscript) error {
				captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				service, err := authority.memory()
				if err != nil {
					return err
				}
				// Import each visible message independently for stable deduplication
				// across periodic saves and process restarts.
				for _, message := range tr.Messages {
					one := tr
					one.Messages = []importer.Message{message}
					data, err := json.Marshal(one)
					if err != nil {
						return err
					}
					result, err := service.ImportSessionTranscript(captureCtx, authority.memoryPrincipal(), authority.runtime.ProjectID(), data, false)
					if err != nil {
						return err
					}
					imported += len(result.ImportedRecords)
				}
				return nil
			}
		}
	}
	if watch.consume == nil {
		return "", errors.New("native Codex memory capture requires an attached runtime")
	}
	if w.navView != nil {
		w.navView.Close()
	}
	resume := w.SuspendTerminal()
	defer resume()
	fmt.Fprintln(os.Stdout, "MARSHAL · native Codex · conversation autosaves to project memory · /quit returns to MARSHAL")
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start native Codex: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case runErr := <-done:
			if err := watch.sync(); err != nil {
				syncErr = err
			}
			result := fmt.Sprintf("Codex exited. %d conversation messages saved to MARSHAL memory.\n/codex continue resumes; /codex new starts a new session.", imported)
			if syncErr != nil {
				result += "\nMemory capture incomplete:\n" + syncErr.Error()
			}
			return result, runErr
		case <-ticker.C:
			if err := watch.sync(); err != nil {
				syncErr = err
			}
		}
	}
}

func nativeUsesModelPreference(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-m" || arg == "--model" || strings.HasPrefix(arg, "--model=") || arg == "-p" || arg == "--profile" {
			return false
		}
	}
	return len(args) == 0 || args[0] == "--" || args[0] == "resume" || args[0] == "fork"
}

type nativeHistoryWatch struct {
	dir, root string
	indexPath string
	seen      map[string]string
	consume   func(importer.SessionTranscript) error
}

func newNativeHistoryWatch(dir, root string) *nativeHistoryWatch {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &nativeHistoryWatch{dir: dir, root: filepath.Clean(root), seen: make(map[string]string)}
}

func (w *nativeHistoryWatch) sync() error {
	var failures []error
	walkErr := filepath.WalkDir(w.dir, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stamp := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		if w.seen[path] == stamp {
			return nil
		}
		if err := w.syncFile(path); err != nil {
			// One damaged or rejected session must not stop all other sessions.
			if len(failures) < 3 {
				failures = append(failures, fmt.Errorf("%s: %w", filepath.Base(path), err))
			}
			return nil
		}
		w.seen[path] = stamp
		return nil
	})
	return errors.Join(append(failures, walkErr, w.saveIndex())...)
}

func (w *nativeHistoryWatch) loadIndex() error {
	if w.indexPath == "" {
		return nil
	}
	f, err := os.Open(w.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var seen map[string]string
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&seen); err != nil {
		return err
	}
	if seen != nil {
		w.seen = seen
	}
	return nil
}

func (w *nativeHistoryWatch) saveIndex() error {
	if w.indexPath == "" {
		return nil
	}
	dir := filepath.Dir(w.indexPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".history-index-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(w.seen)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), w.indexPath)
}

func (w *nativeHistoryWatch) syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	meta, err := r.ReadSlice('\n')
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	meta = bytes.Clone(meta)
	adapter := importer.CodexJSONLAdapter{}
	tr, err := adapter.Decode(meta)
	if err != nil {
		return err
	}
	cwd := tr.CWD
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	if tr.SessionID == "" || filepath.Clean(cwd) != w.root {
		return nil
	}
	for {
		line, readErr := r.ReadSlice('\n')
		// Ignore a partial final event; a later poll retries it after append.
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		item, err := adapter.Decode(line)
		if err != nil {
			return err
		}
		if item.SessionID != "" && item.SessionID != tr.SessionID {
			return errors.New("mixed session IDs")
		}
		tr.Messages = append(tr.Messages, item.Messages...)
		if len(tr.Messages) >= 64 {
			if err := w.consume(tr); err != nil {
				return err
			}
			tr.Messages = nil
		}
	}
	if len(tr.Messages) != 0 {
		return w.consume(tr)
	}
	return nil
}
