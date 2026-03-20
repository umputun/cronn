//go:build e2e

package e2e

import (
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enableAllJobs re-enables any disabled jobs (cards view)
func enableAllJobs(t *testing.T, page playwright.Page) {
	t.Helper()
	for {
		disabledCount, err := page.Locator(".job-card.job-disabled").Count()
		require.NoError(t, err)
		if disabledCount == 0 {
			return
		}
		require.NoError(t, page.Locator(".job-card.job-disabled .btn-toggle").First().Click())
		page.WaitForTimeout(1500)
	}
}

// --- toggle button tests (cards view) ---

func TestToggle_ButtonExistsOnCards(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	count, err := page.Locator(".job-card .btn-toggle").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have toggle buttons on job cards")
}

func TestToggle_DisablesJob(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// ensure all jobs are enabled first
	enableAllJobs(t, page)

	// verify first card is NOT disabled
	firstCard := page.Locator(".job-card").First()
	hasDisabled, err := firstCard.Evaluate("el => el.classList.contains('job-disabled')", nil)
	require.NoError(t, err)
	require.False(t, hasDisabled.(bool), "first card should not be disabled")

	// click toggle button on first job
	require.NoError(t, page.Locator(".job-card .btn-toggle").First().Click())

	// wait for HTMX refresh
	require.NoError(t, page.Locator(".job-card.job-disabled").First().WaitFor())

	count, err := page.Locator(".job-card.job-disabled").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have at least one disabled job card")

	// cleanup
	enableAllJobs(t, page)
}

func TestToggle_ReEnablesJob(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// ensure clean state, then disable one
	enableAllJobs(t, page)
	require.NoError(t, page.Locator(".job-card .btn-toggle").First().Click())
	require.NoError(t, page.Locator(".job-card.job-disabled").First().WaitFor())

	// re-enable
	require.NoError(t, page.Locator(".job-card.job-disabled .btn-toggle").First().Click())
	page.WaitForTimeout(1500)

	afterCount, err := page.Locator(".job-card.job-disabled").Count()
	require.NoError(t, err)
	assert.Equal(t, 0, afterCount, "no cards should be disabled after re-enabling")
}

func TestToggle_DisabledJobHasDisabledRunButton(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// ensure clean state, then disable one
	enableAllJobs(t, page)
	require.NoError(t, page.Locator(".job-card .btn-toggle").First().Click())
	require.NoError(t, page.Locator(".job-card.job-disabled").First().WaitFor())

	// verify the Run button inside a disabled card is disabled
	runBtnDisabled, err := page.Locator(".job-card.job-disabled .btn-compact[disabled]").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, runBtnDisabled, 1, "Run button should be disabled when job is disabled")

	// cleanup
	enableAllJobs(t, page)
}

func TestToggle_PersistsAcrossReload(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// ensure clean state, then disable one
	enableAllJobs(t, page)
	require.NoError(t, page.Locator(".job-card .btn-toggle").First().Click())
	require.NoError(t, page.Locator(".job-card.job-disabled").First().WaitFor())

	// reload page
	_, err := page.Reload()
	require.NoError(t, err)
	waitVisible(t, page.Locator(".header"))
	waitForJobsLoaded(t, page)

	// verify the disabled state persisted
	count, err := page.Locator(".job-card.job-disabled").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "disabled state should persist after reload")

	// cleanup
	enableAllJobs(t, page)
}

// --- next run hidden for disabled jobs ---

func TestToggle_DisabledJobHidesNextRun(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	enableAllJobs(t, page)

	// verify enabled job shows a real next run value (not "-")
	nextRunText, err := page.Locator(".job-card .timing-value").First().InnerText()
	require.NoError(t, err)
	assert.NotEqual(t, "-", nextRunText, "enabled job should show next run time")

	// disable first job
	require.NoError(t, page.Locator(".job-card .btn-toggle").First().Click())
	require.NoError(t, page.Locator(".job-card.job-disabled").First().WaitFor())

	// verify disabled job shows "-" for next run
	disabledNextRun, err := page.Locator(".job-card.job-disabled .timing-value").First().InnerText()
	require.NoError(t, err)
	assert.Equal(t, "-", disabledNextRun, "disabled job should show '-' for next run")

	// verify title attribute says "disabled"
	title, err := page.Locator(".job-card.job-disabled .timing-value").First().GetAttribute("title")
	require.NoError(t, err)
	assert.Equal(t, "disabled", title)

	// cleanup
	enableAllJobs(t, page)
}

func TestToggle_ReEnabledJobShowsNextRun(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	enableAllJobs(t, page)

	// disable then re-enable
	require.NoError(t, page.Locator(".job-card .btn-toggle").First().Click())
	require.NoError(t, page.Locator(".job-card.job-disabled").First().WaitFor())
	require.NoError(t, page.Locator(".job-card.job-disabled .btn-toggle").First().Click())
	page.WaitForTimeout(1500)

	// verify next run is restored (not "-")
	nextRunText, err := page.Locator(".job-card .timing-value").First().InnerText()
	require.NoError(t, err)
	assert.NotEqual(t, "-", nextRunText, "re-enabled job should show next run time again")
}

func TestToggle_DisabledJobHidesNextRunInListView(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	enableAllJobs(t, page)

	// switch to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	// verify enabled job shows next run
	nextRunText, err := page.Locator(".job-row .td-next .time-value").First().InnerText()
	require.NoError(t, err)
	assert.NotEqual(t, "-", nextRunText, "enabled job should show next run in list view")

	// disable first job
	require.NoError(t, page.Locator(".job-row .btn-toggle").First().Click())
	require.NoError(t, page.Locator(".job-row.job-disabled").First().WaitFor())

	// verify disabled job shows "-"
	disabledNextRun, err := page.Locator(".job-row.job-disabled .td-next .time-value").First().InnerText()
	require.NoError(t, err)
	assert.Equal(t, "-", disabledNextRun, "disabled job should show '-' for next run in list view")

	// cleanup: switch back to cards and re-enable
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitForJobsLoaded(t, page)
	enableAllJobs(t, page)
}

// --- toggle button tests (list view) ---

func TestToggle_ButtonExistsInListView(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// switch to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	count, err := page.Locator(".job-row .btn-toggle").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have toggle buttons in list view")
}

func TestToggle_WorksInListView(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// ensure clean state first (in cards view)
	enableAllJobs(t, page)

	// switch to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	// click toggle on first row
	require.NoError(t, page.Locator(".job-row .btn-toggle").First().Click())

	// wait for HTMX refresh
	require.NoError(t, page.Locator(".job-row.job-disabled").First().WaitFor())

	count, err := page.Locator(".job-row.job-disabled").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have at least one disabled job row")

	// cleanup: switch back to cards and re-enable
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitForJobsLoaded(t, page)
	enableAllJobs(t, page)
}

func TestToggle_ButtonTitleChanges(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// ensure all enabled
	enableAllJobs(t, page)

	// should say "Disable job" when enabled
	title, err := page.Locator(".job-card .btn-toggle").First().GetAttribute("title")
	require.NoError(t, err)
	assert.Equal(t, "Disable job", title)

	// toggle to disable
	require.NoError(t, page.Locator(".job-card .btn-toggle").First().Click())
	require.NoError(t, page.Locator(".job-card.job-disabled").First().WaitFor())

	// should say "Enable job" when disabled
	title, err = page.Locator(".job-card.job-disabled .btn-toggle").First().GetAttribute("title")
	require.NoError(t, err)
	assert.Equal(t, "Enable job", title)

	// cleanup
	enableAllJobs(t, page)
}
