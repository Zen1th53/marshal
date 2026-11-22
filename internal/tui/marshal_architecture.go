package tui

import (
	"context"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/browser"
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
			existing := m.architecture
			m.mu.Unlock()
			current, err := w.architectureSessionCurrent(ctx, existing)
			if err != nil {
				return "", err
			}
			m.mu.Lock()
			if m.architecture != existing {
				m.mu.Unlock()
				return w.marshalArchitecture(ctx, goal, occurrence)
			}
			if !current {
				stale := m.architecture
				m.mu.Unlock()
				// Close outside the session lock: an in-flight browser callback
				// may need that lock while shutdown waits for it to finish.
				if err := stale.Close(); err != nil {
					return "", err
				}
				return w.marshalArchitecture(ctx, goal, occurrence)
			}
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
			// The footer names the address without its key: the link is
			// the owner's Accept, and the architecture card shows it whole
			// only where no browser can open it.
			return "Architecture view: " + architectureShownURL(url) + " (opening in your browser)", nil
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
				session, err = w.startArchitectureReviewSession(workerCtx, rt, provider, goal)
			}
		} else {
			m.mu.Lock()
			provider = m.provider
			m.mu.Unlock()
			session, err = start(workerCtx, provider, goal)
		}
		// A review another MARSHAL process serves has no link here: its
		// key is never written where this process could read it, so the
		// error says to use that window's link.
		if err == nil && session == nil {
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
		url := ""
		if session != nil {
			url = session.URL
			for _, id := range w.clearDeferredArchitecture() {
				w.resolveArchitectureProposal(id, err)
			}
		}
		if err != nil {
			if session == nil {
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

// openArchitectureBrowser opens url in the user's desktop browser.
func openArchitectureBrowser(ctx context.Context, url string) error {
	return browser.Open(ctx, url)
}

func (w *Workspace) boundMarshalArchitectureSource() string {
	source, _ := w.boundMarshalArchitectureBinding()
	return source
}

// boundMarshalChatSession is the provider conversation ID of the live tmux
// Marshal chat, or "" while none was captured.
func (w *Workspace) boundMarshalChatSession() string {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	active := w.tmuxActiveWins["marshal-chat"]
	if active == nil || active.role != "marshal-chat" || active.sessionID == "" || active.state == "done" || active.state == "failed" {
		return ""
	}
	return active.sessionID
}

// boundMarshalArchitectureBinding names the live Marshal chat an owner
// instruction is bound to, or says why there is none.
//
// The chat's provider conversation ID is learned from the provider's own
// history file, which a launch may never write where MARSHAL reads it (or not
// yet). Such a chat is still the bound Marshal when it holds the live MCP
// launch its proposals are admitted under: that launch, the one a pending
// project contract belongs to, then names it. A chat with neither is not
// bound, and nothing from it is trusted.
func (w *Workspace) boundMarshalArchitectureBinding() (string, error) {
	w.tmuxMu.Lock()
	active := w.tmuxActiveWins["marshal-chat"]
	var state, session, launch string
	chat := active != nil && active.role == "marshal-chat"
	if chat {
		state, session, launch = active.state, active.sessionID, active.mcpLaunchID
	}
	w.tmuxMu.Unlock()
	switch {
	case !chat:
		return "", errors.New("The Marshal chat is not running. Open it in your terminal, then press Accept again.")
	case state == "done" || state == "failed":
		return "", errors.New("The Marshal chat has ended. Start it again in your terminal, then press Accept again.")
	case session != "":
		return session, nil
	case w.chatLaunchBound(launch):
		return "launch:" + launch, nil
	case state == "mcp-unavailable":
		return "", errors.New("The Marshal chat is open but its MARSHAL connection is unavailable. " + chatMCPRepairInstructions + " Then press Accept again.")
	}
	return "", errors.New("The Marshal chat is open, but MARSHAL has neither its conversation ID nor a live proposal connection for it, so the Accept cannot reach it. " + chatMCPRepairInstructions + " Then press Accept again.")
}
