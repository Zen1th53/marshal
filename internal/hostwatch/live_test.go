package hostwatch

import (
	"context"
	"testing"
)

func TestLiveSamplerRead(t *testing.T) {
	sampler := NewSampler()
	sampler.NetInterface = "lo"
	sample, err := sampler.Read(context.Background())
	if err != nil {
		t.Fatalf("live sampler read failed: %v", err)
	}
	t.Logf("CPU Model: %s, Cores: %d, Threads: %d", sample.CPUModelName, sample.CPUCores, sample.CPUThreads)
	t.Logf("CPU Load: %.2f %.2f %.2f", sample.CPULoadAvg1, sample.CPULoadAvg5, sample.CPULoadAvg15)
	t.Logf("CPU Temp: %.1f C (known: %v)", sample.CPUPackageTempC, sample.CPUPackageTempCKnown)
	t.Logf("Core temps count: %d", len(sample.CPUCoreTempC))
	t.Logf("Freq: %.1f MHz (known: %v)", sample.CPUAverageFreqMHz, sample.CPUAverageFreqMHzKnown)
	t.Logf("Throttle count: %d (known: %v)", sample.CPUPackageThrottleCount, sample.CPUPackageThrottleKnown)
	t.Logf("CPU ticks: total %d idle %d (known: %v)", sample.CPUTicksTotal, sample.CPUTicksIdle, sample.CPUTicksKnown)
	t.Logf("Mem Total: %d, Available: %d", sample.MemTotalBytes, sample.MemAvailableBytes)
	t.Logf("Disk mounts count: %d", len(sample.DiskMounts))
	t.Logf("NVMe temps count: %d", len(sample.NVMeTempC))
	t.Logf("Net lo rx: %d, tx: %d", sample.NetRxBytes, sample.NetTxBytes)
	t.Logf("GPUs count: %d", len(sample.GPUs))
}
