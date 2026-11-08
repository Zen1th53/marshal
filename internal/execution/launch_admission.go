package execution

import "context"

type workerAdmissionKey struct{}
type workerAcknowledgementKey struct{}

// WithWorkerLaunchAdmission defers admission until preparation is complete.
// The backend must retain the returned release through acknowledged launch.
func WithWorkerLaunchAdmission(ctx context.Context, begin func(context.Context) (func(), error)) context.Context {
	return context.WithValue(ctx, workerAdmissionKey{}, begin)
}

func HasWorkerLaunchAdmission(ctx context.Context) bool {
	_, ok := ctx.Value(workerAdmissionKey{}).(func(context.Context) (func(), error))
	return ok
}

func BeginWorkerLaunch(ctx context.Context) (func(), error) {
	if begin, ok := ctx.Value(workerAdmissionKey{}).(func(context.Context) (func(), error)); ok {
		return begin(ctx)
	}
	return func() {}, nil
}

// WithWorkerLaunchAcknowledgement carries a backend's launch acknowledgement.
func WithWorkerLaunchAcknowledgement(ctx context.Context, acknowledge func()) context.Context {
	return context.WithValue(ctx, workerAcknowledgementKey{}, acknowledge)
}

func AcknowledgeWorkerLaunch(ctx context.Context) {
	if ack, ok := ctx.Value(workerAcknowledgementKey{}).(func()); ok {
		ack()
	}
}

// DeferredLaunchHarness prepares outside admission and delegates admission to
// its actual backend. Other harnesses are conservatively protected through
// Execute's return, or an explicit AcknowledgeWorkerLaunch from the backend.
type DeferredLaunchHarness interface{ DefersWorkerLaunch() bool }
