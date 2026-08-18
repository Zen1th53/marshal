package trustcontent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/evidence"
)

// Renderer produces a deterministic, provider-neutral structural envelope.
// It treats every segment body as a JSON string, so source text cannot close
// or create a trust-zone delimiter.
type Renderer struct {
	sanitizer evidence.ByteSanitizer
}

func NewRenderer(sanitizer evidence.ByteSanitizer) *Renderer {
	return &Renderer{sanitizer: sanitizer}
}

func (r *Renderer) Render(ctx context.Context, segments []Segment) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", ErrRenderFailed
	}
	if r == nil || r.sanitizer == nil {
		return "", ErrRenderFailed
	}
	ordered := append([]Segment(nil), segments...)
	for index := range ordered {
		if ordered[index].Digest == "" {
			ordered[index].Digest = Digest(ordered[index].Content)
		}
		if err := ordered[index].Validate(); err != nil {
			return "", err
		}
		clean, err := r.sanitizer.SanitizeBytes(ctx, []byte(ordered[index].Content))
		if err != nil || string(clean) != ordered[index].Content {
			return "", ErrRenderFailed
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Zone.rank() != ordered[j].Zone.rank() {
			return ordered[i].Zone.rank() < ordered[j].Zone.rank()
		}
		return ordered[i].SourceID < ordered[j].SourceID
	})
	var rendered strings.Builder
	for _, segment := range ordered {
		payload := struct {
			Zone     Zone   `json:"zone"`
			SourceID string `json:"source_id"`
			Digest   string `json:"digest"`
			Content  string `json:"content"`
		}{Zone: segment.Zone, SourceID: segment.SourceID, Digest: segment.Digest, Content: segment.Content}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", ErrRenderFailed
		}
		if rendered.Len() > 0 {
			rendered.WriteByte('\n')
		}
		rendered.WriteString("<marshal-trust-zone zone=")
		rendered.WriteString(string(segment.Zone))
		rendered.WriteString(">\n")
		rendered.Write(encoded)
		rendered.WriteString("\n</marshal-trust-zone>")
	}
	return rendered.String(), nil
}
