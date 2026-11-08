//go:build linux

package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// The callback and unixFile layout are pinned to modernc.org/sqlite v1.57.0
// on Linux amd64 and arm64. Each Store gets its own VFS, so its recorded FD
// belongs to its sole pooled connection, even when another Store opens the
// same pathname. A held sql.Conn prevents that connection from being recycled.
type architectureSQLiteFile struct {
	mu       sync.Mutex
	fd       int32
	opened   bool
	original uintptr
}

var architectureVFSSequence atomic.Uint64
var architectureVFSFiles sync.Map // native sqlite3_vfs pointer -> *architectureSQLiteFile
var architectureOpenCallback = architectureNativeOpen

func uintptrToPtr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

func architectureSQLiteDSN(path string) (string, *architectureSQLiteFile, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "state.db" || filepath.Base(filepath.Dir(path)) != statepath.DirName {
		return path, nil, nil
	}
	tls := libc.NewTLS()
	defer tls.Close()
	original := sqlite3.Xsqlite3_vfs_find(tls, 0)
	if original == 0 {
		return "", nil, fmt.Errorf("native SQLite VFS unavailable")
	}
	unixName, err := libc.CString("unix")
	if err != nil {
		return "", nil, err
	}
	unix := sqlite3.Xsqlite3_vfs_find(tls, unixName)
	libc.Xfree(tls, unixName)
	if unix == 0 || unix != original {
		return "", nil, fmt.Errorf("pinned unix SQLite VFS required")
	}
	name := fmt.Sprintf("marshal-identity-%d", architectureVFSSequence.Add(1))
	cName, err := libc.CString(name)
	if err != nil {
		return "", nil, err
	}
	native := libc.Xmalloc(tls, libc.Tsize_t(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
	if native == 0 {
		libc.Xfree(tls, cName)
		return "", nil, fmt.Errorf("allocate identity SQLite VFS")
	}
	vfs := (*sqlite3.Tsqlite3_vfs)(uintptrToPtr(native))
	*vfs = *(*sqlite3.Tsqlite3_vfs)(uintptrToPtr(original))
	vfs.FpNext = 0
	vfs.FzName = cName
	vfs.FxOpen = *(*uintptr)(unsafe.Pointer(&architectureOpenCallback))
	file := &architectureSQLiteFile{original: original}
	architectureVFSFiles.Store(native, file)
	if rc := sqlite3.Xsqlite3_vfs_register(tls, native, 0); rc != sqlite3.SQLITE_OK {
		architectureVFSFiles.Delete(native)
		libc.Xfree(tls, native)
		libc.Xfree(tls, cName)
		return "", nil, fmt.Errorf("register identity SQLite VFS: %d", rc)
	}
	// The registered VFS allocation and callback remain valid for the process.
	query := url.Values{"mode": {"rwc"}, "vfs": {name}}
	u := url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}
	return u.String(), file, nil
}

func architectureNativeOpen(tls *libc.TLS, vfs, name, nativeFile uintptr, flags int32, out uintptr) int32 {
	entry, ok := architectureVFSFiles.Load(vfs)
	if !ok {
		return sqlite3.SQLITE_CANTOPEN
	}
	file := entry.(*architectureSQLiteFile)
	// The VFS was copied from this exact native unix VFS. Its xOpen must be
	// called with its original pointer, avoiding callback recursion.
	original := file.original
	if original == 0 || original == vfs {
		return sqlite3.SQLITE_CANTOPEN
	}
	base := (*sqlite3.Tsqlite3_vfs)(uintptrToPtr(original))
	callback := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&base.FxOpen))
	rc := callback(tls, original, name, nativeFile, flags, out)
	if rc == sqlite3.SQLITE_OK && flags&sqlite3.SQLITE_OPEN_MAIN_DB != 0 {
		fd := (*sqlite3.TunixFile)(uintptrToPtr(nativeFile)).Fh
		file.mu.Lock()
		file.fd, file.opened = fd, fd >= 0
		file.mu.Unlock()
	}
	return rc
}

func (s *Store) validateArchitectureFile(ctx context.Context, path string) error {
	if s == nil || s.db == nil || s.architectureFile == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return model.ErrPolicyDenied
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var name, current string
	var seq int
	if err = conn.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &current); err != nil || name != "main" || current != path {
		return model.ErrPolicyDenied
	}
	return s.validateArchitectureFD(path)
}

// validateArchitectureTransaction queries the active writer transaction. It
// never obtains a second pooled connection while the sole connection is held.
func (s *Store) validateArchitectureTransaction(ctx context.Context, tx *sql.Tx, path string) error {
	if s == nil || tx == nil || s.architectureFile == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return model.ErrPolicyDenied
	}
	var name, current string
	var seq int
	if err := tx.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &current); err != nil || seq != 0 || name != "main" || current != path {
		return model.ErrPolicyDenied
	}
	return s.validateArchitectureFD(path)
}

func (s *Store) validateArchitectureFD(path string) error {
	registered, err := architecturePathNoFollow(path)
	if err != nil {
		return model.ErrPolicyDenied
	}
	s.architectureFile.mu.Lock()
	fd, opened := s.architectureFile.fd, s.architectureFile.opened
	s.architectureFile.mu.Unlock()
	if !opened {
		return model.ErrPolicyDenied
	}
	var actual syscall.Stat_t
	if syscall.Fstat(int(fd), &actual) != nil || actual.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return model.ErrPolicyDenied
	}
	if registered.Dev != actual.Dev || registered.Ino != actual.Ino || registered.Nlink != 1 || actual.Nlink != 1 {
		return model.ErrPolicyDenied
	}
	return nil
}

func architecturePathNoFollow(path string) (syscall.Stat_t, error) {
	var result syscall.Stat_t
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return result, model.ErrPolicyDenied
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return result, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= syscall.O_DIRECTORY
		}
		next, openErr := syscall.Openat(fd, part, flags, 0)
		syscall.Close(fd)
		if openErr != nil {
			return result, openErr
		}
		fd = next
	}
	defer syscall.Close(fd)
	if err = syscall.Fstat(fd, &result); err != nil || result.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return result, model.ErrPolicyDenied
	}
	return result, nil
}
