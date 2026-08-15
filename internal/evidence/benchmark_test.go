package evidence

import (
	"context"
	"strconv"
	"testing"
)

func BenchmarkCanonicalDigest(b *testing.B) {
	for _, fields := range []int{4, 64, 256} {
		b.Run(strconv.Itoa(fields)+"_fields", func(b *testing.B) {
			metadata := make(map[string]string, fields)
			for i := 0; i < fields; i++ {
				metadata["key-"+strconv.Itoa(i)] = "value-" + strconv.Itoa(i)
			}
			b.ReportMetric(float64(fields), "metadata_fields")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := CanonicalDigest(NodeTypeClaim, metadata); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkStrictSanitizer(b *testing.B) {
	for _, fields := range []int{4, 64, 256} {
		b.Run(strconv.Itoa(fields)+"_fields", func(b *testing.B) {
			metadata := make(map[string]string, fields)
			for i := 0; i < fields; i++ {
				metadata["key-"+strconv.Itoa(i)] = "value-" + strconv.Itoa(i)
			}
			node := Node{ID: "BENCH", Type: NodeTypeClaim, Metadata: metadata}
			sanitizer := NewStrictSanitizer(SanitizerConfig{})
			b.ReportMetric(float64(fields), "metadata_fields")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := sanitizer.SanitizeNode(context.Background(), node); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
