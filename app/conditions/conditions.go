// Package conditions provides condition checking for job execution based on system metrics
package conditions

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/umputun/cronn/app/crontab"
)

// Check verifies if all conditions are met
// Returns true if conditions are satisfied, false with reason otherwise
func Check(conditions crontab.ConditionsConfig) (bool, string) {
	// check CPU
	if conditions.CPUBelow != nil {
		if ok, reason := checkCPU(*conditions.CPUBelow); !ok {
			return false, reason
		}
	}

	// check memory
	if conditions.MemoryBelow != nil {
		if ok, reason := checkMemory(*conditions.MemoryBelow); !ok {
			return false, reason
		}
	}

	// check load average
	if conditions.LoadAvgBelow != nil {
		if ok, reason := checkLoadAvg(*conditions.LoadAvgBelow); !ok {
			return false, reason
		}
	}

	// check disk free space
	if conditions.DiskFreeAbove != nil {
		path := conditions.DiskFreePath
		if path == "" {
			path = "/"
		}
		if ok, reason := checkDiskFree(*conditions.DiskFreeAbove, path); !ok {
			return false, reason
		}
	}

	// check custom script
	if conditions.Custom != "" {
		if ok, reason := checkCustom(conditions.Custom); !ok {
			return false, reason
		}
	}

	return true, ""
}

// checkCPU checks if CPU usage is below threshold
func checkCPU(threshold int) (bool, string) {
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err != nil {
		return false, fmt.Sprintf("failed to get CPU: %v", err)
	}
	if len(cpuPercent) == 0 {
		return false, "no CPU data available"
	}
	current := int(cpuPercent[0])
	if current >= threshold {
		return false, fmt.Sprintf("CPU at %d%%, threshold %d%%", current, threshold)
	}
	return true, ""
}

// checkMemory checks if memory usage is below threshold
func checkMemory(threshold int) (bool, string) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return false, fmt.Sprintf("failed to get memory: %v", err)
	}
	current := int(v.UsedPercent)
	if current >= threshold {
		return false, fmt.Sprintf("memory at %d%%, threshold %d%%", current, threshold)
	}
	return true, ""
}

// checkLoadAvg checks if load average is below threshold
func checkLoadAvg(threshold float64) (bool, string) {
	loads, err := load.Avg()
	if err != nil {
		return false, fmt.Sprintf("failed to get load average: %v", err)
	}
	if loads.Load1 >= threshold {
		return false, fmt.Sprintf("load at %.2f, threshold %.2f", loads.Load1, threshold)
	}
	return true, ""
}

// checkDiskFree checks if disk free space is above threshold
func checkDiskFree(minFreePercent int, path string) (bool, string) {
	usage, err := disk.Usage(path)
	if err != nil {
		return false, fmt.Sprintf("failed to get disk usage for %s: %v", path, err)
	}
	freePercent := 100 - int(usage.UsedPercent)
	if freePercent < minFreePercent {
		return false, fmt.Sprintf("disk free at %d%%, need %d%% on %s", freePercent, minFreePercent, path)
	}
	return true, ""
}

// checkCustom runs a custom script and checks its exit code
func checkCustom(script string) (bool, string) {
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Run(); err != nil {
		return false, fmt.Sprintf("custom check failed: %v", err)
	}
	return true, ""
}