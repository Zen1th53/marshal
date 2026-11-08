//go:build governed || !unix

package app

import "context"

func (r *Runtime) workerKnowledgeContext(context.Context, string, string, string) string { return "" }
