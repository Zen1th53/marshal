//go:build !governed

package app

import (
	"fmt"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"reflect"
)

func configureRuntimeHarnessAdmission(r *Runtime, cfg *execution.EngineConfig, owned []execution.WorkerHarness) {
	// This detached list is captured before Engine construction and never exposed.
	allowed := append([]execution.WorkerHarness(nil), owned...)
	cfg.HarnessAdmission = func(h execution.WorkerHarness) error {
		for depth := 0; depth < 8; depth++ {
			wrapper, ok := h.(*taskSupervisedHarness)
			if !ok {
				break
			}
			if wrapper == nil || wrapper.runtime != r {
				return projectcontext.ErrReviewPending
			}
			h = wrapper.inner
		}
		if h != nil {
			value := reflect.ValueOf(h)
			if value.Kind() == reflect.Pointer && !value.IsNil() {
				for _, actual := range allowed {
					if h == actual {
						return nil
					}
				}
			}
		}
		return fmt.Errorf("%w: this Runtime harness cannot deliver mandatory project context", projectcontext.ErrReviewPending)
	}
}

// A foreign preconfigured engine was constructed without this Runtime's fixed
// guard. Do not install provider callbacks or manufacture Runtime ownership.
// An already composed canonical service can be reused without registration.
func privatePreconfiguredExecutionService(r *Runtime, engine *execution.Engine) (*ExecutionService, bool) {
	denied := func() *ExecutionService {
		return &ExecutionService{runtime: r, initErr: projectcontext.ErrReviewPending}
	}
	if r == nil || engine == nil {
		return denied(), true
	}
	r.execMu.Lock()
	defer r.execMu.Unlock()
	if r.execService == nil || r.execService.initErr != nil || r.execService.engine != engine {
		return denied(), true
	}
	return r.execService, true
}
