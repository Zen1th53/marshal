package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// ArchitectureSourceSnapshot is observation data, never an owner capability.
// Tree hashes live bytes, including ignored and untracked files, rather than
// Git's committed tree or the filtered knowledge index. Only Git administrative
// entries and the canonical private root .marshal runtime are omitted.
type ArchitectureSourceSnapshot struct {
	Root, GitDirectory, Head, Tree string
	Dirty                          bool
}

type architectureSourceEntry struct {
	Path                         string
	Mode                         uint32
	Size                         int64
	Digest, Link, RepositoryHead string
}

type architectureSourceReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r architectureSourceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func architectureSnapshotGit(ctx context.Context, root string, args ...string) (string, error) {
	argv := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	// Repository selection must not come from the coordinator's environment.
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		if !strings.HasPrefix(key, "GIT_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("observe source Git identity: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func sameArchitectureSourceFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func architectureSourcePass(ctx context.Context, root string) ([]architectureSourceEntry, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var entries []architectureSourceEntry
	err = fs.WalkDir(dir.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == statepath.DirName {
			info, err := dir.Lstat(path)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
				return model.ErrPolicyDenied
			}
			return fs.SkipDir
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		before, err := dir.Lstat(path)
		if err != nil {
			return err
		}
		item := architectureSourceEntry{Path: path, Mode: uint32(before.Mode()), Size: before.Size()}
		switch {
		case before.IsDir():
			// Directory allocation size is filesystem-specific, not product data.
			item.Size = 0
			if path != "." {
				if _, err := dir.Lstat(filepath.Join(path, ".git")); err == nil {
					item.RepositoryHead, err = architectureSnapshotGit(ctx, filepath.Join(root, path), "rev-parse", "--verify", "HEAD")
					if err != nil {
						return err
					}
				} else if !os.IsNotExist(err) {
					return err
				}
			}
		case before.Mode()&os.ModeSymlink != 0:
			item.Link, err = dir.Readlink(path)
			if err != nil {
				return err
			}
		case before.Mode().IsRegular():
			// A concurrent replacement with a FIFO must not block before Stat
			// can reject its type and identity.
			file, err := dir.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			opened, statErr := file.Stat()
			if statErr != nil || !sameArchitectureSourceFile(before, opened) {
				file.Close()
				return model.ErrPolicyDenied
			}
			h := sha256.New()
			n, copyErr := io.Copy(h, architectureSourceReader{ctx: ctx, reader: file})
			after, statErr := file.Stat()
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if statErr != nil || n != before.Size() || !sameArchitectureSourceFile(before, after) {
				return model.ErrPolicyDenied
			}
			item.Digest = hex.EncodeToString(h.Sum(nil))
		default:
			return fmt.Errorf("unsupported source entry %q: %w", path, model.ErrPolicyDenied)
		}
		after, err := dir.Lstat(path)
		if err != nil || !sameArchitectureSourceFile(before, after) {
			return model.ErrPolicyDenied
		}
		entries = append(entries, item)
		return nil
	})
	return entries, err
}

// SnapshotArchitectureSource rejects worker checkouts and aliased roots. Two
// independent scans and bracketed Git observations detect concurrent changes;
// they do not claim protection from a malicious same-UID writer or establish
// physical-human provenance. Admission and launch must each take a new snapshot.
func SnapshotArchitectureSource(ctx context.Context, root string) (ArchitectureSourceSnapshot, error) {
	var snapshot ArchitectureSourceSnapshot
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return snapshot, model.ErrPolicyDenied
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil || real != root {
		return snapshot, model.ErrPolicyDenied
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() {
		return snapshot, model.ErrPolicyDenied
	}
	top, err := architectureSnapshotGit(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil || top != root {
		return snapshot, model.ErrPolicyDenied
	}
	gitDir, err := architectureSnapshotGit(ctx, root, "rev-parse", "--absolute-git-dir")
	if err != nil || gitDir != filepath.Join(root, ".git") {
		return snapshot, model.ErrPolicyDenied
	}
	gitInfo, err := os.Lstat(gitDir)
	if err != nil || !gitInfo.IsDir() || gitInfo.Mode()&os.ModeSymlink != 0 {
		return snapshot, model.ErrPolicyDenied
	}
	head, err := architectureSnapshotGit(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return snapshot, err
	}
	status, err := architectureSnapshotGit(ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none", "--", ".", ":(exclude)"+statepath.DirName)
	if err != nil {
		return snapshot, err
	}
	first, err := architectureSourcePass(ctx, root)
	if err != nil {
		return snapshot, err
	}
	second, err := architectureSourcePass(ctx, root)
	if err != nil {
		return snapshot, err
	}
	a, err := json.Marshal(first)
	if err != nil {
		return snapshot, err
	}
	b, err := json.Marshal(second)
	if err != nil {
		return snapshot, err
	}
	newHead, err := architectureSnapshotGit(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return snapshot, err
	}
	newStatus, err := architectureSnapshotGit(ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none", "--", ".", ":(exclude)"+statepath.DirName)
	if err != nil {
		return snapshot, err
	}
	newRootInfo, err := os.Lstat(root)
	if err != nil || !sameArchitectureSourceFile(rootInfo, newRootInfo) || string(a) != string(b) || head != newHead || status != newStatus {
		return snapshot, model.ErrPolicyDenied
	}
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	digest := sha256.Sum256(append([]byte("marshal.full-source.v1\x00"), a...))
	return ArchitectureSourceSnapshot{Root: root, GitDirectory: gitDir, Head: head, Tree: hex.EncodeToString(digest[:]), Dirty: status != ""}, nil
}
