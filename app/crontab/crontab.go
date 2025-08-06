// Package crontab deals with the standard 5-elements crontab input from a file
// also supports @descriptors like
package crontab

//go:generate go run internal/schema/main.go schema.json

import (
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
	Minute  string `yaml:"minute,omitempty" json:"minute,omitempty" jsonschema:"description=Minute field (0-59 or * or */n),pattern=^([0-5]?[0-9]|\\*|\\*/[0-9]+|[0-9]+-[0-9]+|[0-9]+,[0-9]+.*)$"`
	Hour    string `yaml:"hour,omitempty" json:"hour,omitempty" jsonschema:"description=Hour field (0-23 or * or */n),pattern=^([01]?[0-9]|2[0-3]|\\*|\\*/[0-9]+|[0-9]+-[0-9]+|[0-9]+,[0-9]+.*)$"`
	Day     string `yaml:"day,omitempty" json:"day,omitempty" jsonschema:"description=Day of month field (1-31 or * or */n),pattern=^([1-9]|[12][0-9]|3[01]|\\*|\\*/[0-9]+|[0-9]+-[0-9]+|[0-9]+,[0-9]+.*)$"`
	Month   string `yaml:"month,omitempty" json:"month,omitempty" jsonschema:"description=Month field (1-12 or * or */n),pattern=^([1-9]|1[0-2]|\\*|\\*/[0-9]+|[0-9]+-[0-9]+|[0-9]+,[0-9]+.*)$"`
	Weekday string `yaml:"weekday,omitempty" json:"weekday,omitempty" jsonschema:"description=Weekday field (0-7 where 0 and 7 are Sunday or * or */n),pattern=^([0-7]|\\*|\\*/[0-9]+|[0-9]+-[0-9]+|[0-9]+,[0-9]+.*)$"`
}

// RepeaterConfig defines job-specific retry settings.
// Uses pointers to distinguish between unset (nil) and explicitly set values,
// allowing proper merging with global defaults from CLI parameters.
type RepeaterConfig struct {
	Attempts *int           `yaml:"attempts,omitempty" json:"attempts,omitempty" jsonschema:"description=Number of retry attempts,minimum=1,maximum=100"`
	Duration *time.Duration `yaml:"duration,omitempty" json:"duration,omitempty" jsonschema:"description=Initial retry delay (e.g. 1s or 500ms)"`
	Factor   *float64       `yaml:"factor,omitempty" json:"factor,omitempty" jsonschema:"description=Backoff multiplication factor,minimum=1.0,maximum=10.0"`
	Jitter   *bool          `yaml:"jitter,omitempty" json:"jitter,omitempty" jsonschema:"description=Enable random jitter to avoid thundering herd"`
}

// JobSpec for spec and cmd + params
type JobSpec struct {
	Spec     string          `yaml:"spec,omitempty" json:"spec,omitempty" jsonschema:"description=Cron specification string (e.g. '0 * * * *' or '@daily'),pattern=^(@(yearly|annually|monthly|weekly|daily|midnight|hourly|every\\s+[0-9]+[smh])|([0-9*,-/]+\\s+){4}[0-9*,-/]+)$"`
	Sched    Schedule        `yaml:"sched,omitempty" json:"sched,omitempty" jsonschema:"description=Structured schedule format (alternative to spec)"`
	Command  string          `yaml:"command" json:"command" jsonschema:"required,description=Command to execute,minLength=1"`
	Name     string          `yaml:"name,omitempty" json:"name,omitempty" jsonschema:"description=Optional job name or description for better logging"`
	Repeater *RepeaterConfig `yaml:"repeater,omitempty" json:"repeater,omitempty" jsonschema:"description=Job-specific repeater configuration to override global settings"`
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
		return []JobSpec{}, err
	}

	if p.isYAML {
		return p.parseYAML(bs)
	}

	// parse as traditional crontab
	lines := strings.Split(string(bs), "\n")
	for _, l := range lines {
		if js, err := Parse(l); err == nil {
			result = append(result, js)
		}
	}
	return result, nil
}

// parseYAML parses YAML configuration
func (p Parser) parseYAML(data []byte) ([]JobSpec, error) {
	var config YamlConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// verify against embedded schema (non-fatal warnings)
	if err := VerifyAgainstEmbeddedSchema(&config); err != nil {
		// log warning but don't fail - schema validation is supplementary
		log.Printf("[WARN] schema validation warning: %v", err)
	}

	// validate and process each job
	for i, job := range config.Jobs {
		// check for conflict between spec and sched
		hasSpec := job.Spec != ""
		hasSched := job.Sched.Minute != "" || job.Sched.Hour != "" || job.Sched.Day != "" || 
			job.Sched.Month != "" || job.Sched.Weekday != ""
		
		if hasSpec && hasSched {
			return nil, fmt.Errorf("job %d has both 'spec' and 'sched' fields, use only one", i+1)
		}
		
		if !hasSpec && !hasSched {
			return nil, fmt.Errorf("job %d has neither 'spec' nor 'sched' field", i+1)
		}
		
		// convert sched to spec if needed
		if hasSched {
			spec := p.schedToSpec(job.Sched)
			config.Jobs[i].Spec = spec
		}
		
		if job.Command == "" {
			return nil, fmt.Errorf("job %d has empty command", i+1)
		}
	}

	return config.Jobs, nil
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
func (p Parser) Changes(ctx context.Context) (<-chan []JobSpec, error) {
	ch := make(chan []JobSpec)

	// get modification time of crontab file
	mtime := func() (time.Time, error) {
		st, err := os.Stat(p.file)
		if err != nil {
			return time.Time{}, fmt.Errorf("can't load cron file %s: %w", p.file, err)
		}
		return st.ModTime(), nil
	}

	lastMtime, err := mtime()
	if err != nil {
		// need file available to start change watcher
		return nil, err
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
				m, err := mtime()
				if err != nil {
					log.Printf("[WARN] can't get info about %s, %v", p.file, err)
					continue
				}
				secsSinceChange := time.Now().Second() - m.Second()
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
