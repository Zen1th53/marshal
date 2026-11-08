package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/statepath"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// marshalInlineKnowledgeMaximumBytes bounds the hidden launch prompt
// (protocol, intake and project knowledge). It leaves room for the native
// planning context inside the 64 KiB combined hidden-prompt bound. Larger
// projects get a pointer instead; the full packet stays readable through
// the project_context MCP tool.
const marshalInlineKnowledgeMaximumBytes = 46 << 10

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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	knowledge, err := w.runtime.MarshalProjectContext(ctx, root, "")
	if err != nil {
		return brief + "\nPROJECT KNOWLEDGE UNAVAILABLE: " + err.Error() + "\nRead project_context successfully before planning; do not substitute remembered or invented architecture.\n"
	}
	if len(brief)+1+len(knowledge) > marshalInlineKnowledgeMaximumBytes {
		return brief + "\nPROJECT KNOWLEDGE NOT INLINED: the current packet (" + strconv.Itoa(len(knowledge)) + " bytes) exceeds the launch budget.\nRead project_context through MCP before planning; do not substitute remembered or invented architecture.\n"
	}
	return brief + "\n" + knowledge
}
