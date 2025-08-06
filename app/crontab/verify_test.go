package crontab

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyAgainstEmbeddedSchema(t *testing.T) {
	tests := []struct {
		name    string
		config  *YamlConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config with spec",
			config:  &YamlConfig{Jobs: []JobSpec{{Spec: "0 * * * *", Command: "echo test"}}},
			wantErr: false,
		},
		{
			name:    "valid config with sched",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "0", Hour: "*"}, Command: "echo test"}}},
			wantErr: false,
		},
		{
			name: "valid config with name and repeater",
			config: &YamlConfig{Jobs: []JobSpec{{
				Spec: "@daily", Command: "backup.sh", Name: "Daily backup",
				Repeater: &RepeaterConfig{
					Attempts: intPtr(3), Duration: durationPtr(time.Second),
					Factor: floatPtr(2.0), Jitter: boolPtr(true),
				},
			}}},
			wantErr: false,
		},
		{
			name:    "multiple valid jobs",
			config:  &YamlConfig{Jobs: []JobSpec{{Spec: "0 * * * *", Command: "job1"}, {Spec: "@daily", Command: "job2", Name: "second"}}},
			wantErr: false,
		},
		{
			name: "sched with all fields",
			config: &YamlConfig{Jobs: []JobSpec{{
				Sched:   Schedule{Minute: "*/5", Hour: "9-17", Day: "1-5", Month: "*", Weekday: "MON-FRI"},
				Command: "business hours job",
			}}},
			wantErr: false,
		},
		{
			name:    "empty jobs list",
			config:  &YamlConfig{Jobs: []JobSpec{}},
			wantErr: true,
			errMsg:  "at least one job is required",
		},
		{
			name:    "nil jobs list",
			config:  &YamlConfig{Jobs: nil},
			wantErr: true,
			errMsg:  "at least one job is required",
		},
		{
			name:    "missing command",
			config:  &YamlConfig{Jobs: []JobSpec{{Spec: "0 * * * *"}}},
			wantErr: true,
			errMsg:  "command is required",
		},
		{
			name:    "empty command",
			config:  &YamlConfig{Jobs: []JobSpec{{Spec: "0 * * * *", Command: ""}}},
			wantErr: true,
			errMsg:  "command is required",
		},
		{
			name:    "missing both spec and sched",
			config:  &YamlConfig{Jobs: []JobSpec{{Command: "echo test"}}},
			wantErr: true,
			errMsg:  "either 'spec' or 'sched' field is required",
		},
		{
			name:    "both spec and sched provided",
			config:  &YamlConfig{Jobs: []JobSpec{{Spec: "0 * * * *", Sched: Schedule{Minute: "0"}, Command: "echo test"}}},
			wantErr: true,
			errMsg:  "'spec' and 'sched' fields are mutually exclusive",
		},
		{
			name: "invalid repeater attempts low",
			config: &YamlConfig{Jobs: []JobSpec{{
				Spec: "@daily", Command: "test", Repeater: &RepeaterConfig{Attempts: intPtr(0)},
			}}},
			wantErr: true,
			errMsg:  "attempts must be between 1 and 100",
		},
		{
			name: "invalid repeater attempts high",
			config: &YamlConfig{Jobs: []JobSpec{{
				Spec: "@daily", Command: "test", Repeater: &RepeaterConfig{Attempts: intPtr(101)},
			}}},
			wantErr: true,
			errMsg:  "attempts must be between 1 and 100",
		},
		{
			name: "invalid repeater duration too small",
			config: &YamlConfig{Jobs: []JobSpec{{
				Spec: "@daily", Command: "test", Repeater: &RepeaterConfig{Duration: durationPtr(100 * time.Microsecond)},
			}}},
			wantErr: true,
			errMsg:  "duration must be at least 1ms",
		},
		{
			name: "invalid repeater duration too large",
			config: &YamlConfig{Jobs: []JobSpec{{
				Spec: "@daily", Command: "test", Repeater: &RepeaterConfig{Duration: durationPtr(2 * time.Hour)},
			}}},
			wantErr: true,
			errMsg:  "duration must not exceed 1h0m0s",
		},
		{
			name: "invalid repeater factor low",
			config: &YamlConfig{Jobs: []JobSpec{{
				Spec: "@daily", Command: "test", Repeater: &RepeaterConfig{Factor: floatPtr(0.5)},
			}}},
			wantErr: true,
			errMsg:  "factor must be between 1.0 and 10.0",
		},
		{
			name: "invalid repeater factor high",
			config: &YamlConfig{Jobs: []JobSpec{{
				Spec: "@daily", Command: "test", Repeater: &RepeaterConfig{Factor: floatPtr(15.0)},
			}}},
			wantErr: true,
			errMsg:  "factor must be between 1.0 and 10.0",
		},
		{
			name: "second job has error",
			config: &YamlConfig{Jobs: []JobSpec{
				{Spec: "0 * * * *", Command: "job1"},
				{Spec: "0 * * * *"}, // missing command
			}},
			wantErr: true,
			errMsg:  "job 2: command is required",
		},
		{
			name: "third job has both spec and sched",
			config: &YamlConfig{Jobs: []JobSpec{
				{Spec: "0 * * * *", Command: "job1"},
				{Spec: "@daily", Command: "job2"},
				{Spec: "0 * * * *", Sched: Schedule{Hour: "1"}, Command: "job3"},
			}},
			wantErr: true,
			errMsg:  "job 3:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyAgainstEmbeddedSchema(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateRepeaterConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *RepeaterConfig
		jobNum  int
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid full config",
			config:  &RepeaterConfig{Attempts: intPtr(5), Duration: durationPtr(time.Second), Factor: floatPtr(2.5), Jitter: boolPtr(true)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid partial - attempts only",
			config:  &RepeaterConfig{Attempts: intPtr(3)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid partial - duration only",
			config:  &RepeaterConfig{Duration: durationPtr(500 * time.Millisecond)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid partial - factor only",
			config:  &RepeaterConfig{Factor: floatPtr(1.5)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid partial - jitter only",
			config:  &RepeaterConfig{Jitter: boolPtr(false)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid boundary - min attempts",
			config:  &RepeaterConfig{Attempts: intPtr(1)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid boundary - max attempts",
			config:  &RepeaterConfig{Attempts: intPtr(100)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid boundary - min duration",
			config:  &RepeaterConfig{Duration: durationPtr(time.Millisecond)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid boundary - max duration",
			config:  &RepeaterConfig{Duration: durationPtr(time.Hour)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid boundary - min factor",
			config:  &RepeaterConfig{Factor: floatPtr(1.0)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "valid boundary - max factor",
			config:  &RepeaterConfig{Factor: floatPtr(10.0)},
			jobNum:  1,
			wantErr: false,
		},
		{
			name:    "attempts too low",
			config:  &RepeaterConfig{Attempts: intPtr(0)},
			jobNum:  1,
			wantErr: true,
			errMsg:  "attempts must be between 1 and 100",
		},
		{
			name:    "attempts negative",
			config:  &RepeaterConfig{Attempts: intPtr(-1)},
			jobNum:  1,
			wantErr: true,
			errMsg:  "attempts must be between 1 and 100",
		},
		{
			name:    "attempts too high",
			config:  &RepeaterConfig{Attempts: intPtr(101)},
			jobNum:  1,
			wantErr: true,
			errMsg:  "attempts must be between 1 and 100",
		},
		{
			name:    "duration too small",
			config:  &RepeaterConfig{Duration: durationPtr(time.Microsecond)},
			jobNum:  2,
			wantErr: true,
			errMsg:  "job 2: repeater.duration must be at least 1ms",
		},
		{
			name:    "duration zero",
			config:  &RepeaterConfig{Duration: durationPtr(0)},
			jobNum:  2,
			wantErr: true,
			errMsg:  "duration must be at least 1ms",
		},
		{
			name:    "duration negative",
			config:  &RepeaterConfig{Duration: durationPtr(-time.Second)},
			jobNum:  2,
			wantErr: true,
			errMsg:  "duration must be at least 1ms",
		},
		{
			name:    "duration too large",
			config:  &RepeaterConfig{Duration: durationPtr(time.Hour + time.Second)},
			jobNum:  2,
			wantErr: true,
			errMsg:  "duration must not exceed 1h0m0s",
		},
		{
			name:    "factor too low",
			config:  &RepeaterConfig{Factor: floatPtr(0.99)},
			jobNum:  3,
			wantErr: true,
			errMsg:  "job 3: repeater.factor must be between 1.0 and 10.0",
		},
		{
			name:    "factor zero",
			config:  &RepeaterConfig{Factor: floatPtr(0)},
			jobNum:  3,
			wantErr: true,
			errMsg:  "factor must be between 1.0 and 10.0",
		},
		{
			name:    "factor negative",
			config:  &RepeaterConfig{Factor: floatPtr(-1.5)},
			jobNum:  3,
			wantErr: true,
			errMsg:  "factor must be between 1.0 and 10.0",
		},
		{
			name:    "factor too high",
			config:  &RepeaterConfig{Factor: floatPtr(10.01)},
			jobNum:  3,
			wantErr: true,
			errMsg:  "factor must be between 1.0 and 10.0",
		},
		{
			name:    "multiple invalid fields",
			config:  &RepeaterConfig{Attempts: intPtr(0), Duration: durationPtr(0), Factor: floatPtr(0)},
			jobNum:  4,
			wantErr: true,
			errMsg:  "attempts must be between 1 and 100", // first error only
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config == nil {
				// skip nil configs as they're handled differently
				return
			}
			err := validateRepeaterConfig(tt.config, tt.jobNum)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateSchedPatterns(t *testing.T) {
	tests := []struct {
		name    string
		config  *YamlConfig
		wantErr bool
		errMsg  string
	}{
		// valid sched patterns
		{
			name:    "sched valid all wildcards",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "*", Hour: "*", Day: "*", Month: "*", Weekday: "*"}, Command: "test"}}},
			wantErr: false,
		},
		{
			name:    "sched valid ranges",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "0-59", Hour: "0-23", Day: "1-31", Month: "1-12", Weekday: "0-6"}, Command: "test"}}},
			wantErr: false,
		},
		{
			name:    "sched valid steps",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "*/5", Hour: "*/2", Day: "*/3", Month: "*/4", Weekday: "*/2"}, Command: "test"}}},
			wantErr: false,
		},
		{
			name:    "sched valid lists",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "0,15,30,45", Hour: "8,12,18", Day: "1,15", Month: "1,6,12", Weekday: "1,3,5"}, Command: "test"}}},
			wantErr: false,
		},
		{
			name:    "sched valid mixed",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "0-30/5", Hour: "8-17", Day: "1,15-20", Month: "*/3", Weekday: "MON-FRI"}, Command: "test"}}},
			wantErr: false,
		},
		// invalid minute patterns
		{
			name:    "sched invalid minute too high",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "60"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid minute",
		},
		{
			name:    "sched invalid minute negative",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "-1"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid minute",
		},
		{
			name:    "sched invalid minute range",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "0-60"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid minute",
		},
		// invalid hour patterns
		{
			name:    "sched invalid hour too high",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Hour: "24"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid hour",
		},
		{
			name:    "sched invalid hour negative",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Hour: "-1"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid hour",
		},
		{
			name:    "sched invalid hour range",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Hour: "0-24"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid hour",
		},
		// invalid day patterns
		{
			name:    "sched invalid day zero",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Day: "0"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid day",
		},
		{
			name:    "sched invalid day too high",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Day: "32"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid day",
		},
		{
			name:    "sched invalid day range",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Day: "1-32"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid day",
		},
		// invalid month patterns
		{
			name:    "sched invalid month zero",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Month: "0"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid month",
		},
		{
			name:    "sched invalid month too high",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Month: "13"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid month",
		},
		{
			name:    "sched invalid month range",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Month: "1-13"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid month",
		},
		// invalid weekday patterns
		{
			name:    "sched invalid weekday too high",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Weekday: "8"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid weekday",
		},
		{
			name:    "sched invalid weekday negative",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Weekday: "-1"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid weekday",
		},
		// invalid syntax patterns
		{
			name:    "sched invalid characters",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Minute: "abc"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid",
		},
		{
			name:    "sched invalid special chars",
			config:  &YamlConfig{Jobs: []JobSpec{{Sched: Schedule{Hour: "@daily"}, Command: "test"}}},
			wantErr: true,
			errMsg:  "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyAgainstEmbeddedSchema(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGenerateSchema(t *testing.T) {
	schema, err := GenerateSchema()
	require.NoError(t, err)
	require.NotNil(t, schema)

	// verify schema can be marshaled to JSON
	data, err := schema.MarshalJSON()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// verify it contains expected fields
	schemaStr := string(data)
	assert.Contains(t, schemaStr, "YamlConfig")
	assert.Contains(t, schemaStr, "JobSpec")
	assert.Contains(t, schemaStr, "Schedule")
	assert.Contains(t, schemaStr, "RepeaterConfig")
	assert.Contains(t, schemaStr, "command")
	assert.Contains(t, schemaStr, "spec")
	assert.Contains(t, schemaStr, "sched")
}

// helper functions for creating pointers
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

func boolPtr(b bool) *bool {
	return &b
}

func durationPtr(d time.Duration) *time.Duration {
	return &d
}
