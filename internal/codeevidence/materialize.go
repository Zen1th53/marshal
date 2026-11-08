package codeevidence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/hostgit"
)

// Materialization bounds. A tree beyond them is not validated (the caller
// reports the receipts as unknown); nothing is truncated silently.
const (
	maxTreeFiles = 20000
	maxTreeBytes = 256 << 20
	maxTreeDepth = 64
)

// ErrTreeTooLarge is returned when a commit's visible tree exceeds the bounds.
var ErrTreeTooLarge = errors.New("code evidence: commit tree exceeds the materialization bounds")

// Materialize writes the regular files of commit that vis accepts into a
// fresh directory below the adapter's state directory and returns it with a
// cleanup function.
//
// Only `git cat-file --batch` runs, through hostgit (no hooks, fsmonitor,
// filters, pager, external diff or lazy fetch). cat-file returns raw objects,
// so no clean/smudge filter or attribute can change the bytes. Trees are
// parsed here; symlinks and submodules are skipped, as the module reads only
// regular files. Writes go through os.Root, so a tree entry cannot escape the
// directory.
func (h Host) Materialize(ctx context.Context, commit string, vis Visibility) (string, func(), error) {
	noop := func() {}
	if !hexID(commit) {
		return "", noop, fmt.Errorf("code evidence: invalid commit id %q", commit)
	}
	if h.Repository == "" {
		return "", noop, errors.New("code evidence: repository is not configured")
	}
	base := h.Dir()
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", noop, err
	}
	dir, err := os.MkdirTemp(base, "tree-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := h.materialize(ctx, commit, vis, dir); err != nil {
		cleanup()
		return "", noop, err
	}
	return dir, cleanup, nil
}

func hexID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

type catFile struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

// Visibility is the part of a policy materialization needs.
type Visibility interface {
	Visible(rel string) bool
	DirMayContainVisible(relDir string) bool
}

func (h Host) openCatFile(ctx context.Context) (*catFile, error) {
	cmd, err := hostgit.Command(ctx, h.Repository, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Env, "GIT_PROTOCOL_FROM_USER=0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &catFile{cmd: cmd, in: stdin, out: bufio.NewReaderSize(stdout, 64<<10)}, nil
}

func (c *catFile) close() {
	_ = c.in.Close()
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
}

// read returns the type and raw content of one object, bounded by limit.
func (c *catFile) read(id string, limit int64) (string, []byte, error) {
	if _, err := io.WriteString(c.in, id+"\n"); err != nil {
		return "", nil, err
	}
	header, err := c.out.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != id {
		return "", nil, fmt.Errorf("code evidence: object %s unavailable", id)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return "", nil, fmt.Errorf("code evidence: bad object size for %s", id)
	}
	if size > limit {
		return "", nil, ErrTreeTooLarge
	}
	data := make([]byte, size+1) // content and the trailing newline
	if _, err := io.ReadFull(c.out, data); err != nil {
		return "", nil, err
	}
	return fields[1], data[:size], nil
}

type treeEntry struct {
	mode, name, id string
}

func parseTree(raw []byte, idLen int) ([]treeEntry, error) {
	var out []treeEntry
	for len(raw) > 0 {
		sp := bytes.IndexByte(raw, ' ')
		if sp <= 0 {
			return nil, errors.New("code evidence: malformed tree")
		}
		mode := string(raw[:sp])
		raw = raw[sp+1:]
		nul := bytes.IndexByte(raw, 0)
		if nul <= 0 || len(raw) < nul+1+idLen {
			return nil, errors.New("code evidence: malformed tree")
		}
		name := string(raw[:nul])
		id := hex.EncodeToString(raw[nul+1 : nul+1+idLen])
		raw = raw[nul+1+idLen:]
		out = append(out, treeEntry{mode: mode, name: name, id: id})
	}
	return out, nil
}

func (h Host) materialize(ctx context.Context, commit string, vis Visibility, dir string) error {
	cf, err := h.openCatFile(ctx)
	if err != nil {
		return err
	}
	defer cf.close()
	kind, raw, err := cf.read(commit, 1<<20)
	if err != nil {
		return err
	}
	if kind != "commit" {
		return fmt.Errorf("code evidence: %s is not a commit", commit)
	}
	first, _, _ := bytes.Cut(raw, []byte("\n"))
	tree, ok := strings.CutPrefix(string(first), "tree ")
	if !ok || !hexID(tree) {
		return fmt.Errorf("code evidence: commit %s has no tree", commit)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	idLen := len(commit) / 2
	var files int
	var total int64
	var walk func(id, prefix string, depth int) error
	walk = func(id, prefix string, depth int) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if depth > maxTreeDepth {
			return ErrTreeTooLarge
		}
		kind, raw, err := cf.read(id, 64<<20)
		if err != nil {
			return err
		}
		if kind != "tree" {
			return fmt.Errorf("code evidence: %s is not a tree", id)
		}
		entries, err := parseTree(raw, idLen)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.name == "." || e.name == ".." || strings.ContainsAny(e.name, "/\\\x00") {
				return errors.New("code evidence: unsafe tree entry name")
			}
			rel := e.name
			if prefix != "" {
				rel = prefix + "/" + e.name
			}
			switch e.mode {
			case "40000":
				if !vis.DirMayContainVisible(rel) {
					continue
				}
				if err := walk(e.id, rel, depth+1); err != nil {
					return err
				}
			case "100644", "100755":
				if !vis.Visible(rel) {
					continue
				}
				files++
				if files > maxTreeFiles {
					return ErrTreeTooLarge
				}
				_, data, err := cf.read(e.id, maxTreeBytes-total)
				if err != nil {
					return err
				}
				total += int64(len(data))
				if d := path.Dir(rel); d != "." {
					if err := root.MkdirAll(d, 0o700); err != nil {
						return err
					}
				}
				if err := root.WriteFile(rel, data, 0o600); err != nil {
					return err
				}
			default:
				// 120000 symlinks and 160000 submodules: never read.
			}
		}
		return nil
	}
	return walk(tree, "", 0)
}
