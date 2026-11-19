package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func (w *Workspace) marshalArchitectureProposal(ctx context.Context, occurrence, reason string) (string, error) {
	m := w.marshalSession()
	m.mu.Lock()
	if strings.TrimSpace(reason) != "" {
		m.architectureReason = strings.TrimSpace(reason)
	}
	m.mu.Unlock()
	return w.marshalArchitecture(ctx, "", occurrence)
}

// marshalArchitecture keeps generation and the HTTP view in the workspace's
// managed lifecycle. Command deadlines never cut off an owner-requested draft.
func (w *Workspace) marshalArchitecture(ctx context.Context, goal, occurrence string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	w.mu.RLock()
	rt := w.runtime
	w.mu.RUnlock()
	if rt == nil {
		return "", errors.New("the MARSHAL runtime is not attached; open an initialized project")
	}
	m := w.marshalSession()
	m.mu.Lock()
	open := m.architectureBrowser
	if open == nil {
		open = openArchitectureBrowser
	}
	if m.architecture != nil {
		select {
		case <-m.architecture.Done:
			m.architecture = nil
		default:
			url := m.architecture.URL
			m.mu.Unlock()
			if strings.TrimSpace(goal) != "" {
				return "", errors.New("architecture already exists; submit a selected change request in its browser view")
			}
			if !w.startBackground(w.workers.context(), func(workerCtx context.Context) {
				err := open(workerCtx, url)
				w.resolveArchitectureProposal(occurrence, err)
				for _, id := range w.clearDeferredArchitecture() {
					w.resolveArchitectureProposal(id, err)
				}
				if err != nil {
					w.RecordActivity(architectureFailedNote(url))
				}
				w.architectureOpenResult(err)
			}) {
				return "", errors.New("workspace is closing")
			}
			return "Architecture view: " + url, nil
		}
	}
	if m.architectureStarting {
		if strings.TrimSpace(goal) != "" {
			m.mu.Unlock()
			return "", errors.New("an architecture draft is already being prepared; wait for its view before requesting changes")
		}
		if occurrence != "" {
			m.architectureOccurrences = append(m.architectureOccurrences, occurrence)
		}
		m.mu.Unlock()
		return "Marshal is preparing the architecture diagram.", nil
	}
	m.architectureStarting = true
	if occurrence != "" {
		m.architectureOccurrences = append(m.architectureOccurrences, occurrence)
	}
	start := m.architectureStart
	m.mu.Unlock()
	if !w.startBackground(w.workers.context(), func(workerCtx context.Context) {
		var provider string
		var session *app.ArchitectureReviewSession
		var err error
		if start == nil {
			workerCtx, err = w.marshalOperationOwnerContext(workerCtx)
			if err == nil {
				_, provider, _, err = w.marshalService(workerCtx, "")
			}
			if err == nil {
				session, err = w.startArchitectureReviewSession(workerCtx, rt, provider, goal)
			}
		} else {
			m.mu.Lock()
			provider = m.provider
			m.mu.Unlock()
			session, err = start(workerCtx, provider, goal)
		}
		var existing *app.ArchitectureReviewOwnedError
		existingURL := ""
		if errors.As(err, &existing) {
			existingURL = existing.URL
			err = open(workerCtx, existingURL)
		}
		if err == nil && session == nil && existingURL == "" {
			err = errors.New("architecture review session was not created")
		}
		if err == nil && session != nil {
			err = open(workerCtx, session.URL)
		}
		m.mu.Lock()
		m.architectureStarting = false
		occurrences := append([]string(nil), m.architectureOccurrences...)
		m.architectureOccurrences = nil
		reason := m.architectureReason
		m.architectureReason = ""
		if session != nil {
			m.architecture = session
		}
		m.mu.Unlock()
		if errors.Is(err, model.ErrGoalNotFound) && strings.TrimSpace(goal) == "" && (len(occurrences) > 0 || w.hasDeferredArchitecture()) {
			// The Marshal asked to open the architecture before a confirmed
			// goal existed. Keep the request and offer it once the
			// requirements are confirmed instead of dropping it.
			w.holdArchitectureRequest(occurrences, reason)
			w.mu.Lock()
			still := w.answer == architectureOpening
			w.mu.Unlock()
			if still {
				w.memoryAnswer("The architecture could not be opened yet: "+plainArchitectureError(err), true)
			}
			return
		}
		for _, id := range occurrences {
			w.resolveArchitectureProposal(id, err)
		}
		url := existingURL
		if session != nil {
			url = session.URL
		}
		if session != nil || existingURL != "" {
			for _, id := range w.clearDeferredArchitecture() {
				w.resolveArchitectureProposal(id, err)
			}
		}
		if err != nil {
			if session == nil && existingURL == "" {
				// No page was made: say why, and the "Opening…" answer
				// gives way to it.
				w.RecordActivity("Architecture view failed: " + err.Error())
				w.mu.Lock()
				still := w.answer == architectureOpening
				w.mu.Unlock()
				if still {
					w.memoryAnswer("The architecture could not be opened: "+plainArchitectureError(err), true)
				}
				return
			}
			// The page is up but no browser opened: where its link is.
			w.RecordActivity(architectureFailedNote(url))
		} else {
			w.RecordActivity(architectureOpenedNote(url))
		}
		w.architectureOpenResult(err)
		if session == nil {
			return
		}
		if session == nil {
			return
		}
		select {
		case <-workerCtx.Done():
		case <-session.Done:
		}
		_ = session.Close()
		m.mu.Lock()
		if m.architecture == session {
			m.architecture = nil
		}
		m.mu.Unlock()
	}) {
		m.mu.Lock()
		m.architectureStarting = false
		m.architectureOccurrences = nil
		m.architectureReason = ""
		m.mu.Unlock()
		return "", errors.New("workspace is closing")
	}
	return "Marshal is preparing a view-only architecture diagram; it opens in your browser when ready.", nil
}

func (w *Workspace) resolveArchitectureProposal(id string, err error) {
	if id == "" || w.store == nil {
		return
	}
	status := "applied"
	if err != nil {
		status = "rejected"
	}
	_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, id, status)
}

func openArchitectureBrowser(ctx context.Context, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := "xdg-open"
	args := []string{url}
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	}
	process := exec.Command(command, args...)
	if err := process.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	// The user's desktop browser has its own lifetime. Reap its launcher without
	// terminating that browser when the MARSHAL workspace closes. A launcher
	// that fails at once (no browser for the link) is a failed open; one still
	// running after browserOpenWait is taken as opened.
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("open browser: %w", err)
		}
	case <-time.After(browserOpenWait):
	case <-ctx.Done():
	}
	return nil
}

// browserOpenWait is how long a browser launcher may take to fail.
const browserOpenWait = 3 * time.Second

func (w *Workspace) boundMarshalArchitectureSource() string {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	active := w.tmuxActiveWins["marshal-chat"]
	if active == nil || active.role != "marshal-chat" || active.sessionID == "" || active.state == "done" || active.state == "failed" {
		return ""
	}
	return active.sessionID
}
