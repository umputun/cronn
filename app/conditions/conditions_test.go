package conditions

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheck(t *testing.T) {
	checker := NewChecker(0) // use default

	tests := []struct {
		name       string
		conditions Config
		setupMocks func()
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
				CPUBelow: intPtr(50),
			},
			setupMocks: func() {
				// real CPU check, should pass with high threshold
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "memory below threshold passes",
			conditions: Config{
				MemoryBelow: intPtr(99),
			},
			setupMocks: func() {
				// real memory check, should pass with high threshold
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "disk free above threshold passes",
			conditions: Config{
				DiskFreeAbove: intPtr(1),
				DiskFreePath:  "/",
			},
			setupMocks: func() {
				// real disk check, should pass with low threshold
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
				CPUBelow:      intPtr(99),
				MemoryBelow:   intPtr(99),
				DiskFreeAbove: intPtr(1),
				Custom:        "exit 0",
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "multiple conditions one fails",
			conditions: Config{
				CPUBelow:      intPtr(99),
				MemoryBelow:   intPtr(99),
				DiskFreeAbove: intPtr(1),
				Custom:        "exit 1",
			},
			wantOK:     false,
			wantReason: "custom check failed: exit status 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupMocks != nil {
				tt.setupMocks()
			}

			gotOK, gotReason := checker.Check(tt.conditions)
			assert.Equal(t, tt.wantOK, gotOK)
			if tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, gotReason)
			}
		})
	}
}

func TestCheckCPU(t *testing.T) {
	checker := NewChecker(0)

	// test with real CPU data - should pass with high threshold
	ok, reason := checker.checkCPU(99)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very low threshold - likely to fail
	ok, reason = checker.checkCPU(0)
	assert.False(t, ok)
	assert.Contains(t, reason, "CPU at")
	assert.Contains(t, reason, "threshold 0%")
}

func TestCheckMemory(t *testing.T) {
	checker := NewChecker(0)

	// test with real memory data - should pass with high threshold
	ok, reason := checker.checkMemory(99)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very low threshold - likely to fail
	ok, reason = checker.checkMemory(0)
	assert.False(t, ok)
	assert.Contains(t, reason, "memory at")
	assert.Contains(t, reason, "threshold 0%")
}

func TestCheckLoadAvg(t *testing.T) {
	checker := NewChecker(0)

	// test with real load data - should pass with high threshold
	ok, reason := checker.checkLoadAvg(100.0)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very low threshold - likely to fail on any system
	ok, reason = checker.checkLoadAvg(0.0)
	assert.False(t, ok)
	assert.Contains(t, reason, "load at")
	assert.Contains(t, reason, "threshold 0.00")
}

func TestCheckDiskFree(t *testing.T) {
	checker := NewChecker(0)

	// test with real disk data - should pass with low threshold
	ok, reason := checker.checkDiskFree(1, "/")
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very high threshold - likely to fail
	ok, reason = checker.checkDiskFree(100, "/")
	assert.False(t, ok)
	assert.Contains(t, reason, "disk free at")
	assert.Contains(t, reason, "need 100%")

	// test with non-existent path
	ok, reason = checker.checkDiskFree(10, "/non/existent/path")
	assert.False(t, ok)
	assert.Contains(t, reason, "failed to get disk usage")
}

func TestCheckCustom(t *testing.T) {
	checker := NewChecker(0)

	// test successful script
	ok, reason := checker.checkCustom("true")
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test failing script
	ok, reason = checker.checkCustom("false")
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")

	// test script with output (should still work)
	ok, reason = checker.checkCustom("echo 'test' && exit 0")
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test non-existent command
	ok, reason = checker.checkCustom("/non/existent/command")
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")
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
	checker := NewChecker(0)

	// test with all conditions passing
	conditions := Config{
		CPUBelow:      intPtr(99),
		MemoryBelow:   intPtr(99),
		LoadAvgBelow:  float64Ptr(100.0),
		DiskFreeAbove: intPtr(1),
		DiskFreePath:  "/",
		Custom:        "true",
	}

	ok, reason := checker.Check(conditions)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with CPU failing
	conditions.CPUBelow = intPtr(0)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "CPU at")

	// test with memory failing
	conditions.CPUBelow = intPtr(99)
	conditions.MemoryBelow = intPtr(0)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "memory at")

	// test with load average failing
	conditions.MemoryBelow = intPtr(99)
	conditions.LoadAvgBelow = float64Ptr(0.0)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "load at")

	// test with disk free failing
	conditions.LoadAvgBelow = float64Ptr(100.0)
	conditions.DiskFreeAbove = intPtr(100)
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "disk free at")

	// test with custom script failing
	conditions.DiskFreeAbove = intPtr(1)
	conditions.Custom = "false"
	ok, reason = checker.Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")
}

func TestCheckDiskFreeDefaultPath(t *testing.T) {
	checker := NewChecker(0)

	// test that empty path defaults to "/"
	conditions := Config{
		DiskFreeAbove: intPtr(1),
		DiskFreePath:  "", // empty path should default to "/"
	}

	ok, _ := checker.Check(conditions)
	assert.True(t, ok)
}

func TestRealSystemMetrics(t *testing.T) {
	// this test verifies that we can actually get real system metrics
	// without errors - important for integration testing

	t.Run("cpu metrics", func(t *testing.T) {
		cpuPercent, err := cpu.Percent(time.Second, false)
		assert.NoError(t, err)
		assert.NotEmpty(t, cpuPercent)
		assert.GreaterOrEqual(t, cpuPercent[0], 0.0)
		assert.LessOrEqual(t, cpuPercent[0], 100.0)
	})

	t.Run("memory metrics", func(t *testing.T) {
		v, err := mem.VirtualMemory()
		assert.NoError(t, err)
		assert.NotNil(t, v)
		assert.GreaterOrEqual(t, v.UsedPercent, 0.0)
		assert.LessOrEqual(t, v.UsedPercent, 100.0)
	})

	t.Run("load average", func(t *testing.T) {
		loads, err := load.Avg()
		assert.NoError(t, err)
		assert.NotNil(t, loads)
		assert.GreaterOrEqual(t, loads.Load1, 0.0)
	})

	t.Run("disk usage", func(t *testing.T) {
		usage, err := disk.Usage("/")
		assert.NoError(t, err)
		assert.NotNil(t, usage)
		assert.GreaterOrEqual(t, usage.UsedPercent, 0.0)
		assert.LessOrEqual(t, usage.UsedPercent, 100.0)

		freePercent := 100 - int(usage.UsedPercent)
		assert.GreaterOrEqual(t, freePercent, 0)
		assert.LessOrEqual(t, freePercent, 100)
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
	var running int32
	var maxRunning int32
	var completed int32

	// start many goroutines trying to check conditions
	numGoroutines := 10
	start := make(chan struct{})
	done := make(chan struct{}, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			<-start // wait for signal to start
			
			// try to check conditions
			ok, reason := checker.Check(cond)
			
			// if we got concurrency limit error, that's expected
			if !ok && reason == "condition check limit reached, try increasing --max-concurrent-checks or wait for running checks to complete" {
				atomic.AddInt32(&completed, 1)
				done <- struct{}{}
				return
			}
			
			// otherwise we're actually running the check
			current := atomic.AddInt32(&running, 1)
			for {
				maxVal := atomic.LoadInt32(&maxRunning)
				if current > maxVal {
					if atomic.CompareAndSwapInt32(&maxRunning, maxVal, current) {
						break
					}
				} else {
					break
				}
			}
			
			// check should succeed (sleep 0.1 exits with 0)
			assert.True(t, ok)
			assert.Empty(t, reason)
			
			atomic.AddInt32(&running, -1)
			atomic.AddInt32(&completed, 1)
			done <- struct{}{}
		}()
	}

	// start all goroutines
	close(start)

	// wait for all to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// verify we never exceeded the limit
	assert.LessOrEqual(t, int(maxRunning), 2, "should never have more than 2 concurrent checks")
	assert.Equal(t, int32(numGoroutines), completed, "all goroutines should complete")
	
	// at least some should have been rejected due to limit
	// (with 10 goroutines and 100ms sleep, we expect some rejections)
	t.Logf("Max concurrent checks: %d", maxRunning)
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

// helper functions
func intPtr(i int) *int {
	return &i
}

func float64Ptr(f float64) *float64 {
	return &f
}
