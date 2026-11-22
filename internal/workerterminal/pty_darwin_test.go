//go:build darwin

package workerterminal

import (
	"strings"
	"syscall"
	"testing"
)

// macOS has no parent-death signal; descendant supervision is Linux-only.
func assertParentDeathSignal(*testing.T, *syscall.SysProcAttr) {}

func TestDarwinPTYNamesItsSlave(t *testing.T) {
	master, _, slave, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if !strings.HasPrefix(slave.Name(), "/dev/ttys") {
		t.Fatalf("slave device %q is not a macOS pseudo-terminal", slave.Name())
	}
	if _, err := slave.WriteString("ping\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := master.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "ping") {
		t.Fatalf("master read %q, %v", buf[:n], err)
	}
}
