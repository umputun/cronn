package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleDescriber_Describe(t *testing.T) {
	d, err := newScheduleDescriber()
	require.NoError(t, err)

	tbl := []struct {
		spec, want string
	}{
		{spec: "*/1 * * * *", want: "Every minute"},
		{spec: "*/2 * * * *", want: "Every 2 minutes"},
		{spec: "0 2 * * *", want: "At 02:00"},
		{spec: "0,30 9-17 * * 1-5", want: "At 0 and 30 minutes past the hour, between 09:00 and 17:59, Monday through Friday"},
		{spec: "15 * * * *", want: "At 15 minutes past the hour"},
		{spec: "0 0 1 */2 *", want: "At 00:00, on day 1 of the month, every 2 months"},
		{spec: "30 8 * * 0", want: "At 08:30, only on Sunday"},
		{spec: "@yearly", want: "Yearly, on January 1 at 00:00"},
		{spec: "@annually", want: "Yearly, on January 1 at 00:00"},
		{spec: "@monthly", want: "Monthly, on day 1 at 00:00"},
		{spec: "@weekly", want: "Weekly, on Sunday at 00:00"},
		{spec: "@daily", want: "Daily at 00:00"},
		{spec: "@midnight", want: "Daily at 00:00"},
		{spec: "@hourly", want: "Hourly"},
		{spec: "@every 1h15m", want: "Every 1h15m"},
		{spec: "@every 2h", want: "Every 2h"},
		{spec: "@every 90s", want: "Every 1m30s"},
		{spec: "@every 30s", want: "Every 30s"},
		{spec: "@every 5m", want: "Every 5m"},
		{spec: "@every 500ms", want: "Every 1s"},
		{spec: "@every 1.5s", want: "Every 1s"},
		{spec: "@every nonsense", want: "@every nonsense"},
		{spec: "@every -5m", want: "@every -5m"},
		{spec: "@reboot", want: "@reboot"},
		{spec: "61 * * * *", want: "61 * * * *"},
		{spec: "not a spec", want: "not a spec"},
	}
	for _, tt := range tbl {
		t.Run(tt.spec, func(t *testing.T) {
			assert.Equal(t, tt.want, d.describe(tt.spec))
		})
	}
}
