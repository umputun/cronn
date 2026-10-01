//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func search(t *testing.T, page playwright.Page, term string) {
	t.Helper()
	_, err := page.ExpectResponse(jobsRe, func() error { return page.Locator("#search").Fill(term) },
		playwright.PageExpectResponseOptions{Timeout: new(5000.0)})
	require.NoError(t, err)
}

func TestSearch_FiltersByCommand(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	search(t, page, "hourly")
	require.Eventually(t, func() bool {
		names := rowNames(t, page)
		return len(names) == 1 && names[0] == jobHourly
	}, 5*time.Second, 100*time.Millisecond)
	text, err := page.Locator("#match-count").TextContent()
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("1 of %d jobs", totalJobs), strings.TrimSpace(text))
}

func TestSearch_FiltersByName(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	search(t, page, "weekday report")
	require.Eventually(t, func() bool {
		names := rowNames(t, page)
		return len(names) == 1 && names[0] == jobWeekday
	}, 5*time.Second, 100*time.Millisecond)
}

func TestSearch_NoResultsShowsEmptyStateAndClears(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	search(t, page, "nonexistent-xyz")
	zero := page.Locator("#jobs-container .zero")
	waitVisible(t, zero)
	text, err := zero.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "No jobs match “nonexistent-xyz”")

	_, err = page.ExpectResponse(jobsRe, func() error {
		return zero.Locator("button", playwright.LocatorLocatorOptions{HasText: "Clear search"}).Click()
	})
	require.NoError(t, err)
	waitForJobsLoaded(t, page)
	value, err := page.Locator("#search").InputValue()
	require.NoError(t, err)
	assert.Empty(t, value, "Clear search should empty the search box")
	count, err := page.Locator("tr.row").Count()
	require.NoError(t, err)
	assert.Equal(t, totalJobs, count)
}

func TestSearch_EmptyStateNamesTheFilterHidingJobs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)

	clickTab(t, page, "Failed")
	search(t, page, "hourly")
	zero := page.Locator("#jobs-container .zero")
	waitVisible(t, zero)
	text, err := zero.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "No failed jobs match “hourly”")
	assert.Contains(t, text, "hidden by the search")

	_, err = page.ExpectResponse(`**/api/filter-mode`, func() error {
		return zero.Locator("button", playwright.LocatorLocatorOptions{HasText: "Show all jobs"}).Click()
	})
	require.NoError(t, err)
	waitVisible(t, page.Locator(".tabs .tab.on").Filter(playwright.LocatorFilterOptions{HasText: "All"}))
	value, err := page.Locator("#search").InputValue()
	require.NoError(t, err)
	assert.Empty(t, value)
	count, err := page.Locator("tr.row").Count()
	require.NoError(t, err)
	assert.Equal(t, totalJobs, count)
}

func TestSearch_SurvivesPollAndSortChange(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	search(t, page, "echo")
	selectSort(t, page, "lastrun")
	for _, name := range rowNames(t, page) {
		assert.NotContains(t, []string{jobSilent, jobSlow}, name, "search should still apply after a sort change")
	}

	_, err := page.ExpectResponse(jobsRe, func() error { return nil }, playwright.PageExpectResponseOptions{Timeout: new(8000.0)})
	require.NoError(t, err)
	for _, name := range rowNames(t, page) {
		assert.NotContains(t, []string{jobSilent, jobSlow}, name, "search should still apply after a poll")
	}
	selectSort(t, page, "default")
}
