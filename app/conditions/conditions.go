// Package conditions provides condition checking for job execution based on system metrics
package conditions

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// Config defines conditions for job execution
type Config struct {
	MaxPostpone   *time.Duration `yaml:"max_postpone,omitempty" json:"max_postpone,omitempty" jsonschema:"type=string"`
	CheckInterval *time.Duration `yaml:"check_interval,omitempty" json:"check_interval,omitempty" jsonschema:"type=string"`
	CPUBelow      *int           `yaml:"cpu_below,omitempty" json:"cpu_below,omitempty"`
	MemoryBelow   *int           `yaml:"memory_below,omitempty" json:"memory_below,omitempty"`
	LoadAvgBelow  *float64       `yaml:"load_avg_below,omitempty" json:"load_avg_below,omitempty"`
	DiskFreeAbove *int           `yaml:"disk_free_above,omitempty" json:"disk_free_above,omitempty"`
	DiskFreePath  string         `yaml:"disk_free_path,omitempty" json:"disk_free_path,omitempty"`
	Custom        string         `yaml:"custom,omitempty" json:"custom,omitempty"`
	CustomTimeout *time.Duration `yaml:"custom_timeout,omitempty" json:"custom_timeout,omitempty" jsonschema:"type=string"`
}

// Checker provides system condition checking with concurrency control
type Checker struct {
	maxConcurrent int
	semaphore     chan struct{}
}

// NewChecker creates a new condition checker with specified concurrency limit
// If maxConcurrent is 0 or negative, defaults to 10
func NewChecker(maxConcurrent int) *Checker {
	if maxConcurrent <= 0 {
		maxConcurrent = 10 // sensible default
	}
	return &Checker{
		maxConcurrent: maxConcurrent,
		semaphore:     make(chan struct{}, maxConcurrent),
	}
}

// Check verifies if all conditions are met
// Returns true if conditions are satisfied, false with reason otherwise
func (c *Checker) Check(conditions Config) (bool, string) {
	// acquire semaphore to limit concurrent checks
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	default:
		// if we can't acquire immediately, still proceed but log warning
		// this prevents deadlock while still providing protection
		return false, "condition check limit reached, try increasing --max-concurrent-checks or wait for running checks to complete"
	}

	// validate all thresholds
	if err := c.validateThresholds(conditions); err != nil {
		return false, err.Error()
	}

	// check CPU
	if conditions.CPUBelow != nil {
		if ok, reason := c.checkCPU(*conditions.CPUBelow); !ok {
			return false, reason
		}
	}

	// check memory
	if conditions.MemoryBelow != nil {
		if ok, reason := c.checkMemory(*conditions.MemoryBelow); !ok {
			return false, reason
		}
	}

	// check load average
	if conditions.LoadAvgBelow != nil {
		if ok, reason := c.checkLoadAvg(*conditions.LoadAvgBelow); !ok {
			return false, reason
		}
	}

	// check disk free space
	if conditions.DiskFreeAbove != nil {
		path := conditions.DiskFreePath
		if path == "" {
			path = "/"
		}
		if ok, reason := c.checkDiskFree(*conditions.DiskFreeAbove, path); !ok {
			return false, reason
		}
	}

	// check custom script
	if conditions.Custom != "" {
		timeout := 30 * time.Second // default timeout
		if conditions.CustomTimeout != nil {
			timeout = *conditions.CustomTimeout
		}
		if ok, reason := c.checkCustom(conditions.Custom, timeout); !ok {
			return false, reason
		}
	}

	return true, ""
}

// validateThresholds validates all condition thresholds
func (c *Checker) validateThresholds(conditions Config) error {
	if conditions.CPUBelow != nil && (*conditions.CPUBelow < 0 || *conditions.CPUBelow > 100) {
		return fmt.Errorf("invalid CPU threshold: %d (must be 0-100)", *conditions.CPUBelow)
	}
	if conditions.MemoryBelow != nil && (*conditions.MemoryBelow < 0 || *conditions.MemoryBelow > 100) {
		return fmt.Errorf("invalid memory threshold: %d (must be 0-100)", *conditions.MemoryBelow)
	}
	if conditions.LoadAvgBelow != nil && *conditions.LoadAvgBelow < 0 {
		return fmt.Errorf("invalid load average threshold: %.2f (must be >= 0)", *conditions.LoadAvgBelow)
	}
	if conditions.DiskFreeAbove != nil && (*conditions.DiskFreeAbove < 0 || *conditions.DiskFreeAbove > 100) {
		return fmt.Errorf("invalid disk free threshold: %d (must be 0-100)", *conditions.DiskFreeAbove)
	}
	return nil
}

// checkCPU checks if CPU usage is below threshold
func (c *Checker) checkCPU(threshold int) (bool, string) {
	// use 1 second interval for accurate CPU sampling
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err != nil {
		return false, fmt.Sprintf("failed to get CPU: %v", err)
	}
	if len(cpuPercent) == 0 {
		return false, "no CPU data available"
	}
	current := int(cpuPercent[0])
	if current >= threshold {
		return false, fmt.Sprintf("CPU: current=%d%%, threshold=%d%%", current, threshold)
	}
	return true, ""
}

// checkMemory checks if memory usage is below threshold
func (c *Checker) checkMemory(threshold int) (bool, string) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return false, fmt.Sprintf("failed to get memory: %v", err)
	}
	current := int(v.UsedPercent)
	if current >= threshold {
		return false, fmt.Sprintf("memory: current=%d%%, threshold=%d%%", current, threshold)
	}
	return true, ""
}

// checkLoadAvg checks if load average is below threshold
func (c *Checker) checkLoadAvg(threshold float64) (bool, string) {
	loads, err := load.Avg()
	if err != nil {
		return false, fmt.Sprintf("failed to get load average: %v", err)
	}
	if loads.Load1 >= threshold {
		return false, fmt.Sprintf("load average: current=%.2f, threshold=%.2f", loads.Load1, threshold)
	}
	return true, ""
}

// checkDiskFree checks if disk free space is above threshold
func (c *Checker) checkDiskFree(minFreePercent int, path string) (bool, string) {
	usage, err := disk.Usage(path)
	if err != nil {
		return false, fmt.Sprintf("failed to get disk usage for %s: %v", path, err)
	}
	freePercent := 100 - int(usage.UsedPercent)
	if freePercent < minFreePercent {
		return false, fmt.Sprintf("disk free: current=%d%%, threshold=%d%%, path=%s", freePercent, minFreePercent, path)
	}
	return true, ""
}

// checkCustom runs a custom script and checks its exit code
func (c *Checker) checkCustom(script string, timeout time.Duration) (bool, string) {
	// add timeout to prevent scripts from hanging indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return false, fmt.Sprintf("custom check timed out after %v", timeout)
		}
		return false, fmt.Sprintf("custom check failed: %v", err)
	}
	return true, ""
}
