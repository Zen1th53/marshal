//go:build governed

package app

import "context"

// keepAgySigninFresh is a no-op in the default build: Community never refreshes
// a provider sign-in or changes provider availability automatically.
func (r *Runtime) keepAgySigninFresh(context.Context) {}

// refreshAgySigninBeforeDispatch is a no-op in the default build.
func (r *Runtime) refreshAgySigninBeforeDispatch(context.Context) {}

// startAgySigninKeepaliveLoop is a no-op in the default build.
func (r *Runtime) startAgySigninKeepaliveLoop(context.Context) {}
