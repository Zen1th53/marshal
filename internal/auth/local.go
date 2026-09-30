package auth

import (
	"context"
	"fmt"
	"os"
	"syscall"
)

// LocalPrincipal is a value identity, separate from agent/token principals.
// Its fields and context key are private; command text cannot construct it.
type LocalPrincipal struct {
	uid     uint32
	project string
}

func (p LocalPrincipal) ID() string        { return fmt.Sprintf("local-uid:%d", p.uid) }
func (p LocalPrincipal) ProjectID() string { return p.project }

type localKey struct{}

// LocalOwner is a trusted in-process composition operation, never a protocol
// handler. UID ownership authenticates the account, not a human subprocess.
func LocalOwner(stateDir, project string) (LocalPrincipal, error) {
	info, err := os.Lstat(stateDir)
	if err != nil {
		return LocalPrincipal{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || st.Uid != uint32(os.Geteuid()) || project == "" {
		return LocalPrincipal{}, fmt.Errorf("local state owner authentication refused")
	}
	return LocalPrincipal{uid: st.Uid, project: project}, nil
}
func (p LocalPrincipal) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, localKey{}, p)
}
func LocalFromContext(ctx context.Context) (LocalPrincipal, bool) {
	p, ok := ctx.Value(localKey{}).(LocalPrincipal)
	return p, ok
}
