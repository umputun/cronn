# Conditional Job Execution Plan

## Overview

This feature adds support for conditional job execution based on system resource thresholds. Jobs can be configured to run only when system resources (CPU, memory, disk I/O, load average) are below specified thresholds. Jobs can either be skipped entirely or postponed with a maximum delay when conditions aren't met.

### Use Cases

1. **Backup Jobs** - Run intensive backups only when system is idle
2. **Batch Processing** - Execute heavy data processing during low-usage periods  
3. **Maintenance Tasks** - Perform system maintenance when resource usage is minimal
4. **Report Generation** - Generate resource-intensive reports during off-peak times

## Configuration Design

### YAML Structure

```yaml
jobs:
  - name: "Heavy backup job"
    spec: "0 2 * * *"
    command: "backup.sh /data"
    conditions:
      max_postpone: 2h      # Optional - if not set, job skips when conditions fail
      check_interval: 30s   # Optional - defaults to 30s
      cpu_below: 50         # CPU usage < 50%
      memory_below: 70      # Memory usage < 70%
      disk_io_below: 30     # Disk I/O < 30 MB/s
      load_avg_below: 2.0   # Load average < 2.0
      custom: "/usr/local/bin/check-conditions.sh"  # Optional custom script
```

### Behavior

- **Without `max_postpone`**: Job is skipped if conditions aren't met
- **With `max_postpone`**: Job waits up to specified duration, checking conditions periodically
- **At deadline**: Job executes regardless of conditions with a warning
- **All conditions**: Must be met (implicit AND) for job to run

### Default Values

- `check_interval`: 30 seconds if not specified
- Other thresholds: No defaults, only checked if specified

## Architecture

### Integration Points

```
Cron Trigger → JobFunc → Check Conditions → Execute/Skip/Postpone
                              ↓
                     [Conditions Met?]
                        /     |     \
                     Yes     No      No
                      ↓       ↓       ↓
                  Execute   Skip   Postpone
                           (no max)  (with max)
                                      ↓
                                  Wait Loop
                                   (ticker)
```

### Component Structure

1. **ConditionsConfig** - Configuration structure in `app/crontab/crontab.go`
2. **ConditionChecker** - System metrics checker in `app/service/conditions.go`
3. **Modified jobFunc** - Enhanced job function in `app/service/service.go`

## Implementation Details

### 1. Data Structures

```go
// app/crontab/crontab.go
type ConditionsConfig struct {
    MaxPostpone   *time.Duration `yaml:"max_postpone,omitempty" json:"max_postpone,omitempty"`
    CheckInterval *time.Duration `yaml:"check_interval,omitempty" json:"check_interval,omitempty"`
    CPUBelow      *int          `yaml:"cpu_below,omitempty" json:"cpu_below,omitempty"`
    MemoryBelow   *int          `yaml:"memory_below,omitempty" json:"memory_below,omitempty"`
    DiskIOBelow   *int          `yaml:"disk_io_below,omitempty" json:"disk_io_below,omitempty"`
    LoadAvgBelow  *float64      `yaml:"load_avg_below,omitempty" json:"load_avg_below,omitempty"`
    Custom        string         `yaml:"custom,omitempty" json:"custom,omitempty"`
}

// Add to JobSpec
type JobSpec struct {
    // ... existing fields ...
    Conditions *ConditionsConfig `yaml:"conditions,omitempty" json:"conditions,omitempty"`
}
```

### 2. Condition Checker

```go
// app/service/conditions.go
type ConditionChecker struct {
    lastDiskStats map[string]disk.IOCountersStat
    lastCheckTime time.Time
    mu            sync.RWMutex
}

func NewConditionChecker() *ConditionChecker {
    return &ConditionChecker{
        lastDiskStats: make(map[string]disk.IOCountersStat),
    }
}

func (c *ConditionChecker) Check(conditions crontab.ConditionsConfig) (bool, string) {
    // Check each condition if specified
    // Return false with reason on first failure
    // Return true if all conditions met
}
```

### 3. Modified Job Function

Note: Context flows from main through the entire call chain:
```go
// In main.go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
// ... handle signals to call cancel()
cronService.Do(ctx)

// In Do() method
func (s *Scheduler) Do(ctx context.Context) {
    // ... pass ctx to schedule()
}

// In schedule() method  
func (s *Scheduler) schedule(ctx context.Context, r crontab.JobSpec) error {
    // ...
    id := s.Schedule(sched, s.jobFunc(ctx, r, sched))
    // ...
}

func (s *Scheduler) jobFunc(ctx context.Context, r crontab.JobSpec, sched Schedule) cron.FuncJob {
    runJob := func(r crontab.JobSpec, rptr Repeater) error {
        // ... existing runJob implementation ...
    }

    return func() {
        jobDesc := s.jobDescription(r)
        jobRepeater := s.getJobRepeater(r.Repeater)
        
        // Check conditions if configured
        if r.Conditions != nil {
            shouldExecute := s.waitForConditions(ctx, *r.Conditions, jobDesc)
            if !shouldExecute {
                return
            }
        }
        
        // Normal execution
        log.Printf("[INFO] executing: %s", jobDesc)
        if err := runJob(r, jobRepeater); err != nil {
            log.Printf("[WARN] job failed: %s, %v", jobDesc, err)
        } else {
            log.Printf("[INFO] completed %s", jobDesc)
        }
        log.Printf("[INFO] next: %s, %s", sched.Next(time.Now()).Format(time.RFC3339), jobDesc)
    }
}

func (s *Scheduler) waitForConditions(ctx context.Context, conditions crontab.ConditionsConfig, jobDesc string) bool {
    met, reason := s.ConditionChecker.Check(conditions)
    if met {
        return true
    }
    
    // No postpone configured - skip job
    if conditions.MaxPostpone == nil {
        log.Printf("[INFO] job skipped: %s, reason: %s", jobDesc, reason)
        return false
    }
    
    // Set up postponement
    deadline := time.Now().Add(*conditions.MaxPostpone)
    log.Printf("[INFO] job postponed: %s, reason: %s, deadline: %s", 
        jobDesc, reason, deadline.Format(time.RFC3339))
    
    checkInterval := 30 * time.Second
    if conditions.CheckInterval != nil {
        checkInterval = *conditions.CheckInterval
    }
    
    ticker := time.NewTicker(checkInterval)
    defer ticker.Stop()
    
    deadlineTimer := time.NewTimer(*conditions.MaxPostpone)
    defer deadlineTimer.Stop()
    
    for {
        select {
        case <-ticker.C:
            met, reason = s.ConditionChecker.Check(conditions)
            if met {
                log.Printf("[INFO] conditions met, executing postponed job: %s", jobDesc)
                return true
            }
            
        case <-deadlineTimer.C:
            log.Printf("[WARN] max postpone reached, executing anyway: %s", jobDesc)
            return true
            
        case <-ctx.Done():
            log.Printf("[INFO] postponed job cancelled: %s", jobDesc)
            return false
        }
    }
}
```

### 4. System Metrics Implementation

```go
// Check CPU usage
func (c *ConditionChecker) checkCPU(threshold int) (bool, string) {
    cpuPercent, err := cpu.Percent(time.Second, false)
    if err != nil {
        return false, fmt.Sprintf("failed to get CPU: %v", err)
    }
    current := int(cpuPercent[0])
    if current >= threshold {
        return false, fmt.Sprintf("CPU at %d%%, threshold %d%%", current, threshold)
    }
    return true, ""
}

// Check memory usage
func (c *ConditionChecker) checkMemory(threshold int) (bool, string) {
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

// Check disk I/O rate (MB/s)
func (c *ConditionChecker) checkDiskIO(threshold int) (bool, string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    stats, err := disk.IOCounters()
    if err != nil {
        return false, fmt.Sprintf("failed to get disk I/O: %v", err)
    }
    
    now := time.Now()
    if !c.lastCheckTime.IsZero() {
        elapsed := now.Sub(c.lastCheckTime).Seconds()
        var totalRate float64
        
        for name, stat := range stats {
            if last, ok := c.lastDiskStats[name]; ok {
                bytesRead := stat.ReadBytes - last.ReadBytes
                bytesWritten := stat.WriteBytes - last.WriteBytes
                rate := float64(bytesRead+bytesWritten) / elapsed / (1024 * 1024) // MB/s
                totalRate += rate
            }
        }
        
        if int(totalRate) >= threshold {
            return false, fmt.Sprintf("disk I/O at %dMB/s, threshold %dMB/s", int(totalRate), threshold)
        }
    }
    
    c.lastDiskStats = stats
    c.lastCheckTime = now
    return true, ""
}

// Check custom script
func (c *ConditionChecker) checkCustom(script string) (bool, string) {
    cmd := exec.Command("sh", "-c", script)
    if err := cmd.Run(); err != nil {
        return false, fmt.Sprintf("custom check failed: %v", err)
    }
    return true, ""
}
```

## Dependencies

Add to `go.mod`:
```
github.com/shirou/gopsutil/v3 v3.24.1
```

## Testing Strategy

### Unit Tests

1. **ConditionChecker Tests** (`app/service/conditions_test.go`)
   - Mock gopsutil functions
   - Test each condition type independently
   - Test combined conditions
   - Test custom script execution

2. **Job Function Tests** (`app/service/service_test.go`)
   - Test skip behavior (no max_postpone)
   - Test postponement with timeout
   - Test successful condition wait
   - Test context cancellation

3. **YAML Parsing Tests** (`app/crontab/crontab_test.go`)
   - Test conditions configuration parsing
   - Test duration parsing
   - Test validation

### Integration Tests

1. **End-to-End Tests**
   - Create test crontab with conditions
   - Mock system metrics
   - Verify job execution/skip/postpone behavior
   - Test deadline enforcement

### Manual Testing Checklist

- [ ] Job skips when CPU threshold exceeded
- [ ] Job postpones and waits for conditions
- [ ] Job executes at deadline with warning
- [ ] Custom script integration works
- [ ] Multiple conditions work together
- [ ] Context cancellation stops waiting jobs

## Documentation Updates

### README.md

Add section on conditional execution:
```markdown
## Conditional Execution

Jobs can be configured to run only when system resources are below specified thresholds:

\```yaml
jobs:
  - name: "Resource-intensive task"
    spec: "0 3 * * *"
    command: "heavy-process.sh"
    conditions:
      cpu_below: 30        # Only run if CPU < 30%
      memory_below: 50     # Only run if memory < 50%
      max_postpone: 1h     # Wait up to 1 hour for conditions
\```

Without `max_postpone`, jobs are skipped if conditions aren't met.
With `max_postpone`, jobs wait and retry until conditions are met or deadline is reached.
\```
```

### Examples

Create `examples/conditional-jobs.yml`:
```yaml
jobs:
  # Skip if system is busy
  - name: "Optional maintenance"
    spec: "0 4 * * *"
    command: "maintenance.sh"
    conditions:
      cpu_below: 20
      load_avg_below: 1.0

  # Wait for quiet period
  - name: "Important backup"
    spec: "0 2 * * *"
    command: "backup.sh"
    conditions:
      max_postpone: 3h
      cpu_below: 30
      memory_below: 40
      disk_io_below: 10

  # Custom condition check
  - name: "Business hours check"
    spec: "*/30 * * * *"
    command: "process.sh"
    conditions:
      custom: "/usr/local/bin/check-business-hours.sh"
```

## Implementation Phases

### Phase 1: Core Infrastructure
1. Add gopsutil dependency
2. Create ConditionsConfig structure
3. Update JobSpec with conditions field
4. Update JSON schema

### Phase 2: Condition Checking
1. Implement ConditionChecker
2. Add CPU, memory, load average checks
3. Add disk I/O monitoring
4. Add custom script support

### Phase 3: Scheduler Integration
1. Modify jobFunc to check conditions
2. Implement skip logic
3. Implement postponement logic
4. Add proper logging

### Phase 4: Testing & Documentation
1. Write unit tests
2. Write integration tests
3. Update documentation
4. Add examples

## Future Enhancements

These are not part of the initial implementation but could be added later:

1. **Network conditions** - Check network throughput/latency
2. **Process count conditions** - Check number of specific processes
3. **Time-based conditions** - Business hours, maintenance windows
4. **Condition combinations** - Support for OR logic, not just AND
5. **Metrics export** - Export postponement/skip statistics
6. **Persistent postponement** - Survive restarts (requires database)
7. **Priority queues** - Handle multiple postponed jobs intelligently
8. **Condition history** - Track when and why jobs were postponed/skipped
9. **Dynamic thresholds** - Adjust thresholds based on time of day
10. **Webhook conditions** - Check external services via HTTP

## Risks and Mitigations

1. **Risk**: Postponed jobs consuming resources while waiting
   - **Mitigation**: Ticker-based approach is lightweight

2. **Risk**: Jobs never running due to strict conditions
   - **Mitigation**: max_postpone ensures eventual execution

3. **Risk**: Disk I/O calculation inaccuracy
   - **Mitigation**: Simple implementation, document limitations

4. **Risk**: Custom scripts hanging
   - **Mitigation**: Consider adding timeout for custom scripts

5. **Risk**: Multiple postponed jobs creating backlog
   - **Mitigation**: Each job handles its own postponement independently

## Success Criteria

1. Jobs can be configured with resource conditions
2. Jobs skip or postpone based on configuration
3. System metrics are accurately measured
4. Custom scripts integrate properly
5. All tests pass
6. Documentation is complete
7. No performance regression for jobs without conditions