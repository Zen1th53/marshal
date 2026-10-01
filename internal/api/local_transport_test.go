//go:build linux

package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"golang.org/x/sys/unix"
)

func TestSocketOwnerRejectsInsecureOrMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if uid, err := socketOwner(dir); err != nil || uid != uint32(os.Geteuid()) {
		t.Fatalf("owner: %d %v", uid, err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := socketOwner(dir); err == nil {
		t.Fatal("insecure directory accepted")
	}
	if _, err := socketOwner(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestKernelPeerUIDSocketPair(t *testing.T) {
	// Check real kernel credentials without a listener or a second OS user.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Skipf("local socketpair unavailable: %v", err)
	}
	files := [2]*os.File{os.NewFile(uintptr(fds[0]), "peer-a"), os.NewFile(uintptr(fds[1]), "peer-b")}
	defer files[0].Close()
	defer files[1].Close()
	conn, err := net.FileConn(files[0])
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			t.Skipf("kernel socket operations denied by execution sandbox: %v", err)
		}
		t.Fatal(err)
	}
	defer conn.Close()
	uid, err := kernelPeerUID(conn)
	if err != nil || uid != uint32(os.Geteuid()) {
		t.Fatalf("kernel peer UID: %d %v", uid, err)
	}
	conn.Close()
	if _, err := kernelPeerUID(conn); err == nil {
		t.Fatal("closed connection accepted")
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if _, err := kernelPeerUID(left); err == nil {
		t.Fatal("non-Unix connection accepted")
	}
}

func TestLocalTransportPeerAndWorkerBoundary(t *testing.T) {
	owner := uint32(os.Geteuid())
	for _, tc := range []struct {
		name, method string
		peer         peerIdentity
		hasPeer      bool
		want         int
	}{
		{"no credentials", "GET", peerIdentity{}, false, 403},
		{"failed credentials", "GET", peerIdentity{uid: owner}, true, 403},
		{"different UID", "GET", peerIdentity{uid: owner + 1, valid: true}, true, 403},
		{"owner read", "GET", peerIdentity{uid: owner, valid: true}, true, 204},
		{"owner worker protocol write", "POST", peerIdentity{uid: owner, valid: true}, true, 204},
		{"different UID write", "POST", peerIdentity{uid: owner + 1, valid: true}, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/v1/goals/x/revise", nil)
			if tc.hasPeer {
				request = request.WithContext(context.WithValue(request.Context(), peerKey{}, tc.peer))
			}
			response := httptest.NewRecorder()
			server := &Server{}
			server.localTransport(owner, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status: %d; %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestUnixServerDifferentUIDRefused(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			runtime, socket := apiRuntime(t)
			server := NewServer(runtime)
			server.peerUID = func(net.Conn) (uint32, error) {
				if failed {
					return 0, fmt.Errorf("peer credentials unavailable")
				}
				return uint32(os.Geteuid()) + 1, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() { done <- server.Serve(ctx, socket) }()
			waitForSocket(t, socket, done)
			if _, _, err := NewClient(socket).Status(context.Background()); !errors.Is(err, model.ErrPolicyDenied) {
				t.Fatalf("peer accepted: %v", err)
			}
		})
	}
}

// The socket authenticates an OS account, not the operator, so operator
// commands must never be routed here: they exist only in-process.
func TestSocketExposesNoOperatorCommands(t *testing.T) {
	routes := (&Server{}).routes()
	for _, path := range []string{"/v1/goals", "/v1/goals/x", "/v1/goals/x/revise", "/v1/control", "/v1/commands"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			response := httptest.NewRecorder()
			routes.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s is routed on the socket: %d", method, path, response.Code)
			}
		}
	}
}
