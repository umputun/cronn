//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModal_JobDetailsOpens(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click info button on first job
	require.NoError(t, page.Locator(".job-card .info-btn").First().Click())
	waitVisible(t, page.Locator("#job-modal"))

	// verify modal is visible
	assert.True(t, isModalVisible(t, page, "#job-modal"), "job modal should be visible")

	// verify modal content
	text, err := page.Locator("#modal-content h2").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "Job Details", text)
}

func TestModal_JobDetailsShowsContent(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click info button on first job
	require.NoError(t, page.Locator(".job-card .info-btn").First().Click())
	waitVisible(t, page.Locator("#job-modal"))

	// verify modal shows job details
	visible, err := page.Locator(".job-modal .modal-label:has-text('Status')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show status label")

	visible, err = page.Locator(".job-modal .modal-label:has-text('Schedule')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show schedule label")

	visible, err = page.Locator(".job-modal .modal-label:has-text('Command')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show command label")
}

func TestModal_JobDetailsCloses(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// open modal
	require.NoError(t, page.Locator(".job-card .info-btn").First().Click())
	waitVisible(t, page.Locator("#job-modal"))

	// click close button
	require.NoError(t, page.Locator("#modal-content .modal-close").Click())
	waitHidden(t, page.Locator("#job-modal"))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#job-modal"), "job modal should be hidden after close")
}

func TestModal_SettingsOpens(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// click settings button in header
	require.NoError(t, page.Locator(".control-btn[title='Settings & About']").Click())
	waitVisible(t, page.Locator("#settings-modal"))

	// verify settings modal is visible
	assert.True(t, isModalVisible(t, page, "#settings-modal"), "settings modal should be visible")

	// verify modal header
	text, err := page.Locator("#settings-content h2").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "Settings & About", text)
}

func TestModal_SettingsShowsConfiguration(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// open settings modal
	require.NoError(t, page.Locator(".control-btn[title='Settings & About']").Click())
	waitVisible(t, page.Locator("#settings-modal"))

	// verify settings sections
	visible, err := page.Locator(".settings-modal:has-text('Application')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Application section")

	visible, err = page.Locator(".settings-modal:has-text('Web Interface')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Web Interface section")

	visible, err = page.Locator(".settings-modal:has-text('Scheduler')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Scheduler section")
}

func TestModal_SettingsCloses(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// open settings modal
	require.NoError(t, page.Locator(".control-btn[title='Settings & About']").Click())
	waitVisible(t, page.Locator("#settings-modal"))

	// click close button
	require.NoError(t, page.Locator("#settings-content .modal-close").Click())
	waitHidden(t, page.Locator("#settings-modal"))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#settings-modal"), "settings modal should be hidden after close")
}

// --- backdrop close tests ---

func TestModal_JobDetailsClosesOnBackdropClick(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// open modal
	require.NoError(t, page.Locator(".job-card .info-btn").First().Click())
	waitVisible(t, page.Locator("#job-modal"))

	// click on backdrop (not on modal content)
	// the backdrop has onclick="if(event.target === this) this.style.display='none'"
	require.NoError(t, page.Locator("#job-modal").Click(defaultClickOpts()))
	waitHidden(t, page.Locator("#job-modal"))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#job-modal"), "job modal should close on backdrop click")
}

func TestModal_SettingsClosesOnBackdropClick(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// open settings modal
	require.NoError(t, page.Locator(".control-btn[title='Settings & About']").Click())
	waitVisible(t, page.Locator("#settings-modal"))

	// click on backdrop
	require.NoError(t, page.Locator("#settings-modal").Click(defaultClickOpts()))
	waitHidden(t, page.Locator("#settings-modal"))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#settings-modal"), "settings modal should close on backdrop click")
}

func TestModal_ConfirmDialogDoesNotCloseOnBackdropClick(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// open confirm dialog by clicking run button
	require.NoError(t, page.Locator(".job-card .btn-compact").First().Click())
	waitVisible(t, page.Locator("#confirm-dialog"))

	// click on backdrop - confirm dialog should NOT close on backdrop click
	// (it doesn't have the onclick handler like other modals)
	require.NoError(t, page.Locator("#confirm-dialog").Click(defaultClickOpts()))

	// verify dialog is still visible (confirm dialog requires explicit cancel/confirm)
	assert.True(t, isModalVisible(t, page, "#confirm-dialog"), "confirm dialog should not close on backdrop click")

	// close with cancel button
	require.NoError(t, page.Locator(".btn-cancel").Click())
	waitHidden(t, page.Locator("#confirm-dialog"))
}
