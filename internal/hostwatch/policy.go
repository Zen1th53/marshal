package hostwatch

import "fmt"

// ActionKind indicates whether a rule is triggered or released.
type ActionKind string

const (
	ActionTrigger ActionKind = "trigger"
	ActionRelease ActionKind = "release"
)

// Action represents an edge-triggered state transition event.
type Action struct {
	Rule   string
	Kind   ActionKind
	Reason string
}

// State tracks the active status of each rule in the policy.
type State struct {
	ActiveRules map[string]bool
}

// Policy defines threshold rules evaluated on a sliding window of samples.
type Policy struct {
	// Thermal hysteresis rules for package temp (deg C)
	PackageTempTriggerC float64 // default 90.0
	PackageTempReleaseC float64 // default 85.0

	// Thresholds
	DiskFreeThresholdGB      float64 // triggers if free disk < threshold GB (1 GB = 1e9 bytes)
	MemAvailableThresholdGiB float64 // triggers if MemAvailable < threshold GiB (1 GiB = 1024^3 bytes)
	ThrottleRatePerMin       float64 // triggers if throttle rate/min > threshold
	GPUTempThresholdC        float64 // triggers if GPU temp > threshold
	NVMeTempThresholdC       float64 // triggers if NVMe temp > threshold
}

// DefaultPolicy returns a Policy populated with common defaults.
func DefaultPolicy() Policy {
	return Policy{
		PackageTempTriggerC:      90.0,
		PackageTempReleaseC:      85.0,
		DiskFreeThresholdGB:      50.0,
		MemAvailableThresholdGiB: 8.0,
		ThrottleRatePerMin:       100.0,
		GPUTempThresholdC:        85.0,
		NVMeTempThresholdC:       75.0,
	}
}

// Evaluate evaluates the policy rules over the provided window of samples.
// It returns the updated State and any edge-triggered Actions.
func (p Policy) Evaluate(window []Sample, prev State) (State, []Action) {
	next := State{
		ActiveRules: make(map[string]bool),
	}
	if prev.ActiveRules != nil {
		for k, v := range prev.ActiveRules {
			next.ActiveRules[k] = v
		}
	}

	var actions []Action
	if len(window) == 0 {
		return next, actions
	}

	latest := window[len(window)-1]

	// 1. Thermal Rule (hysteresis on 30s package temp average)
	ruleThermal := "thermal_package_temp"
	pkgTempTrigger := p.PackageTempTriggerC
	if pkgTempTrigger == 0 {
		pkgTempTrigger = 90.0
	}
	pkgTempRelease := p.PackageTempReleaseC
	if pkgTempRelease == 0 {
		pkgTempRelease = 85.0
	}

	// Filter window to last 30 seconds for package temp average
	windowCutoff := latest.At.Add(-30 * 1000 * 1000 * 1000) // 30s
	var tempSum float64
	var tempCount int
	for _, s := range window {
		if !s.At.Before(windowCutoff) && s.CPUPackageTempCKnown {
			tempSum += s.CPUPackageTempC
			tempCount++
		}
	}
	// Fallback to latest sample if window timestamps are not spread or no samples in 30s
	if tempCount == 0 && latest.CPUPackageTempCKnown {
		tempSum = latest.CPUPackageTempC
		tempCount = 1
	}

	if tempCount > 0 {
		avgTemp := tempSum / float64(tempCount)
		wasActive := prev.ActiveRules != nil && prev.ActiveRules[ruleThermal]
		if !wasActive && avgTemp > pkgTempTrigger {
			next.ActiveRules[ruleThermal] = true
			actions = append(actions, Action{
				Rule:   ruleThermal,
				Kind:   ActionTrigger,
				Reason: fmt.Sprintf("CPU package temp 30s average %.1f C > trigger threshold %.1f C", avgTemp, pkgTempTrigger),
			})
		} else if wasActive && avgTemp < pkgTempRelease {
			next.ActiveRules[ruleThermal] = false
			actions = append(actions, Action{
				Rule:   ruleThermal,
				Kind:   ActionRelease,
				Reason: fmt.Sprintf("CPU package temp 30s average %.1f C < release threshold %.1f C", avgTemp, pkgTempRelease),
			})
		}
	}

	// 2. Disk Free Rule (free < threshold GB)
	if p.DiskFreeThresholdGB > 0 {
		ruleDisk := "disk_free_low"
		wasActive := prev.ActiveRules != nil && prev.ActiveRules[ruleDisk]
		thresholdBytes := uint64(p.DiskFreeThresholdGB * 1e9)
		hasLowDisk := false
		var lowMount string
		var lowFree uint64

		for _, m := range latest.DiskMounts {
			if m.Known && m.FreeBytes < thresholdBytes {
				hasLowDisk = true
				lowMount = m.MountPoint
				lowFree = m.FreeBytes
				break
			}
		}

		if !wasActive && hasLowDisk {
			next.ActiveRules[ruleDisk] = true
			actions = append(actions, Action{
				Rule:   ruleDisk,
				Kind:   ActionTrigger,
				Reason: fmt.Sprintf("Disk free on %s (%.2f GB) < threshold %.2f GB", lowMount, float64(lowFree)/1e9, p.DiskFreeThresholdGB),
			})
		} else if wasActive && !hasLowDisk {
			next.ActiveRules[ruleDisk] = false
			actions = append(actions, Action{
				Rule:   ruleDisk,
				Kind:   ActionRelease,
				Reason: fmt.Sprintf("Disk free on all monitored mounts >= threshold %.2f GB", p.DiskFreeThresholdGB),
			})
		}
	}

	// 3. Memory Available Rule (available < threshold GiB)
	if p.MemAvailableThresholdGiB > 0 && latest.MemAvailableBytesKnown {
		ruleMem := "memory_available_low"
		wasActive := prev.ActiveRules != nil && prev.ActiveRules[ruleMem]
		thresholdBytes := uint64(p.MemAvailableThresholdGiB * 1024 * 1024 * 1024)
		isLow := latest.MemAvailableBytes < thresholdBytes

		if !wasActive && isLow {
			next.ActiveRules[ruleMem] = true
			actions = append(actions, Action{
				Rule:   ruleMem,
				Kind:   ActionTrigger,
				Reason: fmt.Sprintf("Available memory %.2f GiB < threshold %.2f GiB", float64(latest.MemAvailableBytes)/(1024*1024*1024), p.MemAvailableThresholdGiB),
			})
		} else if wasActive && !isLow {
			next.ActiveRules[ruleMem] = false
			actions = append(actions, Action{
				Rule:   ruleMem,
				Kind:   ActionRelease,
				Reason: fmt.Sprintf("Available memory %.2f GiB >= threshold %.2f GiB", float64(latest.MemAvailableBytes)/(1024*1024*1024), p.MemAvailableThresholdGiB),
			})
		}
	}

	// 4. Throttle Rate Per Minute Rule
	if p.ThrottleRatePerMin > 0 && len(window) >= 2 {
		ruleThrottle := "cpu_throttle_rate_high"
		wasActive := prev.ActiveRules != nil && prev.ActiveRules[ruleThrottle]

		first := window[0]
		timeDiffSec := latest.At.Sub(first.At).Seconds()
		if timeDiffSec > 0 && first.CPUPackageThrottleKnown && latest.CPUPackageThrottleKnown {
			countDiff := latest.CPUPackageThrottleCount - first.CPUPackageThrottleCount
			if countDiff < 0 {
				countDiff = 0
			}
			ratePerMin := (float64(countDiff) / timeDiffSec) * 60.0

			if !wasActive && ratePerMin > p.ThrottleRatePerMin {
				next.ActiveRules[ruleThrottle] = true
				actions = append(actions, Action{
					Rule:   ruleThrottle,
					Kind:   ActionTrigger,
					Reason: fmt.Sprintf("CPU throttle rate %.1f/min > threshold %.1f/min", ratePerMin, p.ThrottleRatePerMin),
				})
			} else if wasActive && ratePerMin <= p.ThrottleRatePerMin {
				next.ActiveRules[ruleThrottle] = false
				actions = append(actions, Action{
					Rule:   ruleThrottle,
					Kind:   ActionRelease,
					Reason: fmt.Sprintf("CPU throttle rate %.1f/min <= threshold %.1f/min", ratePerMin, p.ThrottleRatePerMin),
				})
			}
		}
	}

	// 5. GPU Temp Rule (temp > threshold)
	if p.GPUTempThresholdC > 0 && len(latest.GPUs) > 0 {
		ruleGPU := "gpu_temp_high"
		wasActive := prev.ActiveRules != nil && prev.ActiveRules[ruleGPU]
		hasHighGPU := false
		var highName string
		var highTemp float64

		for _, gpu := range latest.GPUs {
			if gpu.TempC > p.GPUTempThresholdC {
				hasHighGPU = true
				highName = gpu.Name
				highTemp = gpu.TempC
				break
			}
		}

		if !wasActive && hasHighGPU {
			next.ActiveRules[ruleGPU] = true
			actions = append(actions, Action{
				Rule:   ruleGPU,
				Kind:   ActionTrigger,
				Reason: fmt.Sprintf("GPU %s temperature %.1f C > threshold %.1f C", highName, highTemp, p.GPUTempThresholdC),
			})
		} else if wasActive && !hasHighGPU {
			next.ActiveRules[ruleGPU] = false
			actions = append(actions, Action{
				Rule:   ruleGPU,
				Kind:   ActionRelease,
				Reason: fmt.Sprintf("All GPU temperatures <= threshold %.1f C", p.GPUTempThresholdC),
			})
		}
	}

	// 6. NVMe Temp Rule (temp > threshold)
	if p.NVMeTempThresholdC > 0 && len(latest.NVMeTempC) > 0 {
		ruleNVMe := "nvme_temp_high"
		wasActive := prev.ActiveRules != nil && prev.ActiveRules[ruleNVMe]
		hasHighNVMe := false
		var highName string
		var highTemp float64

		for name, temp := range latest.NVMeTempC {
			if temp > p.NVMeTempThresholdC {
				hasHighNVMe = true
				highName = name
				highTemp = temp
				break
			}
		}

		if !wasActive && hasHighNVMe {
			next.ActiveRules[ruleNVMe] = true
			actions = append(actions, Action{
				Rule:   ruleNVMe,
				Kind:   ActionTrigger,
				Reason: fmt.Sprintf("NVMe %s temperature %.1f C > threshold %.1f C", highName, highTemp, p.NVMeTempThresholdC),
			})
		} else if wasActive && !hasHighNVMe {
			next.ActiveRules[ruleNVMe] = false
			actions = append(actions, Action{
				Rule:   ruleNVMe,
				Kind:   ActionRelease,
				Reason: fmt.Sprintf("All NVMe temperatures <= threshold %.1f C", p.NVMeTempThresholdC),
			})
		}
	}

	return next, actions
}
