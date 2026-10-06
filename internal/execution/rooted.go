package execution

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func safeRelative(path string) bool {
	return path != "." && path != "" && filepath.IsLocal(path) && !strings.Contains(path, `\`) && filepath.Clean(path) == path
}

// openDirectory pins each component and rejects links, including links within the root.
func openDirectory(root *os.Root, path string, create bool, created *[]string) (*os.Root, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if path == "." {
		return current, nil
	}
	prefix := ""
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		info, err := current.Lstat(part)
		if os.IsNotExist(err) && create {
			err = current.Mkdir(part, 0755)
			if err == nil {
				if created != nil {
					*created = append(*created, prefix)
				}
				info, err = current.Lstat(part)
			}
		}
		if err != nil {
			current.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, fmt.Errorf("%w: unsafe directory component", ErrIsolationCompromised)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		actual, err := next.Stat(".")
		current.Close()
		if err != nil || !os.SameFile(info, actual) {
			next.Close()
			return nil, fmt.Errorf("%w: directory changed", ErrIsolationCompromised)
		}
		current = next
	}
	return current, nil
}

func readRegular(root *os.Root, name string) ([]byte, os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: delivery must contain regular files", ErrIsolationCompromised)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, nil, fmt.Errorf("%w: file changed", ErrIsolationCompromised)
	}
	data, err := io.ReadAll(file)
	return data, info, err
}
