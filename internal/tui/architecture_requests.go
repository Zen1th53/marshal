package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/app"
)

// architectureRequestBox holds the owner's requests from the architecture
// page that wait for the Marshal chat's answer, by request id.
type architectureRequestBox struct {
	mu      sync.Mutex
	waiting map[string]chan app.ArchitectureAnswer
}

func (b *architectureRequestBox) open(id string) chan app.ArchitectureAnswer {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiting == nil {
		b.waiting = map[string]chan app.ArchitectureAnswer{}
	}
	ch := make(chan app.ArchitectureAnswer, 1)
	b.waiting[id] = ch
	return ch
}

func (b *architectureRequestBox) close(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiting, id)
}

// deliver hands an answer to the waiting request; it reports false when no
// request with that id is waiting (answered, withdrawn or timed out).
func (b *architectureRequestBox) deliver(id string, answer app.ArchitectureAnswer) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.waiting[id]
	if !ok {
		return false
	}
	delete(b.waiting, id)
	ch <- answer
	return true
}

// errArchitectureRequestUnanswered fails a page request the Marshal did not
// answer while the page waited.
var errArchitectureRequestUnanswered = errors.New("The Marshal did not answer this request in time. Open the Marshal chat, then send the request again or withdraw it.")

// architectureRequestNudge wakes an idle Marshal; the request itself is only
// in the inbox.
const architectureRequestNudge = marshalNotePrefix + "The owner sent a request from the architecture page. Check your inbox."

// forwardArchitectureRequest writes the owner's exact selection and request
// to the Marshal's inbox, wakes an open chat and waits for the answer that
// marshal_architecture_request_answer delivers.
func (w *Workspace) forwardArchitectureRequest(ctx context.Context, req app.ArchitectureRequest) (app.ArchitectureAnswer, error) {
	if w.runtime == nil {
		return app.ArchitectureAnswer{}, errNoRuntime
	}
	ch := w.architectureRequests.open(req.ID)
	defer w.architectureRequests.close(req.ID)
	view, err := openInboxView(w.runtime.ProjectRoot(), "marshal", false)
	if err == nil {
		err = view.write(architectureRequestMessage(req))
	}
	if err != nil {
		return app.ArchitectureAnswer{}, fmt.Errorf("the request could not reach the Marshal: %w", err)
	}
	if w.sayPane() != "" || w.nativeChatShown() {
		w.noticeToMarshal(w.workers.context(), architectureRequestNudge)
	}
	select {
	case answer := <-ch:
		return answer, nil
	case <-ctx.Done():
		return app.ArchitectureAnswer{}, errArchitectureRequestUnanswered
	}
}

// architectureRequestMessage is the inbox entry for one page request. The
// selection is quoted exactly, as JSON, so the Marshal never has to guess
// which part the owner meant.
func architectureRequestMessage(req app.ArchitectureRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n## architecture request %s\n\n", req.ID)
	kind := "Comment (answer it; change nothing)"
	if req.Type == "change" {
		kind = "Change request"
	}
	derived := len(req.Derived) > 0
	if derived {
		kind = "Question about code (" + req.Type + "; derived from code, not reviewed: answer it, change nothing)"
	}
	fmt.Fprintf(&b, "%s from the owner on the architecture page, revision %s.\n\n", kind, req.Revision)
	fmt.Fprintf(&b, "Owner's words: %q\n\n", req.Text)
	exact := map[string]json.RawMessage{
		"source_contract": req.Source, "anchor": req.Anchor,
		"components": req.Components, "connections": req.Connections,
	}
	if derived {
		exact["derived"] = req.Derived
	}
	selection, _ := json.Marshal(exact)
	fmt.Fprintf(&b, "Exact selection (only these parts are meant):\n```json\n%s\n```\n\n", selection)
	if derived {
		b.WriteString("The derived field names the exact code elements (kind, repository path, declaration name) inside the selected part. They are not part of the reviewed contract. ")
		b.WriteString("Answer with marshal_architecture_request_answer and this request_id: status answered (a reply) or needs_clarification (questions). Never answer proposed_change and submit no project contract revision for it; if a change is needed, say in the reply what you would change and ask the owner to request it in the chat.\n")
		return b.String()
	}
	b.WriteString("Answer with marshal_architecture_request_answer and this request_id: status answered (a reply), needs_clarification (questions), or proposed_change. ")
	b.WriteString("For proposed_change first submit the revised project contract with marshal_project_contract_submit, changing only the selected parts; ")
	b.WriteString("adding or removing parts, changing other parts or moving a dependency is refused on the page, so for those say so in the reply and ask the owner to request it in the chat.\n")
	return b.String()
}
