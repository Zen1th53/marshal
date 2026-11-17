package architecturereview

import (
	"encoding/json"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
)

// The drawing types and the generic layout live in architecturecodec; the
// review uses them as is.
type (
	Rect   = architecturecodec.Rect
	Node   = architecturecodec.Node
	Edge   = architecturecodec.Edge
	Layout = architecturecodec.Layout
)

// GenericLayouts places declared objects; it never supplies missing topology.
func GenericLayouts(proposal json.RawMessage) (map[string]Layout, error) {
	return architecturecodec.GenericLayouts(proposal)
}

// BindStoredLayouts validates the original persisted geometry and applies the
// same version binding used by Review.
func BindStoredLayouts(architecture, raw json.RawMessage) (map[string]Layout, map[string]Layout, string, error) {
	return architecturecodec.BindStoredLayouts(architecture, raw)
}

func validateLayouts(a map[string]any, views map[string]Layout) error {
	return architecturecodec.ValidateLayouts(a, views)
}

func bindTarget(views map[string]Layout, target map[string]any) (map[string]any, error) {
	return architecturecodec.BindTarget(views, target)
}

func selectionFor(a map[string]any, views map[string]Layout, target map[string]any) map[string]any {
	return architecturecodec.SelectionFor(a, views, target)
}
