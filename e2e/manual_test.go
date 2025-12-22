//go:build e2e

package e2e

import (
	"testing"

	"github.com/playwright-community/playwright-go"
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
	require.NoError(t, page.Locator("#confirm-dialog").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

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
	require.NoError(t, page.Locator("#confirm-dialog").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))

	// click cancel button
	require.NoError(t, page.Locator(".btn-cancel").Click())
	require.NoError(t, page.Locator("#confirm-dialog").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateHidden,
	}))

	// verify dialog is hidden
	assert.False(t, isModalVisible(t, page, "#confirm-dialog"), "confirm dialog should be hidden after cancel")
}
