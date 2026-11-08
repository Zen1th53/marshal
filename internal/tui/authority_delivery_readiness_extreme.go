//go:build !governed

package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// encoding/json quotes ASCII controls, but DEL, C1 and Unicode format controls
// must also stay escaped data when the resulting JSON enters terminal input.
func terminalSafeDecisionJSON(data []byte) string {
	var out strings.Builder
	for _, r := range string(data) {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			if r > 0xffff {
				hi, lo := utf16.EncodeRune(r)
				fmt.Fprintf(&out, "\\u%04x\\u%04x", hi, lo)
			} else {
				fmt.Fprintf(&out, "\\u%04x", r)
			}
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// These observations decide when it is safe to type, never whether a request is
// approved. Unknown screens and active turns keep the original request queued.
func (w *Workspace) queuePendingDecision(id string, p *buildPendingDecision) {
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	if w.permissions.build.pending[id] == p {
		w.permissions.queue.Add(p.request)
	}
}

func (w *Workspace) codexDecisionReady(ctx context.Context, p *buildPendingDecision, payload string) bool {
	if ctx.Err() != nil || p.launchID == "" || p.launchID != w.proposalLaunchID() {
		return false
	}
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["marshal-chat"]
	if a == nil || a.paneID != p.paneID || a.provider != p.provider || a.state != "working" {
		w.tmuxMu.Unlock()
		return false
	}
	session, history := a.sessionID, a.historyPath
	w.tmuxMu.Unlock()
	// Only a genuinely new launch can bootstrap before its first transcript.
	// A recovered session with missing history is not an idle observation.
	if session != "" || history != "" {
		if session == "" || history == "" || w.runtime == nil || !codexTranscriptIdle(history, session, w.runtime.ProjectRoot()) {
			return false
		}
	}
	status, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", p.paneID, "#{pane_id}\t#{cursor_x}\t#{cursor_y}\t#{pane_height}\t#{pane_in_mode}")
	if err != nil {
		return false
	}
	frame, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-e", "-t", p.paneID)
	if err != nil || len(frame) > 1<<20 || !codexComposerReady(string(frame), strings.TrimSpace(string(status)), p.paneID, payload) || p.launchID != w.proposalLaunchID() {
		return false
	}
	return session == "" && history == "" || codexTranscriptIdle(history, session, w.runtime.ProjectRoot())
}

func codexComposerReady(frame, status, pane, payload string) bool {
	fields := strings.Split(status, "\t")
	if len(fields) != 5 || fields[0] != pane || fields[4] != "0" {
		return false
	}
	x, e1 := strconv.Atoi(fields[1])
	y, e2 := strconv.Atoi(fields[2])
	height, e3 := strconv.Atoi(fields[3])
	rawLines := strings.Split(strings.TrimSuffix(frame, "\n"), "\n")
	lines := strings.Split(strings.TrimSuffix(StripANSI(frame), "\n"), "\n")
	if e1 != nil || e2 != nil || e3 != nil || x < 2 || y < 0 || y >= len(lines) || height < len(lines) {
		return false
	}
	start := y
	for start >= 0 && !strings.HasPrefix(lines[start], "› ") && lines[start] != "›" {
		start--
	}
	if start < 0 {
		return false
	}
	footer := -1
	for i := y + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "esc to interrupt") || strings.Contains(line, "esc to cancel") || strings.Contains(line, "enter to submit") {
			return false
		}
		// Grounded in Codex 0.162.1's normal composer footer. A modal's
		// option list and unknown provider layouts do not meet this guard.
		if strings.HasPrefix(line, "GPT-") && strings.Contains(line, " · ") {
			footer = i
			break
		}
	}
	if footer < 0 {
		return false
	}
	composer := strings.TrimSpace(strings.TrimPrefix(lines[start], "›"))
	for _, line := range lines[start+1 : footer] {
		composer += strings.TrimSpace(line)
	}
	if payload == "" {
		// Codex 0.162.1 paints its empty-input placeholder in dim text, with
		// the cursor before it. The same words typed as a draft are not empty.
		const placeholder = "\x1b[0;1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
		const resumedPlaceholder = "\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
		knownPlaceholder := (rawLines[start] == placeholder || rawLines[start] == resumedPlaceholder) && composer == "Ask Codex to do anything"
		return (composer == "" || knownPlaceholder) && start == y && x == 2
	}
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	return compact(composer) == compact(payload) || composer == fmt.Sprintf("[Pasted Content %d chars]", utf8.RuneCountInString(payload))
}

// Read only the exact bound regular transcript. Metadata pins root/session and
// a bounded tail pins the latest native turn; torn/unknown data is not idle.
func codexTranscriptIdle(path, session, projectRoot string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false
	}
	defer root.Close()
	f, err := openProposalFile(root, filepath.Base(path))
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false
	}
	first := bufio.NewReader(io.LimitReader(f, 128<<10))
	metadata, err := first.ReadBytes('\n')
	if err != nil {
		return false
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID      string `json:"id"`
			CWD     string `json:"cwd"`
			Version string `json:"cli_version"`
		} `json:"payload"`
	}
	if json.Unmarshal(metadata, &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID != session || meta.Payload.CWD != projectRoot || meta.Payload.Version != "0.162.1" {
		return false
	}
	const bound = int64(2 << 20)
	offset := info.Size() - bound
	if offset < 0 {
		offset = 0
	}
	data := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(data, offset); err != nil {
		return false
	}
	if offset > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return false
		}
		data = data[i+1:]
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return false
	}
	turn, idle := "", false
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Turn string `json:"turn_id"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &event) != nil {
			return false
		}
		if event.Type != "event_msg" && event.Type != "session_meta" {
			idle = false
		}
		if event.Type != "event_msg" {
			continue
		}
		switch event.Payload.Type {
		case "task_started":
			if event.Payload.Turn == "" {
				return false
			}
			turn, idle = event.Payload.Turn, false
		case "task_complete":
			if event.Payload.Turn == "" {
				return false
			}
			if offset > 0 && turn == "" {
				// A truncated prefix may omit an old start. It is unknown,
				// never idle evidence; require a later fully observed pair.
				continue
			}
			if turn == "" || event.Payload.Turn != turn {
				return false
			}
			idle = true
		case "token_count":
			// Native accounting after a completed turn is not another turn.
		default:
			idle = false
		}
	}
	after, err := f.Stat()
	return idle && err == nil && after.Size() == info.Size() && after.ModTime() == info.ModTime()
}

func (w *Workspace) deliverCodexDecision(ctx context.Context, id string, p *buildPendingDecision) {
	if !w.permissions.build.deliveryMu.TryLock() {
		return
	}
	defer w.permissions.build.deliveryMu.Unlock()
	// A message the operator typed on the Board may be going into the
	// same composer; the two never interleave.
	w.chatTypeMu.Lock()
	defer w.chatTypeMu.Unlock()
	transportPacket := privateProcess05QuestionIntent(p.request)
	items, payload, bounded := w.privateDecisionPacket(id, p)
	fail := func(reason string) {
		for _, item := range items {
			w.permissions.mu.Lock()
			current := w.permissions.build.pending[item.id] == item.p
			answered := current && item.p.answer != nil && item.p.request == item.request
			if current && !answered {
				delete(w.permissions.build.pending, item.id)
			}
			w.permissions.mu.Unlock()
			if answered {
				w.routeBuildPermission(context.WithoutCancel(ctx), item.request)
			} else if current && reason == "decision packet too large" {
				// Structural: the exact description cannot be delivered
				// without truncation, so re-delivery cannot succeed.
				w.finishBuildDecision(context.WithoutCancel(ctx), item.request, item.id, false, reason, item.p.provider)
			} else if current {
				w.settleUnansweredDecision(context.WithoutCancel(ctx), item.request, item.id, item.p, reason)
			}
		}
	}
	if ctx.Err() != nil || p.launchID != "" && p.launchID != w.proposalLaunchID() {
		fail("decision delivery failed")
		return
	}
	valid := func() bool {
		if p.launchID == "" || p.launchID != w.proposalLaunchID() {
			return false
		}
		if transportPacket {
			for _, item := range items {
				if !w.privateProcess05Question(item.request) {
					return false
				}
			}
		}
		w.permissions.mu.Lock()
		defer w.permissions.mu.Unlock()
		selected := map[string]bool{}
		for _, item := range items {
			selected[item.id] = true
		}
		for otherID, other := range w.permissions.build.pending {
			if !selected[otherID] && other.provider == p.provider && other.launchID == p.launchID && other.delivered {
				return false
			}
		}
		for _, item := range items {
			if w.permissions.build.pending[item.id] != item.p || item.p.delivered || item.p.answer != nil || item.p.request != item.request || !item.p.deadline.Equal(item.deadline) || item.p.provider != p.provider || item.p.paneID != p.paneID || item.p.launchID != p.launchID || !time.Now().Before(item.deadline) {
				return false
			}
		}
		return ctx.Err() == nil
	}
	if !valid() {
		return
	}
	if !bounded {
		fail("decision packet too large")
		return
	}
	if !w.codexDecisionReady(ctx, p, "") {
		return
	}
	buffer := "marshal-" + id
	_, err := tmux.RunCommand(ctx, "set-buffer", "-b", buffer, "--", payload)
	if err == nil {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_, _ = tmux.RunCommand(cleanup, "delete-buffer", "-b", buffer)
		}()
		if !valid() || !w.codexDecisionReady(ctx, p, "") {
			if ctx.Err() != nil {
				fail("decision delivery failed")
			}
			return
		}
		_, err = tmux.RunCommand(ctx, "paste-buffer", "-d", "-p", "-b", buffer, "-t", p.paneID)
	}
	if err == nil {
		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-timer.C:
		}
		timer.Stop()
	}
	if err == nil && (!valid() || !w.codexDecisionReady(ctx, p, payload)) {
		err = fmt.Errorf("composer changed before submission")
	}
	if err == nil {
		_, err = tmux.RunCommand(ctx, "send-keys", "-t", p.paneID, "Enter")
	}
	if err != nil {
		fail("decision delivery failed")
		return
	}
	w.permissions.mu.Lock()
	for _, item := range items {
		if w.permissions.build.pending[item.id] == item.p {
			item.p.delivered = true
		}
	}
	w.permissions.mu.Unlock()
	for _, item := range items {
		w.RecordActivity("Marshal decision requested: " + item.id + " " + item.p.request.Kind + " " + item.p.request.Object)
	}
}
