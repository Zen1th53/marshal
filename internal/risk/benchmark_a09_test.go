package risk

import (
	"fmt"
	"testing"
)

func BenchmarkAssessStructured(b *testing.B) {
	for _, size := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("cases=%d", size), func(b *testing.B) {
			requests := make([]AssessmentRequest, size)
			for i := range requests {
				requests[i] = AssessmentRequest{
					ID: AssessmentID(fmt.Sprintf("benchmark-%d", i)),
					Descriptor: ToolDescriptor{
						Tool: "git", Action: "push", Resource: "repo:marshal",
						Factors: Factors{ExternalWrite: true, ScopeBreadth: i % 4},
					},
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				request := requests[i%len(requests)]
				if err := request.Validate(); err != nil {
					b.Fatal(err)
				}
				_ = classify(request)
			}
		})
	}
}
