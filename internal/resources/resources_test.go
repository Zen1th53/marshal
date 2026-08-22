package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadMemInfoAndCgroupLimits(t *testing.T) {
	dir := t.TempDir()
	mem := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(mem, []byte("MemTotal:       16384 kB\nMemAvailable:   4096 kB\nSwapTotal:      2048 kB\nSwapFree:       1024 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readMemInfo(mem)
	if err != nil || got.TotalBytes != 16384*1024 || got.AvailableBytes != 4096*1024 || got.SwapUsedBytes != 1024*1024 {
		t.Fatalf("memory=%+v err=%v", got, err)
	}
	cgroup := filepath.Join(dir, "fs", "cgroup")
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cgroup, "cpu.max"), []byte("150000 100000\n"), 0o600)
	_ = os.WriteFile(filepath.Join(cgroup, "memory.max"), []byte("1048576\n"), 0o600)
	cpu, limit, ok := cgroupLimits(dir)
	if !ok || cpu != 2 || limit != 1048576 {
		t.Fatalf("cgroup cpu=%d memory=%d ok=%t", cpu, limit, ok)
	}
}

func TestCompatibilityAndSafeConcurrencyAreConservative(t *testing.T) {
	size := uint64(2 << 30)
	good, _ := fit(Model{SizeBytes: &size}, 8<<30, nil)
	if good != CompatibilityRecommended {
		t.Fatalf("good fit=%s", good)
	}
	large := uint64(9 << 30)
	bad, _ := fit(Model{SizeBytes: &large}, 8<<30, nil)
	if bad != CompatibilityNotRecommended {
		t.Fatalf("bad fit=%s", bad)
	}
	unknown, _ := fit(Model{}, 8<<30, nil)
	if unknown != CompatibilityUnknown {
		t.Fatalf("unknown fit=%s", unknown)
	}
	r := recommend(CPU{Effective: 8}, Memory{AvailableBytes: 3 << 30}, Health{Overall: StatusOK}, nil)
	if r.Concurrency != 1 {
		t.Fatalf("concurrency=%d", r.Concurrency)
	}
	critical := recommend(CPU{Effective: 32}, Memory{AvailableBytes: 64 << 30}, Health{Overall: StatusCritical}, nil)
	if critical.Concurrency != 1 {
		t.Fatalf("critical concurrency=%d", critical.Concurrency)
	}
}

func TestOllamaDiscoveryIsLocalAndMalformedResponseIsSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"safe-model","size":2147483648,"details":{"family":"llama","quantization_level":"Q4"}}]}`))
	}))
	defer server.Close()
	c := NewCollector()
	c.HTTPClient = server.Client()
	// The collector never takes endpoint input from model data. Redirecting its
	// transport keeps the test local while asserting the fixed local API path.
	c.HTTPClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	o := c.ollama(context.Background(), 8<<30, nil)
	if o.Status != "DETECTED" || len(o.Models) != 1 || o.Models[0].Compatibility != CompatibilityRecommended {
		t.Fatalf("ollama=%+v", o)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnknownThermalIsNotZero(t *testing.T) {
	c := NewCollector()
	c.SysRoot = t.TempDir()
	state, warning := c.thermal()
	if state != StatusUnknown || warning != "" {
		t.Fatalf("thermal=%s warning=%q", state, warning)
	}
}

func TestCollectCPUOnlyFixtureUsesCgroupAndNeverNeedsVendorTools(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	sys := filepath.Join(root, "sys")
	if err := os.MkdirAll(filepath.Join(sys, "fs", "cgroup"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(proc, "cpuinfo"), []byte("model name\t: Fixture CPU\n"), 0o600)
	_ = os.WriteFile(filepath.Join(proc, "meminfo"), []byte("MemTotal: 8388608 kB\nMemAvailable: 6291456 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"), 0o600)
	_ = os.WriteFile(filepath.Join(sys, "fs", "cgroup", "cpu.max"), []byte("200000 100000"), 0o600)
	_ = os.WriteFile(filepath.Join(sys, "fs", "cgroup", "memory.max"), []byte("4294967296"), 0o600)
	c := NewCollector()
	c.ProcRoot = proc
	c.SysRoot = sys
	c.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	c.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, os.ErrNotExist })}
	s := c.Collect(context.Background(), root)
	if s.CPU.Effective != 2 || s.Memory.TotalBytes != 4294967296 || len(s.Accelerators) != 0 || s.Ollama.Status != "NOT_AVAILABLE" {
		t.Fatalf("snapshot=%+v", s)
	}
}
