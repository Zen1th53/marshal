package trustcontent

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func BenchmarkRendererRepositoryFixture(b *testing.B) {
	renderer := NewRenderer(evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	segments := []Segment{
		{Zone: System, SourceID: "runtime/system", Content: "MARSHAL runtime instructions."},
		{Zone: RepositoryData, SourceID: "repo/README.md", Content: strings.Repeat("repository data ", 256)},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := renderer.Render(context.Background(), segments); err != nil {
			b.Fatal(err)
		}
	}
}
