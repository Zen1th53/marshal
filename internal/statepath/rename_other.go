//go:build !linux

package statepath

import "fmt"

func renameNamespace(old, target string) error {
	return fmt.Errorf("automatic state namespace migration requires Linux atomic no-replace rename; back up and move %s to %s manually with all sessions closed", old, target)
}
