package fsread

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// History is a read-only view of a git repository's object database, used
// by the offline oracle to reconstruct historical snapshots.
//
// It runs only object-reading plumbing (rev-parse, rev-list, ls-tree,
// diff-tree --raw, cat-file --batch) with the hardened environment and
// command-line overrides of git ls-files. It never asks git for content
// diffs, so no diff driver, textconv or external diff can apply, and
// cat-file --batch returns raw objects without clean/smudge filters.
// Lazy fetches from promisor remotes and every transport are disabled, so
// a partial clone cannot cause network access.
type History struct {
	abs string
}

// OpenHistory opens the git repository rooted at dir (which must contain a
// .git entry).
func OpenHistory(dir string) (*History, error) {
	r, err := Open(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if !r.HasGitDir() {
		return nil, fmt.Errorf("fsread: %s has no .git", r.Abs())
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("fsread: git not found on PATH")
	}
	h := &History{abs: r.Abs()}
	if err := h.refuseAlternates(context.Background()); err != nil {
		return nil, err
	}
	return h, nil
}

// ErrAlternates is returned for repositories whose object database
// borrows objects from elsewhere (objects/info/alternates or
// http-alternates): a planted alternates file could make the oracle read,
// and materialize, another repository's objects.
var ErrAlternates = errors.New("fsread: repository uses object alternates; refusing to read objects outside it (run `git repack -a -d` and remove objects/info/alternates)")

func (h *History) refuseAlternates(ctx context.Context) error {
	out, err := h.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" || !filepath.IsAbs(dir) {
		return fmt.Errorf("fsread: unexpected git-common-dir %q", dir)
	}
	for _, f := range []string{"alternates", "http-alternates"} {
		if _, err := os.Lstat(filepath.Join(dir, "objects", "info", f)); err == nil {
			return ErrAlternates
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func historyEnv(root string) []string {
	return append(gitEnv(root), "GIT_NO_LAZY_FETCH=1", "GIT_PROTOCOL_FROM_USER=0")
}

func historyArgs(sub ...string) []string {
	base := gitArgs("-c", "protocol.allow=never", "-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"-c", "core.commitGraph=false", "-c", "log.showSignature=false")
	return append(base, sub...)
}

func (h *History) command(ctx context.Context, sub ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", historyArgs(sub...)...)
	cmd.Dir = h.abs
	cmd.Env = historyEnv(h.abs)
	return cmd
}

func (h *History) run(ctx context.Context, sub ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := h.command(ctx, sub...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("fsread: git %s: %v: %s", sub[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func isHex40(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ResolveCommit resolves rev to a full commit id.
func (h *History) ResolveCommit(ctx context.Context, rev string) (string, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return "", fmt.Errorf("fsread: invalid revision %q", rev)
	}
	out, err := h.run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("fsread: unknown commit %q", rev)
	}
	s := strings.TrimSpace(string(out))
	if !isHex40(s) {
		return "", fmt.Errorf("fsread: unexpected rev-parse output for %q", rev)
	}
	return s, nil
}

// CommitRef is a commit id with its parents.
type CommitRef struct {
	SHA     string
	Parents []string
}

// RevList lists non-merge commits reachable from the given revision
// arguments (already-resolved ids, or "^id" exclusions), newest first.
func (h *History) RevList(ctx context.Context, revs []string, max int) ([]CommitRef, error) {
	args := []string{"rev-list", "--no-merges", "--parents"}
	if max > 0 {
		args = append(args, "--max-count="+strconv.Itoa(max))
	}
	for _, r := range revs {
		x := strings.TrimPrefix(r, "^")
		if !isHex40(x) {
			return nil, fmt.Errorf("fsread: rev-list wants resolved ids, got %q", r)
		}
	}
	args = append(args, "--end-of-options")
	args = append(args, revs...)
	out, err := h.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var cs []CommitRef
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		cs = append(cs, CommitRef{SHA: f[0], Parents: f[1:]})
	}
	return cs, nil
}

// TreeEntry is one ls-tree record.
type TreeEntry struct {
	Mode string // e.g. 100644, 100755, 120000 (symlink), 160000 (submodule)
	Type string // blob, tree, commit
	SHA  string
	Path string // cleaned repository-relative path
}

// RegularBlob reports whether the entry is a regular file blob.
func (e TreeEntry) RegularBlob() bool {
	return e.Type == "blob" && (e.Mode == "100644" || e.Mode == "100755")
}

// Tree lists every blob of a commit's tree. Entries whose paths fail
// CleanRel (absolute, "..", inside .git) are dropped.
func (h *History) Tree(ctx context.Context, commit string) ([]TreeEntry, error) {
	if !isHex40(commit) {
		return nil, fmt.Errorf("fsread: invalid commit id %q", commit)
	}
	out, err := h.run(ctx, "ls-tree", "-r", "-z", "--full-tree", "--end-of-options", commit)
	if err != nil {
		return nil, err
	}
	var es []TreeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, p, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			continue
		}
		f := strings.Fields(string(meta))
		if len(f) != 3 {
			continue
		}
		c, err := CleanRel(string(p))
		if err != nil {
			continue
		}
		es = append(es, TreeEntry{Mode: f[0], Type: f[1], SHA: f[2], Path: c})
	}
	return es, nil
}

// Change is one diff-tree --raw record.
type Change struct {
	Status           byte // 'A', 'D', 'M', 'T', 'R', 'C'
	OldMode, NewMode string
	OldSHA, NewSHA   string
	OldPath, NewPath string // OldPath empty for A, NewPath empty for D
}

// DiffTree lists changed blobs between two commits (no content diff).
// With renames, git's internal rename detection pairs deletions and
// additions; it reads blob contents only, without drivers or filters.
func (h *History) DiffTree(ctx context.Context, a, b string, renames bool) ([]Change, error) {
	if !isHex40(a) || !isHex40(b) {
		return nil, errors.New("fsread: diff-tree wants resolved ids")
	}
	rn := "--no-renames"
	if renames {
		rn = "--find-renames"
	}
	out, err := h.run(ctx, "diff-tree", "-r", "-z", "--raw", "--no-abbrev", "--no-ext-diff", "--no-textconv", rn, "--end-of-options", a, b)
	if err != nil {
		return nil, err
	}
	recs := bytes.Split(out, []byte{0})
	var cs []Change
	for i := 0; i < len(recs); i++ {
		meta := string(recs[i])
		if !strings.HasPrefix(meta, ":") {
			continue
		}
		f := strings.Fields(meta[1:])
		if len(f) != 5 || f[4] == "" {
			continue
		}
		c := Change{Status: f[4][0], OldMode: f[0], NewMode: f[1], OldSHA: f[2], NewSHA: f[3]}
		npaths := 1
		if c.Status == 'R' || c.Status == 'C' {
			npaths = 2
		}
		if i+npaths >= len(recs) {
			break
		}
		p1, err1 := CleanRel(string(recs[i+1]))
		p2, err2 := p1, err1
		if npaths == 2 {
			p2, err2 = CleanRel(string(recs[i+2]))
		}
		i += npaths
		if err1 != nil || err2 != nil {
			continue
		}
		switch c.Status {
		case 'A':
			c.NewPath = p1
		case 'D':
			c.OldPath = p1
		case 'R', 'C':
			c.OldPath, c.NewPath = p1, p2
		default:
			c.OldPath, c.NewPath = p1, p1
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// ObjectReader streams raw objects from one `git cat-file --batch`.
// It is not safe for concurrent use.
type ObjectReader struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	in     io.WriteCloser
	out    *bufio.Reader
}

// MaxObjectBytes caps objects returned by ObjectReader.Read; larger
// objects are drained and reported with ErrTooLarge.
const MaxObjectBytes = 16 << 20

// Objects starts an object reader.
func (h *History) Objects(ctx context.Context) (*ObjectReader, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := h.command(ctx, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	return &ObjectReader{cmd: cmd, cancel: cancel, in: in, out: bufio.NewReaderSize(out, 1<<16)}, nil
}

// Read returns the type and raw bytes of an object.
func (r *ObjectReader) Read(sha string) (string, []byte, error) {
	if !isHex40(sha) {
		return "", nil, fmt.Errorf("fsread: invalid object id %q", sha)
	}
	if _, err := io.WriteString(r.in, sha+"\n"); err != nil {
		return "", nil, err
	}
	hdr, err := r.out.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	f := strings.Fields(hdr)
	if len(f) == 2 && f[1] == "missing" {
		return "", nil, fmt.Errorf("fsread: object %s missing", sha)
	}
	if len(f) != 3 || f[0] != sha {
		return "", nil, fmt.Errorf("fsread: bad cat-file header %q", strings.TrimSpace(hdr))
	}
	n, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || n < 0 {
		return "", nil, fmt.Errorf("fsread: bad object size %q", f[2])
	}
	if n > MaxObjectBytes {
		if _, err := io.CopyN(io.Discard, r.out, n+1); err != nil {
			return "", nil, err
		}
		return f[1], nil, ErrTooLarge
	}
	buf := make([]byte, n+1)
	if _, err := io.ReadFull(r.out, buf); err != nil {
		return "", nil, err
	}
	return f[1], buf[:n], nil
}

// Close stops the reader.
func (r *ObjectReader) Close() error {
	r.in.Close()
	err := r.cmd.Wait()
	r.cancel()
	return err
}

// CommitMessage extracts the message from a raw commit object.
func CommitMessage(raw []byte) string {
	if _, msg, ok := bytes.Cut(raw, []byte("\n\n")); ok {
		return string(msg)
	}
	return ""
}

// WriteTreeFile writes data to rel under root via os.Root, creating parent
// directories. A path blocked by an existing file or directory of the
// other kind is removed first. Paths failing CleanRel are refused.
func WriteTreeFile(root *os.Root, rel string, data []byte) error {
	rel, err := CleanRel(rel)
	if err != nil {
		return err
	}
	native := filepath.FromSlash(rel)
	if dir := filepath.Dir(native); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			// A file is in the way of a directory: remove the blocking
			// ancestor and retry once.
			parts := strings.Split(filepath.ToSlash(dir), "/")
			for i := range parts {
				anc := filepath.FromSlash(strings.Join(parts[:i+1], "/"))
				if st, e := root.Lstat(anc); e == nil && !st.IsDir() {
					root.Remove(anc)
					break
				}
			}
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
	}
	if st, e := root.Lstat(native); e == nil && !st.Mode().IsRegular() {
		if err := root.RemoveAll(native); err != nil {
			return err
		}
	}
	return root.WriteFile(native, data, 0o644)
}

// RemoveTreeFile removes rel under root (missing is not an error).
func RemoveTreeFile(root *os.Root, rel string) error {
	rel, err := CleanRel(rel)
	if err != nil {
		return err
	}
	err = root.Remove(filepath.FromSlash(rel))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
