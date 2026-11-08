//go:build governed || !unix

package sandbox

import "github.com/Zen1th53/marshal/internal/model"

func workerProjectCacheMounts(model.SandboxRequest, bool) ([]string, string, error) {
	return nil, "", nil
}
