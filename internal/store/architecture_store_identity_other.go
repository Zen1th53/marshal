//go:build !linux

package store

type architectureSQLiteFile struct{}

func architectureSQLiteDSN(path string) (string, *architectureSQLiteFile, error) {
	return path, nil, nil
}
