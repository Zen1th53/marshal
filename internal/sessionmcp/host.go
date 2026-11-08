package sessionmcp

import (
	"context"
	"io"
)

// WorkerHostSpec is created by the admitting Runtime, never by relay messages.
// Runtime is opaque here to keep the transport independent of the app package.
type WorkerHostSpec struct {
	Runtime                   any
	Root, TaskID, PrincipalID string
	Validate                  func(context.Context) error
	RequestContext            func(context.Context) context.Context
}
type WorkerServer func(context.Context, io.Reader, io.Writer) error
type WorkerServerFactory func(WorkerHostSpec) (WorkerServer, error)
type factoryKey struct{}

// The factory is scoped to this host launch; no global mutable registry exists.
func WithWorkerServerFactory(ctx context.Context, factory WorkerServerFactory) context.Context {
	return context.WithValue(ctx, factoryKey{}, factory)
}
func WorkerServerFactoryFrom(ctx context.Context) WorkerServerFactory {
	factory, _ := ctx.Value(factoryKey{}).(WorkerServerFactory)
	return factory
}
