package conditions

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name       string
		conditions crontab.ConditionsConfig
		setupMocks func()
		wantOK     bool
		wantReason string
	}{
		{
			name:       "no conditions",
			conditions: crontab.ConditionsConfig{},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "cpu below threshold passes",
			conditions: crontab.ConditionsConfig{
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
			conditions: crontab.ConditionsConfig{
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
			conditions: crontab.ConditionsConfig{
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
			conditions: crontab.ConditionsConfig{
				Custom: "exit 0",
			},
			wantOK:     true,
			wantReason: "",
		},
		{
			name: "custom script failure",
			conditions: crontab.ConditionsConfig{
				Custom: "exit 1",
			},
			wantOK:     false,
			wantReason: "custom check failed: exit status 1",
		},
		{
			name: "multiple conditions all pass",
			conditions: crontab.ConditionsConfig{
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
			conditions: crontab.ConditionsConfig{
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

			gotOK, gotReason := Check(tt.conditions)
			assert.Equal(t, tt.wantOK, gotOK)
			if tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, gotReason)
			}
		})
	}
}

func TestCheckCPU(t *testing.T) {
	// test with real CPU data - should pass with high threshold
	ok, reason := checkCPU(99)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very low threshold - likely to fail
	ok, reason = checkCPU(0)
	assert.False(t, ok)
	assert.Contains(t, reason, "CPU at")
	assert.Contains(t, reason, "threshold 0%")
}

func TestCheckMemory(t *testing.T) {
	// test with real memory data - should pass with high threshold
	ok, reason := checkMemory(99)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very low threshold - likely to fail
	ok, reason = checkMemory(0)
	assert.False(t, ok)
	assert.Contains(t, reason, "memory at")
	assert.Contains(t, reason, "threshold 0%")
}

func TestCheckLoadAvg(t *testing.T) {
	// test with real load data - should pass with high threshold
	ok, reason := checkLoadAvg(100.0)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very low threshold - likely to fail on any system
	ok, reason = checkLoadAvg(0.0)
	assert.False(t, ok)
	assert.Contains(t, reason, "load at")
	assert.Contains(t, reason, "threshold 0.00")
}

func TestCheckDiskFree(t *testing.T) {
	// test with real disk data - should pass with low threshold
	ok, reason := checkDiskFree(1, "/")
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with very high threshold - likely to fail
	ok, reason = checkDiskFree(100, "/")
	assert.False(t, ok)
	assert.Contains(t, reason, "disk free at")
	assert.Contains(t, reason, "need 100%")

	// test with non-existent path
	ok, reason = checkDiskFree(10, "/non/existent/path")
	assert.False(t, ok)
	assert.Contains(t, reason, "failed to get disk usage")
}

func TestCheckCustom(t *testing.T) {
	// test successful script
	ok, reason := checkCustom("true")
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test failing script
	ok, reason = checkCustom("false")
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")

	// test script with output (should still work)
	ok, reason = checkCustom("echo 'test' && exit 0")
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test non-existent command
	ok, reason = checkCustom("/non/existent/command")
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")
}

func TestCheckWithCustomScript(t *testing.T) {
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
	
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	// test when marker file doesn't exist
	conditions := crontab.ConditionsConfig{
		Custom: scriptPath,
	}
	ok, reason := Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")

	// create marker file
	markerFile := "/tmp/cronn-test-marker"
	err = os.WriteFile(markerFile, []byte("test"), 0644)
	require.NoError(t, err)
	defer os.Remove(markerFile)

	// test when marker file exists
	ok, reason = Check(conditions)
	assert.True(t, ok)
	assert.Empty(t, reason)
}

func TestCheckMultipleConditions(t *testing.T) {
	// test with all conditions passing
	conditions := crontab.ConditionsConfig{
		CPUBelow:      intPtr(99),
		MemoryBelow:   intPtr(99),
		LoadAvgBelow:  float64Ptr(100.0),
		DiskFreeAbove: intPtr(1),
		DiskFreePath:  "/",
		Custom:        "true",
	}
	
	ok, reason := Check(conditions)
	assert.True(t, ok)
	assert.Empty(t, reason)

	// test with CPU failing
	conditions.CPUBelow = intPtr(0)
	ok, reason = Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "CPU at")

	// test with memory failing
	conditions.CPUBelow = intPtr(99)
	conditions.MemoryBelow = intPtr(0)
	ok, reason = Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "memory at")

	// test with load average failing
	conditions.MemoryBelow = intPtr(99)
	conditions.LoadAvgBelow = float64Ptr(0.0)
	ok, reason = Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "load at")

	// test with disk free failing
	conditions.LoadAvgBelow = float64Ptr(100.0)
	conditions.DiskFreeAbove = intPtr(100)
	ok, reason = Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "disk free at")

	// test with custom script failing
	conditions.DiskFreeAbove = intPtr(1)
	conditions.Custom = "false"
	ok, reason = Check(conditions)
	assert.False(t, ok)
	assert.Contains(t, reason, "custom check failed")
}

func TestCheckDiskFreeDefaultPath(t *testing.T) {
	// test that empty path defaults to "/"
	conditions := crontab.ConditionsConfig{
		DiskFreeAbove: intPtr(1),
		DiskFreePath:  "", // empty path should default to "/"
	}
	
	ok, _ := Check(conditions)
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

// helper functions
func intPtr(i int) *int {
	return &i
}

func float64Ptr(f float64) *float64 {
	return &f
}

func durationPtr(d time.Duration) *time.Duration {
	return &d
}