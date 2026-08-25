// Package resources collects a bounded, read-only Community host snapshot.
// It deliberately makes recommendations only; it never changes runtime limits,
// provider policy, sandboxing, or task scheduling.
package resources

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Status string

const (
	StatusOK       Status = "OK"
	StatusWarn     Status = "WARN"
	StatusCritical Status = "CRITICAL"
	StatusUnknown  Status = "UNKNOWN"
)

type CPU struct {
	Model        string `json:"model,omitempty"`
	Logical      int    `json:"logical"`
	Effective    int    `json:"effective"`
	Architecture string `json:"architecture"`
	Source       string `json:"source"`
}
type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapUsedBytes  uint64 `json:"swap_used_bytes"`
	Source         string `json:"source"`
}
type Storage struct {
	Path       string `json:"path"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	Source     string `json:"source"`
}
type Accelerator struct {
	Vendor          string   `json:"vendor"`
	Model           string   `json:"model,omitempty"`
	MemorySemantics string   `json:"memory_semantics"`
	TotalVRAMBytes  *uint64  `json:"total_vram_bytes,omitempty"`
	UsedVRAMBytes   *uint64  `json:"used_vram_bytes,omitempty"`
	TemperatureC    *float64 `json:"temperature_c,omitempty"`
	Source          string   `json:"source"`
	InventorySource string   `json:"inventory_source"`
	TelemetrySource string   `json:"telemetry_source"`
}
type Health struct {
	RAM      Status   `json:"ram"`
	Swap     Status   `json:"swap"`
	Disk     Status   `json:"disk"`
	Thermal  Status   `json:"thermal"`
	Overall  Status   `json:"overall"`
	Warnings []string `json:"warnings,omitempty"`
}
type Model struct {
	Name          string        `json:"name"`
	SizeBytes     *uint64       `json:"size_bytes,omitempty"`
	Family        string        `json:"family,omitempty"`
	Quantization  string        `json:"quantization,omitempty"`
	ModifiedAt    time.Time     `json:"modified_at,omitempty"`
	Compatibility Compatibility `json:"compatibility"`
	Reason        string        `json:"reason"`
}
type Compatibility string

const (
	CompatibilityRecommended    Compatibility = "RECOMMENDED"
	CompatibilityMayFit         Compatibility = "MAY_FIT"
	CompatibilityNotRecommended Compatibility = "NOT_RECOMMENDED"
	CompatibilityUnknown        Compatibility = "UNKNOWN"
)

type Ollama struct {
	Status   string  `json:"status"`
	Endpoint string  `json:"endpoint"`
	Version  string  `json:"version,omitempty"`
	Models   []Model `json:"models"`
	Source   string  `json:"source"`
}
type Recommendation struct {
	Concurrency      int      `json:"concurrency"`
	Profile          string   `json:"profile"`
	Reasons          []string `json:"reasons"`
	RecommendedModel string   `json:"recommended_model,omitempty"`
}
type Snapshot struct {
	CPU            CPU            `json:"cpu"`
	Memory         Memory         `json:"memory"`
	Storage        Storage        `json:"storage"`
	Accelerators   []Accelerator  `json:"accelerators"`
	Ollama         Ollama         `json:"ollama"`
	Health         Health         `json:"health"`
	Recommendation Recommendation `json:"recommendation"`
	CollectedAt    time.Time      `json:"collected_at"`
	Failures       []string       `json:"failures,omitempty"`
}

type Collector struct {
	ProcRoot   string
	SysRoot    string
	HTTPClient *http.Client
	LookPath   func(string) (string, error)
	Run        func(context.Context, string, ...string) (string, error)
	Now        func() time.Time
}

func NewCollector() *Collector {
	return &Collector{ProcRoot: "/proc", SysRoot: "/sys", HTTPClient: &http.Client{Timeout: 750 * time.Millisecond}, LookPath: exec.LookPath, Run: commandOutput, Now: func() time.Time { return time.Now().UTC() }}
}

func (c *Collector) Collect(ctx context.Context, statePath string) Snapshot {
	if c == nil {
		c = NewCollector()
	}
	if c.ProcRoot == "" {
		c.ProcRoot = "/proc"
	}
	if c.SysRoot == "" {
		c.SysRoot = "/sys"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 750 * time.Millisecond}
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	s := Snapshot{CollectedAt: c.Now(), CPU: CPU{Logical: runtime.NumCPU(), Effective: runtime.NumCPU(), Architecture: runtime.GOARCH, Source: "runtime"}, Ollama: Ollama{Status: "NOT_AVAILABLE", Endpoint: "http://127.0.0.1:11434", Source: "local_api"}}
	if cpu, err := readCPU(filepath.Join(c.ProcRoot, "cpuinfo")); err == nil {
		s.CPU.Model = cpu
		s.CPU.Source = "/proc/cpuinfo"
	} else {
		s.Failures = append(s.Failures, "cpu inventory unavailable")
	}
	if mem, err := readMemInfo(filepath.Join(c.ProcRoot, "meminfo")); err == nil {
		s.Memory = mem
		s.Memory.Source = "/proc/meminfo"
	} else {
		s.Failures = append(s.Failures, "memory inventory unavailable")
	}
	if n, limit, ok := cgroupLimits(c.SysRoot); ok {
		if n > 0 && n < s.CPU.Effective {
			s.CPU.Effective = n
			s.Failures = append(s.Failures, "CPU constrained by cgroup")
		}
		if limit > 0 && (s.Memory.TotalBytes == 0 || limit < s.Memory.TotalBytes) {
			s.Memory.TotalBytes = limit
			if s.Memory.AvailableBytes > limit {
				s.Memory.AvailableBytes = limit
			}
			s.Failures = append(s.Failures, "memory constrained by cgroup")
		}
	}
	if storage, err := statStorage(statePath); err == nil {
		s.Storage = storage
	} else {
		s.Failures = append(s.Failures, "storage inventory unavailable")
	}
	s.Accelerators = c.accelerators(ctx)
	thermal, thermalWarning := c.thermal()
	s.Health = assess(s.Memory, s.Storage, thermal, thermalWarning)
	s.Ollama = c.ollama(ctx, s.Memory.AvailableBytes, s.Accelerators)
	s.Recommendation = recommend(s.CPU, s.Memory, s.Health, s.Ollama.Models)
	return s
}

func readCPU(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		p := strings.SplitN(scanner.Text(), ":", 2)
		if len(p) == 2 && (strings.TrimSpace(p[0]) == "model name" || strings.TrimSpace(p[0]) == "Hardware") {
			return strings.TrimSpace(p[1]), nil
		}
	}
	return "", scanner.Err()
}
func readMemInfo(path string) (Memory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, err
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			n, e := strconv.ParseUint(f[1], 10, 64)
			if e == nil {
				values[strings.TrimSuffix(f[0], ":")] = n * 1024
			}
		}
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if available == 0 {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	if total == 0 {
		return Memory{}, fmt.Errorf("missing MemTotal")
	}
	swap := values["SwapTotal"]
	free := values["SwapFree"]
	used := uint64(0)
	if swap >= free {
		used = swap - free
	}
	return Memory{TotalBytes: total, AvailableBytes: available, SwapTotalBytes: swap, SwapUsedBytes: used}, nil
}
func cgroupLimits(sysRoot string) (int, uint64, bool) {
	base := filepath.Join(sysRoot, "fs", "cgroup")
	cpuRaw, cpuErr := os.ReadFile(filepath.Join(base, "cpu.max"))
	memRaw, memErr := os.ReadFile(filepath.Join(base, "memory.max"))
	if cpuErr != nil && memErr != nil {
		return 0, 0, false
	}
	var cpus int
	if f := strings.Fields(string(cpuRaw)); len(f) == 2 && f[0] != "max" {
		quota, _ := strconv.ParseUint(f[0], 10, 64)
		period, _ := strconv.ParseUint(f[1], 10, 64)
		if quota > 0 && period > 0 {
			cpus = int((quota + period - 1) / period)
		}
	}
	var memory uint64
	v := strings.TrimSpace(string(memRaw))
	if v != "" && v != "max" {
		memory, _ = strconv.ParseUint(v, 10, 64)
	}
	return cpus, memory, true
}
func statStorage(path string) (Storage, error) {
	if path == "" {
		path = "."
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Storage{}, err
	}
	size := uint64(st.Bsize)
	return Storage{Path: path, TotalBytes: st.Blocks * size, FreeBytes: st.Bavail * size, Source: "statfs"}, nil
}
func (c *Collector) accelerators(ctx context.Context) []Accelerator {
	result := c.drmAccelerators()
	res := c.enrichNVIDIA(ctx, result)
	if res == nil {
		return make([]Accelerator, 0)
	}
	return res
}

// drmAccelerators is intentionally limited to the generic Linux DRM and hwmon
// interfaces. It does not require privileges or retain a polling process.
func (c *Collector) drmAccelerators() []Accelerator {
	cards, _ := filepath.Glob(filepath.Join(c.SysRoot, "class", "drm", "card[0-9]*"))
	result := make([]Accelerator, 0, len(cards))
	for _, card := range cards {
		deviceDir := filepath.Join(card, "device")
		vendorID, err := readHex(filepath.Join(deviceDir, "vendor"))
		if err != nil {
			continue
		}
		vendor := acceleratorVendor(vendorID)
		if vendor == "" {
			continue
		}
		deviceID, _ := readHex(filepath.Join(deviceDir, "device"))
		a := Accelerator{
			Vendor:          vendor,
			Model:           acceleratorModel(vendor, deviceID, deviceDir),
			MemorySemantics: "SHARED_OR_UNKNOWN",
			Source:          "sysfs-drm",
			InventorySource: "sysfs-drm",
			TelemetrySource: "UNKNOWN",
		}
		if total, err := readUint(filepath.Join(deviceDir, "mem_info_vram_total")); err == nil && total > 0 {
			a.TotalVRAMBytes = &total
			a.MemorySemantics = "DEDICATED"
			if used, err := readUint(filepath.Join(deviceDir, "mem_info_vram_used")); err == nil {
				a.UsedVRAMBytes = &used
			}
			a.TelemetrySource = "sysfs-drm"
		}
		if temp, ok := c.drmTemperature(deviceDir); ok {
			a.TemperatureC = &temp
			a.TelemetrySource = appendSource(a.TelemetrySource, "sysfs-hwmon")
		}
		result = append(result, a)
	}
	return result
}

func acceleratorVendor(id uint64) string {
	switch id {
	case 0x8086:
		return "Intel"
	case 0x1002:
		return "AMD"
	case 0x10de:
		return "NVIDIA"
	default:
		return ""
	}
}

func acceleratorModel(vendor string, device uint64, deviceDir string) string {
	for _, name := range []string{"product_name", "name"} {
		if raw, err := os.ReadFile(filepath.Join(deviceDir, name)); err == nil && strings.TrimSpace(string(raw)) != "" {
			return strings.TrimSpace(string(raw))
		}
	}
	switch vendor {
	case "Intel":
		if device >= 0x5600 && device <= 0x56ff {
			return "Intel Arc Graphics"
		}
		return "Intel Graphics"
	case "AMD":
		return "AMD Radeon Graphics"
	case "NVIDIA":
		return "NVIDIA GPU"
	default:
		return ""
	}
}

func readHex(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(string(raw)), "0x"), 16, 64)
}

func readUint(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
}

func (c *Collector) drmTemperature(deviceDir string) (float64, bool) {
	paths, _ := filepath.Glob(filepath.Join(deviceDir, "hwmon", "hwmon*", "temp*_input"))
	for _, path := range paths {
		if temp, ok := readTemperature(path); ok {
			return temp, true
		}
	}
	// Some drivers expose hwmon only through the generic class directory. A
	// direct device link keeps this association bounded to the DRM card.
	hwmons, _ := filepath.Glob(filepath.Join(c.SysRoot, "class", "hwmon", "hwmon*"))
	for _, hwmon := range hwmons {
		linkedDevice, err := filepath.EvalSymlinks(filepath.Join(hwmon, "device"))
		if err != nil || linkedDevice != deviceDir {
			continue
		}
		paths, _ := filepath.Glob(filepath.Join(hwmon, "temp*_input"))
		for _, path := range paths {
			if temp, ok := readTemperature(path); ok {
				return temp, true
			}
		}
	}
	return 0, false
}

func readTemperature(path string) (float64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	temp, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil || temp < 0 {
		return 0, false
	}
	if temp > 1000 {
		temp /= 1000
	}
	return temp, true
}

func appendSource(current, source string) string {
	if current == "" || current == "UNKNOWN" {
		return source
	}
	if strings.Contains(current, source) {
		return current
	}
	return current + "+" + source
}

func (c *Collector) enrichNVIDIA(ctx context.Context, result []Accelerator) []Accelerator {
	if c.LookPath == nil || c.Run == nil {
		return result
	}
	bin, err := c.LookPath("nvidia-smi")
	if err != nil {
		return result
	}
	out, err := run(ctx, c.Run, bin, "--query-gpu=name,memory.total,memory.used,temperature.gpu", "--format=csv,noheader,nounits")
	if err != nil {
		return result
	}
	nvidiaIndex := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		p := strings.Split(line, ",")
		if len(p) != 4 {
			continue
		}
		total, e1 := strconv.ParseUint(strings.TrimSpace(p[1]), 10, 64)
		used, e2 := strconv.ParseUint(strings.TrimSpace(p[2]), 10, 64)
		temp, e3 := strconv.ParseFloat(strings.TrimSpace(p[3]), 64)
		var a *Accelerator
		for i := nvidiaIndex; i < len(result); i++ {
			if result[i].Vendor == "NVIDIA" {
				a = &result[i]
				nvidiaIndex = i + 1
				break
			}
		}
		if a == nil {
			result = append(result, Accelerator{Vendor: "NVIDIA", MemorySemantics: "SHARED_OR_UNKNOWN", Source: "nvidia-smi", InventorySource: "nvidia-smi", TelemetrySource: "UNKNOWN"})
			a = &result[len(result)-1]
		}
		a.Model = strings.TrimSpace(p[0])
		a.Source = appendSource(a.Source, "nvidia-smi")
		a.InventorySource = appendSource(a.InventorySource, "nvidia-smi")
		if e1 == nil && e2 == nil {
			total *= 1024 * 1024
			used *= 1024 * 1024
			a.TotalVRAMBytes, a.UsedVRAMBytes = &total, &used
			a.MemorySemantics = "DEDICATED"
			a.TelemetrySource = appendSource(a.TelemetrySource, "nvidia-smi")
		}
		if e3 == nil {
			a.TemperatureC = &temp
			a.TelemetrySource = appendSource(a.TelemetrySource, "nvidia-smi")
		}
	}
	return result
}
func (c *Collector) thermal() (Status, string) {
	paths, _ := filepath.Glob(filepath.Join(c.SysRoot, "class", "thermal", "thermal_zone*", "temp"))
	max := float64(0)
	known := false
	for _, p := range paths {
		raw, e := os.ReadFile(p)
		if e != nil {
			continue
		}
		n, e := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if e != nil || n < 0 {
			continue
		}
		if n > 1000 {
			n /= 1000
		}
		if n > max {
			max = n
		}
		known = true
	}
	if !known {
		return StatusUnknown, ""
	}
	if max >= 90 {
		return StatusCritical, "thermal sensor reports critical temperature"
	}
	if max >= 80 {
		return StatusWarn, "thermal sensor reports elevated temperature"
	}
	return StatusOK, ""
}
func assess(m Memory, d Storage, thermal Status, thermalWarning string) Health {
	h := Health{RAM: StatusUnknown, Swap: StatusUnknown, Disk: StatusUnknown, Thermal: thermal, Overall: StatusUnknown}
	if m.TotalBytes > 0 {
		ratio := float64(m.AvailableBytes) / float64(m.TotalBytes)
		if ratio < .05 {
			h.RAM = StatusCritical
			h.Warnings = append(h.Warnings, "very low available RAM")
		} else if ratio < .10 {
			h.RAM = StatusWarn
			h.Warnings = append(h.Warnings, "low available RAM")
		} else {
			h.RAM = StatusOK
		}
	}
	if m.SwapTotalBytes > 0 {
		ratio := float64(m.SwapUsedBytes) / float64(m.SwapTotalBytes)
		if ratio >= .80 {
			h.Swap = StatusCritical
			h.Warnings = append(h.Warnings, "high swap pressure")
		} else if ratio >= .50 {
			h.Swap = StatusWarn
			h.Warnings = append(h.Warnings, "elevated swap pressure")
		} else {
			h.Swap = StatusOK
		}
	}
	if d.TotalBytes > 0 {
		ratio := float64(d.FreeBytes) / float64(d.TotalBytes)
		if ratio < .05 {
			h.Disk = StatusCritical
			h.Warnings = append(h.Warnings, "very low disk space")
		} else if ratio < .10 {
			h.Disk = StatusWarn
			h.Warnings = append(h.Warnings, "low disk space")
		} else {
			h.Disk = StatusOK
		}
	}
	if thermalWarning != "" {
		h.Warnings = append(h.Warnings, thermalWarning)
	}
	for _, x := range []Status{h.RAM, h.Swap, h.Disk, h.Thermal} {
		if x == StatusCritical {
			h.Overall = StatusCritical
			break
		}
		if x == StatusWarn && h.Overall != StatusCritical {
			h.Overall = StatusWarn
		}
		if x == StatusOK && h.Overall == StatusUnknown {
			h.Overall = StatusOK
		}
	}
	return h
}
func (c *Collector) ollama(ctx context.Context, available uint64, accelerators []Accelerator) Ollama {
	o := Ollama{Status: "NOT_AVAILABLE", Endpoint: "http://127.0.0.1:11434", Source: "local_api", Models: make([]Model, 0)}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Endpoint+"/api/tags", nil)
	if err != nil {
		return o
	}
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return o
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return o
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return o
	}
	var payload struct {
		Models []struct {
			Name       string    `json:"name"`
			Size       uint64    `json:"size"`
			ModifiedAt time.Time `json:"modified_at"`
			Details    struct {
				Family       string `json:"family"`
				Quantization string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return o
	}
	o.Status = "DETECTED"
	o.Models = make([]Model, 0, len(payload.Models))
	for _, raw := range payload.Models {
		m := Model{Name: raw.Name, Family: raw.Details.Family, Quantization: raw.Details.Quantization, ModifiedAt: raw.ModifiedAt}
		if raw.Size > 0 {
			v := raw.Size
			m.SizeBytes = &v
		}
		m.Compatibility, m.Reason = fit(m, available, accelerators)
		o.Models = append(o.Models, m)
	}
	return o
}
func fit(m Model, available uint64, accelerators []Accelerator) (Compatibility, string) {
	if m.SizeBytes == nil {
		return CompatibilityUnknown, "model size is unavailable"
	}
	budget := available * 55 / 100
	for _, a := range accelerators {
		if a.TotalVRAMBytes != nil {
			free := *a.TotalVRAMBytes
			if a.UsedVRAMBytes != nil && free >= *a.UsedVRAMBytes {
				free -= *a.UsedVRAMBytes
			}
			if free > budget {
				budget = free * 55 / 100
			}
		}
	}
	if budget == 0 {
		return CompatibilityUnknown, "available memory is unavailable"
	}
	if *m.SizeBytes <= budget {
		return CompatibilityRecommended, "model footprint fits conservative available-memory reserve"
	}
	if *m.SizeBytes <= available*80/100 {
		return CompatibilityMayFit, "model may fit but leaves limited memory headroom"
	}
	return CompatibilityNotRecommended, "model footprint exceeds conservative available-memory reserve"
}
func recommend(cpu CPU, m Memory, h Health, models []Model) Recommendation {
	n := cpu.Effective / 2
	if n < 1 {
		n = 1
	}
	if m.AvailableBytes > 0 {
		byRAM := int(m.AvailableBytes / (2 << 30))
		if byRAM < 1 {
			byRAM = 1
		}
		if byRAM < n {
			n = byRAM
		}
	}
	if h.Overall == StatusCritical {
		n = 1
	}
	r := Recommendation{Concurrency: n, Profile: "Safe", Reasons: []string{fmt.Sprintf("%d effective CPUs", cpu.Effective), "OS and MARSHAL memory reserve retained"}}
	if h.Overall == StatusCritical {
		r.Reasons = append(r.Reasons, "critical pressure limits recommendation to one task")
	}
	for _, m := range models {
		if m.Compatibility == CompatibilityRecommended {
			r.RecommendedModel = m.Name
			break
		}
	}
	return r
}
func commandOutput(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.Output()
	return string(out), err
}
func run(ctx context.Context, fn func(context.Context, string, ...string) (string, error), path string, args ...string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	return fn(callCtx, path, args...)
}
