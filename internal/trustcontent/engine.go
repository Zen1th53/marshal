package trustcontent

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

type EngineConfig struct {
	Repository Repository
	Sanitizer  interface {
		SanitizeBytes(context.Context, []byte) ([]byte, error)
	}
	Authorizer Authorizer
	Detector   Detector
	EventStore events.Store
	Metrics    *evidence.MetricsRecorder
	MCPZone    Zone
	Now        func() time.Time
}

// Engine assigns transport-derived zones before persisting a bounded immutable
// projection. Content text is never an authority input.
type Engine struct {
	repository Repository
	sanitizer  interface {
		SanitizeBytes(context.Context, []byte) ([]byte, error)
	}
	authorizer Authorizer
	detector   Detector
	eventStore events.Store
	metrics    *evidence.MetricsRecorder
	mcpZone    Zone
	now        func() time.Time
}

func NewEngine(config EngineConfig) *Engine {
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if config.MCPZone == "" {
		config.MCPZone = TrustedTool
	}
	return &Engine{repository: config.Repository, sanitizer: config.Sanitizer, authorizer: config.Authorizer, detector: config.Detector, eventStore: config.EventStore, metrics: config.Metrics, mcpZone: config.MCPZone, now: now}
}

func (e *Engine) Ingest(ctx context.Context, request IngestRequest) (segment Segment, resultErr error) {
	started := time.Now()
	defer func() { e.observe(resultErr, time.Since(started)) }()
	if err := ctx.Err(); err != nil {
		return Segment{}, ErrRenderFailed
	}
	if e == nil || e.repository == nil || e.sanitizer == nil || e.authorizer == nil || !e.mcpZone.Valid() {
		return Segment{}, ErrUpgradeForbidden
	}
	if err := request.Validate(); err != nil {
		return Segment{}, err
	}
	zone, err := e.zoneFor(request.Source)
	if err != nil {
		return Segment{}, err
	}
	if err := e.authorizer.AuthorizeTrustContent(ctx, request, zone); err != nil {
		return Segment{}, ErrUpgradeForbidden
	}
	clean, err := e.sanitizer.SanitizeBytes(ctx, []byte(request.Content))
	if err != nil || string(clean) != request.Content {
		return Segment{}, ErrRenderFailed
	}
	record := Record{
		ID: request.ID, IdempotencyKey: request.IdempotencyKey, SourceID: request.SourceID,
		Zone: zone, Digest: Digest(request.Content), ContentRef: Digest(request.Content),
		State: StateIngested, CreatedAt: e.now().UTC(),
	}
	if err := record.Validate(); err != nil {
		return Segment{}, err
	}
	if existing, getErr := e.repository.GetTrustedContentSegment(ctx, record.ID); getErr == nil {
		if !sameRecord(existing, record) {
			return Segment{}, ErrUpgradeForbidden
		}
		record = existing
	} else if !errors.Is(getErr, model.ErrNotFound) {
		return Segment{}, ErrRenderFailed
	} else if err := e.repository.PutTrustedContentSegment(ctx, record); err != nil {
		return Segment{}, ErrRenderFailed
	}
	if record.State == StateIngested {
		if err := e.repository.TransitionTrustedContentSegment(ctx, record.ID, StateIngested, StateZoned); err != nil {
			latest, getErr := e.repository.GetTrustedContentSegment(ctx, record.ID)
			if getErr != nil || latest.State != StateZoned {
				return Segment{}, ErrRenderFailed
			}
			record = latest
		} else {
			record.State = StateZoned
		}
	}
	if record.State != StateZoned && record.State != StateRendered {
		return Segment{}, ErrRenderFailed
	}
	segment = Segment{Zone: record.Zone, SourceID: record.SourceID, Content: request.Content, Digest: record.Digest}
	suspected := false
	if e.detector != nil {
		suspected, err = e.detector.SuspectTrustContent(ctx, segment)
		if err != nil {
			return Segment{}, ErrRenderFailed
		}
	}
	if err := e.emitZonedEvents(ctx, request, record, suspected); err != nil {
		return Segment{}, ErrRenderFailed
	}
	return segment, nil
}

func (e *Engine) Render(ctx context.Context, request RenderRequest) (payload string, resultErr error) {
	started := time.Now()
	defer func() { e.observe(resultErr, time.Since(started)) }()
	if e == nil || e.sanitizer == nil {
		return "", ErrRenderFailed
	}
	for _, value := range []string{request.SubjectID, request.TaskID, request.RunID} {
		if value != "" && !safeIdentifier(value) {
			return "", ErrZoneInvalid
		}
	}
	if len(request.SegmentIDs) != 0 && len(request.SegmentIDs) != len(request.Segments) {
		return "", ErrZoneInvalid
	}
	for _, id := range request.SegmentIDs {
		if !safeIdentifier(id) {
			return "", ErrZoneInvalid
		}
	}
	payload, err := NewRenderer(e.sanitizer).Render(ctx, request.Segments)
	if err != nil {
		return "", err
	}
	for index, id := range request.SegmentIDs {
		if e.repository == nil {
			return "", ErrRenderFailed
		}
		record, err := e.repository.GetTrustedContentSegment(ctx, id)
		if err != nil || record.Zone != request.Segments[index].Zone || record.SourceID != request.Segments[index].SourceID || record.Digest != Digest(request.Segments[index].Content) {
			return "", ErrUpgradeForbidden
		}
		switch record.State {
		case StateRendered:
			continue
		case StateZoned:
			if err := e.repository.TransitionTrustedContentSegment(ctx, id, StateZoned, StateRendered); err != nil {
				latest, getErr := e.repository.GetTrustedContentSegment(ctx, id)
				if getErr != nil || latest.State != StateRendered {
					return "", ErrRenderFailed
				}
			}
		default:
			return "", ErrRenderFailed
		}
	}
	if e.eventStore == nil || request.SubjectID == "" {
		return payload, nil
	}
	digest := Digest(payload)
	_, err = e.eventStore.Append(ctx, events.Event{
		ID:             "TRUSTCONTENT-rendered-" + digest,
		Type:           events.EventTypeTrustContentRendered,
		Subject:        request.SubjectID,
		TaskID:         request.TaskID,
		RunID:          request.RunID,
		ResourceID:     "trustcontent-render-" + digest,
		At:             e.now().UTC(),
		Data:           map[string]any{"result": "rendered", "digest": digest, "segment_count": strconv.Itoa(len(request.Segments))},
		IdempotencyKey: "trustcontent.rendered/" + digest,
	})
	if err != nil {
		return "", ErrRenderFailed
	}
	return payload, nil
}

func (e *Engine) observe(err error, duration time.Duration) {
	if e == nil || e.metrics == nil {
		return
	}
	result := evidence.MetricResultSuccess
	reason := ""
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		result, reason = evidence.MetricResultCancelled, string(CodeRenderFailed)
	case errors.Is(err, ErrUpgradeForbidden):
		result, reason = evidence.MetricResultDenied, string(CodeUpgradeForbidden)
	case errors.Is(err, ErrZoneInvalid), errors.Is(err, ErrSegmentTooLarge):
		result, reason = evidence.MetricResultInvalid, string(CodeZoneInvalid)
		if errors.Is(err, ErrSegmentTooLarge) {
			reason = string(CodeSegmentTooLarge)
		}
	default:
		result, reason = evidence.MetricResultError, string(CodeRenderFailed)
	}
	e.metrics.Observe(evidence.MetricOperationTrustContent, result, reason, duration)
}

func (e *Engine) emitZonedEvents(ctx context.Context, request IngestRequest, record Record, suspected bool) error {
	if e.eventStore == nil || request.SubjectID == "" {
		return nil
	}
	items := []struct {
		typeName events.EventType
		result   string
	}{
		{events.EventTypeTrustContentSegmentIngested, "ingested"},
		{events.EventTypeTrustContentZoneAssigned, "zoned"},
	}
	if suspected {
		items = append(items, struct {
			typeName events.EventType
			result   string
		}{events.EventTypeTrustContentInjectionSuspected, "suspected"})
	}
	for _, item := range items {
		data := map[string]any{
			"segment_id": record.ID, "source_id": record.SourceID, "zone": string(record.Zone), "digest": record.Digest,
			"result": item.result,
		}
		event := events.Event{
			ID:             "TRUSTCONTENT-" + string(item.typeName) + "-" + record.ID,
			Type:           item.typeName,
			Subject:        request.SubjectID,
			TaskID:         request.TaskID,
			RunID:          request.RunID,
			ResourceID:     "trustcontent-" + record.ID,
			At:             e.now().UTC(),
			Data:           data,
			IdempotencyKey: string(item.typeName) + "/" + record.ID,
		}
		if _, err := e.eventStore.Append(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) zoneFor(source Source) (Zone, error) {
	switch source {
	case SourceSystem:
		return System, nil
	case SourceOwnerPolicy:
		return OwnerPolicy, nil
	case SourceProjectPolicy:
		return ProjectPolicy, nil
	case SourceTrustedTool:
		return TrustedTool, nil
	case SourceRepository:
		return RepositoryData, nil
	case SourceWeb:
		return WebData, nil
	case SourceMCP:
		return e.mcpZone, nil
	case SourceUntrusted:
		return UntrustedContent, nil
	default:
		return "", ErrZoneInvalid
	}
}

func sameRecord(left, right Record) bool {
	return left.ID == right.ID && left.IdempotencyKey == right.IdempotencyKey && left.SourceID == right.SourceID &&
		left.Zone == right.Zone && left.Digest == right.Digest && left.ContentRef == right.ContentRef
}
