package nativehistory

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/memory/importer"
)

// claudeResume is where an earlier sync stopped: the byte after the last
// complete record, and the session header that record's file belongs to.
type claudeResume struct {
	offset int64
	head   []byte // the file's first bytes and the bytes just before offset,
	tail   []byte // to detect a file rewritten rather than appended to
	header importer.SessionTranscript
}

const claudeResumeTail = 64

// Claude histories may begin with file snapshots rather than session metadata.
// Decode complete entries and take identity from the first conversation entry.
func (w *Capture) syncClaudeFile(ctx context.Context, path string) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var tr importer.SessionTranscript
	var offset int64
	// A live Marshal chat appends to one large file many times a minute.
	// Re-decoding it from the start on every change kept a core busy, so a
	// file that only grew continues after the last complete record. A file
	// that shrank was rewritten and is read again from the start.
	if resume, ok := w.claudeResume[path]; ok {
		head, tail := make([]byte, len(resume.head)), make([]byte, len(resume.tail))
		if info, statErr := f.Stat(); statErr == nil && info.Size() >= resume.offset {
			_, headErr := f.ReadAt(head, 0)
			_, tailErr := f.ReadAt(tail, resume.offset-int64(len(tail)))
			if headErr == nil && tailErr == nil && bytes.Equal(head, resume.head) && bytes.Equal(tail, resume.tail) {
				if _, seekErr := f.Seek(resume.offset, io.SeekStart); seekErr == nil {
					tr, offset = resume.header, resume.offset
				}
			}
		}
	}
	if tr.SessionID == "" {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	defer func() {
		if w.claudeResume == nil {
			w.claudeResume = map[string]claudeResume{}
		}
		if err != nil || tr.SessionID == "" {
			delete(w.claudeResume, path)
			return
		}
		header := tr
		header.Messages = nil
		head, tail := make([]byte, min(offset, claudeResumeTail)), make([]byte, min(offset, claudeResumeTail))
		_, headErr := f.ReadAt(head, 0)
		_, tailErr := f.ReadAt(tail, offset-int64(len(tail)))
		if headErr != nil || tailErr != nil {
			delete(w.claudeResume, path)
			return
		}
		w.claudeResume[path] = claudeResume{offset: offset, head: head, tail: tail, header: header}
	}()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		// Check between records so a large granted history stops promptly when
		// its context is cancelled rather than reading to the end first.
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := r.ReadSlice('\n')
		if errors.Is(err, io.EOF) {
			break // a torn final record is read again once complete
		}
		if err != nil {
			return err
		}
		offset += int64(len(line))
		item, err := (importer.ClaudeJSONLAdapter{CaptureTools: w.captureTools}).Decode(line)
		if err != nil {
			return err
		}
		if item.SessionID == "" {
			continue
		}
		if tr.SessionID == "" {
			cwd := item.CWD
			if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
				cwd = resolved
			}
			if filepath.Clean(cwd) != w.root {
				return nil
			}
			tr = item
			if w.observeSession != nil {
				if err := w.observeSession(tr); err != nil {
					return err
				}
			}
		} else {
			if item.SessionID != tr.SessionID {
				return errors.New("mixed Claude session IDs")
			}
			// Claude records cwd on every entry and it moves as the agent works
			// inside subdirectories of the project. That is ordinary, not
			// corruption, so it must not abort capture: this check previously
			// discarded the in-flight batch and, because sync leaves the file
			// unseen on error, re-failed on every poll. On this project's own
			// history that lost every message of two large sessions.
			//
			// The session identity above is the real binding, and the first
			// entry already proved the session belongs to this root. A path
			// below that root stays in scope; anything outside it does not.
			if item.CWD != "" && !claudeEntryWithinRoot(item.CWD, w.root) {
				return errors.New("Claude entry outside the project root")
			}
			tr.Messages = append(tr.Messages, item.Messages...)
		}
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

// claudeEntryWithinRoot reports whether a per-entry cwd still belongs to the
// project. Claude moves cwd into subdirectories during a session, so
// containment rather than equality is the correct test. Symlinks are resolved
// where possible so /tmp and its real target compare equal.
func claudeEntryWithinRoot(cwd, root string) bool {
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	cwd = filepath.Clean(cwd)
	root = filepath.Clean(root)
	if cwd == root {
		return true
	}
	relative, err := filepath.Rel(root, cwd)
	if err != nil {
		return false
	}
	// ".." anywhere in the relative path means the entry escaped the root.
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
