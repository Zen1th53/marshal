package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
)

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
				if err != nil {
					w.RecordActivity("Architecture view: " + err.Error() + ". URL: " + url)
				} else {
					w.clearAnswer(architectureOpening)
				}
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
		if session != nil {
			m.architecture = session
		}
		m.mu.Unlock()
		for _, id := range occurrences {
			w.resolveArchitectureProposal(id, err)
		}
		if err != nil {
			message := "Architecture view failed: " + err.Error()
			if session != nil {
				message += ". URL: " + session.URL
			}
			w.RecordActivity(message)
			if session == nil {
				return
			}
		} else {
			url := existingURL
			if session != nil {
				url = session.URL
			}
			w.RecordActivity("Architecture view: " + url)
			w.clearAnswer(architectureOpening)
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
	// terminating that browser when the MARSHAL workspace closes.
	go func() { _ = process.Wait() }()
	return nil
}

func (w *Workspace) boundMarshalArchitectureSource() string {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	active := w.tmuxActiveWins["marshal-chat"]
	if active == nil || active.role != "marshal-chat" || active.sessionID == "" || active.state == "done" || active.state == "failed" {
		return ""
	}
	return active.sessionID
}
