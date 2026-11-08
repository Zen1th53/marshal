//go:build governed

package app

import "context"

func (*Runtime) requiredLegacyRunAdmission(context.Context, RunRequest) error { return nil }
