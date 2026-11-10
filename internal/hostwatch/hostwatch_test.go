package hostwatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSamplerReadNormal(t *testing.T) {
	sampler := &Sampler{
		SysRoot:      "testdata/sys",
		ProcRoot:     "testdata/proc",
		DiskMounts:   []string{t.TempDir()},
		NetInterface: "eth0",
		NvidiaSmiBin: "nonexistent-nvidia-smi-xyz",
	}

	sample, err := sampler.Read(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CPU tests
	if sample.CPUModelName != "Test Virtual CPU @ 2.50GHz" {
		t.Errorf("expected CPUModelName %q, got %q", "Test Virtual CPU @ 2.50GHz", sample.CPUModelName)
	}
	if sample.CPUCores != 2 || sample.CPUThreads != 2 {
		t.Errorf("expected 2 cores / 2 threads, got %d / %d", sample.CPUCores, sample.CPUThreads)
	}
	if !sample.CPULoadAvgKnown || sample.CPULoadAvg1 != 0.45 || sample.CPULoadAvg5 != 0.55 || sample.CPULoadAvg15 != 0.65 {
		t.Errorf("unexpected load avg: %v (known: %v)", sample.CPULoadAvg1, sample.CPULoadAvgKnown)
	}
	if !sample.CPUAverageFreqMHzKnown || sample.CPUAverageFreqMHz != 2500.0 {
		t.Errorf("expected average freq 2500.0 MHz, got %v (known: %v)", sample.CPUAverageFreqMHz, sample.CPUAverageFreqMHzKnown)
	}
	if !sample.CPUPackageThrottleKnown || sample.CPUPackageThrottleCount != 42 {
		t.Errorf("expected package throttle count 42, got %v", sample.CPUPackageThrottleCount)
	}
	if !sample.CPUTicksKnown || sample.CPUTicksTotal != 10000 || sample.CPUTicksIdle != 8500 {
		t.Errorf("unexpected CPU ticks: total %d idle %d (known: %v)", sample.CPUTicksTotal, sample.CPUTicksIdle, sample.CPUTicksKnown)
	}
	if !sample.CPUPackageTempCKnown || sample.CPUPackageTempC != 65.0 {
		t.Errorf("expected CPU package temp 65.0 C, got %v", sample.CPUPackageTempC)
	}
	if len(sample.CPUCoreTempC) != 2 || sample.CPUCoreTempC["Core 0"] != 61.0 || sample.CPUCoreTempC["Core 1"] != 59.0 {
		t.Errorf("unexpected core temps: %+v", sample.CPUCoreTempC)
	}

	// Memory tests
	if !sample.MemTotalBytesKnown || sample.MemTotalBytes != 16384000*1024 {
		t.Errorf("unexpected MemTotal: %v", sample.MemTotalBytes)
	}
	if !sample.MemAvailableBytesKnown || sample.MemAvailableBytes != 8192000*1024 {
		t.Errorf("unexpected MemAvailable: %v", sample.MemAvailableBytes)
	}
	if !sample.MemCachedBytesKnown || sample.MemCachedBytes != 2048000*1024 {
		t.Errorf("unexpected MemCached: %v", sample.MemCachedBytes)
	}
	if !sample.SwapTotalBytesKnown || sample.SwapTotalBytes != 4096000*1024 {
		t.Errorf("unexpected SwapTotal: %v", sample.SwapTotalBytes)
	}
	if !sample.SwapFreeBytesKnown || sample.SwapFreeBytes != 2048000*1024 {
		t.Errorf("unexpected SwapFree: %v", sample.SwapFreeBytes)
	}

	// Pressure tests
	if !sample.MemPressureSomeKnown || sample.MemPressureSomeAvg10Percent != 1.23 {
		t.Errorf("unexpected Mem pressure some: %v", sample.MemPressureSomeAvg10Percent)
	}
	if !sample.MemPressureFullKnown || sample.MemPressureFullAvg10Percent != 0.45 {
		t.Errorf("unexpected Mem pressure full: %v", sample.MemPressureFullAvg10Percent)
	}
	if !sample.CPUPressureSomeKnown || sample.CPUPressureSomeAvg10Percent != 2.34 {
		t.Errorf("unexpected CPU pressure some: %v", sample.CPUPressureSomeAvg10Percent)
	}
	if !sample.CPUPressureFullKnown || sample.CPUPressureFullAvg10Percent != 0.00 {
		t.Errorf("unexpected CPU pressure full: %v", sample.CPUPressureFullAvg10Percent)
	}

	// Disk & NVMe tests
	if len(sample.DiskMounts) != 1 || !sample.DiskMounts[0].Known || sample.DiskMounts[0].TotalBytes == 0 {
		t.Errorf("unexpected disk mounts: %+v", sample.DiskMounts)
	}
	if len(sample.NVMeTempC) != 1 {
		t.Errorf("expected 1 NVMe temp, got %+v", sample.NVMeTempC)
	} else {
		for _, temp := range sample.NVMeTempC {
			if temp != 45.0 {
				t.Errorf("expected NVMe temp 45.0 C, got %v", temp)
			}
		}
	}

	// Network tests
	if !sample.NetRxBytesKnown || sample.NetRxBytes != 12345678 {
		t.Errorf("unexpected NetRxBytes: %v", sample.NetRxBytes)
	}
	if !sample.NetTxBytesKnown || sample.NetTxBytes != 87654321 {
		t.Errorf("unexpected NetTxBytes: %v", sample.NetTxBytes)
	}

	// GPU absent (nonexistent binary)
	if len(sample.GPUs) != 0 {
		t.Errorf("expected 0 GPUs, got %+v", sample.GPUs)
	}
}

func TestSamplerMissingHwmon(t *testing.T) {
	tempDir := t.TempDir()
	sampler := &Sampler{
		SysRoot:    tempDir,
		ProcRoot:   "testdata/proc",
		DiskMounts: []string{tempDir},
	}

	sample, err := sampler.Read(context.Background())
	if err != nil {
		t.Fatalf("read failed with missing hwmon: %v", err)
	}

	if sample.CPUPackageTempCKnown {
		t.Errorf("expected CPU package temp to be unknown")
	}
	if len(sample.CPUCoreTempC) != 0 {
		t.Errorf("expected empty core temps, got %+v", sample.CPUCoreTempC)
	}
	if len(sample.NVMeTempC) != 0 {
		t.Errorf("expected empty NVMe temps, got %+v", sample.NVMeTempC)
	}
}

func TestSamplerMissingPressureFiles(t *testing.T) {
	tempProc := t.TempDir()
	// Copy meminfo and loadavg only, no pressure directory
	memData, _ := os.ReadFile("testdata/proc/meminfo")
	_ = os.WriteFile(filepath.Join(tempProc, "meminfo"), memData, 0644)
	loadData, _ := os.ReadFile("testdata/proc/loadavg")
	_ = os.WriteFile(filepath.Join(tempProc, "loadavg"), loadData, 0644)

	sampler := &Sampler{
		SysRoot:  t.TempDir(),
		ProcRoot: tempProc,
	}

	sample, err := sampler.Read(context.Background())
	if err != nil {
		t.Fatalf("read failed with missing pressure files: %v", err)
	}

	if sample.MemPressureSomeKnown || sample.MemPressureFullKnown {
		t.Errorf("expected memory pressure to be unknown")
	}
	if sample.CPUPressureSomeKnown || sample.CPUPressureFullKnown {
		t.Errorf("expected CPU pressure to be unknown")
	}
	if sample.CPUTicksKnown {
		t.Errorf("expected CPU ticks to be unknown without /proc/stat")
	}
}

func TestSamplerStatfsOnTempDir(t *testing.T) {
	tempDir := t.TempDir()
	sampler := &Sampler{
		SysRoot:    t.TempDir(),
		ProcRoot:   t.TempDir(),
		DiskMounts: []string{tempDir, "/nonexistent/invalid/mount/path"},
	}

	sample, err := sampler.Read(context.Background())
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}

	if len(sample.DiskMounts) != 2 {
		t.Fatalf("expected 2 mount results, got %d", len(sample.DiskMounts))
	}

	if !sample.DiskMounts[0].Known || sample.DiskMounts[0].TotalBytes == 0 {
		t.Errorf("expected valid statfs for temp dir: %+v", sample.DiskMounts[0])
	}
	if sample.DiskMounts[1].Known {
		t.Errorf("expected unknown statfs for nonexistent mount: %+v", sample.DiskMounts[1])
	}
}

func TestSamplerNvidiaSmiAbsent(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	sampler := &Sampler{
		SysRoot:      t.TempDir(),
		ProcRoot:     t.TempDir(),
		NvidiaSmiBin: "nvidia-smi",
	}

	sample, err := sampler.Read(context.Background())
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}

	if len(sample.GPUs) != 0 {
		t.Errorf("expected 0 GPUs when nvidia-smi absent, got %d", len(sample.GPUs))
	}
}

func TestPolicyHysteresis(t *testing.T) {
	policy := Policy{
		PackageTempTriggerC: 90.0,
		PackageTempReleaseC: 85.0,
	}

	now := time.Now()
	var state State
	var actions []Action

	// 1. Below trigger threshold (88 C): no action
	s1 := Sample{
		At:                   now,
		CPUPackageTempC:      88.0,
		CPUPackageTempCKnown: true,
	}
	state, actions = policy.Evaluate([]Sample{s1}, state)
	if state.ActiveRules["thermal_package_temp"] || len(actions) != 0 {
		t.Errorf("expected inactive rule, got state=%+v, actions=%+v", state, actions)
	}

	// 2. Crosses trigger threshold (92 C): trigger action fires
	s2 := Sample{
		At:                   now.Add(10 * time.Second),
		CPUPackageTempC:      92.0,
		CPUPackageTempCKnown: true,
	}
	state, actions = policy.Evaluate([]Sample{s2}, state)
	if !state.ActiveRules["thermal_package_temp"] {
		t.Errorf("expected active thermal rule")
	}
	if len(actions) != 1 || actions[0].Kind != ActionTrigger {
		t.Errorf("expected 1 trigger action, got %+v", actions)
	}

	// 3. Stays between trigger and release (87 C): stays active, no new actions
	s3 := Sample{
		At:                   now.Add(20 * time.Second),
		CPUPackageTempC:      87.0,
		CPUPackageTempCKnown: true,
	}
	state, actions = policy.Evaluate([]Sample{s3}, state)
	if !state.ActiveRules["thermal_package_temp"] {
		t.Errorf("expected rule to stay active at 87 C")
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions while remaining active, got %+v", actions)
	}

	// 4. Drops below release threshold (<85 C, e.g. 84 C): release action fires
	s4 := Sample{
		At:                   now.Add(30 * time.Second),
		CPUPackageTempC:      84.0,
		CPUPackageTempCKnown: true,
	}
	state, actions = policy.Evaluate([]Sample{s4}, state)
	if state.ActiveRules["thermal_package_temp"] {
		t.Errorf("expected rule to be released at 84 C")
	}
	if len(actions) != 1 || actions[0].Kind != ActionRelease {
		t.Errorf("expected 1 release action, got %+v", actions)
	}

	// 5. Subsequent sample below release (82 C): remains inactive, no actions
	s5 := Sample{
		At:                   now.Add(40 * time.Second),
		CPUPackageTempC:      82.0,
		CPUPackageTempCKnown: true,
	}
	state, actions = policy.Evaluate([]Sample{s5}, state)
	if state.ActiveRules["thermal_package_temp"] {
		t.Errorf("expected rule to stay inactive")
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions while remaining inactive, got %+v", actions)
	}
}

func TestPolicyDiskAndMemoryRules(t *testing.T) {
	policy := Policy{
		DiskFreeThresholdGB:      10.0,
		MemAvailableThresholdGiB: 2.0,
	}

	now := time.Now()
	var state State
	var actions []Action

	// Sample with low disk (5 GB free) and plenty of memory (4 GiB)
	s1 := Sample{
		At:                     now,
		MemAvailableBytes:      4 * 1024 * 1024 * 1024,
		MemAvailableBytesKnown: true,
		DiskMounts: []DiskMountUsage{
			{MountPoint: "/", FreeBytes: 5 * 1000 * 1000 * 1000, Known: true},
		},
	}

	state, actions = policy.Evaluate([]Sample{s1}, state)
	if !state.ActiveRules["disk_free_low"] {
		t.Errorf("expected disk_free_low to be active")
	}
	if state.ActiveRules["memory_available_low"] {
		t.Errorf("expected memory_available_low to be inactive")
	}
	if len(actions) != 1 || actions[0].Rule != "disk_free_low" || actions[0].Kind != ActionTrigger {
		t.Errorf("expected disk_free_low trigger action, got %+v", actions)
	}

	// Sample with low memory (1 GiB) and plenty of disk (20 GB)
	s2 := Sample{
		At:                     now.Add(time.Second),
		MemAvailableBytes:      1 * 1024 * 1024 * 1024,
		MemAvailableBytesKnown: true,
		DiskMounts: []DiskMountUsage{
			{MountPoint: "/", FreeBytes: 20 * 1000 * 1000 * 1000, Known: true},
		},
	}

	state, actions = policy.Evaluate([]Sample{s2}, state)
	if state.ActiveRules["disk_free_low"] {
		t.Errorf("expected disk_free_low to be released")
	}
	if !state.ActiveRules["memory_available_low"] {
		t.Errorf("expected memory_available_low to be active")
	}
	if len(actions) != 2 {
		t.Fatalf("expected 2 edge-triggered actions (disk release + mem trigger), got %d: %+v", len(actions), actions)
	}

	// Another sample with same low memory state: edge-triggered action fires ONCE
	s3 := Sample{
		At:                     now.Add(2 * time.Second),
		MemAvailableBytes:      1 * 1024 * 1024 * 1024,
		MemAvailableBytesKnown: true,
		DiskMounts: []DiskMountUsage{
			{MountPoint: "/", FreeBytes: 20 * 1000 * 1000 * 1000, Known: true},
		},
	}

	state, actions = policy.Evaluate([]Sample{s2, s3}, state)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions for steady state, got %+v", actions)
	}
}

func TestRingBuffer(t *testing.T) {
	ring := NewRing(3)
	if ring.Cap() != 3 || ring.Len() != 0 {
		t.Errorf("expected cap 3, len 0")
	}

	ring.Push(Sample{CPUPackageTempC: 50.0})
	ring.Push(Sample{CPUPackageTempC: 60.0})

	samples := ring.Samples()
	if len(samples) != 2 || samples[0].CPUPackageTempC != 50.0 || samples[1].CPUPackageTempC != 60.0 {
		t.Errorf("unexpected samples: %+v", samples)
	}

	// Overflow buffer
	ring.Push(Sample{CPUPackageTempC: 70.0})
	ring.Push(Sample{CPUPackageTempC: 80.0})

	if ring.Len() != 3 {
		t.Errorf("expected len 3, got %d", ring.Len())
	}

	samples = ring.Samples()
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}
	if samples[0].CPUPackageTempC != 60.0 || samples[1].CPUPackageTempC != 70.0 || samples[2].CPUPackageTempC != 80.0 {
		t.Errorf("expected [60, 70, 80], got [%v, %v, %v]", samples[0].CPUPackageTempC, samples[1].CPUPackageTempC, samples[2].CPUPackageTempC)
	}
}

func TestSamplerGPUEveryReusesResult(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	bin := filepath.Join(dir, "fake-nvidia-smi")
	script := "#!/bin/sh\necho x >> " + count + "\necho 'Fake GPU, 50, 10, 512, 16384'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Sampler{SysRoot: dir, ProcRoot: dir, NvidiaSmiBin: bin, GPUEvery: time.Hour}
	for range 3 {
		got, err := s.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(got.GPUs) != 1 || got.GPUs[0].Name != "Fake GPU" {
			t.Fatalf("GPUs = %+v", got.GPUs)
		}
	}
	data, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "x"); n != 1 {
		t.Fatalf("nvidia-smi ran %d times, want 1", n)
	}
}
