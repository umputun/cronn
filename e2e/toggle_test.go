//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func waitWeekdayState(t *testing.T, page playwright.Page, off bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		s, err := row(page, jobWeekday).GetAttribute("data-state")
		return err == nil && s != "" && (s == "off") == off
	}, 8*time.Second, 100*time.Millisecond, "row %q disabled=%v", jobWeekday, off)
}

func TestToggle_DisableFromJobPageAndEnableBack(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobWeekday)
	setEnabled(t, id, true)
	t.Cleanup(func() { setEnabled(t, id, true) })
	disabledBefore := countOf(t, page, "disabled")
	gotoJob(t, page, id, "")

	sw := page.Locator("#toggle-" + id)
	require.NoError(t, sw.Click())
	waitVisible(t, page.Locator(`#toggle-`+id+`[aria-checked="false"]`))
	toast, err := page.Locator("#toasts .t").TextContent()
	require.NoError(t, err)
	assert.Contains(t, toast, "Schedule disabled: "+jobWeekday)
	assert.False(t, jobStatus(t, id).Enabled)
	waitHidden(t, page.Locator("#run-now"))
	note, err := page.Locator("#runact").TextContent()
	require.NoError(t, err)
	assert.Contains(t, note, "Enable the schedule to run this job")
	next, err := page.Locator("#fact-next").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "—", strings.TrimSpace(next))
	badge, err := page.Locator("#job-badge").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "Disabled", strings.TrimSpace(badge))
	assert.Equal(t, disabledBefore+1, countOf(t, page, "disabled"), "the pill count follows")
	require.NoError(t, page.Locator("#cmdin").Press("Enter"))
	assert.Never(t, func() bool {
		count, e := page.Locator("#run-dialog").Count()
		return e != nil || count > 0
	}, 1*time.Second, 100*time.Millisecond, "Enter in the command of a disabled job opens no confirmation")
	assert.True(t, focusOn(t, page, "cmdin"), "focus stays in the command field")

	require.NoError(t, page.Locator("#toggle-"+id).Click())
	waitVisible(t, page.Locator(`#toggle-`+id+`[aria-checked="true"]`))
	waitVisible(t, page.Locator("#run-now"))
	assert.True(t, jobStatus(t, id).Enabled)
	readonly, err := page.Locator("#cmdin").GetAttribute("readonly")
	require.NoError(t, err)
	assert.Empty(t, readonly, "the command stays editable")
	require.NoError(t, page.Locator("#cmdin").Fill(`echo "edited after enable"`))
	val, err := page.Locator("#cmdin").InputValue()
	require.NoError(t, err)
	assert.Equal(t, `echo "edited after enable"`, val)
}

func TestToggle_DisabledRowHasNoRunButton(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobWeekday)
	setEnabled(t, id, false)
	t.Cleanup(func() { setEnabled(t, id, true) })

	waitWeekdayState(t, page, true)
	r := row(page, jobWeekday)
	next, err := r.Locator(".next").TextContent()
	require.NoError(t, err)
	assert.Contains(t, next, "disabled", "a disabled job shows no next run")
	count, err := r.Locator("#run-" + id).Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a disabled job offers no Run")

	setEnabled(t, id, true)
	waitWeekdayState(t, page, false)
	waitVisible(t, row(page, jobWeekday).Locator("#run-"+id))
}

func TestToggle_DisabledTabListsOnlyDisabledJobs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobWeekday)
	setEnabled(t, id, false)
	t.Cleanup(func() { setEnabled(t, id, true) })

	waitWeekdayState(t, page, true)
	clickPill(t, page, "disabled")
	assert.Contains(t, rowNames(t, page), jobWeekday)
	assert.True(t, evalBool(t, page, `() => [...document.querySelectorAll('.it')].every(r => r.dataset.state === 'off')`))
	clickPill(t, page, "all")
}

func TestToggle_PersistsAcrossReload(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobWeekday)
	setEnabled(t, id, false)
	t.Cleanup(func() { setEnabled(t, id, true) })

	_, err := page.Reload()
	require.NoError(t, err)
	waitForJobsLoaded(t, page)
	waitWeekdayState(t, page, true)
}
