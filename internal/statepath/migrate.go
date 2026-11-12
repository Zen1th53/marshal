package statepath

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// ValidateRoot prevents an unrestricted worker from treating the entire host or
// the user's home as its project. Resolve aliases before comparing identities.
func ValidateRoot(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home before selecting project: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if root == string(filepath.Separator) || root == filepath.Clean(home) {
		return &RefusedRootError{Root: root, Home: root != string(filepath.Separator)}
	}
	return nil
}

// RefusedRootError is the refusal of the home folder or / as a project.
// Workers run on the host without a sandbox, so neither is ever a project.
type RefusedRootError struct {
	// Root is the refused directory, with aliases resolved.
	Root string
	// Home reports whether Root is the home folder rather than /.
	Home bool
}

// RefusedRootMessage is what a person running Marshal directly in the home
// folder or / is told.
const RefusedRootMessage = "Marshal does not use your home folder (or /) as a project. Make a folder for your project, cd into it and run marshal there."

func (e *RefusedRootError) Error() string { return RefusedRootMessage }

// InsideReason says, in plain words, what is wrong with a folder below the
// refused root whose Git repository is that root, and what to do.
func (e *RefusedRootError) InsideReason() string {
	if e.Home {
		return "This folder is inside the Git repository of your home folder. Run `marshal` here and it will start a separate repository for this project."
	}
	return "This folder is inside a Git repository that covers your whole computer (/). Run `marshal` here and it will start a separate repository for this project."
}

const migrationMarker = ".namespace-migration-pending"

// Prepare renames the former engine namespace once. The rename is atomic;
// subsequent path updates are idempotent and a durable marker blocks normal
// startup until they finish. No existing destination is overwritten.
func Prepare(root string, repairs ...func(string, string) error) error {
	if err := ValidateRoot(root); err != nil {
		return err
	}
	if !PublicEngine {
		return nil
	}
	old := filepath.Join(root, ".marshal-extreme")
	target := Path(root)
	_, targetErr := os.Lstat(target)
	_, pendingErr := os.Lstat(filepath.Join(target, migrationMarker))
	if targetErr == nil && os.IsNotExist(pendingErr) {
		return nil
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return targetErr
	}
	source := old
	if targetErr == nil {
		source = target
	}
	if _, err := os.Lstat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	// Never follow an alias or traverse checked-out project contents.
	if err := validateNamespace(root, filepath.Base(source), ""); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(source, "runtime-owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("close the running Marshal windows before migrating .marshal-extreme: %w", err)
	}
	// The former engine also has native provider sessions outside this lock.
	// Require their runtime socket to be closed rather than contacting tmux.
	if _, err := os.Lstat(filepath.Join(source, "runtime.sock")); err == nil {
		return fmt.Errorf("close the old Marshal runtime before migration; if stopped, remove its stale runtime.sock after backing up .marshal-extreme")
	} else if !os.IsNotExist(err) {
		return err
	}
	marker := filepath.Join(source, migrationMarker)
	if err := writeMigrationFile(marker, []byte(old+"\n"), 0600, false); err != nil {
		return err
	}
	if err := syncMigrationDir(source); err != nil {
		return err
	}
	if source == old {
		// Recheck after taking the owner lock. A concurrent migration must not
		// replace even an empty destination.
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			return fmt.Errorf("migration destination .marshal already exists; retry startup")
		}
		if err := renameNamespace(old, target); err != nil {
			return err
		}
		if err := syncMigrationDir(root); err != nil {
			return err
		}
	}
	if err := relocateState(target, old, target); err != nil {
		return fmt.Errorf("Marshal namespace migration paused safely (data retained in .marshal); close old sessions and retry, or restore your backup: %w", err)
	}
	for _, repair := range repairs {
		if err := repair(root, target); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(target, migrationMarker)); err != nil {
		return err
	}
	return syncMigrationDir(target)
}

func relocateState(state, old, target string) error {
	return filepath.WalkDir(state, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("migration refuses symlink %s", path)
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(state, path)
			if err != nil {
				return err
			}
			if rel == "worktrees" || rel == "branches" || rel == "checkpoints" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == migrationMarker || strings.HasSuffix(path, ".namespace-backup") {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("close old sessions before migrating special file %s", path)
		}
		if strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
			return relocateSQLite(path, old, target)
		}
		if !bytes.Contains(data, []byte(old)) {
			return nil
		}
		if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
			return fmt.Errorf("binary file contains a former state path; relocate manually: %s", path)
		}
		if err := writeMigrationFile(path+".namespace-backup", data, 0600, true); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		tmp := path + ".namespace-tmp"
		if err := writeMigrationFile(tmp, relocatePaths(data, old, target), info.Mode().Perm(), false); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
		return syncMigrationDir(filepath.Dir(path))
	})
}

func sqlName(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// SQLite updates run in one transaction, including JSON state held as TEXT.
// Immutable records whose triggers prohibit relocation refuse safely instead
// of bypassing their evidence controls.
func relocateSQLite(path, old, target string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := os.Lstat(path + ".namespace-backup"); os.IsNotExist(err) {
		if _, err := db.ExecContext(context.Background(), "VACUUM INTO ?", path+".namespace-backup"); err != nil {
			return err
		}
		if err := syncMigrationDir(filepath.Dir(path)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, table := range tables {
		cols, err := tx.Query("PRAGMA table_info(" + sqlName(table) + ")")
		if err != nil {
			return err
		}
		var names []string
		for cols.Next() {
			var id, notnull, pk int
			var name, kind string
			var defaultValue any
			if err := cols.Scan(&id, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
				cols.Close()
				return err
			}
			names = append(names, name)
		}
		if err := cols.Err(); err != nil {
			cols.Close()
			return err
		}
		cols.Close()
		for _, name := range names {
			col := sqlName(name)
			expr := "CASE WHEN " + col + "=? THEN ? ELSE " + col + " END"
			args := []any{old, target}
			for _, sep := range pathDelimiters {
				expr = "replace(" + expr + ", ?, ?)"
				args = append(args, old+sep, target+sep)
			}
			query := "UPDATE " + sqlName(table) + " SET " + col + "=" + expr + " WHERE typeof(" + col + ")='text' AND " + col + "!=" + expr
			args = append(args, args...)
			if _, err := tx.ExecContext(context.Background(), query, args...); err != nil {
				return err
			}

		}
	}
	return tx.Commit()
}

var pathDelimiters = []string{"/", `"`, "'", " ", "\n", "\t", `\`}

func relocatePaths(data []byte, old, target string) []byte {
	if string(data) == old {
		return []byte(target)
	}
	for _, sep := range pathDelimiters {
		data = bytes.ReplaceAll(data, []byte(old+sep), []byte(target+sep))
	}
	if bytes.HasSuffix(data, []byte(old)) {
		data = append(data[:len(data)-len(old)], []byte(target)...)
	}
	return data
}

func writeMigrationFile(path string, data []byte, mode os.FileMode, exclusive bool) error {
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if exclusive {
		flags = os.O_CREATE | os.O_WRONLY | os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, mode)
	if exclusive && os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

func syncMigrationDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
