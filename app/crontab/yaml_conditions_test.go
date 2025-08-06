package crontab

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLParserWithConditions(t *testing.T) {
	// create test YAML file
	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "test-conditions.yml")
	
	yamlContent := `# Test YAML with conditions
- name: "cpu-check"
  command: "echo test"
  schedule: "0 * * * *"
  conditions:
    cpu_below: 50

- name: "memory-check"
  command: "echo memory"
  schedule: "0 * * * *"
  conditions:
    memory_below: 70
    max_postpone: "1h"
    check_interval: "5m"

- name: "disk-check"
  command: "echo disk"
  schedule: "0 * * * *"
  conditions:
    disk_free_above: 20
    disk_free_path: "/tmp"

- name: "multi-conditions"
  command: "echo multi"
  schedule: "0 * * * *"
  conditions:
    cpu_below: 60
    memory_below: 80
    load_avg_below: 2.5
    disk_free_above: 10
    custom: "/usr/local/bin/check.sh"
    max_postpone: "2h"
    check_interval: "1m"

- name: "no-conditions"
  command: "echo normal"
  schedule: "0 * * * *"
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	require.NoError(t, err)

	parser := NewYAML(yamlFile, time.Hour, nil)
	jobs, err := parser.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 5)

	// test cpu-check job
	assert.Equal(t, "cpu-check", jobs[0].Name)
	assert.NotNil(t, jobs[0].Conditions)
	assert.NotNil(t, jobs[0].Conditions.CPUBelow)
	assert.Equal(t, 50, *jobs[0].Conditions.CPUBelow)
	assert.Nil(t, jobs[0].Conditions.MaxPostpone)

	// test memory-check job with postponement
	assert.Equal(t, "memory-check", jobs[1].Name)
	assert.NotNil(t, jobs[1].Conditions)
	assert.NotNil(t, jobs[1].Conditions.MemoryBelow)
	assert.Equal(t, 70, *jobs[1].Conditions.MemoryBelow)
	assert.NotNil(t, jobs[1].Conditions.MaxPostpone)
	assert.Equal(t, time.Hour, *jobs[1].Conditions.MaxPostpone)
	assert.NotNil(t, jobs[1].Conditions.CheckInterval)
	assert.Equal(t, 5*time.Minute, *jobs[1].Conditions.CheckInterval)

	// test disk-check job
	assert.Equal(t, "disk-check", jobs[2].Name)
	assert.NotNil(t, jobs[2].Conditions)
	assert.NotNil(t, jobs[2].Conditions.DiskFreeAbove)
	assert.Equal(t, 20, *jobs[2].Conditions.DiskFreeAbove)
	assert.Equal(t, "/tmp", jobs[2].Conditions.DiskFreePath)

	// test multi-conditions job
	assert.Equal(t, "multi-conditions", jobs[3].Name)
	assert.NotNil(t, jobs[3].Conditions)
	assert.NotNil(t, jobs[3].Conditions.CPUBelow)
	assert.Equal(t, 60, *jobs[3].Conditions.CPUBelow)
	assert.NotNil(t, jobs[3].Conditions.MemoryBelow)
	assert.Equal(t, 80, *jobs[3].Conditions.MemoryBelow)
	assert.NotNil(t, jobs[3].Conditions.LoadAvgBelow)
	assert.Equal(t, 2.5, *jobs[3].Conditions.LoadAvgBelow)
	assert.NotNil(t, jobs[3].Conditions.DiskFreeAbove)
	assert.Equal(t, 10, *jobs[3].Conditions.DiskFreeAbove)
	assert.Equal(t, "/usr/local/bin/check.sh", jobs[3].Conditions.Custom)
	assert.NotNil(t, jobs[3].Conditions.MaxPostpone)
	assert.Equal(t, 2*time.Hour, *jobs[3].Conditions.MaxPostpone)
	assert.NotNil(t, jobs[3].Conditions.CheckInterval)
	assert.Equal(t, time.Minute, *jobs[3].Conditions.CheckInterval)

	// test no-conditions job
	assert.Equal(t, "no-conditions", jobs[4].Name)
	assert.Nil(t, jobs[4].Conditions)
}

func TestYAMLParserConditionsWithRepeater(t *testing.T) {
	// create test YAML file
	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "test-both.yml")
	
	yamlContent := `# Test YAML with both conditions and repeater
- name: "with-both"
  command: "echo test"
  schedule: "0 * * * *"
  conditions:
    cpu_below: 50
    max_postpone: "30m"
  repeater:
    attempts: 5
    duration: "5s"
    factor: 2.0
    jitter: true
`

	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	require.NoError(t, err)

	parser := NewYAML(yamlFile, time.Hour, nil)
	jobs, err := parser.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 1)

	job := jobs[0]
	assert.Equal(t, "with-both", job.Name)
	
	// check conditions
	assert.NotNil(t, job.Conditions)
	assert.NotNil(t, job.Conditions.CPUBelow)
	assert.Equal(t, 50, *job.Conditions.CPUBelow)
	assert.NotNil(t, job.Conditions.MaxPostpone)
	assert.Equal(t, 30*time.Minute, *job.Conditions.MaxPostpone)
	
	// check repeater
	assert.NotNil(t, job.Repeater)
	assert.NotNil(t, job.Repeater.Attempts)
	assert.Equal(t, 5, *job.Repeater.Attempts)
	assert.NotNil(t, job.Repeater.Duration)
	assert.Equal(t, 5*time.Second, *job.Repeater.Duration)
	assert.NotNil(t, job.Repeater.Factor)
	assert.Equal(t, 2.0, *job.Repeater.Factor)
	assert.NotNil(t, job.Repeater.Jitter)
	assert.Equal(t, true, *job.Repeater.Jitter)
}