package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// marshalInlineKnowledgeMaximumBytes bounds the hidden launch prompt
// (protocol, intake and project knowledge). It leaves room for the native
// planning context inside the 64 KiB combined hidden-prompt bound. Larger
// projects get a pointer instead; the full packet stays readable through
// the project_context MCP tool.
const marshalInlineKnowledgeMaximumBytes = 46 << 10

var projectKnowledgeFetch = func(ctx context.Context, rt *app.Runtime, root string) (string, error) {
	return rt.MarshalProjectContext(ctx, root, "")
}

func (w *Workspace) appendProjectKnowledge(root, brief string) string {
	if w.runtime == nil {
		return brief
	}
	owner := w.runtime.ProjectRoot()
	if root != owner {
		rel, err := filepath.Rel(filepath.Join(owner, statepath.DirName, "worktrees"), root)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return brief
		}
	}

	var knowledge string
	var err error

	w.launch.mu.Lock()
	cachedRoot := w.launch.scan.KnowledgeRoot
	cachedKnowledge := w.launch.scan.Knowledge
	cachedErr := w.launch.scan.KnowledgeErr
	w.launch.mu.Unlock()

	// A prefetch that ran out of time is not a value: fetch again with the
	// full budget rather than reporting the knowledge unavailable.
	timedOut := errors.Is(cachedErr, context.DeadlineExceeded) || errors.Is(cachedErr, context.Canceled)
	if cachedRoot != "" && cachedRoot == root && !timedOut {
		knowledge, err = cachedKnowledge, cachedErr
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		knowledge, err = projectKnowledgeFetch(ctx, w.runtime, root)
	}

	if err != nil {
		return brief + "\nPROJECT KNOWLEDGE UNAVAILABLE: " + err.Error() + "\nRead project_context successfully before planning; do not substitute remembered or invented architecture.\n"
	}
	if len(brief)+1+len(knowledge) > marshalInlineKnowledgeMaximumBytes {
		return brief + "\nPROJECT KNOWLEDGE NOT INLINED: the current packet (" + strconv.Itoa(len(knowledge)) + " bytes) exceeds the launch budget.\nRead project_context through MCP before planning; do not substitute remembered or invented architecture.\n"
	}
	return brief + "\n" + knowledge
}
