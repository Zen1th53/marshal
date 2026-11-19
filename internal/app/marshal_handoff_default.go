//go:build governed

package app

import "context"

func (r *Runtime) readHandoffContract(context.Context) (string, bool, string, bool) {
	return "", false, "", false
}
