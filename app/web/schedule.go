package web

import (
	"fmt"
	"strings"
	"time"

	crondesc "github.com/lnquy/cron"
	"github.com/robfig/cron/v3"
)

// scheduleDescriber turns cron specs into readable text for the dashboard
type scheduleDescriber struct {
	desc *crondesc.ExpressionDescriptor
}

// newScheduleDescriber makes a describer with 24-hour times and Sunday as day 0, as robfig/cron reads specs
func newScheduleDescriber() (*scheduleDescriber, error) {
	desc, err := crondesc.NewDescriptor(crondesc.Use24HourTimeFormat(true), crondesc.DayOfWeekStartsAtOne(false))
	if err != nil {
		return nil, fmt.Errorf("failed to create schedule describer: %w", err)
	}
	return &scheduleDescriber{desc: desc}, nil
}

// describe returns readable text for a cron spec or descriptor, or the spec itself when it cannot be described.
// descriptors are formatted here because the library only understands field expressions
func (d *scheduleDescriber) describe(spec string) string {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "@yearly", "@annually":
		return "Yearly, on January 1 at 00:00"
	case "@monthly":
		return "Monthly, on day 1 at 00:00"
	case "@weekly":
		return "Weekly, on Sunday at 00:00"
	case "@daily", "@midnight":
		return "Daily at 00:00"
	case "@hourly":
		return "Hourly"
	}
	if every, ok := strings.CutPrefix(spec, "@every "); ok {
		dur, err := time.ParseDuration(strings.TrimSpace(every))
		if err != nil || dur <= 0 {
			return spec
		}
		text := cron.Every(dur).Delay.String() // the scheduler rounds to whole seconds, at least one
		if strings.HasSuffix(text, "m0s") {
			text = strings.TrimSuffix(text, "0s")
		}
		if strings.HasSuffix(text, "h0m") {
			text = strings.TrimSuffix(text, "0m")
		}
		return "Every " + text
	}
	if strings.HasPrefix(spec, "@") {
		return spec
	}
	text, err := d.desc.ToDescription(spec, crondesc.Locale_en)
	if err != nil || text == "" {
		return spec
	}
	return text
}
