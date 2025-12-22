//go:build e2e

package e2e

import (
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModal_JobDetailsOpens(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click info button on first job
	require.NoError(t, page.Locator(".job-card .info-btn").First().Click())
	require.NoError(t, page.Locator("#job-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

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
	require.NoError(t, page.Locator("#job-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

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
	require.NoError(t, page.Locator("#job-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

	// click close button
	require.NoError(t, page.Locator("#modal-content .modal-close").Click())
	require.NoError(t, page.Locator("#job-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateHidden,
	}))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#job-modal"), "job modal should be hidden after close")
}

func TestModal_SettingsOpens(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// click settings button in header
	require.NoError(t, page.Locator(".control-btn[title='Settings & About']").Click())
	require.NoError(t, page.Locator("#settings-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

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
	require.NoError(t, page.Locator("#settings-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

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
	require.NoError(t, page.Locator("#settings-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

	// click close button
	require.NoError(t, page.Locator("#settings-content .modal-close").Click())
	require.NoError(t, page.Locator("#settings-modal").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateHidden,
	}))

	// verify modal is hidden
	assert.False(t, isModalVisible(t, page, "#settings-modal"), "settings modal should be hidden after close")
}
