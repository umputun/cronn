package conditions

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheck(t *testing.T) {
	checker := fixedChecker()

	tests := []struct {
		name       string
		conditions Config
		wantOK     bool
		wantReason string
	}{
		{
			name:       "no conditions",
			conditions: Config{},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "cpu below threshold passes",
			conditions: Config{
				CPUBelow: new(90),
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "memory below threshold passes",
			conditions: Config{
				MemoryBelow: new(99),
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "disk free above threshold passes",
			conditions: Config{
				DiskFreeAbove: new(1),
				DiskFreePath:  "/",
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "custom script success",
			conditions: Config{
				Custom: "exit 0",
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "custom script failure",
			conditions: Config{
				Custom: "exit 1",
			},
			wantOK:     false,
			wantReason: "custom check failed: exit status 1",
		},
		{
			name: "multiple conditions all pass",
			conditions: Config{
				CPUBelow:      new(99),
				MemoryBelow:   new(99),
				DiskFreeAbove: new(1),
				Custom:        "exit 0",
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "multiple conditions one fails",
			conditions: Config{
				CPUBelow:      new(99),
				MemoryBelow:   new(99),
				DiskFreeAbove: new(1),
				Custom:        "exit 1",
			},
			wantOK:     false,
			wantReason: "custom check failed: exit status 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotReason := checker.Check(tt.conditions)
			assert.Equal(t, tt.wantOK, gotOK)
			if tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, gotReason)
			}
		})
	}
}

func TestCheck_ValidationBoundaries(t *testing.T) {
	checker := fixedChecker()

	tests := []struct {
		name       string
		conditions Config
		wantOK     bool
		wantReason string
	}{
		{
			name:       "invalid CPU below negative",
			conditions: Config{CPUBelow: new(-1)},
			wantOK:     false,
			wantReason: "invalid CPU threshold: -1 (must be 0-100)",
		},
		{
			name:       "invalid CPU below over 100",
			conditions: Config{CPUBelow: new(101)},
			wantOK:     false,
			wantReason: "invalid CPU threshold: 101 (must be 0-100)",
		},
		{
			name:       "valid CPU at boundary 0",
			conditions: Config{CPUBelow: new(0)},
			wantOK:     false,
			wantReason: "CPU: current=10%, threshold=0%",
		},
		{
			name:       "valid CPU at boundary 100",
			conditions: Config{CPUBelow: new(100)},
			wantOK:     true,
			wantReason: "",
		},
		{
			name:       "invalid memory below negative",
			conditions: Config{MemoryBelow: new(-1)},
			wantOK:     false,
			wantReason: "invalid memory threshold: -1 (must be 0-100)",
		},
		{
			name:       "invalid memory below over 100",
			conditions: Config{MemoryBelow: new(101)},
			wantOK:     false,
			wantReason: "invalid memory threshold: 101 (must be 0-100)",
		},
		{
			name:       "invalid load average negative",
			conditions: Config{LoadAvgBelow: new(-0.1)},
			wantOK:     false,
			wantReason: "invalid load average threshold: -0.10 (must be >= 0)",
		},
		{
			name:       "valid load average at boundary 0",
			conditions: Config{LoadAvgBelow: new(0.0)},
			wantOK:     false,
			wantReason: "load average: current=1.50, threshold=0.00",
		},
		{
			name:       "invalid disk free negative",
			conditions: Config{DiskFreeAbove: new(-1)},
			wantOK:     false,
			wantReason: "invalid disk free threshold: -1 (must be 0-100)",
		},
		{
			name:       "invalid disk free over 100",
			conditions: Config{DiskFreeAbove: new(101)},
			wantOK:     false,
			wantReason: "invalid disk free threshold: 101 (must be 0-100)",
		},
		{
			name:       "valid disk free at boundary 0",
			conditions: Config{DiskFreeAbove: new(0)},
			wantOK:     true,
			wantReason: "",
		},
		{
			name:       "valid disk free at boundary 100",
			conditions: Config{DiskFreeAbove: new(100)},
			wantOK:     false,
			wantReason: "disk free: current=60%, threshold=100%, path=/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotReason := checker.Check(tt.conditions)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

func TestCheckCPU(t *testing.T) {
	tests := []struct {
		name       string
		sampler    func() ([]float64, error)
		threshold  int
		wantOK     bool
		wantReason string
	}{
		{name: "below threshold", sampler: fixedCPU(89), threshold: 90, wantOK: true},
		{name: "at threshold", sampler: fixedCPU(90), threshold: 90, wantReason: "CPU: current=90%, threshold=90%"},
		{name: "above threshold", sampler: fixedCPU(95), threshold: 90, wantReason: "CPU: current=95%, threshold=90%"},
		{name: "zero threshold", sampler: fixedCPU(0), threshold: 0, wantReason: "CPU: current=0%, threshold=0%"},
		{name: "sampler error", sampler: func() ([]float64, error) { return nil, errors.New("boom") }, threshold: 90,
			wantReason: "failed to get CPU: boom"},
		{name: "empty sample", sampler: func() ([]float64, error) { return []float64{}, nil }, threshold: 90,
			wantReason: "no CPU data available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewChecker(0)
			checker.cpuPercent = tt.sampler
			ok, reason := checker.checkCPU(tt.threshold)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

func fixedCPU(v float64) func() ([]float64, error) {
	return func() ([]float64, error) { return []float64{v}, nil }
}

func fixedValue(v float64) func() (float64, error) {
	return func() (float64, error) { return v, nil }
}

func failingValue() (float64, error) { return 0, errors.New("boom") }

func fixedChecker() *Checker {
	checker := NewChecker(0)
	checker.cpuPercent = fixedCPU(10)
	checker.memPercent = fixedValue(50)
	checker.loadAvg = fixedValue(1.5)
	checker.diskUsedPercent = func(string) (float64, error) { return 40, nil }
	return checker
}

func TestCheckMemory(t *testing.T) {
	tests := []struct {
		name       string
		sampler    func() (float64, error)
		threshold  int
		wantOK     bool
		wantReason string
	}{
		{name: "below threshold", sampler: fixedValue(89.9), threshold: 90, wantOK: true},
		{name: "at threshold", sampler: fixedValue(90), threshold: 90, wantReason: "memory: current=90%, threshold=90%"},
		{name: "above threshold", sampler: fixedValue(95), threshold: 90, wantReason: "memory: current=95%, threshold=90%"},
		{name: "sampler error", sampler: failingValue, threshold: 90, wantReason: "failed to get memory: boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewChecker(0)
			checker.memPercent = tt.sampler
			ok, reason := checker.checkMemory(tt.threshold)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestCheckLoadAvg(t *testing.T) {
	tests := []struct {
		name       string
		sampler    func() (float64, error)
		threshold  float64
		wantOK     bool
		wantReason string
	}{
		{name: "below threshold", sampler: fixedValue(1.99), threshold: 2, wantOK: true},
		{name: "at threshold", sampler: fixedValue(2), threshold: 2, wantReason: "load average: current=2.00, threshold=2.00"},
		{name: "above threshold", sampler: fixedValue(101), threshold: 100,
			wantReason: "load average: current=101.00, threshold=100.00"},
		{name: "sampler error", sampler: failingValue, threshold: 2, wantReason: "failed to get load average: boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewChecker(0)
			checker.loadAvg = tt.sampler
			ok, reason := checker.checkLoadAvg(tt.threshold)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestCheckDiskFree(t *testing.T) {
	tests := []struct {
		name       string
		used       float64
		err        error
		minFree    int
		wantOK     bool
		wantReason string
	}{
		{name: "above minimum", used: 5, minFree: 90, wantOK: true},
		{name: "at minimum", used: 10, minFree: 90, wantOK: true},
		{name: "fractional used rounds free up", used: 10.9, minFree: 90, wantOK: true},
		{name: "below minimum", used: 11, minFree: 90, wantReason: "disk free: current=89%, threshold=90%, path=/data"},
		{name: "usage error", err: errors.New("boom"), minFree: 90, wantReason: "failed to get disk usage for /data: boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			checker := NewChecker(0)
			checker.diskUsedPercent = func(path string) (float64, error) {
				gotPath = path
				return tt.used, tt.err
			}
			ok, reason := checker.checkDiskFree(tt.minFree, "/data")
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
			assert.Equal(t, "/data", gotPath)
		})
	}
}

func TestCheckCustom(t *testing.T) {
	checker := NewChecker(0)

	// test successful script
	ok, reason := checker.checkCustom("true", 30*time.Second)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test failing script
	ok, reason = checker.checkCustom("false", 30*time.Second)
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")

	// test script with output (should still work)
	ok, reason = checker.checkCustom("echo 'test' && exit 0", 30*time.Second)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test non-existent command
	ok, reason = checker.checkCustom("/non/existent/command", 30*time.Second)
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")
}

func TestCheckCustom_Timeout(t *testing.T) {
	checker := NewChecker(0)

	// test with default timeout
	start := time.Now()
	ok, reason := checker.checkCustom("sleep 2", 1*time.Second)
	duration := time.Since(start)

	assert.False(t, ok)
	assert.Equal(t, "custom check timed out after 1s", reason)
	assert.GreaterOrEqual(t, duration, 1*time.Second)
	assert.Less(t, duration, 2*time.Second)

	// test with custom timeout from config
	conditions := Config{
		Custom:        "sleep 2",
		CustomTimeout: new(500 * time.Millisecond),
	}

	start = time.Now()
	ok, reason = checker.Check(conditions)
	duration = time.Since(start)

	assert.False(t, ok)
	assert.Equal(t, "custom check timed out after 500ms", reason)
	assert.GreaterOrEqual(t, duration, 500*time.Millisecond)
	assert.Less(t, duration, 1*time.Second)
}

func TestCheckWithCustomScript(t *testing.T) {
	checker := NewChecker(0)

	// create a temporary script
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "check.sh")

	// create a script that checks if a file exists
	script := `#!/bin/sh
if [ -f /tmp/cronn-test-marker ]; then
    exit 0
else
    exit 1
fi`

	err := os.WriteFile(scriptPath, []byte(script), 0o755) //nolint:gosec // script needs to be executable
	require.NoError(t, err)

	// test when marker file doesn't exist
	conditions := Config{
		Custom: scriptPath,
	}
	ok, reason := checker.Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")

	// create marker file
	markerFile := "/tmp/cronn-test-marker"
	err = os.WriteFile(markerFile, []byte("test"), 0o600)
	require.NoError(t, err)
	defer os.Remove(markerFile)

	// test when marker file exists
	ok, reason = checker.Check(conditions)
	assert.True(t, ok)
	assert.Empty(t, reason)
}

func TestCheckMultipleConditions(t *testing.T) {
	checker := fixedChecker()

	conditions := Config{
		CPUBelow:      new(99),
		MemoryBelow:   new(99),
		LoadAvgBelow:  new(100.0),
		DiskFreeAbove: new(1),
		DiskFreePath:  "/",
		Custom:        "true",
	}

	ok, reason := checker.Check(conditions)
	assert.True(t, ok)
	assert.Empty(t, reason)

	conditions.CPUBelow = new(0)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Equal(t, "CPU: current=10%, threshold=0%", reason)

	conditions.CPUBelow = new(99)
	conditions.MemoryBelow = new(0)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Equal(t, "memory: current=50%, threshold=0%", reason)

	conditions.MemoryBelow = new(99)
	conditions.LoadAvgBelow = new(0.0)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Equal(t, "load average: current=1.50, threshold=0.00", reason)

	conditions.LoadAvgBelow = new(100.0)
	conditions.DiskFreeAbove = new(100)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Equal(t, "disk free: current=60%, threshold=100%, path=/", reason)

	conditions.DiskFreeAbove = new(1)
	conditions.Custom = "false"
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")
}

func TestCheckDiskFreeDefaultPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantPath string
	}{
		{name: "empty path uses root", path: "", wantPath: "/"},
		{name: "explicit path", path: "/data", wantPath: "/data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			checker := fixedChecker()
			checker.diskUsedPercent = func(path string) (float64, error) {
				gotPath = path
				return 40, nil
			}
			ok, reason := checker.Check(Config{DiskFreeAbove: new(1), DiskFreePath: tt.path})
			assert.True(t, ok)
			assert.Empty(t, reason)
			assert.Equal(t, tt.wantPath, gotPath)
		})
	}
}

func TestRealSystemMetrics(t *testing.T) {
	checker := NewChecker(0)

	t.Run("cpu metrics", func(t *testing.T) {
		cpuPercent, err := checker.cpuPercent()
		require.NoError(t, err)
		assert.NotEmpty(t, cpuPercent)
		assert.GreaterOrEqual(t, cpuPercent[0], 0.0)
		assert.LessOrEqual(t, cpuPercent[0], 100.0)
	})

	t.Run("memory metrics", func(t *testing.T) {
		used, err := checker.memPercent()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, used, 0.0)
		assert.LessOrEqual(t, used, 100.0)
	})

	t.Run("load average", func(t *testing.T) {
		load1, err := checker.loadAvg()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, load1, 0.0)
	})

	t.Run("disk usage", func(t *testing.T) {
		used, err := checker.diskUsedPercent("/")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, used, 0.0)
		assert.LessOrEqual(t, used, 100.0)
	})

	t.Run("disk usage for missing path", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		ok, reason := checker.checkDiskFree(10, missing)
		assert.False(t, ok)
		assert.Contains(t, reason, "failed to get disk usage for "+missing+": ")
	})
}

func TestMaxConcurrentChecks(t *testing.T) {
	// create checker with very small limit
	checker := NewChecker(2) // only 2 concurrent checks allowed

	// create condition that takes time to check
	cond := Config{
		Custom: "sleep 0.1", // 100ms sleep
	}

	// track how many checks are running concurrently
	var running atomic.Int32
	var maxRunning atomic.Int32
	var completed atomic.Int32

	// start many goroutines trying to check conditions
	numGoroutines := 10
	start := make(chan struct{})
	done := make(chan struct{}, numGoroutines)

	for range numGoroutines {
		go func() {
			<-start // wait for signal to start

			// try to check conditions
			ok, reason := checker.Check(cond)

			// if we got concurrency limit error, that's expected
			if !ok && reason == "condition check limit reached, try increasing --max-concurrent-checks or wait for running checks to complete" {
				completed.Add(1)
				done <- struct{}{}
				return
			}

			// otherwise we're actually running the check
			current := running.Add(1)
			for {
				maxVal := maxRunning.Load()
				if current > maxVal {
					if maxRunning.CompareAndSwap(maxVal, current) {
						break
					}
				} else {
					break
				}
			}

			// check should succeed (sleep 0.1 exits with 0)
			assert.True(t, ok)
			assert.Empty(t, reason)

			running.Add(-1)
			completed.Add(1)
			done <- struct{}{}
		}()
	}

	// start all goroutines
	close(start)

	// wait for all to complete
	for range numGoroutines {
		<-done
	}

	// verify we never exceeded the limit
	assert.LessOrEqual(t, int(maxRunning.Load()), 2, "should never have more than 2 concurrent checks")
	assert.Equal(t, int32(numGoroutines), completed.Load(), "all goroutines should complete")

	// at least some should have been rejected due to limit
	// (with 10 goroutines and 100ms sleep, we expect some rejections)
	t.Logf("Max concurrent checks: %d", maxRunning.Load())
}

func TestConcurrentChecksDifferentLimits(t *testing.T) {
	tests := []struct {
		name     string
		limit    int
		expected int
	}{
		{"negative becomes 10", -1, 10},
		{"zero becomes 10", 0, 10},
		{"custom limit 5", 5, 5},
		{"custom limit 1", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewChecker(tt.limit)
			assert.Equal(t, tt.expected, checker.maxConcurrent)
			assert.Equal(t, tt.expected, cap(checker.semaphore))
		})
	}
}
