package hostgit

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

type fileIdentity struct {
	Device uint64
	Inode  uint64
}
type repositoryIdentity struct {
	Worktree      string
	Gitdir        string
	Common        string
	Root          fileIdentity
	Descriptor    fileIdentity
	Metadata      fileIdentity
	Shared        fileIdentity
	Pointer       string
	CommonPointer string
}

var repositories = struct {
	sync.RWMutex
	pins map[string]repositoryIdentity
}{pins: make(map[string]repositoryIdentity)}

func identity(path string) (fileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileIdentity{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fileIdentity{}, fmt.Errorf("Git identity path is a symlink: %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, fmt.Errorf("Git identity unavailable")
	}
	return fileIdentity{uint64(stat.Dev), uint64(stat.Ino)}, nil
}

func capture(dir string) (repositoryIdentity, error) {
	root, metadata, err := repositoryPaths(dir, false)
	if err != nil {
		return repositoryIdentity{}, err
	}
	pin := repositoryIdentity{Worktree: root, Gitdir: metadata, Common: metadata}
	for _, entry := range []struct {
		path string
		dest *fileIdentity
	}{
		{root, &pin.Root}, {filepath.Join(root, ".git"), &pin.Descriptor}, {metadata, &pin.Metadata},
	} {
		*entry.dest, err = identity(entry.path)
		if err != nil {
			return pin, err
		}
	}
	info, err := os.Lstat(filepath.Join(root, ".git"))
	if err != nil {
		return pin, err
	}
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(filepath.Join(root, ".git"))
		if err != nil {
			return pin, err
		}
		pin.Pointer = string(data)
	}
	data, err := os.ReadFile(filepath.Join(metadata, "commondir"))
	if err == nil {
		pin.CommonPointer = string(data)
		pin.Common = filepath.Clean(filepath.Join(metadata, strings.TrimSpace(string(data))))
	} else if !os.IsNotExist(err) {
		return pin, err
	}
	pin.Shared, err = identity(pin.Common)
	return pin, err
}

func pinPath(repository, tree string) (string, error) {
	_, metadata, err := repositoryPaths(repository, false)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(tree)
	if err != nil {
		return "", err
	}
	return filepath.Join(metadata, "marshal-identities", fmt.Sprintf("%x.json", sha256.Sum256([]byte(absolute)))), nil
}

// RecordRepository is called only after MARSHAL creates a task repository,
// before granting the worker access. The record lives in host project metadata.
func RecordRepository(repository, tree string) error {
	pin, err := capture(tree)
	if err != nil {
		return err
	}
	path, err := pinPath(repository, tree)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(pin)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	repositories.Lock()
	repositories.pins[pin.Worktree] = pin
	repositories.Unlock()
	return nil
}

// RestoreRepository loads the host-owned identity, never a new worker identity.
func RestoreRepository(repository, tree string) error {
	path, err := pinPath(repository, tree)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("task Git identity missing: %w", err)
	}
	var pin repositoryIdentity
	if err := json.Unmarshal(data, &pin); err != nil {
		return err
	}
	absolute, err := filepath.Abs(tree)
	if err != nil {
		return err
	}
	if pin.Worktree != absolute {
		return fmt.Errorf("task Git identity path differs")
	}
	if err := verify(pin); err != nil {
		return err
	}
	repositories.Lock()
	repositories.pins[absolute] = pin
	repositories.Unlock()
	return nil
}

func verify(pin repositoryIdentity) error {
	current, err := capture(pin.Worktree)
	if err != nil || current != pin {
		return fmt.Errorf("task Git metadata identity changed")
	}
	return nil
}

func pinnedPaths(dir string) (string, string, bool, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false, err
	}
	repositories.RLock()
	pin, ok := repositories.pins[absolute]
	repositories.RUnlock()
	if !ok {
		return "", "", false, nil
	}
	if err := verify(pin); err != nil {
		return "", "", true, err
	}
	return pin.Worktree, pin.Gitdir, true, nil
}
