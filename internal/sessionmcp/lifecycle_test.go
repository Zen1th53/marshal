//go:build unix

package sessionmcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerMCPBridgeCleanup(t *testing.T) {
	for _, scenario := range []string{"success", "startup-failure", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "server")
			mode := os.FileMode(0700)
			if scenario == "startup-failure" {
				mode = 0600
			}
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nwhile IFS= read -r line; do printf '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\\n'; done\n"), mode); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, err := startBridge(ctx, "/own-project", "TASK-own", binary, "LEASE-fixture")
			if err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(b.dir); err != nil || info.Mode().Perm() != 0700 {
				t.Fatal("bridge directory must be private")
			}
			for _, path := range []string{b.requests, b.responses} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 || info.Mode()&os.ModeNamedPipe == 0 {
					t.Fatal("bridge endpoints must be private FIFOs")
				}
			}
			if scenario == "cancel" {
				cancel()
			} else {
				requestFile, err := os.OpenFile(b.requests, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer requestFile.Close()
				// Trigger the lazily started immutable host process, then close regardless
				// of child start success. Cleanup must unblock every host read/copy path.
				if _, err := requestFile.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")); err != nil {
					t.Fatal(err)
				}
				if scenario == "success" {
					buffer := make([]byte, 1024)
					if _, err := b.responseFile.Read(buffer); err != nil {
						t.Fatal(err)
					}
				} else {
					select {
					case <-b.done:
					case <-time.After(3 * time.Second):
						t.Fatal("failed startup retained host reader")
					}
				}
				b.close()
			}
			deadline := time.After(3 * time.Second)
			for {
				if _, err := os.Stat(b.dir); os.IsNotExist(err) {
					break
				}
				select {
				case <-deadline:
					t.Fatal("bridge artifacts survived cleanup")
				case <-time.After(10 * time.Millisecond):
				}
			}
			// Repeated cleanup must be safe and bounded after cancellation.
			b.close()
		})
	}
}

func TestWorkerMCPBridgeNaturalStdinEOF(t *testing.T) {
	for _, input := range []string{"", "ping\n"} {
		name := "natural"
		if input == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			// Repeated sessions exercise completion ordering when EOF and process
			// exit are ready together. Empty input must not launch the host child.
			for i := 0; i < 30; i++ {
				binary := filepath.Join(t.TempDir(), "server")
				program := "#!/bin/sh\nwhile IFS= read -r line; do :; done\nprintf '%s\\n' '{\"closed\":true}'\n"
				if err := os.WriteFile(binary, []byte(program), 0700); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				b, err := startBridge(ctx, "/own-project", "TASK-own", binary, "LEASE-fixture")
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				var response bytes.Buffer
				err = Relay(ctx, b.requests, b.responses, io.NopCloser(strings.NewReader(input)), &response)
				b.close()
				cancel()
				if err != nil {
					t.Fatalf("session %d did not finish after stdin EOF: %v", i, err)
				}
				want := ""
				if input != "" {
					want = "{\"closed\":true}\n"
				}
				if got := response.String(); got != want {
					t.Fatalf("session %d response = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestWorkerMCPBridgeMultiWriteStreaming(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nwhile IFS= read -r line; do printf '%s\\n' \"$line\"; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := startBridge(ctx, "/own-project", "TASK-own", binary, "LEASE-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	input, writer := io.Pipe()
	output, sink := io.Pipe()
	defer input.Close()
	defer writer.Close()
	defer output.Close()
	defer sink.Close()
	relayDone := make(chan error, 1)
	go func() {
		err := Relay(ctx, b.requests, b.responses, input, sink)
		input.CloseWithError(err)
		sink.CloseWithError(err)
		relayDone <- err
	}()
	lines := bufio.NewReader(output)
	// The first write starts the child; subsequent writes must use the same
	// live FIFO reader. Each reply proves forwarding occurs before stdin EOF.
	for i := 0; i < 128; i++ {
		size := 4096
		if i == 0 {
			size = 128 * 1024 // Exceed both FIFO capacity and the host read buffer.
		}
		want := fmt.Sprintf("%d:%s\n", i, strings.Repeat("x", size))
		if _, err := io.WriteString(writer, want); err != nil {
			t.Fatal(err)
		}
		got, err := lines.ReadString('\n')
		if err != nil || got != want {
			t.Fatalf("write %d: response length %d, err %v", i, len(got), err)
		}
	}
	writer.Close()
	if _, err := lines.ReadByte(); err != io.EOF {
		t.Fatalf("response did not end naturally: %v", err)
	}
	if err := <-relayDone; err != nil {
		t.Fatalf("streaming relay: %v", err)
	}
}
