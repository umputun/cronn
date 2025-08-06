package crontab

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
)

//go:embed schema.json
var embeddedSchemaData []byte

// VerifyAgainstEmbeddedSchema validates the config against the embedded JSON schema
func VerifyAgainstEmbeddedSchema(cfg *YamlConfig) error {
	// parse embedded schema
	var schema map[string]interface{}
	if err := json.Unmarshal(embeddedSchemaData, &schema); err != nil {
		return fmt.Errorf("parse embedded schema: %w", err)
	}

	// basic validation using embedded schema data
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
	// validate minute field (0-59)
	if sched.Minute != "" && sched.Minute != "*" {
		if err := validateCronField(sched.Minute, 0, 59, "minute"); err != nil {
			return fmt.Errorf("job %d: invalid minute field '%s': %w", jobNum, sched.Minute, err)
		}
	}

	// validate hour field (0-23)
	if sched.Hour != "" && sched.Hour != "*" {
		if err := validateCronField(sched.Hour, 0, 23, "hour"); err != nil {
			return fmt.Errorf("job %d: invalid hour field '%s': %w", jobNum, sched.Hour, err)
		}
	}

	// validate day field (1-31)
	if sched.Day != "" && sched.Day != "*" {
		if err := validateCronField(sched.Day, 1, 31, "day"); err != nil {
			return fmt.Errorf("job %d: invalid day field '%s': %w", jobNum, sched.Day, err)
		}
	}

	// validate month field (1-12)
	if sched.Month != "" && sched.Month != "*" {
		if err := validateCronField(sched.Month, 1, 12, "month"); err != nil {
			return fmt.Errorf("job %d: invalid month field '%s': %w", jobNum, sched.Month, err)
		}
	}

	// validate weekday field (0-7, where 0 and 7 are Sunday)
	if sched.Weekday != "" && sched.Weekday != "*" {
		// allow weekday names like MON-FRI
		if !isWeekdayName(sched.Weekday) {
			if err := validateCronField(sched.Weekday, 0, 7, "weekday"); err != nil {
				return fmt.Errorf("job %d: invalid weekday field '%s': %w", jobNum, sched.Weekday, err)
			}
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
	// handle step values like */5
	if strings.HasPrefix(value, "*/") {
		stepStr := value[2:]
		step, err := strconv.Atoi(stepStr)
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step value")
		}
		return nil
	}

	// handle ranges with optional step like 1-5 or 1-5/2
	if strings.Contains(value, "-") && !strings.Contains(value, ",") {
		rangeStr := value
		
		// check if there's a step value
		if strings.Contains(value, "/") {
			parts := strings.Split(value, "/")
			if len(parts) != 2 {
				return fmt.Errorf("invalid range/step format")
			}
			rangeStr = parts[0]
			step, err := strconv.Atoi(parts[1])
			if err != nil || step <= 0 {
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

	// handle lists like 1,5,10
	if strings.Contains(value, ",") {
		parts := strings.Split(value, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			// check if part is a range
			if strings.Contains(part, "-") {
				if err := validateCronField(part, minVal, maxVal, fieldName); err != nil {
					return err
				}
			} else {
				// check single value
				val, err := strconv.Atoi(part)
				if err != nil {
					return fmt.Errorf("invalid value in list")
				}
				if val < minVal || val > maxVal {
					return fmt.Errorf("value %d out of bounds (%d-%d)", val, minVal, maxVal)
				}
			}
		}
		return nil
	}

	// handle single values
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
		if *cfg.Attempts < 1 || *cfg.Attempts > 100 {
			return fmt.Errorf("job %d: repeater.attempts must be between 1 and 100", jobNum)
		}
	}

	if cfg.Duration != nil {
		if *cfg.Duration < time.Millisecond {
			return fmt.Errorf("job %d: repeater.duration must be at least 1ms", jobNum)
		}
		if *cfg.Duration > time.Hour {
			return fmt.Errorf("job %d: repeater.duration must not exceed 1 hour", jobNum)
		}
	}

	if cfg.Factor != nil {
		if *cfg.Factor < 1.0 || *cfg.Factor > 10.0 {
			return fmt.Errorf("job %d: repeater.factor must be between 1.0 and 10.0", jobNum)
		}
	}

	return nil
}

// GenerateSchema generates a JSON schema for the YamlConfig struct
func GenerateSchema() (*jsonschema.Schema, error) {
	return jsonschema.Reflect(&YamlConfig{}), nil
}