//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
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
	assert.Equal(t, fmt.Sprintf("1 of %d", totalJobs), strings.TrimSpace(text))
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
	empty := page.Locator("#rows .empty")
	waitVisible(t, empty)
	text, err := empty.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "No jobs match “nonexistent-xyz”")

	_, err = page.ExpectResponse(jobsRe, func() error { return page.Locator("#clear-search").Click() })
	require.NoError(t, err)
	waitForJobsLoaded(t, page)
	value, err := page.Locator("#search").InputValue()
	require.NoError(t, err)
	assert.Empty(t, value, "Clear search should empty the search box")
	assert.Len(t, rowIDs(t, page), totalJobs)
}

func TestSearch_EmptyStateNamesTheFilterHidingJobs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)

	clickPill(t, page, "failed")
	search(t, page, "hourly")
	empty := page.Locator("#rows .empty")
	waitVisible(t, empty)
	text, err := empty.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "No failing jobs match “hourly”")
	assert.Contains(t, text, "hidden by the search")

	_, err = page.ExpectResponse(`**/api/filter-mode`, func() error { return page.Locator("#show-all").Click() })
	require.NoError(t, err)
	waitVisible(t, page.Locator("#pill-all.on"))
	value, err := page.Locator("#search").InputValue()
	require.NoError(t, err)
	assert.Empty(t, value)
	assert.Len(t, rowIDs(t, page), totalJobs)
}

func TestSearch_SurvivesPollAndSortChange(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	search(t, page, "echo")
	clickSort(t, page, "lastrun", "down")
	for _, name := range rowNames(t, page) {
		assert.NotContains(t, []string{jobSilent, jobSlow}, name, "search should still apply after a sort change")
	}

	waitForPoll(t, page)
	for _, name := range rowNames(t, page) {
		assert.NotContains(t, []string{jobSilent, jobSlow}, name, "search should still apply after a poll")
	}
}

func TestJump_ListOpensTheChosenJobKeepingListState(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	hourly := jobID(t, page, jobHourly)
	clickPill(t, page, "all")
	clickSort(t, page, "nextrun", "down")
	id := openJob(t, page, jobFiveMin)

	jump := page.Locator("#search")
	_, err := page.ExpectResponse(`**/api/jobs/jump*`, func() error { return jump.Fill("hourly") })
	require.NoError(t, err)
	items := page.Locator("#jdrop .ji")
	require.Eventually(t, func() bool {
		n, e := items.Count()
		return e == nil && n == 1
	}, 3*time.Second, 50*time.Millisecond, "one job matches")
	txt, err := items.First().TextContent()
	require.NoError(t, err)
	assert.Contains(t, txt, jobHourly)

	_, err = page.ExpectResponse(`**/api/jobs/jump*`, func() error { return jump.Fill("no-such-job-xyz") })
	require.NoError(t, err)
	waitVisible(t, page.Locator("#jdrop", playwright.PageLocatorOptions{HasText: "No job matches"}))

	_, err = page.ExpectResponse(`**/api/jobs/jump*`, func() error { return jump.Fill("hourly") })
	require.NoError(t, err)
	require.NoError(t, page.Locator(`#jdrop .ji[data-job-id="`+hourly+`"]`).Click())
	waitJobPage(t, page, hourly)
	assert.Contains(t, page.URL(), "sort=nextrun", "the list state follows the jump")
	assert.NotEqual(t, id, hourly)

	require.NoError(t, page.Locator("#back-link").Click())
	waitForJobsLoaded(t, page)
	waitVisible(t, page.Locator(`#sort-nextrun.on[data-dir="down"]`))
	clickSort(t, page, "default", "down")
}
