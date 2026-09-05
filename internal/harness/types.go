package harness

import "github.com/Zen1th53/marshal/internal/model"

type FeatureStatus = model.FeatureStatus
type HarnessProfile = model.HarnessProfile
type ULTRARouteRequest = model.ULTRARouteRequest
type ULTRARoutePlan = model.ULTRARoutePlan

const (
	StatusNative        = model.StatusNative
	StatusEmulated      = model.StatusEmulated
	StatusProbeRequired = model.StatusProbeRequired
	StatusUnsupported   = model.StatusUnsupported
)
