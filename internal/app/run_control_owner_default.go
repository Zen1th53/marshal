//go:build governed

package app

func admitRunControlLifecycle(*Runtime) (func(), error) { return func() {}, nil }
