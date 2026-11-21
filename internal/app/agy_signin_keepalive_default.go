//go:build governed

package app

import "context"

// startAgySigninKeepaliveLoop is a no-op in the default build.
func (r *Runtime) startAgySigninKeepaliveLoop(context.Context) {}
