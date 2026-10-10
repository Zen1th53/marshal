package tui

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/model"
)

func (w *Workspace) marshalStatus(ctx context.Context) (string, error) {
	panel := w.marshalPanel()
	if w.runtime != nil && (panel == nil || !w.runtime.IsLifecycleOwner()) {
		service := w.runtime.Marshal()
		if service != nil {
			id, record, err := service.Store.LatestMarshalRun(ctx, service.ProjectID)
			if err != nil && !errors.Is(err, model.ErrNotFound) {
				return "", err
			}
			if err == nil {
				provider := ""
				if panel != nil && panel.RunID == id {
					provider = panel.Provider
				}
				panel = newMarshalPanel(id, provider, record.Value, "stored Marshal run")
				report, err := service.CompletionReport(ctx, id)
				if err != nil {
					return "", err
				}
				panel.Usage, panel.Report = report.Usage, &report
			} else {
				panel = nil
			}
			w.setMarshalPanel(panel)
		}
	}
	return marshalStatusText(panel), nil
}
