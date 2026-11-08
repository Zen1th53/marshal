//go:build !governed

package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// Only the in-process single-use observation sink supplies these records.
// WorkerReported, request model selectors and stdout are never observations.
func privateRuntimeObservedModel(req Request, provider string, records []marshal.CommandRecord) (string, bool, error) {
	const prefix = "process05 provider-observation "
	const suffix = "\nRuntime lifecycle observation; paid inference intervals are not recorded."
	model, present := "", true
	lane, canonical := req.Task.Process05WaitRunID, ""
	for _, r := range records {
		if strings.HasPrefix(r.Command, "process05 ") && !strings.HasPrefix(r.Command, prefix) {
			candidate := strings.TrimPrefix(r.Command, "process05 ")
			if candidate == "" || (lane != "" && candidate != lane) {
				return "", false, fmt.Errorf("runtime import lane differs")
			}
			lane = candidate
		}
	}
	for _, record := range records {
		if !strings.HasPrefix(record.Command, prefix) {
			continue
		}
		if record.ExitCode != 0 || !strings.HasSuffix(record.Output, suffix) || len(record.Output) > 4096+len(suffix) {
			return "", false, fmt.Errorf("runtime model observation malformed")
		}
		var o execution.ProviderObservation
		d := json.NewDecoder(strings.NewReader(strings.TrimSuffix(record.Output, suffix)))
		d.DisallowUnknownFields()
		if err := d.Decode(&o); err != nil {
			return "", false, fmt.Errorf("runtime model observation malformed")
		}
		encoded, _ := json.Marshal(o)
		if !bytes.Equal(encoded, []byte(strings.TrimSuffix(record.Output, suffix))) {
			return "", false, fmt.Errorf("runtime model observation is not canonical")
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return "", false, fmt.Errorf("runtime model observation trailing data")
		}
		actualProvider := strings.TrimSuffix(strings.TrimSuffix(o.Provider, "-app-server"), "-stream")
		if execution.ValidateProviderObservation(&o) != nil || o.TaskID != req.Task.PlanTaskID || actualProvider != provider || o.RunID == "" || o.CanonicalTaskID == "" {
			return "", false, fmt.Errorf("runtime model observation differs from governed task")
		}
		if lane != "" && o.RunID != lane {
			return "", false, fmt.Errorf("runtime model observation lane differs")
		}
		lane = o.RunID
		if canonical != "" && o.CanonicalTaskID != canonical {
			return "", false, fmt.Errorf("runtime model observation canonical task differs")
		}
		canonical = o.CanonicalTaskID
		present = true
		if o.Model == "" {
			continue
		}
		if model != "" && model != o.Model {
			return "", false, fmt.Errorf("runtime model observations conflict")
		}
		model = o.Model
	}
	return model, present, nil
}

// Reserve precisely one additional bounded provenance slot for the complete
// supported private envelope: one import, sixteen observations, one provenance.
// Unclassified callback output keeps the Community count bound.
func privateRuntimeObservationCapacity(records []marshal.CommandRecord) int {
	if len(records) != 18 {
		return 17
	}
	imports, observations, provenance := 0, 0, 0
	for _, r := range records {
		switch {
		case strings.HasPrefix(r.Command, "process05 provider-observation "):
			observations++
		case strings.HasPrefix(r.Command, "runtime provenance process05 ") && len(r.Output) <= 8192:
			provenance++
		case strings.HasPrefix(r.Command, "process05 "):
			imports++
		default:
			return 17
		}
	}
	if imports == 1 && observations == 16 && provenance == 1 {
		return 18
	}
	return 17
}
