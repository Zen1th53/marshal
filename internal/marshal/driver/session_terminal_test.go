//go:build linux

package driver

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/workerterminal"
)

func TestNativeSessionHasRealTerminalDescriptors(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var conn net.Conn
	ctx = workerterminal.WithHost(ctx, func(_ context.Context, socket string) error {
		var err error
		conn, err = net.Dial("unix", socket)
		if err == nil {
			go io.Copy(io.Discard, conn)
		}
		return err
	})
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	h, err := LaunchSession(ctx, "test", "/bin/sh", t.TempDir(), []string{"-c", "test -t 0 && test -t 1 && test -t 2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-h.done
	if h.observed.ExitCode != 0 {
		t.Fatalf("native session is not a terminal: %+v", h.observed)
	}
}
