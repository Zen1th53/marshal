package app

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// The agy Marshal's first architecture used package paths as IDs; submit
// took it and only the owner's y failed. Submit now refuses it with the fix.
func TestContractMustBeDrawableToSubmit(t *testing.T) {
	good := projectcontext.Contract{ID: "fixes", Revision: 1, Purpose: "Fix the review page", Components: []projectcontext.Component{
		{ID: "architecturereview", Responsibility: "page", Paths: []string{"internal/architecturereview"}, Dependencies: []string{"architecturecodec"}},
		{ID: "architecturecodec", Responsibility: "layout", Paths: []string{"internal/architecturecodec"}},
	}}
	if err := ValidateContractDrawable(good); err != nil {
		t.Fatalf("a drawable contract was refused: %v", err)
	}
	bad := good
	bad.Components = []projectcontext.Component{
		{ID: "internal/architecturereview", Responsibility: "page", Paths: []string{"internal/architecturereview"}},
	}
	err := ValidateContractDrawable(bad)
	if err == nil || !strings.Contains(err.Error(), "exact and unique") || !strings.Contains(err.Error(), "no '/'") {
		t.Fatalf("a path used as an id was not refused with the fix: %v", err)
	}
}
