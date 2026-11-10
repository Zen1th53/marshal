package hostwatch

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Sampler samples host performance metrics.
type Sampler struct {
	SysRoot      string
	ProcRoot     string
	DiskMounts   []string
	NetInterface string
	NvidiaSmiBin string // optional override for nvidia-smi binary path
	// GPUEvery, when positive, is the shortest time between two nvidia-smi
	// runs. Reads in between reuse the last GPU result, so a caller can
	// sample every second without starting a process every second.
	GPUEvery time.Duration

	gpuMu   sync.Mutex
	gpuAt   time.Time
	gpuLast []GPUSample
}

// NewSampler creates a Sampler configured with system defaults.
func NewSampler() *Sampler {
	return &Sampler{
		SysRoot:    "/sys",
		ProcRoot:   "/proc",
		DiskMounts: []string{"/"},
	}
}

// Read collects host telemetry at current time.
func (s *Sampler) Read(ctx context.Context) (Sample, error) {
	sample := Sample{
		At:           time.Now(),
		NetInterface: s.NetInterface,
		CPUCoreTempC: make(map[string]float64),
		NVMeTempC:    make(map[string]float64),
	}

	sysRoot := s.SysRoot
	if sysRoot == "" {
		sysRoot = "/sys"
	}
	procRoot := s.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}

	// 1. CPU info: model name, core/thread count
	s.readCPUInfo(procRoot, &sample)

	// 2. Load average
	s.readLoadAvg(procRoot, &sample)

	// 2b. CPU tick counters
	s.readCPUStat(procRoot, &sample)

	// 3. CPU frequency
	s.readCPUFreq(sysRoot, &sample)

	// 4. Package throttle count
	s.readCPUThrottle(sysRoot, &sample)

	// 5. hwmon: coretemp (CPU package & cores) and NVMe temperatures
	s.readHwmon(sysRoot, &sample)

	// 6. Memory & pressure
	s.readMemInfo(procRoot, &sample)
	s.readPressure(procRoot, &sample)

	// 7. Disk statfs
	s.readDiskMounts(&sample)

	// 8. Network stats
	if s.NetInterface != "" {
		s.readNetworkStats(sysRoot, &sample)
	}

	// 9. GPU stats via nvidia-smi, at most once per GPUEvery
	s.readGPUCached(ctx, &sample)

	return sample, nil
}

func (s *Sampler) readCPUInfo(procRoot string, sample *Sample) {
	data, err := os.ReadFile(filepath.Join(procRoot, "cpuinfo"))
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	coreIDs := make(map[string]bool)
	processors := 0
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "model name":
			if sample.CPUModelName == "" {
				sample.CPUModelName = val
			}
		case "processor":
			processors++
		case "core id":
			coreIDs[val] = true
		}
	}
	sample.CPUThreads = processors
	if len(coreIDs) > 0 {
		sample.CPUCores = len(coreIDs)
	} else {
		sample.CPUCores = processors
	}
}

func (s *Sampler) readLoadAvg(procRoot string, sample *Sample) {
	data, err := os.ReadFile(filepath.Join(procRoot, "loadavg"))
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		l1, e1 := strconv.ParseFloat(fields[0], 64)
		l5, e2 := strconv.ParseFloat(fields[1], 64)
		l15, e3 := strconv.ParseFloat(fields[2], 64)
		if e1 == nil && e2 == nil && e3 == nil {
			sample.CPULoadAvg1 = l1
			sample.CPULoadAvg5 = l5
			sample.CPULoadAvg15 = l15
			sample.CPULoadAvgKnown = true
		}
	}
}

func (s *Sampler) readCPUStat(procRoot string, sample *Sample) {
	data, err := os.ReadFile(filepath.Join(procRoot, "stat"))
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:] // drop the "cpu" label
		if len(fields) < 8 {
			return
		}
		var total, idle uint64
		for i := 0; i < 8; i++ {
			v, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return
			}
			total += v
			if i == 3 || i == 4 { // idle + iowait
				idle += v
			}
		}
		sample.CPUTicksTotal = total
		sample.CPUTicksIdle = idle
		sample.CPUTicksKnown = true
		return
	}
}

func (s *Sampler) readCPUFreq(sysRoot string, sample *Sample) {
	pattern := filepath.Join(sysRoot, "devices/system/cpu/cpu*/cpufreq/scaling_cur_freq")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return
	}

	var sumKHz float64
	var count int
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			continue
		}
		valStr := strings.TrimSpace(string(data))
		val, err := strconv.ParseFloat(valStr, 64)
		if err == nil && val > 0 {
			sumKHz += val
			count++
		}
	}

	if count > 0 {
		// scaling_cur_freq is in kHz, convert to MHz (1 MHz = 1000 kHz)
		sample.CPUAverageFreqMHz = (sumKHz / float64(count)) / 1000.0
		sample.CPUAverageFreqMHzKnown = true
	}
}

func (s *Sampler) readCPUThrottle(sysRoot string, sample *Sample) {
	path := filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/package_throttle_count")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err == nil {
		sample.CPUPackageThrottleCount = val
		sample.CPUPackageThrottleKnown = true
	}
}

func (s *Sampler) readHwmon(sysRoot string, sample *Sample) {
	hwmonDir := filepath.Join(sysRoot, "class/hwmon")
	entries, err := os.ReadDir(hwmonDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		dirPath := filepath.Join(hwmonDir, entry.Name())
		nameBytes, err := os.ReadFile(filepath.Join(dirPath, "name"))
		if err != nil {
			continue
		}
		driverName := strings.TrimSpace(string(nameBytes))

		// Read temp*_label and temp*_input files in this hwmon directory
		tempFiles, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}

		labels := make(map[string]string) // prefix (e.g. "temp1") -> label
		inputs := make(map[string]int64)  // prefix (e.g. "temp1") -> millidegrees C

		for _, tf := range tempFiles {
			fname := tf.Name()
			if strings.HasPrefix(fname, "temp") && strings.HasSuffix(fname, "_label") {
				prefix := strings.TrimSuffix(fname, "_label")
				c, err := os.ReadFile(filepath.Join(dirPath, fname))
				if err == nil {
					labels[prefix] = strings.TrimSpace(string(c))
				}
			} else if strings.HasPrefix(fname, "temp") && strings.HasSuffix(fname, "_input") {
				prefix := strings.TrimSuffix(fname, "_input")
				c, err := os.ReadFile(filepath.Join(dirPath, fname))
				if err == nil {
					v, err := strconv.ParseInt(strings.TrimSpace(string(c)), 10, 64)
					if err == nil {
						inputs[prefix] = v
					}
				}
			}
		}

		if driverName == "coretemp" {
			for prefix, milli := range inputs {
				tempC := float64(milli) / 1000.0
				label := labels[prefix]
				if label == "" {
					label = prefix
				}

				if strings.HasPrefix(label, "Package id") {
					sample.CPUPackageTempC = tempC
					sample.CPUPackageTempCKnown = true
				} else if strings.HasPrefix(label, "Core") {
					sample.CPUCoreTempC[label] = tempC
				}
			}
		} else if strings.Contains(driverName, "nvme") {
			for prefix, milli := range inputs {
				tempC := float64(milli) / 1000.0
				label := labels[prefix]
				if label == "Composite" || prefix == "temp1" {
					key := entry.Name()
					if label != "" {
						key = fmt.Sprintf("%s (%s)", entry.Name(), label)
					}
					sample.NVMeTempC[key] = tempC
				}
			}
		}
	}
}

func (s *Sampler) readMemInfo(procRoot string, sample *Sample) {
	data, err := os.ReadFile(filepath.Join(procRoot, "meminfo"))
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valFields := strings.Fields(parts[1])
		if len(valFields) == 0 {
			continue
		}
		kbVal, err := strconv.ParseUint(valFields[0], 10, 64)
		if err != nil {
			continue
		}
		bytesVal := kbVal * 1024

		switch key {
		case "MemTotal":
			sample.MemTotalBytes = bytesVal
			sample.MemTotalBytesKnown = true
		case "MemAvailable":
			sample.MemAvailableBytes = bytesVal
			sample.MemAvailableBytesKnown = true
		case "Cached":
			sample.MemCachedBytes = bytesVal
			sample.MemCachedBytesKnown = true
		case "SwapTotal":
			sample.SwapTotalBytes = bytesVal
			sample.SwapTotalBytesKnown = true
		case "SwapFree":
			sample.SwapFreeBytes = bytesVal
			sample.SwapFreeBytesKnown = true
		}
	}
}

func (s *Sampler) readPressure(procRoot string, sample *Sample) {
	// /proc/pressure/memory
	readPSI := func(path string, someKnown *bool, someVal *float64, fullKnown *bool, fullVal *float64) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			kind := fields[0] // "some" or "full"
			for _, f := range fields[1:] {
				if strings.HasPrefix(f, "avg10=") {
					vStr := strings.TrimPrefix(f, "avg10=")
					v, err := strconv.ParseFloat(vStr, 64)
					if err == nil {
						if kind == "some" && someVal != nil {
							*someVal = v
							if someKnown != nil {
								*someKnown = true
							}
						} else if kind == "full" && fullVal != nil {
							*fullVal = v
							if fullKnown != nil {
								*fullKnown = true
							}
						}
					}
				}
			}
		}
	}

	readPSI(filepath.Join(procRoot, "pressure/memory"),
		&sample.MemPressureSomeKnown, &sample.MemPressureSomeAvg10Percent,
		&sample.MemPressureFullKnown, &sample.MemPressureFullAvg10Percent)

	readPSI(filepath.Join(procRoot, "pressure/cpu"),
		&sample.CPUPressureSomeKnown, &sample.CPUPressureSomeAvg10Percent,
		&sample.CPUPressureFullKnown, &sample.CPUPressureFullAvg10Percent)
}

func (s *Sampler) readDiskMounts(sample *Sample) {
	mounts := s.DiskMounts
	if len(mounts) == 0 {
		return
	}

	for _, m := range mounts {
		var stat syscall.Statfs_t
		err := syscall.Statfs(m, &stat)
		if err != nil {
			sample.DiskMounts = append(sample.DiskMounts, DiskMountUsage{
				MountPoint: m,
				Known:      false,
			})
			continue
		}

		bsize := uint64(stat.Bsize)
		total := stat.Blocks * bsize
		free := stat.Bavail * bsize // unprivileged user available space
		var used uint64
		if total >= stat.Bfree*bsize {
			used = total - (stat.Bfree * bsize)
		} else {
			used = 0
		}

		sample.DiskMounts = append(sample.DiskMounts, DiskMountUsage{
			MountPoint: m,
			TotalBytes: total,
			FreeBytes:  free,
			UsedBytes:  used,
			Known:      true,
		})
	}
}

func (s *Sampler) readNetworkStats(sysRoot string, sample *Sample) {
	rxPath := filepath.Join(sysRoot, "class/net", s.NetInterface, "statistics/rx_bytes")
	txPath := filepath.Join(sysRoot, "class/net", s.NetInterface, "statistics/tx_bytes")

	if rxData, err := os.ReadFile(rxPath); err == nil {
		if val, err := strconv.ParseUint(strings.TrimSpace(string(rxData)), 10, 64); err == nil {
			sample.NetRxBytes = val
			sample.NetRxBytesKnown = true
		}
	}

	if txData, err := os.ReadFile(txPath); err == nil {
		if val, err := strconv.ParseUint(strings.TrimSpace(string(txData)), 10, 64); err == nil {
			sample.NetTxBytes = val
			sample.NetTxBytesKnown = true
		}
	}
}

func (s *Sampler) readGPUCached(ctx context.Context, sample *Sample) {
	if s.GPUEvery <= 0 {
		s.readGPU(ctx, sample)
		return
	}
	s.gpuMu.Lock()
	defer s.gpuMu.Unlock()
	if !s.gpuAt.IsZero() && sample.At.Sub(s.gpuAt) < s.GPUEvery && sample.At.Sub(s.gpuAt) >= 0 {
		sample.GPUs = append([]GPUSample(nil), s.gpuLast...)
		return
	}
	s.readGPU(ctx, sample)
	s.gpuAt = sample.At
	s.gpuLast = append([]GPUSample(nil), sample.GPUs...)
}

func (s *Sampler) readGPU(ctx context.Context, sample *Sample) {
	binName := s.NvidiaSmiBin
	if binName == "" {
		binName = "nvidia-smi"
	}

	binPath, err := exec.LookPath(binName)
	if err != nil {
		return
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, binPath,
		"--query-gpu=name,temperature.gpu,utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits",
	)

	out, err := cmd.Output()
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 5 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		temp, e1 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		util, e2 := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		memUsed, e3 := strconv.ParseUint(strings.TrimSpace(parts[3]), 10, 64)
		memTotal, e4 := strconv.ParseUint(strings.TrimSpace(parts[4]), 10, 64)

		if e1 == nil && e2 == nil && e3 == nil && e4 == nil {
			sample.GPUs = append(sample.GPUs, GPUSample{
				Name:               name,
				TempC:              temp,
				UtilizationPercent: util,
				MemoryUsedBytes:    memUsed * 1024 * 1024, // nvidia-smi nounits reports memory in MiB
				MemoryTotalBytes:   memTotal * 1024 * 1024,
			})
		}
	}
}
