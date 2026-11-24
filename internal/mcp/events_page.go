package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

const defaultEventsPageLimit = 50

// eventsPageMaxBytes bounds one encoded events_list page. A page that would
// pass it ends early with a cursor and a limit naming the next call.
const eventsPageMaxBytes = 64 << 10

// eventsPage is the paged events_list answer: only events after the cursor
// that match the filter, so a Marshal reads deltas instead of the history.
type eventsPage struct {
	Events     []model.Event `json:"events"`
	NextCursor string        `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
	// Limit is set when the byte bound, not the count, ended the page.
	Limit *model.Limit `json:"limit,omitempty"`
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
	var before []string // the cursor before each kept event
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
		prior := page.NextCursor
		page.NextCursor = eventCursor(e)
		if (len(allowed) > 0 && !allowed[e.Type]) || (taskID != "" && e.TaskID != taskID) {
			continue
		}
		page.Events = append(page.Events, e)
		before = append(before, prior)
		size := encodedEventsPage(page)
		if size <= eventsPageMaxBytes {
			continue
		}
		if len(page.Events) == 1 {
			return eventsPage{}, true, model.NewLimitError(
				fmt.Errorf("%w: event %s alone is larger than the %d-byte events page", model.ErrInvalid, e.ID, eventsPageMaxBytes),
				model.Limit{Kind: "event", Subject: "event " + e.ID, ActualBytes: int64(len(mustJSON(e))), CapBytes: eventsPageMaxBytes, Cursor: eventCursor(e),
					Action: fmt.Sprintf("Continue past it with since_cursor %q, or leave it out with types or task_id.", eventCursor(e))})
		}
		// The page fitted before this event: it ends there, and the cursor
		// goes back to just before it. The limit is left out when it alone
		// would not fit; has_more and next_cursor still lead on.
		last := len(page.Events) - 1
		page.Events, page.NextCursor, page.HasMore = page.Events[:last], before[last], true
		page.Limit = &model.Limit{Kind: "events_page", Subject: "events_list page", ActualBytes: int64(size), CapBytes: eventsPageMaxBytes, Limited: true, Cursor: page.NextCursor, Action: "Call events_list again with since_cursor set to next_cursor for the rest."}
		if kept := encodedEventsPage(page); kept <= eventsPageMaxBytes {
			page.Limit.KeptBytes = int64(kept)
		} else {
			page.Limit = nil
		}
		break
	}
	return page, true, nil
}

// encodedEventsPage measures a page as it travels: escaped as the text of
// the tool result.
func encodedEventsPage(page eventsPage) int { return len(mustJSON(string(mustJSON(page)))) }

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
