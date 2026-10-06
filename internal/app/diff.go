package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/redaction"
)

const (
	DiffMaxFiles      = 100
	DiffMaxFileBytes  = 64 * 1024
	DiffMaxTotalBytes = 1024 * 1024
)

type DiffEntry struct{ Scope, Path, Preview string }
type DiffInventory struct {
	Entries   []DiffEntry
	Truncated bool
}

// cappedDiffOutput bounds memory even when Git emits a large patch.
type cappedDiffOutput struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedDiffOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	b.Buffer.Write(p)
	return n, nil
}
func diffGit(ctx context.Context, dir string, limit int, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd, err := hostgit.Command(ctx, dir, args...)
	if err != nil {
		return "", false, err
	}
	cmd.Env = append(cmd.Env, "GIT_LITERAL_PATHSPECS=1")
	out := &cappedDiffOutput{limit: limit}
	cmd.Stdout = out
	// Diagnostics may contain project content; never forward raw Git stderr.
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("Git inspection failed: %w", err)
	}
	return out.String(), out.truncated, nil
}

// LoadDiffInventory reads Git and project files without altering the index.
// Each scope is observed sequentially; this is not an atomic worktree snapshot.
func LoadDiffInventory(ctx context.Context, dir, scope string) (DiffInventory, error) {
	var result DiffInventory
	if scope != "" && scope != "staged" && scope != "unstaged" && scope != "untracked" {
		return result, fmt.Errorf("invalid diff scope")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	total := 0
	for _, section := range []string{"staged", "unstaged", "untracked"} {
		if scope != "" && scope != section {
			continue
		}
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", "--no-renames", "--relative"}
		if section == "staged" {
			args = append(args, "--cached")
		}
		if section == "untracked" {
			args = []string{"ls-files", "--others", "--exclude-standard", "-z"}
		}
		names, overflow, err := diffGit(ctx, dir, DiffMaxTotalBytes, args...)
		if err != nil {
			return DiffInventory{}, err
		}
		if overflow {
			return DiffInventory{}, fmt.Errorf("Git file inventory exceeds byte limit")
		}
		for _, name := range strings.Split(names, "\x00") {
			if name == "" {
				continue
			}
			if len(result.Entries) >= DiffMaxFiles || total >= DiffMaxTotalBytes {
				result.Truncated = true
				continue
			}
			limit := min(DiffMaxFileBytes, DiffMaxTotalBytes-total)
			var preview string
			var truncated bool
			if section == "untracked" {
				info, e := root.Lstat(name)
				if e != nil {
					return DiffInventory{}, fmt.Errorf("untracked file inspection failed: %w", e)
				}
				if info.Mode()&os.ModeSymlink != 0 {
					preview = "[symlink: preview omitted]"
				} else if !info.Mode().IsRegular() {
					preview = "[non-regular file: preview omitted]"
				} else {
					f, e := root.Open(name)
					if e != nil {
						return DiffInventory{}, fmt.Errorf("untracked preview failed: %w", e)
					}
					data, e := io.ReadAll(io.LimitReader(f, int64(limit+1)))
					f.Close()
					if e != nil {
						return DiffInventory{}, e
					}
					truncated = len(data) > limit
					if truncated {
						data = data[:limit]
					}
					if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
						preview = "[binary file: preview omitted]"
					} else {
						preview = string(data)
					}
				}
			} else {
				args = []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "--relative"}
				if section == "staged" {
					args = append(args, "--cached")
				}
				args = append(args, "--", name)
				preview, truncated, err = diffGit(ctx, dir, limit, args...)
				if err != nil {
					return DiffInventory{}, err
				}
				if strings.IndexByte(preview, 0) >= 0 || (!truncated && !utf8.ValidString(preview)) {
					preview = "[binary file: preview omitted]"
					truncated = false
				}
			}

			// Redact before retaining any preview. Drop an incomplete final line so
			// truncation cannot cut a secret in half before the helper sees it.
			if truncated {
				if i := strings.LastIndexByte(preview, '\n'); i >= 0 {
					preview = preview[:i+1]
				} else {
					preview = ""
				}
				preview += "\n[preview truncated]"
			}
			preview = redaction.RedactContent(preview, nil)
			if len(preview) > limit {
				const marker = "\n[preview truncated]"
				if limit < len(marker) {
					preview = marker[:limit]
				} else {
					preview = preview[:limit-len(marker)]
					if i := strings.LastIndexByte(preview, '\n'); i >= 0 {
						preview = preview[:i+1]
					} else {
						preview = ""
					}
					preview += marker
				}
			}
			total += len(preview)
			result.Entries = append(result.Entries, DiffEntry{Scope: section, Path: strconv.Quote(name), Preview: preview})
		}
	}
	return result, nil
}
