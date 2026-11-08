//go:build governed || !unix

package cli

import "context"

func withWorkerHost(ctx context.Context) context.Context { return ctx }
