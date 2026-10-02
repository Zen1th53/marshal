package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/model"
)

// ArtifactDetail is a stored artifact with a fresh check of its bytes. It is a
// read: inspecting evidence never promotes or changes it.
type ArtifactDetail struct {
	Artifact model.Artifact
	Payload  string
	// PayloadErr explains an UNREADABLE payload.
	PayloadErr string
}

func (r *Runtime) ArtifactDetail(ctx context.Context, id string) (ArtifactDetail, error) {
	if r == nil || r.store == nil {
		return ArtifactDetail{}, model.ErrUnavailable
	}
	a, err := r.store.GetArtifact(ctx, id)
	if err != nil {
		return ArtifactDetail{}, err
	}
	detail := ArtifactDetail{Artifact: a}
	detail.Payload, err = artifact.Verify(r.layout.Artifacts, a)
	if err != nil {
		detail.PayloadErr = err.Error()
	}
	return detail, nil
}
