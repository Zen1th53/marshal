//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func checkOperatorMemoryPromotionRead(context.Context, *MemoryService, authz.Principal, model.MemoryRecordV2) error {
	return nil
}

func noteOperatorMemoryPromotionPersisted(context.Context, *MemoryService, authz.Principal, model.MemoryRecordV2) {
}

func indexOperatorMemoryPromotion(ctx context.Context, service *MemoryService, record model.MemoryRecordV2) error {
	return service.IndexRecord(ctx, record)
}
