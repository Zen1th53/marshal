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

func TestAssessAllUnavailableIsUnknown(t *testing.T) {
	health := assess(Memory{}, Storage{}, StatusUnknown, "")
	if health.Overall != StatusUnknown {
		t.Fatalf("overall=%s, want UNKNOWN", health.Overall)
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

func TestCollectDRMFixturesInventoryIntelAMDAndNVIDIA(t *testing.T) {
	root := t.TempDir()
	proc, sys := fixtureRoots(t, root)
	writeDRMCard(t, sys, "card0", "0x8086", "0x56a0", "", "") // Arc iGPU-like shared-memory fixture.
	writeDRMCard(t, sys, "card1", "0x1002", "0x73bf", "AMD Radeon RX 6800 XT", "8589934592")
	writeDRMCard(t, sys, "card2", "0x10de", "0x2684", "", "")
	amdDevice := filepath.Join(sys, "class", "drm", "card1", "device")
	writeFile(t, filepath.Join(amdDevice, "mem_info_vram_used"), "2147483648")
	hwmon := filepath.Join(sys, "class", "hwmon", "hwmon0")
	if err := os.MkdirAll(hwmon, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(amdDevice, filepath.Join(hwmon, "device")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(hwmon, "temp1_input"), "65000")
	c := NewCollector()
	c.ProcRoot, c.SysRoot = proc, sys
	c.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	c.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, os.ErrNotExist })}
	s := c.Collect(context.Background(), root)
	if len(s.Accelerators) != 3 {
		t.Fatalf("accelerators=%+v", s.Accelerators)
	}
	intel, amd, nvidia := s.Accelerators[0], s.Accelerators[1], s.Accelerators[2]
	if intel.Vendor != "Intel" || intel.Model != "Intel Arc Graphics" || intel.TotalVRAMBytes != nil || intel.MemorySemantics != "SHARED_OR_UNKNOWN" || intel.TelemetrySource != "UNKNOWN" {
		t.Fatalf("intel=%+v", intel)
	}
	if amd.Vendor != "AMD" || amd.Model != "AMD Radeon RX 6800 XT" || amd.TotalVRAMBytes == nil || *amd.TotalVRAMBytes != 8589934592 || amd.TemperatureC == nil || *amd.TemperatureC != 65 || amd.TelemetrySource != "sysfs-drm+sysfs-hwmon" {
		t.Fatalf("amd=%+v", amd)
	}
	if nvidia.Vendor != "NVIDIA" || nvidia.Model != "NVIDIA GPU" || nvidia.TelemetrySource != "UNKNOWN" {
		t.Fatalf("nvidia=%+v", nvidia)
	}
}

func TestNVIDIASMIEnrichesDRMInventory(t *testing.T) {
	root := t.TempDir()
	proc, sys := fixtureRoots(t, root)
	writeDRMCard(t, sys, "card0", "0x10de", "0x2684", "", "")
	c := NewCollector()
	c.ProcRoot, c.SysRoot = proc, sys
	c.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	c.Run = func(context.Context, string, ...string) (string, error) {
		return "NVIDIA GeForce RTX 4090, 24564, 1024, 48\n", nil
	}
	c.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, os.ErrNotExist })}
	s := c.Collect(context.Background(), root)
	if len(s.Accelerators) != 1 {
		t.Fatalf("accelerators=%+v", s.Accelerators)
	}
	a := s.Accelerators[0]
	if a.Model != "NVIDIA GeForce RTX 4090" || a.TotalVRAMBytes == nil || *a.TotalVRAMBytes != 24564*1024*1024 || a.TelemetrySource != "nvidia-smi" || a.InventorySource != "sysfs-drm+nvidia-smi" {
		t.Fatalf("nvidia=%+v", a)
	}
}

func TestMalformedDRMAndMissingSensorsAreUnknownAndSafe(t *testing.T) {
	root := t.TempDir()
	proc, sys := fixtureRoots(t, root)
	writeFile(t, filepath.Join(sys, "class", "drm", "card0", "device", "vendor"), "not-hex")
	writeDRMCard(t, sys, "card1", "0x1002", "0x73bf", "", "")
	writeFile(t, filepath.Join(sys, "class", "drm", "card1", "device", "hwmon", "hwmon0", "temp1_input"), "not-a-temperature")
	c := NewCollector()
	c.ProcRoot, c.SysRoot = proc, sys
	c.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	c.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, os.ErrNotExist })}
	s := c.Collect(context.Background(), root)
	if len(s.Accelerators) != 1 || s.Accelerators[0].Vendor != "AMD" || s.Accelerators[0].TelemetrySource != "UNKNOWN" || s.Accelerators[0].TemperatureC != nil {
		t.Fatalf("snapshot=%+v", s)
	}
}

func fixtureRoots(t *testing.T, root string) (string, string) {
	t.Helper()
	proc, sys := filepath.Join(root, "proc"), filepath.Join(root, "sys")
	if err := os.MkdirAll(filepath.Join(sys, "fs", "cgroup"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(proc, "cpuinfo"), "model name: Fixture CPU\n")
	writeFile(t, filepath.Join(proc, "meminfo"), "MemTotal: 8388608 kB\nMemAvailable: 6291456 kB\n")
	return proc, sys
}

func writeDRMCard(t *testing.T, sys, card, vendor, device, model, total string) {
	t.Helper()
	base := filepath.Join(sys, "class", "drm", card, "device")
	writeFile(t, filepath.Join(base, "vendor"), vendor)
	writeFile(t, filepath.Join(base, "device"), device)
	if model != "" {
		writeFile(t, filepath.Join(base, "product_name"), model)
	}
	if total != "" {
		writeFile(t, filepath.Join(base, "mem_info_vram_total"), total)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
