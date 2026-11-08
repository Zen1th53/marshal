//go:build governed

package driver

import "github.com/Zen1th53/marshal/internal/marshal"

func privateRuntimeObservedModel(Request, string, []marshal.CommandRecord) (string, bool, error) {
	return "", false, nil
}

func privateRuntimeObservationCapacity([]marshal.CommandRecord) int { return 17 }
