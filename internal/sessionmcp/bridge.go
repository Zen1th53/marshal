//go:build unix

package sessionmcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
	"golang.org/x/sys/unix"
)

type bridge struct {
	agyWorktree                              string
	agyFiles                                 bool
	agyMask                                  model.WorkerMCPMask
	dir, binary, requests, responses, config string
	requestFile, responseFile                *os.File
	env                                      []string
	cancel                                   context.CancelFunc
	done                                     chan struct{}
	once                                     sync.Once
}

func (b *bridge) closeFiles() {
	request, response := b.requestFile, b.responseFile
	if request != nil {
		_ = request.Close()
	}
	if response != nil {
		_ = response.Close()
	}
}

func startBridge(ctx context.Context, root, task, binary, lease string, servers ...WorkerServer) (*bridge, error) {
	dir, err := os.MkdirTemp("", "marshal-worker-mcp-")
	if err != nil {
		return nil, err
	}
	b := &bridge{dir: dir, binary: binary, requests: filepath.Join(dir, "requests"), responses: filepath.Join(dir, "responses"), done: make(chan struct{})}
	fail := func(err error) (*bridge, error) {
		if b.requestFile != nil {
			b.requestFile.Close()
		}
		if b.responseFile != nil {
			b.responseFile.Close()
		}
		os.RemoveAll(dir)
		return nil, err
	}
	for _, path := range []string{b.requests, b.responses} {
		if err := unix.Mkfifo(path, 0600); err != nil {
			return fail(err)
		}
	}
	// Keep one reader for the entire session: writers can open immediately,
	// and their final close (including empty stdin) remains observable as EOF.
	b.requestFile, err = os.OpenFile(b.requests, os.O_RDONLY|unix.O_NONBLOCK, 0600)
	if err != nil {
		return fail(err)
	}
	// Hold the response writer until the child exits or empty stdin is observed.
	b.responseFile, err = os.OpenFile(b.responses, os.O_RDWR, 0600)
	if err != nil {
		return fail(err)
	}
	request, response := b.requestFile, b.responseFile
	requestFD := int(request.Fd())
	runCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	go func() { <-runCtx.Done(); b.closeFiles() }()
	go func() {
		defer close(b.done)
		defer response.Close()
		defer request.Close()
		first := make([]byte, 64*1024)
		n, err := waitRead(runCtx, requestFD, first)
		if err != nil || n == 0 {
			return
		}
		// The worker cannot substitute a role, project, or canonical task. Only JSON-RPC
		// bytes cross the IPC channel; all host process arguments are bound here.
		var stdin io.WriteCloser
		var wait func()
		if len(servers) > 0 && servers[0] != nil {
			// The relay never receives a principal, runtime handle or host
			// configuration. Its JSON-RPC reaches only this captured server.
			reader, writer := io.Pipe()
			stdin = writer
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer reader.Close()
				_ = servers[0](runCtx, reader, response)
			}()
			wait = func() { <-finished }
		} else {
			// Lease-bound read-only knowledge transport: the child never receives
			// a principal or a privileged Marshal fallback.
			cmd := exec.CommandContext(runCtx, binary, "mcp", "serve", "--stdio", "--project", root, "--role", "worker", "--task", task, "--lease", lease)
			var err error
			stdin, err = cmd.StdinPipe()
			if err != nil {
				return
			}
			cmd.Stdout = response
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				stdin.Close()
				return
			}
			wait = func() { _ = cmd.Wait() }
		}
		copied := make(chan struct{})
		go func() {
			defer close(copied)
			defer stdin.Close()
			buffer := first
			for {
				if _, err := stdin.Write(buffer[:n]); err != nil {
					return
				}
				n, err = waitRead(runCtx, requestFD, buffer)
				if err != nil || n == 0 {
					return
				}
			}
		}()
		wait()
		// Stop a pending FIFO read if the child exits before provider stdin.
		cancel()
		<-copied
	}()
	go func() { <-ctx.Done(); b.close() }()
	return b, nil
}

func (b *bridge) close() {
	b.once.Do(func() { b.cancel(); b.closeFiles(); <-b.done; _ = os.RemoveAll(b.dir) })
}

// SandboxRequest adds only the generated IPC endpoints/config and relay executable.
// It never exposes the owning project or changes socket confinement.
func SandboxRequest(ctx context.Context, request model.SandboxRequest) (model.SandboxRequest, error) {
	s, _ := ctx.Value(key{}).(*scope)
	if s == nil {
		if !request.WorkerMCPMask.Empty() {
			return model.SandboxRequest{}, fmt.Errorf("%w: AGY configuration requires its invocation context", model.ErrConflict)
		}
		return request, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bridge
	if !request.WorkerMCPMask.Empty() && (b == nil || !b.agyFiles) {
		return model.SandboxRequest{}, fmt.Errorf("%w: foreign provider AGY configuration", model.ErrConflict)
	}
	if b == nil {
		return request, nil
	}
	request.ReadOnlyBinds = append([]model.Bind(nil), request.ReadOnlyBinds...)
	if b.requests != "" {
		request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: b.binary, Target: b.binary}, model.Bind{Source: b.requests, Target: b.requests}, model.Bind{Source: b.responses, Target: b.responses})
	}
	if b.agyFiles {
		var err error
		request, err = antigravitySandboxConfiguration(b, request)
		if err != nil {
			return model.SandboxRequest{}, err
		}
	}
	if b.config != "" {
		request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: b.config, Target: b.config})
	}
	if len(b.env) != 0 {
		env, err := openCodeSandboxEnvironment(request.ExtraEnv, b.env)
		if err != nil {
			return model.SandboxRequest{}, err
		}
		request.ExtraEnv = env
	}
	return request, nil
}

// Relay uses named pipes because supervised workers intentionally cannot create
// Unix sockets. The host child enforces the read-only task/project API.
func Relay(ctx context.Context, requests, responses string, in io.Reader, out io.Writer) error {
	source, ok := in.(io.ReadCloser)
	if !ok {
		return fmt.Errorf("MCP relay requires cancellable stdin")
	}
	for _, path := range []string{requests, responses} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeNamedPipe == 0 {
			return fmt.Errorf("MCP relay endpoint is not a named pipe")
		}
	}
	input, err := os.OpenFile(requests, os.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := unix.Flock(int(input.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("MCP relay already active: %w", err)
	}
	output, err := os.OpenFile(responses, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer output.Close()
	outputFD := int(output.Fd())
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			source.Close()
			input.Close()
			output.Close()
			if closer, ok := out.(io.Closer); ok {
				closer.Close()
			}
		case <-done:
		}
	}()
	defer close(done)
	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(input, source)
		// Publish completion before closing the writer: response EOF can follow
		// immediately, and must never outrun the successful copy result.
		copied <- err
		// Closing the FIFO writer at stdin EOF is required for the host MCP
		// process to observe EOF and terminate its stdio session.
		_ = input.Close()
		if err != nil {
			output.Close()
		}
	}()
	var readErr error
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			readErr = ctx.Err()
		default:
		}
		if readErr != nil {
			break
		}
		poll := []unix.PollFd{{Fd: int32(outputFD), Events: unix.POLLIN | unix.POLLHUP}}
		_, err := unix.Poll(poll, 100)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			readErr = err
			break
		}
		if poll[0].Revents == 0 {
			continue
		}
		n, err := unix.Read(outputFD, buffer)
		if n > 0 {
			if _, writeErr := out.Write(buffer[:n]); writeErr != nil {
				readErr = writeErr
				break
			}
		}
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			readErr = err
			break
		}
		if n == 0 {
			break
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var inputErr error
	select {
	case inputErr = <-copied:
	default:
		source.Close()
		input.Close()
		inputErr = <-copied
		if readErr == nil {
			readErr = fmt.Errorf("MCP host transport closed before stdin")
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil {
		return readErr
	}
	return inputErr
}

func waitRead(ctx context.Context, fd int, buffer []byte) (int, error) {
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLHUP}}
		_, err := unix.Poll(poll, 100)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			return 0, err
		}
		if poll[0].Revents == 0 {
			continue
		}
		n, err := unix.Read(fd, buffer)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		return n, err
	}
}
