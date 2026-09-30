package api

import (
	"fmt"
	"net/http"
	"os"
	"syscall"

	"github.com/Zen1th53/marshal/internal/model"
)

type peerKey struct{}
type peerIdentity struct {
	uid   uint32
	valid bool
}

func socketOwner(dir string) (uint32, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || st.Uid != uint32(os.Geteuid()) {
		return 0, fmt.Errorf("local socket owner authentication refused")
	}
	return st.Uid, nil
}

func (s *Server) localTransport(owner uint32, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, ok := r.Context().Value(peerKey{}).(peerIdentity)
		if !ok || !peer.valid || peer.uid != owner {
			writeError(w, "", fmt.Errorf("%w: local socket peer authentication refused", model.ErrPolicyDenied))
			return
		}
		// Matching UID proves an OS account, not an operator: it cannot tell
		// the operator from a same-UID worker. The socket therefore carries the
		// existing worker protocol only; operator commands (LocalControl) are
		// never exposed here and run in the trusted in-process workspace.
		next.ServeHTTP(w, r)
	})
}
