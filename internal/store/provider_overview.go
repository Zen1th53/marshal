package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// ProviderStatus represents the status of a model provider for the MARSHAL TUI Providers view.
type ProviderStatus struct {
	Provider        string
	Model           string
	VerifiedAt      time.Time
	ExpiresAt       time.Time
	Expired         bool
	LastLimitAt     time.Time
	LastLimitReason string
	// LimitUntil is when the newest cooldown says the provider takes work
	// again; zero when the newest limit did not say.
	LimitUntil  time.Time
	ActiveLanes int
}

// providerLimitEventTypes names the structured event types indicating provider limits/quota/cooldown.
// If the runtime records additional structured limit events in structured_events, include them here.
var providerLimitEventTypes = []string{
	string(events.EventTypeMarshalEscalated),
}

// providerLimitMarkers are the words an escalation reason carries when a
// provider limit or sign-in, not the task, stopped the work.
var providerLimitMarkers = []string{"usage limit", "rate limit", "quota", "authentication required", "cooldown"}

func isProviderLimitReason(reason string) bool {
	r := strings.ToLower(reason)
	for _, m := range providerLimitMarkers {
		if strings.Contains(r, m) {
			return true
		}
	}
	return false
}

// ProviderOverview returns an overview of all providers known from harness profiles and the newest marshal run.
func (s *Store) ProviderOverview(ctx context.Context, projectID string, now time.Time) ([]ProviderStatus, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store database is not configured")
	}

	// 1. Query harness profiles
	rows, err := s.db.QueryContext(ctx, `
		SELECT harness_name, default_model, probed_at, expires_at
		FROM harness_profiles
	`)
	if err != nil {
		return nil, fmt.Errorf("query harness profiles: %w", err)
	}
	defer rows.Close()

	providers := make(map[string]*ProviderStatus)

	for rows.Next() {
		var (
			harnessName  string
			defaultModel string
			probedAtStr  string
			expiresAtStr string
		)
		if err := rows.Scan(&harnessName, &defaultModel, &probedAtStr, &expiresAtStr); err != nil {
			return nil, fmt.Errorf("scan harness profile: %w", err)
		}

		var verifiedAt, expiresAt time.Time
		if probedAtStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, probedAtStr); err == nil {
				verifiedAt = t
			} else if t, err := time.Parse(time.RFC3339, probedAtStr); err == nil {
				verifiedAt = t
			}
		}

		if expiresAtStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, expiresAtStr); err == nil {
				expiresAt = t
			} else if t, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
				expiresAt = t
			}
		}

		prof := model.HarnessProfile{
			Harness:      harnessName,
			DefaultModel: defaultModel,
			ProbedAt:     verifiedAt,
			ExpiresAt:    expiresAt,
		}

		providers[harnessName] = &ProviderStatus{
			Provider:   harnessName,
			Model:      defaultModel,
			VerifiedAt: verifiedAt,
			ExpiresAt:  expiresAt,
			Expired:    !prof.IsFresh(now),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate harness profiles: %w", err)
	}

	// 2. Count active lanes from the newest marshal run of the project
	_, runRec, err := s.LatestMarshalRun(ctx, projectID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, fmt.Errorf("get latest marshal run: %w", err)
	}

	if err == nil {
		for _, task := range runRec.Value.Tasks {
			worker := strings.TrimSpace(task.Worker)
			if worker == "" {
				continue
			}

			// A lane runs only while its task is dispatched; a handed-in,
			// returned or escalated task holds no worker.
			isActive := task.State == marshal.Dispatched

			if isActive {
				st, ok := providers[worker]
				if !ok {
					st = &ProviderStatus{
						Provider: worker,
					}
					providers[worker] = st
				}
				st.ActiveLanes++
			} else {
				if _, ok := providers[worker]; !ok {
					providers[worker] = &ProviderStatus{
						Provider: worker,
					}
				}
			}
		}
	}

	// 3. Limit events from structured_events using the (event_type, sequence) index
	if len(providerLimitEventTypes) > 0 {
		placeholders := make([]string, len(providerLimitEventTypes))
		args := make([]any, len(providerLimitEventTypes))
		for i, et := range providerLimitEventTypes {
			placeholders[i] = "?"
			args[i] = et
		}

		// Read only the newest few events matching the limit event types using idx_type_sequence
		limitQuery := fmt.Sprintf(`
			SELECT event_type, subject, at, data_json
			FROM structured_events INDEXED BY structured_events_by_type_sequence
			WHERE event_type IN (%s)
			ORDER BY sequence DESC
			LIMIT 50
		`, strings.Join(placeholders, ","))

		eventRows, err := s.db.QueryContext(ctx, limitQuery, args...)
		if err != nil {
			return nil, err
		}
		{
			defer eventRows.Close()
			for eventRows.Next() {
				var (
					eventType   string
					subject     string
					atStr       string
					dataJSONStr string
				)
				if err := eventRows.Scan(&eventType, &subject, &atStr, &dataJSONStr); err != nil {
					continue
				}

				providerKey := strings.TrimSpace(subject)
				var data map[string]any
				if dataJSONStr != "" {
					_ = json.Unmarshal([]byte(dataJSONStr), &data)
				}
				if p, ok := data["provider"].(string); ok && p != "" {
					providerKey = p
				}

				st, exists := providers[providerKey]
				if !exists {
					continue
				}

				// Only record the newest event per provider
				if !st.LastLimitAt.IsZero() {
					continue
				}

				var eventAt time.Time
				if atStr != "" {
					if t, err := time.Parse(time.RFC3339Nano, atStr); err == nil {
						eventAt = t
					} else if t, err := time.Parse(time.RFC3339, atStr); err == nil {
						eventAt = t
					}
				}

				reason := ""
				if r, ok := data["reason"].(string); ok {
					reason = r
				} else if r, ok := data["error"].(string); ok {
					reason = r
				} else if r, ok := data["message"].(string); ok {
					reason = r
				}

				if !isProviderLimitReason(reason) {
					continue
				}
				st.LastLimitAt = eventAt
				st.LastLimitReason = reason
			}
		}
	}

	// Provider cooldowns are runtime observations kept in audit_events.
	// A cooldown newer than the escalation found above replaces it.
	cooldowns, err := s.db.QueryContext(ctx, `
		SELECT timestamp, data_json FROM audit_events
		WHERE event_type = 'provider.cooldown'
		ORDER BY rowid DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer cooldowns.Close()
	for cooldowns.Next() {
		var atStr, raw string
		if err := cooldowns.Scan(&atStr, &raw); err != nil {
			return nil, err
		}
		var data map[string]any
		_ = json.Unmarshal([]byte(raw), &data)
		name, _ := data["provider"].(string)
		st, ok := providers[name]
		if !ok {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, atStr)
		if err != nil || !at.After(st.LastLimitAt) {
			continue
		}
		reason, _ := data["reason"].(string)
		if reason == "" {
			reason = "provider cooldown"
		}
		st.LastLimitAt, st.LastLimitReason, st.LimitUntil = at, reason, time.Time{}
		if reset, _ := data["reset_time"].(string); reset != "" {
			if until, err := time.Parse(time.RFC3339Nano, reset); err == nil && until.After(at) {
				st.LimitUntil = until
			}
		}
	}
	if err := cooldowns.Err(); err != nil {
		return nil, err
	}

	// 4. Sort results by provider name
	result := make([]ProviderStatus, 0, len(providers))
	for _, status := range providers {
		result = append(result, *status)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Provider < result[j].Provider
	})

	return result, nil
}
