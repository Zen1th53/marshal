package router

import (
	"context"
	"reflect"
	"testing"
)

func TestRankAdvancedFirstEqualsRouteAdvanced(t *testing.T) {
	r := NewRouter()
	req := RouteRequest{RequiredCapabilities: []string{"code"}, MinContext: 1000}
	ranked, err := r.RankAdvanced(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	best, err := r.RouteAdvanced(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) < 2 || !reflect.DeepEqual(ranked[0], *best) {
		t.Fatalf("ranked=%+v best=%+v", ranked, best)
	}
}
