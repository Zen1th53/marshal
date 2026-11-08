package tui

import (
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

type marshalPopupFixture struct {
	w       *Workspace
	runtime *app.Runtime
	service *app.MarshalService
	run     marshal.Run
	pack    *marshal.PlanPack
	dir     string
}
