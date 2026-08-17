package cell

import (
	"fmt"
	"testing"
)

func BenchmarkA09SpecValidation(b *testing.B) {
	for _, cases := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("cases=%d", cases), func(b *testing.B) {
			specs := make([]Spec, cases)
			for i := range specs {
				specs[i] = Spec{
					TaskID:      TaskID(fmt.Sprintf("TASK-bench-%d", i)),
					Workspace:   fmt.Sprintf("/tmp/cell-bench-%d", i),
					Backend:     BackendNative,
					CPUQuota:    1,
					MemoryBytes: 1024,
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, spec := range specs {
					if err := spec.Validate(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
