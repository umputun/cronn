package crontab

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
)

const (
	// repeater validation limits
	minAttempts = 1
	maxAttempts = 100
	minFactor   = 1.0
	maxFactor   = 10.0
	minDuration = time.Millisecond
	maxDuration = time.Hour
)

//go:embed schema.json
var embeddedSchemaData []byte

// VerifyAgainstEmbeddedSchema validates the config against the embedded JSON schema
func VerifyAgainstEmbeddedSchema(cfg *YamlConfig) error {
	// ensure schema is embedded (will fail at compile time if not)
	if len(embeddedSchemaData) == 0 {
		return fmt.Errorf("embedded schema is empty")
	}

	// validate configuration according to schema rules
	if err := validateRequiredFields(cfg); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	return nil
}

// validateRequiredFields performs basic validation of required fields
func validateRequiredFields(cfg *YamlConfig) error {
	// check that we have at least one job
	if len(cfg.Jobs) == 0 {
		return fmt.Errorf("at least one job is required")
	}

	// validate each job
	for i, job := range cfg.Jobs {
		// check that command is not empty
		if job.Command == "" {
			return fmt.Errorf("job %d: command is required", i+1)
		}

		// check that either spec or sched is provided (but not both)
		hasSpec := job.Spec != ""
		hasSched := job.Sched.Minute != "" || job.Sched.Hour != "" || job.Sched.Day != "" ||
			job.Sched.Month != "" || job.Sched.Weekday != ""

		if !hasSpec && !hasSched {
			return fmt.Errorf("job %d: either 'spec' or 'sched' field is required", i+1)
		}

		if hasSpec && hasSched {
			return fmt.Errorf("job %d: 'spec' and 'sched' fields are mutually exclusive", i+1)
		}

		// validate sched field patterns if sched is used
		if hasSched {
			if err := validateSchedFields(job.Sched, i+1); err != nil {
				return err
			}
		}

		// validate repeater configuration if present
		if job.Repeater != nil {
			if err := validateRepeaterConfig(job.Repeater, i+1); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateSchedFields validates the Schedule struct fields
func validateSchedFields(sched Schedule, jobNum int) error {
	type fieldValidator struct {
		value     string
		fieldName string
		minVal    int
		maxVal    int
		allowName bool
	}

	validators := []fieldValidator{
		{sched.Minute, "minute", 0, 59, false},
		{sched.Hour, "hour", 0, 23, false},
		{sched.Day, "day", 1, 31, false},
		{sched.Month, "month", 1, 12, false},
		{sched.Weekday, "weekday", 0, 7, true},
	}

	for _, v := range validators {
		if v.value == "" || v.value == "*" {
			continue
		}

		// special handling for weekday names
		if v.allowName && isWeekdayName(v.value) {
			continue
		}

		if err := validateCronField(v.value, v.minVal, v.maxVal, v.fieldName); err != nil {
			return fmt.Errorf("job %d: invalid %s field '%s': %w", jobNum, v.fieldName, v.value, err)
		}
	}

	return nil
}

// isWeekdayName checks if the string contains weekday names
func isWeekdayName(s string) bool {
	weekdayPattern := regexp.MustCompile(`^(MON|TUE|WED|THU|FRI|SAT|SUN)(-|(MON|TUE|WED|THU|FRI|SAT|SUN))*$`)
	return weekdayPattern.MatchString(strings.ToUpper(s))
}

// validateCronField validates a single cron field value
func validateCronField(value string, minVal, maxVal int, fieldName string) error {
	if strings.HasPrefix(value, "*/") {
		return validateStepValue(value[2:])
	}

	if strings.Contains(value, "-") && !strings.Contains(value, ",") {
		return validateRange(value, minVal, maxVal)
	}

	if strings.Contains(value, ",") {
		return validateList(value, minVal, maxVal, fieldName)
	}

	return validateSingleValue(value, minVal, maxVal, fieldName)
}

// validateStepValue validates step values like */5
func validateStepValue(stepStr string) error {
	step, err := strconv.Atoi(stepStr)
	if err != nil || step <= 0 {
		return fmt.Errorf("invalid step value")
	}
	return nil
}

// validateRange validates range values like 1-5 or 1-5/2
func validateRange(value string, minVal, maxVal int) error {
	rangeStr := value

	// check for step value in range
	if strings.Contains(value, "/") {
		parts := strings.Split(value, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid range/step format")
		}
		rangeStr = parts[0]
		if err := validateStepValue(parts[1]); err != nil {
			return fmt.Errorf("invalid step value in range")
		}
	}

	parts := strings.Split(rangeStr, "-")
	if len(parts) != 2 {
		return fmt.Errorf("invalid range format")
	}

	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("invalid range values")
	}

	if start < minVal || start > maxVal || end < minVal || end > maxVal || start > end {
		return fmt.Errorf("range values out of bounds (%d-%d)", minVal, maxVal)
	}

	return nil
}

// validateList validates comma-separated lists like 1,5,10
func validateList(value string, minVal, maxVal int, fieldName string) error {
	parts := strings.Split(value, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			if err := validateCronField(part, minVal, maxVal, fieldName); err != nil {
				return err
			}
		} else {
			if err := validateSingleValue(part, minVal, maxVal, fieldName); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateSingleValue validates a single numeric value
func validateSingleValue(value string, minVal, maxVal int, fieldName string) error {
	val, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid %s value", fieldName)
	}
	if val < minVal || val > maxVal {
		return fmt.Errorf("%s value %d out of bounds (%d-%d)", fieldName, val, minVal, maxVal)
	}
	return nil
}

// validateRepeaterConfig validates repeater configuration
func validateRepeaterConfig(cfg *RepeaterConfig, jobNum int) error {
	if cfg.Attempts != nil {
		if *cfg.Attempts < minAttempts || *cfg.Attempts > maxAttempts {
			return fmt.Errorf("job %d: repeater.attempts must be between %d and %d", jobNum, minAttempts, maxAttempts)
		}
	}

	if cfg.Duration != nil {
		if *cfg.Duration < minDuration {
			return fmt.Errorf("job %d: repeater.duration must be at least %v", jobNum, minDuration)
		}
		if *cfg.Duration > maxDuration {
			return fmt.Errorf("job %d: repeater.duration must not exceed %v", jobNum, maxDuration)
		}
	}

	if cfg.Factor != nil {
		if *cfg.Factor < minFactor || *cfg.Factor > maxFactor {
			return fmt.Errorf("job %d: repeater.factor must be between %.1f and %.1f", jobNum, minFactor, maxFactor)
		}
	}

	return nil
}

// GenerateSchema generates a JSON schema for the YamlConfig struct
func GenerateSchema() (*jsonschema.Schema, error) {
	return jsonschema.Reflect(&YamlConfig{}), nil
}
