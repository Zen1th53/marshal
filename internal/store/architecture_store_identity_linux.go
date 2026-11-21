//go:build linux

package store

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

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
