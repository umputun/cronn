//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManualRun_RunButtonExists(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify run button exists on job cards (button has class btn-compact and contains span with text "Run")
	count, err := page.Locator(".job-card .btn-compact").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have run buttons on job cards")
}

func TestManualRun_ConfirmDialogOpens(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click run button on first enabled job
	require.NoError(t, page.Locator(".job-card .btn-compact").First().Click())
	waitVisible(t, page.Locator("#confirm-dialog"))

	// verify confirm dialog is visible
	assert.True(t, isModalVisible(t, page, "#confirm-dialog"), "confirm dialog should be visible")

	// verify dialog content
	visible, err := page.Locator("#confirm-command").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "command textarea should be visible in confirm dialog")
}

func TestManualRun_ConfirmDialogCancel(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click run button
	require.NoError(t, page.Locator(".job-card .btn-compact").First().Click())
	waitVisible(t, page.Locator("#confirm-dialog"))

	// click cancel button
	require.NoError(t, page.Locator(".btn-cancel").Click())
	waitHidden(t, page.Locator("#confirm-dialog"))

	// verify dialog is hidden
	assert.False(t, isModalVisible(t, page, "#confirm-dialog"), "confirm dialog should be hidden after cancel")
}

func TestManualRun_ConfirmDialogShowsCommand(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click run button
	require.NoError(t, page.Locator(".job-card .btn-compact").First().Click())
	waitVisible(t, page.Locator("#confirm-dialog"))

	// verify command textarea has content
	command, err := page.Locator("#confirm-command").InputValue()
	require.NoError(t, err)
	assert.NotEmpty(t, command, "command textarea should contain the job command")
	assert.Contains(t, command, "echo", "command should contain expected content")

	// close dialog
	require.NoError(t, page.Locator(".btn-cancel").Click())
	waitHidden(t, page.Locator("#confirm-dialog"))
}

func TestManualRun_ConfirmDialogHasDateField(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click run button
	require.NoError(t, page.Locator(".job-card .btn-compact").First().Click())
	waitVisible(t, page.Locator("#confirm-dialog"))

	// verify date field exists (may be hidden by default)
	dateField := page.Locator("#confirm-date-field")
	exists, err := dateField.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, exists, "date field container should exist")

	// verify date input exists
	dateInput := page.Locator("#confirm-date")
	inputExists, err := dateInput.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, inputExists, "date input should exist")

	// close dialog
	require.NoError(t, page.Locator(".btn-cancel").Click())
	waitHidden(t, page.Locator("#confirm-dialog"))
}

func TestManualRun_RunButtonInListView(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// switch to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	// verify run button exists in list view
	count, err := page.Locator(".job-row .btn-compact").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have run buttons in list view")

	// click run button in list view
	require.NoError(t, page.Locator(".job-row .btn-compact").First().Click())
	waitVisible(t, page.Locator("#confirm-dialog"))

	// verify confirm dialog opens
	assert.True(t, isModalVisible(t, page, "#confirm-dialog"), "confirm dialog should open from list view")

	// close dialog
	require.NoError(t, page.Locator(".btn-cancel").Click())
	waitHidden(t, page.Locator("#confirm-dialog"))
}

func TestManualRun_HistoryButtonDisabledForIdleJobs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify history button is disabled for idle jobs (jobs that haven't run)
	disabledCount, err := page.Locator(".job-card .history-btn[disabled]").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, disabledCount, 1, "history button should be disabled for idle jobs")
}

func TestManualRun_HistoryButtonInListView(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// switch to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	// verify history button exists and is disabled for idle jobs
	disabledCount, err := page.Locator(".job-row .history-btn[disabled]").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, disabledCount, 1, "history button should be disabled for idle jobs in list view")
}
