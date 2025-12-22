//go:build e2e

package e2e

import (
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- view mode tests ---

func TestViewMode_DefaultIsCards(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify job cards are visible
	count, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should show job cards by default")
}

func TestViewMode_ToggleToList(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click view toggle button
	require.NoError(t, page.Locator(".view-toggle").Click())

	// wait for table to appear
	err := page.Locator(".jobs-table").WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	})
	require.NoError(t, err)

	// verify we have table rows
	count, err := page.Locator(".job-row").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should show job rows in list view")
}

func TestViewMode_ToggleBackToCards(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// toggle to list
	require.NoError(t, page.Locator(".view-toggle").Click())
	err := page.Locator(".jobs-table").WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	})
	require.NoError(t, err)

	// toggle back to cards
	require.NoError(t, page.Locator(".view-toggle").Click())
	err = page.Locator(".job-card").First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	})
	require.NoError(t, err)

	// verify cards are visible again
	count, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should show job cards after toggling back")
}

// --- theme tests ---

func TestTheme_ToggleDarkLight(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// get initial theme
	initialTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)

	// click theme toggle (triggers HX-Refresh which does full page reload)
	require.NoError(t, page.Locator(".theme-toggle").Click())

	// wait for HX-Refresh to complete (full page reload)
	err = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateDomcontentloaded,
	})
	require.NoError(t, err)

	// get new theme
	newTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)

	// verify theme changed
	assert.NotEqual(t, initialTheme, newTheme, "theme should change after toggle")
}

// --- sort tests ---

func TestSort_DefaultOrder(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify sort button shows "Original Order"
	text, err := page.Locator(".sort-button .sort-label").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Original Order")
}

func TestSort_ToggleSortMode(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click sort button to change mode
	require.NoError(t, page.Locator(".sort-button").Click())
	require.NoError(t, page.Locator(".sort-button .sort-label:has-text('Last Run')").WaitFor())

	// verify sort mode changed to "Last Run"
	text, err := page.Locator(".sort-button .sort-label").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Last Run")

	// click again to change to "Next Run"
	require.NoError(t, page.Locator(".sort-button").Click())
	require.NoError(t, page.Locator(".sort-button .sort-label:has-text('Next Run')").WaitFor())

	text, err = page.Locator(".sort-button .sort-label").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Next Run")
}

// --- filter tests ---

func TestFilter_DefaultShowsAll(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify filter button shows "All Jobs"
	text, err := page.Locator(".filter-button .filter-label").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "All Jobs")
}

func TestFilter_ToggleFilterMode(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// click filter button to cycle modes
	require.NoError(t, page.Locator(".filter-button").Click())
	require.NoError(t, page.Locator(".filter-button .filter-label:has-text('Running')").WaitFor())

	// verify filter mode changed to "Running"
	text, err := page.Locator(".filter-button .filter-label").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Running")

	// click again to change to "Success"
	require.NoError(t, page.Locator(".filter-button").Click())
	require.NoError(t, page.Locator(".filter-button .filter-label:has-text('Success')").WaitFor())

	text, err = page.Locator(".filter-button .filter-label").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Success")
}
