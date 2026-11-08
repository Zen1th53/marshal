//go:build governed

package app

import "context"

func buildPermissionAudit(_ context.Context, data map[string]any) map[string]any { return data }
