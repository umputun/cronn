//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func waitWeekdayDisabled(t *testing.T, page playwright.Page) {
	t.Helper()
	require.Eventually(t, func() bool {
		s, err := row(page, jobWeekday).GetAttribute("data-state")
		return err == nil && s == "off"
	}, 8*time.Second, 100*time.Millisecond, "row %q should be disabled", jobWeekday)
}

func waitRowEnabled(t *testing.T, page playwright.Page, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		s, err := row(page, name).GetAttribute("data-state")
		return err == nil && s != "" && s != "off"
	}, 8*time.Second, 100*time.Millisecond, "row %q should be enabled", name)
}

func TestToggle_DisableFromRowAndEnableBack(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobWeekday)
	setEnabled(t, id, true)
	waitRowEnabled(t, page, jobWeekday)
	disabledBefore := countOf(t, page, "disabled")

	require.NoError(t, row(page, jobWeekday).Locator(".toggle-btn").Click())
	waitWeekdayDisabled(t, page)
	assert.False(t, jobStatus(t, id).Enabled)

	r := row(page, jobWeekday)
	tag, err := r.Locator(".tag.off").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "disabled", tag)
	next, err := r.Locator("td.c-next").TextContent()
	require.NoError(t, err)
	assert.Contains(t, next, "disabled", "a disabled job shows no next run")
	count, err := r.Locator(".run-btn").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a disabled job offers Enable instead of Run")
	assert.Equal(t, disabledBefore+1, countOf(t, page, "disabled"))

	require.NoError(t, r.Locator("button", playwright.LocatorLocatorOptions{HasText: "Enable"}).Click())
	waitRowEnabled(t, page, jobWeekday)
	assert.True(t, jobStatus(t, id).Enabled)
	next, err = row(page, jobWeekday).Locator("td.c-next").TextContent()
	require.NoError(t, err)
	assert.Contains(t, next, "in ")
}

func TestToggle_DisabledTabListsOnlyDisabledJobs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobWeekday)
	setEnabled(t, id, false)
	t.Cleanup(func() { setEnabled(t, id, true) })

	waitWeekdayDisabled(t, page)
	clickTab(t, page, "Disabled")
	names := rowNames(t, page)
	assert.Contains(t, names, jobWeekday)
	assert.True(t, evalBool(t, page, `() => [...document.querySelectorAll('tr.row')].every(r => r.dataset.state === 'off')`))
	clickTab(t, page, "All")
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
	waitWeekdayDisabled(t, page)
}

func TestToggle_FromInspector(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobWeekday)
	setEnabled(t, id, true)

	openInspector(t, page, jobWeekday)
	require.NoError(t, page.Locator("#insp-toggle").Click())
	require.Eventually(t, func() bool {
		txt, err := page.Locator("#insp-toggle").TextContent()
		return err == nil && strings.TrimSpace(txt) == "Enable"
	}, 8*time.Second, 100*time.Millisecond, "the inspector refreshes after the toggle")
	assert.Contains(t, inspectorText(t, page, ".chips"), "Disabled")
	count, err := page.Locator("#insp-run").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a disabled job has no Run now button")
	waitWeekdayDisabled(t, page)

	require.NoError(t, page.Locator("#insp-toggle").Click())
	waitRowEnabled(t, page, jobWeekday)
}
