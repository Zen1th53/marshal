package secrets

import (
	"context"
	"errors"
	"testing"
)

func TestEnvironmentProviderResolvesOnlyValidReferenceWithoutLogging(t *testing.T) {
	provider := EnvironmentProvider{Lookup: func(name string) (string, bool) {
		if name == "API_TOKEN" {
			return "MARSHAL_TEST_SECRET_T21_A05", true
		}
		return "", false
	}}
	value, err := provider.Resolve(context.Background(), Ref{Provider: "env", Name: "API_TOKEN", Version: "v1"})
	if err != nil || string(value) != "MARSHAL_TEST_SECRET_T21_A05" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if _, err := provider.Resolve(context.Background(), Ref{Provider: "env", Name: "missing", Version: "v1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error=%v, want ErrNotFound", err)
	}
	if _, err := provider.Resolve(context.Background(), Ref{Provider: "env", Name: "bad-name", Version: "v1"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("invalid name error=%v, want ErrDenied", err)
	}
}

func TestEnvironmentProviderHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := EnvironmentProvider{Lookup: func(string) (string, bool) { return "secret", true }}
	if _, err := provider.Resolve(ctx, Ref{Provider: "env", Name: "TOKEN", Version: "v1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v, want context.Canceled", err)
	}
}

func BenchmarkEnvironmentProviderResolve(b *testing.B) {
	provider := EnvironmentProvider{Lookup: func(name string) (string, bool) { return name, true }}
	ref := Ref{Provider: "env", Name: "API_TOKEN", Version: "v1"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := provider.Resolve(context.Background(), ref); err != nil {
			b.Fatal(err)
		}
	}
}
