// Package crontab deals with the standard 5-elements crontab input from a file
// also supports @descriptors like
package crontab

//go:generate go run internal/schema/main.go schema.json

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"
	"gopkg.in/yaml.v3"

	"github.com/umputun/cronn/app/conditions"
)

// Parser file, thread safe
type Parser struct {
	file        string
	updInterval time.Duration
	hupCh       <-chan struct{}
	isYAML      bool
}

// Schedule represents structured cron schedule with separate fields
type Schedule struct {
	Minute  string `yaml:"minute,omitempty" json:"minute,omitempty" jsonschema:"description=Minute field (0-59)"`
	Hour    string `yaml:"hour,omitempty" json:"hour,omitempty" jsonschema:"description=Hour field (0-23)"`
	Day     string `yaml:"day,omitempty" json:"day,omitempty" jsonschema:"description=Day of month field (1-31)"`
	Month   string `yaml:"month,omitempty" json:"month,omitempty" jsonschema:"description=Month field (1-12)"`
	Weekday string `yaml:"weekday,omitempty" json:"weekday,omitempty" jsonschema:"description=Weekday field (0-7 where 0 and 7 are Sunday)"`
}

// RepeaterConfig defines job-specific retry settings using pointers to distinguish
// between unset (nil) and explicitly set values, allowing proper merging with global
// defaults from CLI parameters.
type RepeaterConfig struct {
	Attempts *int           `yaml:"attempts,omitempty" json:"attempts,omitempty" jsonschema:"description=Number of retry attempts"`
	Duration *time.Duration `yaml:"duration,omitempty" json:"duration,omitempty" jsonschema:"type=string,description=Initial retry delay"`
	Factor   *float64       `yaml:"factor,omitempty" json:"factor,omitempty" jsonschema:"description=Backoff multiplication factor"`
	Jitter   *bool          `yaml:"jitter,omitempty" json:"jitter,omitempty" jsonschema:"description=Enable random jitter"`
}

// JobSpec for spec and cmd + params
type JobSpec struct {
	Spec       string             `yaml:"spec,omitempty" json:"spec,omitempty" jsonschema:"description=Cron specification string"`
	Sched      Schedule           `yaml:"sched,omitempty" json:"sched,omitempty" jsonschema:"description=Structured schedule format (alternative to spec)"` //nolint:modernize // yaml.v3 doesn't support omitzero
	Command    string             `yaml:"command" json:"command" jsonschema:"required,description=Command to execute"`
	Name       string             `yaml:"name,omitempty" json:"name,omitempty" jsonschema:"description=Optional job name or description"`
	Repeater   *RepeaterConfig    `yaml:"repeater,omitempty" json:"repeater,omitempty" jsonschema:"description=Job-specific repeater configuration"`
	Conditions *conditions.Config `yaml:"conditions,omitempty" json:"conditions,omitempty" jsonschema:"description=Resource thresholds for conditional execution"`
}

// YamlConfig represents the YAML configuration structure
type YamlConfig struct {
	Jobs []JobSpec `yaml:"jobs" json:"jobs" jsonschema:"required,description=List of cron jobs to execute,minItems=1"`
}

// New creates Parser for file, but not parsing yet
func New(file string, updInterval time.Duration, hupCh <-chan struct{}) *Parser {
	updIntervalStr := fmt.Sprintf("update every %v", updInterval)
	if updInterval == time.Duration(math.MaxInt64) {
		updIntervalStr = "no updates"
	}

	// detect format by file extension
	ext := strings.ToLower(filepath.Ext(file))
	isYAML := ext == ".yml" || ext == ".yaml"

	format := "crontab"
	if isYAML {
		format = "yaml"
	}

	log.Printf("[INFO] config file %s (%s format), %s", file, format, updIntervalStr)
	return &Parser{file: file, updInterval: updInterval, hupCh: hupCh, isYAML: isYAML}
}

// List parses crontab and returns lit of jobs
func (p Parser) List() (result []JobSpec, err error) {
	bs, err := os.ReadFile(p.file)
	if err != nil {
		return []JobSpec{}, fmt.Errorf("failed to read file: %w", err)
	}

	if p.isYAML {
		return p.parseYAML(bs)
	}

	// parse as traditional crontab
	for l := range strings.SplitSeq(string(bs), "\n") {
		if js, err := Parse(l); err == nil {
			result = append(result, js)
		}
	}
	return result, nil
}

// parseYAML parses YAML configuration
func (p Parser) parseYAML(data []byte) ([]JobSpec, error) {
	// handle empty file gracefully - return empty list without error
	if len(bytes.TrimSpace(data)) == 0 {
		return []JobSpec{}, nil
	}

	var config YamlConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// allow empty jobs list (for update mode with initially empty file)
	// skip schema validation for empty jobs - validation requires minItems=1
	if len(config.Jobs) == 0 {
		return []JobSpec{}, nil
	}

	// validate configuration only when jobs are present
	if err := VerifyAgainstEmbeddedSchema(&config); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	// convert sched to spec if needed
	for i := range config.Jobs {
		if hasSchedule(config.Jobs[i].Sched) {
			spec := p.schedToSpec(config.Jobs[i].Sched)
			config.Jobs[i].Spec = spec
		}
	}

	return config.Jobs, nil
}

// hasSchedule checks if Schedule has any fields set
func hasSchedule(sched Schedule) bool {
	return sched.Minute != "" || sched.Hour != "" || sched.Day != "" ||
		sched.Month != "" || sched.Weekday != ""
}

// schedToSpec converts Schedule struct to cron spec string
func (p Parser) schedToSpec(sched Schedule) string {
	minute := sched.Minute
	if minute == "" {
		minute = "*"
	}

	hour := sched.Hour
	if hour == "" {
		hour = "*"
	}

	day := sched.Day
	if day == "" {
		day = "*"
	}

	month := sched.Month
	if month == "" {
		month = "*"
	}

	weekday := sched.Weekday
	if weekday == "" {
		weekday = "*"
	}

	return fmt.Sprintf("%s %s %s %s %s", minute, hour, day, month, weekday)
}

func (p Parser) String() string {
	return p.file
}

// Changes gets updates channel. Each time crontab file updated and modification time changed
// it will get parsed and the full list of jobs will be sent to the channel. Update checked periodically
// and postponed for short time to prevent changes on every small intermediate save.
// In addition it also performs forced reload on hupCh event.
// The method tolerates missing files - if file doesn't exist initially, it will wait for creation.
func (p Parser) Changes(ctx context.Context) (<-chan []JobSpec, error) {
	ch := make(chan []JobSpec)

	// get modification time of crontab file, returns (time, exists)
	mtime := func() (time.Time, bool) {
		st, err := os.Stat(p.file)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("[WARN] can't stat crontab file %s: %v", p.file, err)
			}
			return time.Time{}, false
		}
		return st.ModTime(), true
	}

	lastMtime, exists := mtime()
	if !exists {
		log.Printf("[INFO] crontab file %s doesn't exist yet, waiting for creation", p.file)
	}

	ticker := time.NewTicker(p.updInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				close(ch)
				return
			case <-p.hupCh:
				log.Printf("[INFO] refresh requested")
				jobs, err := p.List()
				if err != nil {
					log.Printf("[WARN] can't get list of jobs from %s, %v", p.file, err)
					continue
				}
				ch <- jobs
			case <-ticker.C: // react on changes every X seconds
				m, fileExists := mtime()
				if !fileExists {
					// file doesn't exist yet, skip this iteration
					continue
				}
				secsSinceChange := int(time.Since(m).Seconds())
				secsThreshold := int(p.updInterval.Seconds() / 2)
				if m != lastMtime && secsSinceChange >= secsThreshold {
					// change should be at least X/2 secs old to prevent changes on every small intermediate save
					lastMtime = m
					jobs, err := p.List()
					if err != nil {
						log.Printf("[WARN] can't get list of jobs from %s, %v", p.file, err)
						continue
					}
					ch <- jobs
				}
			}
		}
	}()

	return ch, nil
}

// Parse spec+command and return JobSpec
func Parse(line string) (result JobSpec, err error) {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return JobSpec{}, errors.New("comment line " + line)
	}

	// strip inline comments (# and everything after)
	if idx := strings.Index(line, " #"); idx != -1 {
		line = line[:idx]
	}

	reWhtSpaces := regexp.MustCompile(`[\s\p{Zs}]{2,}`)
	l := strings.TrimSpace(line)
	l = strings.ReplaceAll(l, "\t", " ")
	singleSpace := reWhtSpaces.ReplaceAllString(l, " ")
	elems := strings.Split(singleSpace, " ")

	if len(elems) < 2 {
		return JobSpec{}, errors.New("not enough elements in " + line)
	}

	// @every 2h5m
	if elems[0] == "@every" && len(elems) >= 3 {
		return JobSpec{Spec: strings.Join(elems[:2], " "), Command: strings.Join(elems[2:], " ")}, nil
	}

	// @midnight
	if strings.HasPrefix(elems[0], "@") {
		return JobSpec{Spec: elems[0], Command: strings.Join(elems[1:], " ")}, nil
	}

	if len(elems) < 6 {
		return JobSpec{}, errors.New("not enough elements in " + line)
	}
	// * * * * *
	return JobSpec{Spec: strings.Join(elems[:5], " "), Command: strings.Join(elems[5:], " ")}, nil
}
