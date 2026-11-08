//go:build governed

package execution

import "os"

func writeRunSnapshot(path string, data []byte) error {
	if err := os.WriteFile(path+".tmp", data, 0644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
