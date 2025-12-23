//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runJobAndWaitForCompletion triggers a manual job run and waits for it to complete
func runJobAndWaitForCompletion(t *testing.T, page playwright.Page) {
	t.Helper()

	// click run button on first job
	require.NoError(t, page.Locator(".job-card .btn-compact").First().Click())
	waitVisible(t, page.Locator("#confirm-dialog"))

	// click confirm to run the job
	require.NoError(t, page.Locator(".btn-confirm").Click())
	waitHidden(t, page.Locator("#confirm-dialog"))

	// wait for job to complete (status changes from running to success/failed)
	// the echo commands are instant, so this should be quick
	assert.Eventually(t, func() bool {
		// check if any job has success status (means it ran)
		count, e := page.Locator(".status-indicator.success, .status-badge.success").Count()
		return e == nil && count >= 1
	}, 10*time.Second, 200*time.Millisecond, "job should complete")
}

func TestHistory_ModalOpensAfterJobRun(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// now click history button (should be enabled after job ran)
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// verify history modal is visible
	assert.True(t, isModalVisible(t, page, "#history-modal"), "history modal should be visible")

	// verify modal header
	text, err := page.Locator("#history-content h2").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "Execution History", text)
}

func TestHistory_ShowsExecutionRecords(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// verify history table exists
	visible, err := page.Locator(".history-table").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "history table should be visible")

	// verify at least one execution record
	count, err := page.Locator(".history-table tbody tr").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have at least one execution record")

	// verify execution shows success status (use First() as there may be multiple executions)
	visible, err = page.Locator(".history-table .status-badge.success").First().IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "execution should show success status")
}

func TestHistory_ShowsCommandInModal(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// verify command is shown in modal
	command, err := page.Locator(".history-command").TextContent()
	require.NoError(t, err)
	assert.Contains(t, command, "echo", "history modal should show command")
}

func TestHistory_ViewLogsButton(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// click view logs button
	logsBtn := page.Locator(".btn-logs").First()
	require.NoError(t, logsBtn.Click())

	// wait for logs view to load
	waitVisible(t, page.Locator(".logs-output"))

	// verify logs view shows output
	visible, err := page.Locator(".logs-output").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "logs output should be visible")

	// verify back button exists
	visible, err = page.Locator(".modal-back").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "back button should be visible in logs view")
}

func TestHistory_BackButtonFromLogs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// click view logs button
	logsBtn := page.Locator(".btn-logs").First()
	require.NoError(t, logsBtn.Click())
	waitVisible(t, page.Locator(".logs-output"))

	// click back button
	require.NoError(t, page.Locator(".modal-back").Click())

	// wait for history table to reappear
	waitVisible(t, page.Locator(".history-table"))

	// verify we're back to history view
	visible, err := page.Locator(".history-table").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should return to history view after clicking back")
}

func TestHistory_ModalClosesOnBackdropClick(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// click on backdrop to close
	require.NoError(t, page.Locator("#history-modal").Click(defaultClickOpts()))
	waitHidden(t, page.Locator("#history-modal"))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#history-modal"), "history modal should close on backdrop click")
}

func TestHistory_ModalClosesWithCloseButton(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// click close button
	require.NoError(t, page.Locator("#history-content .modal-close").Click())
	waitHidden(t, page.Locator("#history-modal"))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#history-modal"), "history modal should close with close button")
}

func TestHistory_LogsShowJobOutput(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// click view logs button
	logsBtn := page.Locator(".btn-logs").First()
	require.NoError(t, logsBtn.Click())
	waitVisible(t, page.Locator(".logs-output"))

	// verify logs contain expected output (echo commands output their text)
	logsContent, err := page.Locator(".logs-output pre code").TextContent()
	require.NoError(t, err)
	assert.NotEmpty(t, logsContent, "logs should contain job output")
	assert.Contains(t, logsContent, "job", "logs should contain job output text")

	// verify execution info is shown
	visible, err := page.Locator(".logs-info-row").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "logs should show execution info row")

	// verify status is shown
	visible, err = page.Locator(".logs-info-row .status-badge").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "logs should show status badge")
}

func TestHistory_LogsShowExitCode(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// run a job first
	runJobAndWaitForCompletion(t, page)

	// open history modal
	historyBtn := page.Locator(".job-card .history-btn:not([disabled])").First()
	require.NoError(t, historyBtn.Click())
	waitVisible(t, page.Locator("#history-modal"))

	// click view logs button
	logsBtn := page.Locator(".btn-logs").First()
	require.NoError(t, logsBtn.Click())
	waitVisible(t, page.Locator(".logs-output"))

	// verify status shows exit code
	statusText, err := page.Locator(".logs-info-row .status-badge").TextContent()
	require.NoError(t, err)
	assert.Contains(t, statusText, "exit code", "status should show exit code")
	assert.Contains(t, statusText, "0", "successful job should have exit code 0")
}
