package hostwatch

import "time"

// Sample captures host telemetry at a point in time with explicit units.
type Sample struct {
	At time.Time

	// CPU metrics
	CPUModelName            string
	CPUCores                int
	CPUThreads              int
	CPULoadAvg1             float64
	CPULoadAvg5             float64
	CPULoadAvg15            float64
	CPULoadAvgKnown         bool
	CPUAverageFreqMHz       float64
	CPUAverageFreqMHzKnown  bool
	CPUPackageThrottleCount int64
	CPUPackageThrottleKnown bool
	CPUPackageTempC         float64
	CPUPackageTempCKnown    bool
	CPUCoreTempC            map[string]float64 // core label -> temp in C

	// CPUTicksTotal and CPUTicksIdle are /proc/stat's cumulative "cpu" line:
	// total is user+nice+system+idle+iowait+irq+softirq+steal (guest is
	// already inside user), idle is idle+iowait. Busy percent needs two samples.
	CPUTicksTotal uint64
	CPUTicksIdle  uint64
	CPUTicksKnown bool

	// CPU Pressure
	CPUPressureSomeAvg10Percent float64
	CPUPressureSomeKnown        bool
	CPUPressureFullAvg10Percent float64
	CPUPressureFullKnown        bool

	// Memory metrics
	MemTotalBytes               uint64
	MemTotalBytesKnown          bool
	MemAvailableBytes           uint64
	MemAvailableBytesKnown      bool
	MemCachedBytes              uint64
	MemCachedBytesKnown         bool
	SwapTotalBytes              uint64
	SwapTotalBytesKnown         bool
	SwapFreeBytes               uint64
	SwapFreeBytesKnown          bool
	MemPressureSomeAvg10Percent float64
	MemPressureSomeKnown        bool
	MemPressureFullAvg10Percent float64
	MemPressureFullKnown        bool

	// Disk metrics
	DiskMounts []DiskMountUsage
	NVMeTempC  map[string]float64 // sensor name or path -> temp in C

	// Network metrics
	NetInterface    string
	NetRxBytes      uint64
	NetRxBytesKnown bool
	NetTxBytes      uint64
	NetTxBytesKnown bool

	// GPU metrics
	GPUs []GPUSample
}

// DiskMountUsage contains disk capacity and usage for a mount point.
type DiskMountUsage struct {
	MountPoint string
	TotalBytes uint64
	FreeBytes  uint64
	UsedBytes  uint64
	Known      bool
}

// GPUSample contains metrics for a single NVIDIA GPU.
type GPUSample struct {
	Name               string
	TempC              float64
	UtilizationPercent float64
	MemoryUsedBytes    uint64
	MemoryTotalBytes   uint64
}
