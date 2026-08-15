package policytest

import "testing"

func FuzzParseJSONSuiteNoPanic(f *testing.F) {
	f.Add([]byte(`{"id":"suite","cases":[]}`))
	f.Add([]byte(`{"id":"suite","cases":[{"id":"case","name":"case","given":{},"when":{},"expect":{}}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseJSONSuite(data)
	})
}
