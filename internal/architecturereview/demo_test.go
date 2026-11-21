package architecturereview

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"testing"
)

// demoFixtures are the drawings the fixture demo serves, in port order.
func demoFixtures() []struct {
	name string
	raw  json.RawMessage
} {
	return []struct {
		name string
		raw  json.RawMessage
	}{
		{"marshal-17-all-new", marshalShapeFixture(false)},
		{"marshal-17-mixed", marshalShapeFixture(true)},
		{"generated-80-300", generatedFixture(80, 300)},
		{"small-5", generatedFixture(5, 6)},
	}
}

// TestArchitectureDemo serves the real page over fixtures without a live run:
//
//	MARSHAL_ARCH_DEMO=8765 go test ./internal/architecturereview -run TestArchitectureDemo -count=1 -timeout 0 -v
//
// Each fixture gets its own loopback port starting at the given one; the URLs
// are printed. Stop with Ctrl-C.
func TestArchitectureDemo(t *testing.T) {
	value := os.Getenv("MARSHAL_ARCH_DEMO")
	if value == "" {
		t.Skip("set MARSHAL_ARCH_DEMO to a base port to serve the fixture demo")
	}
	base, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	const token = "demo-token-0123456789abcdef"
	// The derived code levels of the Advanced view read this repository.
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for i, fixture := range demoFixtures() {
		r, err := New(Config{Context: t.Context(), Project: "demo", ArchitectureID: "architecture-demo", StateDir: filepath.Join(t.TempDir(), fixture.name), Proposal: fixture.raw, CodeRoot: root})
		if err != nil {
			t.Fatalf("%s: %v", fixture.name, err)
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+i))
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = http.Serve(listener, r.Handler(token)) }()
		fmt.Printf("%-20s http://%s/?token=%s\n", fixture.name, listener.Addr(), token)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	select {
	case <-stop:
	case <-t.Context().Done():
	}
}

// Every demo fixture opens as a review and validates.
func TestDemoFixturesOpen(t *testing.T) {
	for _, fixture := range demoFixtures() {
		if _, err := New(Config{Context: t.Context(), Project: "demo", ArchitectureID: "architecture-demo", StateDir: filepath.Join(t.TempDir(), fixture.name), Proposal: fixture.raw}); err != nil {
			t.Errorf("%s: %v", fixture.name, err)
		}
	}
}
