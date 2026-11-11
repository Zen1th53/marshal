//go:build !governed

package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/store"
)

type operatorMemoryReadState struct {
	mu      sync.Mutex
	runtime *app.Runtime
	reads   map[string]*app.OperatorMemoryRead
	order   []string
	closed  bool
}

func (h *CommandHandler) prepareOperatorMemoryRuntimeAttach() func() {
	if h == nil {
		return func() {}
	}
	h.memoryReads.mu.Lock()
	h.clearOperatorMemoryReadsLocked()
	return h.memoryReads.mu.Unlock
}

func (h *CommandHandler) closeOperatorMemoryReads() {
	if h == nil {
		return
	}
	h.memoryReads.mu.Lock()
	defer h.memoryReads.mu.Unlock()
	h.memoryReads.closed = true
	h.clearOperatorMemoryReadsLocked()
}

func (h *CommandHandler) clearOperatorMemoryReadsLocked() {
	for _, token := range h.memoryReads.reads {
		token.Invalidate()
	}
	h.memoryReads.reads, h.memoryReads.order, h.memoryReads.runtime = nil, nil, nil
}

const operatorMemoryReviewUsage = "Usage: /memory review [page] | /memory review after <id> | /memory review live [after <id>] | /memory read <id> [next offset] | /memory promote <id> <review rationale>"

func (h *CommandHandler) handleOperatorMemoryReview(ctx context.Context, args []string) (string, bool, error) {
	if len(args) == 0 || args[0] != "review" && args[0] != "read" && args[0] != "promote" {
		return "", false, nil
	}
	if h.ws == nil {
		return "", true, errNoRuntime
	}
	h.memoryReads.mu.Lock()
	defer h.memoryReads.mu.Unlock()
	if h.memoryReads.closed {
		return "", true, errNoRuntime
	}
	control := h.ws.controlSource()
	a, _ := control.Authority.(*runtimeControlAuthority)
	h.ws.mu.RLock()
	currentRuntime := h.ws.runtime
	h.ws.mu.RUnlock()
	if a == nil || a.runtime == nil || a.runtime != currentRuntime || a.localControl == nil || a.localControlErr != nil {
		return "", true, errNoRuntime
	}
	ctx = a.localControl.Context(ctx)
	r := a.runtime
	if args[0] == "review" {
		if len(args) > 1 && (args[1] == "after" || args[1] == "live") {
			after := ""
			live := args[1] == "live"
			if !live && len(args) == 3 {
				after = args[2]
			} else if live && len(args) == 4 && args[2] == "after" {
				after = args[3]
			} else if !live || len(args) != 2 {
				return operatorMemoryReviewUsage, true, nil
			}
			var page store.MemoryReviewPage
			var err error
			if live {
				page, err = r.OperatorMemoryLiveReview(ctx, after, 12)
			} else {
				page, err = r.OperatorMemoryReview(ctx, after, 12)
			}
			if err != nil {
				return "", true, err
			}
			return operatorMemoryKeysetPage(page, live), true, nil
		}
		page := 1
		if len(args) > 2 {
			return operatorMemoryReviewUsage, true, nil
		}
		if len(args) == 2 {
			var err error
			page, err = strconv.Atoi(args[1])
			if err != nil || page < 1 || page > 1024 {
				return operatorMemoryReviewUsage, true, nil
			}
		}
		text, err := h.operatorMemoryReviewPage(ctx, r, page)
		return text, true, err
	}
	if args[0] == "read" {
		if len(args) < 2 || len(args) > 3 {
			return operatorMemoryReviewUsage, true, nil
		}
		offset := 0
		if len(args) == 3 {
			var err error
			offset, err = strconv.Atoi(args[2])
			if err != nil || offset < 0 {
				return operatorMemoryReviewUsage, true, nil
			}
		}
		h.ws.mu.RLock()
		secrets := append([]string(nil), h.ws.state.KnownSecrets...)
		h.ws.mu.RUnlock()
		if h.memoryReads.runtime != r {
			h.clearOperatorMemoryReadsLocked()
			h.memoryReads.runtime = r
		}
		if h.memoryReads.reads == nil {
			h.memoryReads.reads = map[string]*app.OperatorMemoryRead{}
		}
		token := h.memoryReads.reads[args[1]]
		if offset == 0 {
			if token != nil {
				token.Invalidate()
			} else {
				if len(h.memoryReads.order) == 32 {
					old := h.memoryReads.order[0]
					h.memoryReads.reads[old].Invalidate()
					delete(h.memoryReads.reads, old)
					h.memoryReads.order = h.memoryReads.order[1:]
				}
				h.memoryReads.order = append(h.memoryReads.order, args[1])
			}
			var err error
			token, err = r.BeginOperatorMemoryRead(ctx, args[1])
			if err != nil {
				return "", true, err
			}
			h.memoryReads.reads[args[1]] = token
		}
		view, err := r.ReadOperatorMemoryPage(ctx, token, offset, func(body string) string { return RedactContent(body, secrets) })
		if err != nil {
			return "", true, err
		}
		status := "Read complete"
		if view.Withheld {
			status = "Private content withheld; full review unavailable"
		} else if !view.FullRead {
			status = fmt.Sprintf("Continue: /memory read %s %d", memoryRestoreData(view.ID, 256), view.NextOffset)
		}
		text := fmt.Sprintf("MEMORY %s · revision %d · %s/%s · confidence %s · source %s · %d evidence refs\nBody bytes %d–%d of %d · %s\nDATA ONLY; retention and accepted project rules are separate.\n%s", memoryRestoreData(view.ID, 256), view.Revision, view.Lifecycle, view.Authority, view.Confidence, memoryRestoreData(view.SourceKind, 64), view.EvidenceCount, view.Offset, view.NextOffset, view.BodyBytes, status, view.Body)
		return text, true, nil
	}
	if len(args) < 3 {
		return operatorMemoryReviewUsage, true, nil
	}
	token := h.memoryReads.reads[args[1]]
	if h.memoryReads.runtime != r {
		token = nil
	}
	result, err := r.CommandPromoteReviewedMemory(ctx, token, strings.Join(args[2:], " "))
	if err != nil {
		if result.Persisted {
			return fmt.Sprintf("Promoted %s at revision %d; canonical commit retained, but current state changed or cannot be read. Further index repair requires a current authorized review.", memoryRestoreData(result.ID, 256), result.Revision), true, err
		}
		return "", true, err
	}
	if !result.Indexed {
		return fmt.Sprintf("Promoted %s at revision %d; durable record retained, index repair pending. Repeat the same command to repair its unchanged index. Accepted project classification still requires separate review.", memoryRestoreData(result.ID, 256), result.Revision), true, nil
	}
	return fmt.Sprintf("Promoted %s at revision %d; index ready. Accepted project classification still requires separate review.", memoryRestoreData(result.ID, 256), result.Revision), true, nil
}

func operatorMemoryReviewSubcommands(existing []string) []string {
	return append(existing, "read", "promote")
}
func operatorMemoryReviewHelp() string {
	return "\n  /memory review [after <id>|live [after <id>]] | read <id> [next offset] | promote <id> <rationale>  Inspect untrusted notes; promotion requires a complete authorized read"
}

func operatorMemoryKeysetPage(page store.MemoryReviewPage, live bool) string {
	if len(page.Entries) == 0 {
		return "No unreviewed retained or live memory candidates on this page."
	}
	var lines []string
	for _, e := range page.Entries {
		kind := fmt.Sprintf("revision %d · retained %s/%s", e.Revision, e.Lifecycle, e.Authority)
		if live {
			kind = "pending retention"
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s", memoryRestoreData(e.ID, 256), kind, memoryRestoreData(e.Title, 80)))
	}
	text := "MEMORY REVIEW · DATA ONLY; retained notes remain untrusted\n" + strings.Join(lines, "\n")
	if live {
		text += "\nLive notes keep their original separate retention Decision; no canonical read or promotion is granted by this list."
	} else {
		text += "\nRead: /memory read <id>. Promote only after complete read: /memory promote <id> <rationale>. Durable agent imports require an explicit new reviewed fact. Live retention Decisions: /memory review live."
	}
	if page.NextID != "" {
		prefix := "/memory review after "
		if live {
			prefix = "/memory review live after "
		}
		text += "\nNext: " + prefix + memoryRestoreData(page.NextID, 256)
	}
	return text
}

func (h *CommandHandler) operatorMemoryReviewPage(ctx context.Context, r *app.Runtime, page int) (string, error) {
	const size = 12
	after := ""
	var current store.MemoryReviewPage
	lastPage := 0
	for i := 1; i <= page; i++ {
		var err error
		current, err = r.OperatorMemoryReview(ctx, after, size)
		if err != nil {
			return "", err
		}
		if i == page || current.NextID == "" {
			lastPage = i
			break
		}
		after = current.NextID
	}
	var pending []store.MemoryReviewEntry
	for after := ""; ; {
		live, err := r.OperatorMemoryLiveReview(ctx, after, 32)
		if err != nil {
			return "", err
		}
		for _, e := range live.Entries {
			if !r.OperatorMemoryRetained(ctx, e.ID) {
				pending = append(pending, e)
			}
		}
		if live.NextID == "" {
			break
		}
		after = live.NextID
	}
	var lines []string
	next := false
	if lastPage == page {
		for _, e := range current.Entries {
			lines = append(lines, fmt.Sprintf("%s · revision %d · retained %s/%s · %s", memoryRestoreData(e.ID, 256), e.Revision, e.Lifecycle, e.Authority, memoryRestoreData(e.Title, 80)))
		}
		next = current.NextID != ""
		if !next {
			for _, rec := range pending[:min(len(pending), size-len(lines))] {
				lines = append(lines, fmt.Sprintf("%s · pending retention · %s", memoryRestoreData(rec.ID, 256), memoryRestoreData(rec.Title, 80)))
			}
			next = len(current.Entries)+len(pending) > size
		}
	} else {
		persisted := (lastPage-1)*size + len(current.Entries)
		start := (page-1)*size - persisted
		if start < len(pending) {
			for _, rec := range pending[start:min(start+size, len(pending))] {
				lines = append(lines, fmt.Sprintf("%s · pending retention · %s", memoryRestoreData(rec.ID, 256), memoryRestoreData(rec.Title, 80)))
			}
			next = start+size < len(pending)
		}
	}
	if len(lines) == 0 {
		return "No unreviewed retained or live memory candidates on this page.", nil
	}
	text := "MEMORY REVIEW · retained notes remain untrusted until explicit evidence-gated promotion\n" + strings.Join(lines, "\n") + "\nRead retained text: /memory read <id>. Promote only after complete read: /memory promote <id> <rationale>. Pending retention keeps its original separate Decision. Durable agent imports require an explicit new reviewed fact; this command cannot silently promote them."
	text += "\nLive retention Decisions: /memory review live."
	if next {
		if current.NextID != "" {
			text += "\nNext: /memory review after " + memoryRestoreData(current.NextID, 256)
		} else {
			text += "\nNext: /memory review " + strconv.Itoa(page+1)
		}
	}
	return text, nil
}

func memoryRestoreData(value string, limit int) string {
	value = RedactContent(value, nil)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}
