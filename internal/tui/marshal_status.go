package tui

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/model"
)

func (w *Workspace) marshalStatus(ctx context.Context) (string, error) {
	panel := w.marshalPanel()
	if panel == nil && w.runtime != nil {
		service := w.runtime.Marshal()
		if service != nil {
			id, record, err := service.Store.LatestMarshalRun(ctx, service.ProjectID)
			if err != nil && !errors.Is(err, model.ErrNotFound) {
				return "", err
			}
			if err == nil {
				panel = newMarshalPanel(id, "", record.Value, "stored Marshal run")
			}
		}
	}
	return marshalStatusText(panel), nil
}
