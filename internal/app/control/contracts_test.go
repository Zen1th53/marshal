package control

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestDTOIsolation(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var check func(reflect.Type)
	check = func(typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		if strings.Contains(typ.PkgPath(), "/store") {
			t.Fatalf("store type: %v", typ)
		}
		switch typ.Kind() {
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				check(typ.Field(i).Type)
			}
		case reflect.Slice, reflect.Pointer, reflect.Array:
			check(typ.Elem())
		case reflect.Map:
			check(typ.Key())
			check(typ.Elem())
		}
	}
	for _, v := range []any{Snapshot{}, Command{}, Receipt{}, Event{}, RunDTO{}, TaskDTO{}} {
		check(reflect.TypeOf(v))
	}
}
func TestSubscriptionOverflowResync(t *testing.T) {
	h := NewHub("epoch", 1)
	s, err := h.Subscribe(context.Background(), h.Cursor())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h.Publish("one", 1)
	h.Publish("two", 2)
	e := <-s.Events()
	if e.Kind != ResyncRequired || e.Cursor.Sequence != 2 {
		t.Fatalf("%+v", e)
	}
	if _, ok := <-s.Events(); ok {
		t.Fatal("overflow must disconnect")
	}
	other, _ := h.Subscribe(context.Background(), Cursor{HostEpoch: "old"})
	defer other.Close()
	if e := <-other.Events(); e.Kind != ResyncRequired {
		t.Fatal(e)
	}
}
