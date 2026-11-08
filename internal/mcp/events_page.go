package mcp

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

const defaultEventsPageLimit = 50

// eventsPage is the paged events_list answer: only events after the cursor
// that match the filter, so a Marshal reads deltas instead of the history.
type eventsPage struct {
	Events     []model.Event `json:"events"`
	NextCursor string        `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
}

// eventCursor orders events as the store lists them: timestamp, then ID.
func eventCursor(e model.Event) string {
	return strconv.FormatInt(e.Timestamp.UnixNano(), 10) + ":" + e.ID
}

func afterCursor(e model.Event, nanos int64, id string) bool {
	n := e.Timestamp.UnixNano()
	return n > nanos || (n == nanos && e.ID > id)
}

// pageEvents applies the optional events_list arguments. paged is false when
// none is given, so old callers keep the bare full array.
func pageEvents(events []model.Event, args map[string]any) (eventsPage, bool, error) {
	cursor, types, taskID := stringArg(args, "since_cursor"), stringArg(args, "types"), stringArg(args, "task_id")
	_, hasLimit := args["limit"]
	if cursor == "" && types == "" && taskID == "" && !hasLimit {
		return eventsPage{}, false, nil
	}
	limit := defaultEventsPageLimit
	if hasLimit {
		f, ok := args["limit"].(float64)
		if !ok || f < 1 || f > 1000 || f != math.Trunc(f) {
			return eventsPage{}, true, fmt.Errorf("%w: limit must be an integer from 1 to 1000", model.ErrInvalid)
		}
		limit = int(f)
	}
	var nanos int64 = math.MinInt64
	var lastID string
	if cursor != "" {
		head, id, ok := strings.Cut(cursor, ":")
		n, err := strconv.ParseInt(head, 10, 64)
		if !ok || err != nil || id == "" {
			return eventsPage{}, true, fmt.Errorf("%w: since_cursor is not a cursor returned by events_list", model.ErrInvalid)
		}
		nanos, lastID = n, id
	}
	allowed := map[string]bool{}
	for _, t := range strings.Split(types, ",") {
		if t = strings.TrimSpace(t); t != "" {
			allowed[t] = true
		}
	}
	page := eventsPage{Events: []model.Event{}, NextCursor: cursor}
	for _, e := range events {
		if cursor != "" && !afterCursor(e, nanos, lastID) {
			continue
		}
		if len(page.Events) == limit {
			page.HasMore = true
			break
		}
		// The cursor advances past filtered-out events too, so the next
		// call never rescans them.
		page.NextCursor = eventCursor(e)
		if (len(allowed) > 0 && !allowed[e.Type]) || (taskID != "" && e.TaskID != taskID) {
			continue
		}
		page.Events = append(page.Events, e)
	}
	return page, true, nil
}
