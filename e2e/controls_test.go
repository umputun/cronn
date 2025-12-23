//go:build e2e

package e2e

import (
	"testing"

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
	waitVisible(t, page.Locator(".jobs-table"))

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
	waitVisible(t, page.Locator(".jobs-table"))

	// toggle back to cards
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".job-card").First())

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

	// wait for page reload by waiting for header to be visible again
	waitVisible(t, page.Locator(".header"))

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

func TestFilter_FullCycle(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// cycle through all filter modes: all -> running -> success -> failed -> all
	modes := []string{"All Jobs", "Running", "Success", "Failed", "All Jobs"}

	for i, expected := range modes {
		text, err := page.Locator(".filter-button .filter-label").TextContent()
		require.NoError(t, err)
		assert.Contains(t, text, expected, "filter mode %d should be %s", i, expected)

		if i < len(modes)-1 {
			require.NoError(t, page.Locator(".filter-button").Click())
			require.NoError(t, page.Locator(".filter-button .filter-label:has-text('"+modes[i+1]+"')").WaitFor())
		}
	}
}

func TestSort_FullCycle(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// cycle through all sort modes: default -> lastrun -> nextrun -> default
	modes := []string{"Original Order", "Last Run", "Next Run", "Original Order"}

	for i, expected := range modes {
		text, err := page.Locator(".sort-button .sort-label").TextContent()
		require.NoError(t, err)
		assert.Contains(t, text, expected, "sort mode %d should be %s", i, expected)

		if i < len(modes)-1 {
			require.NoError(t, page.Locator(".sort-button").Click())
			require.NoError(t, page.Locator(".sort-button .sort-label:has-text('"+modes[i+1]+"')").WaitFor())
		}
	}
}

// --- persistence tests ---

func TestTheme_DarkThemeWorks(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// toggle until we get dark theme
	for i := 0; i < 3; i++ {
		theme, err := page.Locator("html").GetAttribute("data-theme")
		require.NoError(t, err)
		if theme == "dark" {
			break
		}
		require.NoError(t, page.Locator(".theme-toggle").Click())
		waitVisible(t, page.Locator(".header"))
	}

	// verify we're in dark theme
	theme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	assert.Equal(t, "dark", theme, "should be in dark theme")

	// verify page works in dark theme
	waitForJobsLoaded(t, page)

	visible, err := page.Locator(".header").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "header should be visible in dark theme")

	count, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "job cards should be visible in dark theme")

	// verify modal works in dark theme
	require.NoError(t, page.Locator(".job-card .info-btn").First().Click())
	waitVisible(t, page.Locator("#job-modal"))

	visible, err = page.Locator(".job-modal").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "job modal should work in dark theme")

	// close modal
	require.NoError(t, page.Locator("#modal-content .modal-close").Click())
	waitHidden(t, page.Locator("#job-modal"))
}

func TestTheme_LightThemeWorks(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// get initial theme
	initialTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)

	// toggle until we get light theme
	for i := 0; i < 3; i++ {
		theme, err := page.Locator("html").GetAttribute("data-theme")
		require.NoError(t, err)
		if theme == "light" {
			break
		}
		require.NoError(t, page.Locator(".theme-toggle").Click())
		waitVisible(t, page.Locator(".header"))
	}

	// verify we're in light theme
	theme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	assert.Equal(t, "light", theme, "should be in light theme")

	// verify page still works in light theme - check key elements
	waitForJobsLoaded(t, page)

	visible, err := page.Locator(".header").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "header should be visible in light theme")

	visible, err = page.Locator(".stats-bar").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "stats bar should be visible in light theme")

	count, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "job cards should be visible in light theme")

	// verify controls work in light theme
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	visible, err = page.Locator(".jobs-table").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "list view should work in light theme")

	// restore original theme if different
	if initialTheme != "light" {
		for i := 0; i < 3; i++ {
			currentTheme, _ := page.Locator("html").GetAttribute("data-theme")
			if currentTheme == initialTheme {
				break
			}
			require.NoError(t, page.Locator(".theme-toggle").Click())
			waitVisible(t, page.Locator(".header"))
		}
	}
}

func TestTheme_PersistsAcrossReload(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// get initial theme
	initialTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)

	// toggle theme
	require.NoError(t, page.Locator(".theme-toggle").Click())
	waitVisible(t, page.Locator(".header"))

	// verify theme changed
	newTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	assert.NotEqual(t, initialTheme, newTheme)

	// reload page
	_, err = page.Reload()
	require.NoError(t, err)
	waitVisible(t, page.Locator(".header"))

	// verify theme persisted
	persistedTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	assert.Equal(t, newTheme, persistedTheme, "theme should persist after reload")
}

func TestViewMode_PersistsAcrossReload(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// default should be cards
	count, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1, "should start with cards view")

	// toggle to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	// reload page
	_, err = page.Reload()
	require.NoError(t, err)
	waitVisible(t, page.Locator(".header"))
	waitForJobsLoaded(t, page)

	// verify list view persisted
	visible, err := page.Locator(".jobs-table").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "list view should persist after reload")
}
