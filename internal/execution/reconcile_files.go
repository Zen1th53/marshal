package execution

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type deliveryFile struct {
	path    string
	data    []byte
	mode    os.FileMode
	before  []byte
	info    os.FileInfo
	parent  *os.Root
	staged  string
	backup  string
	applied bool
}

func reconcileFiles(source, target *os.Root, permitted, skips []string) (modified []string, resultErr error) {
	scopes := make([]string, 0, len(permitted))
	for _, path := range permitted {
		if filepath.IsAbs(path) {
			var err error
			path, err = filepath.Rel(target.Name(), path)
			if err != nil {
				return nil, err
			}
		}
		path = filepath.Clean(path)
		if !safeRelative(path) {
			return nil, fmt.Errorf("%w: unsafe permitted path", ErrIsolationCompromised)
		}
		scopes = append(scopes, path)
	}
	var files []*deliveryFile
	var directories []string
	// Snapshot all source bytes and destination identities before any writes.
	err := fs.WalkDir(source.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		for _, skip := range skips {
			if entry.Name() == skip {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if path == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			// An unchanged link is not part of a regular-file delivery. Compare
			// link text without following either link or destination ancestors.
			parent, err := openDirectory(target, filepath.Dir(path), false, nil)
			if err == nil {
				defer parent.Close()
				info, statErr := parent.Lstat(filepath.Base(path))
				if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
					before, beforeErr := parent.Readlink(filepath.Base(path))
					after, afterErr := source.Readlink(path)
					if beforeErr == nil && afterErr == nil && before == after {
						return nil
					}
				}
			}
			return fmt.Errorf("%w: delivery contains a link", ErrIsolationCompromised)
		}
		if entry.IsDir() {
			dest, err := openDirectory(target, path, false, nil)
			if err == nil {
				dest.Close()
			} else if !os.IsNotExist(err) {
				return err
			}
			if permitted == nil {
				directories = append(directories, path)
			}
			return nil
		}
		parent, err := openDirectory(source, filepath.Dir(path), false, nil)
		if err != nil {
			return err
		}
		data, info, err := readRegular(parent, filepath.Base(path))
		parent.Close()
		if err != nil {
			return err
		}
		f := &deliveryFile{path: path, data: data, mode: info.Mode().Perm()}
		destParent, err := openDirectory(target, filepath.Dir(path), false, nil)
		if err == nil {
			f.before, f.info, err = readRegular(destParent, filepath.Base(path))
			destParent.Close()
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && bytes.Equal(data, f.before) && f.mode == f.info.Mode().Perm() {
			return nil
		}
		allowed := permitted == nil
		for _, scope := range scopes {
			if path == scope || strings.HasPrefix(path, scope+"/") {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("%w: unpermitted delivery file %s", ErrIsolationCompromised, path)
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var created []string
	defer func() {
		for _, f := range files {
			if f.parent == nil {
				continue
			}
			if f.staged != "" {
				_ = f.parent.Remove(f.staged)
			}
			if f.backup != "" {
				_ = f.parent.Remove(f.backup)
			}
			f.parent.Close()
		}
		if resultErr != nil {
			for i := len(created) - 1; i >= 0; i-- {
				if err := target.Remove(created[i]); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("restore directory: %w", err))
				}
			}
			modified = nil
		}
	}()
	for _, path := range directories {
		dir, err := openDirectory(target, path, true, &created)
		if err != nil {
			return nil, err
		}
		dir.Close()
	}
	// Stage the complete validated snapshot and retain original inodes for rollback.
	for _, f := range files {
		f.parent, err = openDirectory(target, filepath.Dir(f.path), true, &created)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(f.path)
		if err := validateDeliveryDestination(f); err != nil {
			return nil, err
		}
		f.staged, err = model.NewID(".marshal-stage-")
		if err != nil {
			return nil, err
		}
		out, err := f.parent.OpenFile(f.staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.mode)
		if err != nil {
			return nil, err
		}
		_, err = out.Write(f.data)
		err = errors.Join(err, out.Chmod(f.mode), out.Close())
		if err != nil {
			return nil, err
		}
		if f.info != nil {
			f.backup, err = model.NewID(".marshal-backup-")
			if err != nil {
				return nil, err
			}
			if err := f.parent.Link(name, f.backup); err != nil {
				return nil, err
			}
			backupInfo, err := f.parent.Lstat(f.backup)
			if err != nil || !os.SameFile(f.info, backupInfo) {
				return nil, fmt.Errorf("%w: delivery destination changed", ErrIsolationCompromised)
			}
		}
	}
	return publishDelivery(files)
}

func validateDeliveryDestination(f *deliveryFile) error {
	current, info, err := readRegular(f.parent, filepath.Base(f.path))
	if f.info == nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("%w: delivery destination appeared", ErrIsolationCompromised)
		}
	} else if err != nil || !os.SameFile(f.info, info) || !bytes.Equal(f.before, current) || f.info.Mode() != info.Mode() {
		return fmt.Errorf("%w: delivery destination changed", ErrIsolationCompromised)
	}
	return nil
}

func publishDelivery(files []*deliveryFile) (modified []string, resultErr error) {
	defer func() {
		if resultErr == nil {
			return
		}
		for i := len(files) - 1; i >= 0; i-- {
			f := files[i]
			if !f.applied {
				continue
			}
			var err error
			if f.backup != "" {
				err = f.parent.Rename(f.backup, filepath.Base(f.path))
			} else {
				err = f.parent.Remove(filepath.Base(f.path))
			}
			if err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("restore delivery: %w", err))
				// Preserve the original inode if restoration itself is unavailable.
				f.backup = ""
			}
		}
		modified = nil
	}()
	for _, f := range files {
		if err := validateDeliveryDestination(f); err != nil {
			return nil, err
		}
		if err := f.parent.Rename(f.staged, filepath.Base(f.path)); err != nil {
			return nil, fmt.Errorf("publish delivery: %w", err)
		}
		f.applied = true
		modified = append(modified, f.path)
	}
	return modified, nil
}
