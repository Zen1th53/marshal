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

// startProjectKnowledgeIndex starts deferred background project knowledge
// indexing on the attached runtime (if any) and keeps the Board's progress
// line updated without blocking the first paint.
func (w *Workspace) startProjectKnowledgeIndex(ctx context.Context) {
	if w == nil || w.runtime == nil {
		return
	}
	w.runtime.StartKnowledgeIndex(ctx, func(st app.KnowledgeIndexStatus) {
		line := st.ProgressLine()
		if st.Done {
			line = ""
		}
		w.launch.mu.Lock()
		changed := w.launch.knowledgeProgress != line
		w.launch.knowledgeProgress = line
		w.launch.mu.Unlock()
		if st.Done && st.Err != nil && !errors.Is(st.Err, context.Canceled) {
			w.RecordActivity("Project indexing failed: " + st.Err.Error())
		}
		if changed || st.Done {
			w.requestRepaint()
		}
	})
}

func (w *Workspace) knowledgeIndexLine(unicode bool) string {
	w.launch.mu.Lock()
	line := w.launch.knowledgeProgress
	w.launch.mu.Unlock()
	if line == "" && w.runtime != nil {
		if st := w.runtime.KnowledgeIndexStatus(); st.Indexing || st.Pending {
			line = st.ProgressLine()
		}
	}
	if !unicode {
		line = strings.ReplaceAll(line, "…", "...")
	}
	return line
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

	// A prefetch that ran out of time or saw initial indexing still in progress
	// is not a final value: re-check live status rather than reporting the
	// knowledge permanently unavailable.
	transient := errors.Is(cachedErr, context.DeadlineExceeded) || errors.Is(cachedErr, context.Canceled) || errors.Is(cachedErr, app.ErrKnowledgeIndexing)
	if cachedRoot != "" && cachedRoot == root && !transient {
		knowledge, err = cachedKnowledge, cachedErr
	} else {
		if root == owner {
			if st := w.runtime.KnowledgeIndexStatus(); st.Pending || st.Indexing {
				line := st.ProgressLine()
				if line == "" {
					line = "Indexing this project..."
				}
				return brief + "\nPROJECT KNOWLEDGE STILL INDEXING: " + line + "\nRead project_context through MCP once indexing finishes; do not substitute remembered or invented architecture.\n"
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		knowledge, err = projectKnowledgeFetch(ctx, w.runtime, root)
	}

	if err != nil {
		if errors.Is(err, app.ErrKnowledgeIndexing) {
			return brief + "\nPROJECT KNOWLEDGE STILL INDEXING: " + err.Error() + "\nRead project_context through MCP once indexing finishes; do not substitute remembered or invented architecture.\n"
		}
		return brief + "\nPROJECT KNOWLEDGE UNAVAILABLE: " + err.Error() + "\nRead project_context successfully before planning; do not substitute remembered or invented architecture.\n"
	}
	if len(brief)+1+len(knowledge) > marshalInlineKnowledgeMaximumBytes {
		return brief + "\nPROJECT KNOWLEDGE NOT INLINED: the current packet (" + strconv.Itoa(len(knowledge)) + " bytes) exceeds the launch budget.\nRead project_context through MCP before planning; do not substitute remembered or invented architecture.\n"
	}
	return brief + "\n" + knowledge
}
