//go:build !governed && unix

package sessionmcp

import (
	"bufio"
	"context"
	"io"
	"os"
	"testing"
	"time"
)

func TestWorkerMCPBoundHostCancellationCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	b, err := startBridge(ctx, "/host-owned", "TASK-host", "/unused-server", "", func(ctx context.Context, in io.Reader, out io.Writer) error {
		scanner := bufio.NewScanner(in)
		if scanner.Scan() {
			close(started)
		}
		for scanner.Scan() {
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(b.requests, os.O_WRONLY, 0)
	if err != nil {
		b.close()
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Write([]byte("{}\n")); err != nil {
		b.close()
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		b.close()
		t.Fatal("bound host did not start")
	}
	cancel()
	done := make(chan struct{})
	go func() { b.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bound host pipe/server remained live after cancellation")
	}
	if _, err := os.Stat(b.dir); !os.IsNotExist(err) {
		t.Fatal("bound host artifacts survived owned invocation")
	}
}
