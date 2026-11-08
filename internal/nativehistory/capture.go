package nativehistory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

type Capture struct {
	binary     string
	runTimeout time.Duration
	authorized func(string) bool
	chatFile   func(string) bool
	claude     bool
	// openCodeRun is set for OpenCode's SQLite-backed history. Its public CLI
	// supplies JSON exports, so MARSHAL never reads the database or depends on
	// its private schema. The adapter selects visible conversation fields.
	openCodeRun func(context.Context, ...string) ([]byte, error)
	// openCodeDB is the store read while a session is open. The CLI export
	// above remains what runs at exit and stays the source of record.
	openCodeDB string
	// antigravity is set for agy's per-conversation SQLite history, which is
	// read directly and read-only: agy has no export command. The adapter
	// takes only fields observed to carry visible conversation and tool
	// evidence, and never reads the model's reasoning.
	antigravity          bool
	antigravitySummaries string
	// captureTools records tool calls and their results alongside conversation,
	// so a later session can see what the agent actually ran and changed rather
	// than only what it said about it.
	captureTools bool
	dir, root    string
	indexPath    string
	indexDirty   bool
	indexSaved   bool
	seen         map[string]string
	// claudeResume remembers how far each Claude history file was consumed,
	// so a file that only grew is read from there instead of from the start.
	claudeResume   map[string]claudeResume
	consume        func(importer.SessionTranscript) error
	observeSession func(importer.SessionTranscript) error
}

func newNativeHistoryWatch(dir, root string) *Capture {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &Capture{dir: dir, root: filepath.Clean(root), seen: make(map[string]string)}
}

func (w *Capture) sync(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.authorized != nil && !w.authorized(w.dir) {
		return nil
	}
	if w.openCodeDB != "" {
		err := w.syncOpenCodeLive(ctx)
		// A store that is not the shape MARSHAL reads is not a failure, it is a
		// reason to use the supported interface instead. The export is slower
		// and cannot run mid-session, so the work arrives at exit rather than as
		// it happens — late, but never wrong and never missing.
		if errors.Is(err, errOpenCodeSchemaMoved) && w.openCodeRun != nil {
			w.openCodeDB = ""
			return w.syncOpenCode(ctx)
		}
		return err
	}
	if w.openCodeRun != nil {
		return w.syncOpenCode(ctx)
	}
	if w.antigravity {
		return w.syncAntigravity(ctx)
	}
	var failures []error
	walkErr := filepath.WalkDir(w.dir, func(path string, entry fs.DirEntry, err error) error {
		// Cancellation stops the whole scan rather than one file; it is the
		// one condition the walk must not swallow.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
		if err := w.syncFile(ctx, path); err != nil {
			if ctx.Err() != nil {
				return err
			}
			// One damaged or rejected session must not stop all other sessions.
			if len(failures) < 3 {
				failures = append(failures, fmt.Errorf("%s: %w", filepath.Base(path), err))
			}
			return nil
		}
		w.seen[path] = stamp
		w.indexDirty = true
		return nil
	})
	return errors.Join(append(failures, walkErr, w.saveIndexIfChanged())...)
}

func (w *Capture) loadIndex() error {
	if w.indexPath == "" {
		return nil
	}
	if err := statepath.CheckPath(w.root, w.indexPath); err != nil {
		return err
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
	w.indexSaved = true
	return nil
}

func (w *Capture) saveIndex() error {
	if w.indexPath == "" {
		return nil
	}
	if err := statepath.CheckPath(w.root, w.indexPath); err != nil {
		return err
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
	if err := os.Rename(f.Name(), w.indexPath); err != nil {
		return err
	}
	w.indexDirty = false
	w.indexSaved = true
	return nil
}

func (w *Capture) syncFile(ctx context.Context, path string) error {
	if w.chatFile != nil && !w.chatFile(path) {
		return nil
	}
	if w.authorized != nil && !w.authorized(path) {
		return nil
	}
	if w.claude {
		return w.syncClaudeFile(ctx, path)
	}
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
	adapter := importer.CodexJSONLAdapter{CaptureTools: w.captureTools}
	tr, err := adapter.Decode(meta)
	if err != nil {
		return err
	}
	tr.Provider = "codex"
	cwd := tr.CWD
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	if tr.SessionID == "" || filepath.Clean(cwd) != w.root {
		return nil
	}
	if w.observeSession != nil {
		if err := w.observeSession(tr); err != nil {
			return err
		}
	}
	for {
		// Check between records so a large granted history stops promptly when
		// its context is cancelled rather than reading to the end first.
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := r.ReadSlice('\n')
		if errors.Is(readErr, bufio.ErrBufferFull) {
			// Tool payloads can exceed the provider adapter's one-line bound.
			// Drain that event and continue with later conversation instead of
			// making the entire live history permanently unimportable.
			for errors.Is(readErr, bufio.ErrBufferFull) {
				_, readErr = r.ReadSlice('\n')
			}
			if readErr == nil {
				continue
			}
		}
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
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := w.consume(tr); err != nil {
				return err
			}
			tr.Messages = nil
		}
	}
	if len(tr.Messages) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		return w.consume(tr)
	}
	return nil
}

func (w *Capture) saveIndexIfChanged() error {
	if w.indexSaved && !w.indexDirty {
		return nil
	}
	return w.saveIndex()
}
