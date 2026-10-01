//go:build e2e

package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rowNames(t *testing.T, page playwright.Page) []string {
	t.Helper()
	names, err := page.Locator("tr.row .job-open").AllTextContents()
	require.NoError(t, err)
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	return names
}

func selectSort(t *testing.T, page playwright.Page, mode string) {
	t.Helper()
	_, err := page.Evaluate(`() => { document.querySelector('select[name=sort]').dataset.stale = '1' }`)
	require.NoError(t, err)
	_, err = page.ExpectResponse(`**/api/sort-mode`, func() error {
		if _, e := page.Locator("select[name=sort]").SelectOption(playwright.SelectOptionValues{Values: &[]string{mode}}); e != nil {
			return fmt.Errorf("select sort mode %s: %w", mode, e)
		}
		return nil
	})
	require.NoError(t, err)
	_, err = page.WaitForFunction(`() => {
		const s = document.querySelector('select[name=sort]');
		return !!s && !s.dataset.stale && !s.closest('.htmx-added');
	}`, nil)
	require.NoError(t, err, "the swapped-in select must be processed by htmx before the next change")
}

func clickTab(t *testing.T, page playwright.Page, label string) {
	t.Helper()
	tab := page.Locator(".tabs .tab").Filter(playwright.LocatorFilterOptions{HasText: label})
	_, err := page.ExpectResponse(`**/api/filter-mode`, func() error { return tab.Click() })
	require.NoError(t, err)
	waitVisible(t, page.Locator(".tabs .tab.on").Filter(playwright.LocatorFilterOptions{HasText: label}))
}

func TestTheme_ToggleDarkLight(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	initialTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	newTheme := clickThemeToggle(t, page, initialTheme)
	assert.NotEqual(t, initialTheme, newTheme, "theme should change after toggle")
	clickThemeToggle(t, page, newTheme)
}

func TestTheme_ToggleReachableOnPhone(t *testing.T) {
	page := newPageSized(t, 390, 844)
	navigateToDashboard(t, page)

	initial, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	next := clickThemeToggle(t, page, initial)
	assert.NotEqual(t, initial, next)
	clickThemeToggle(t, page, next)
}

func TestTheme_BothThemesRenderDashboardAndInspector(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	theme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	initial := theme

	backgrounds := map[string]string{}
	for range 2 {
		waitForJobsLoaded(t, page)
		insp := openInspector(t, page, jobHourly)
		bg, err := page.Evaluate(`() => getComputedStyle(document.body).backgroundColor`)
		require.NoError(t, err)
		backgrounds[theme] = bg.(string)
		visible, err := insp.Locator(".cmdbox").IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "inspector should render in %s theme", theme)
		theme = clickThemeToggle(t, page, theme)
	}
	assert.Equal(t, initial, theme)
	require.Len(t, backgrounds, 2)
	assert.NotEqual(t, backgrounds["dark"], backgrounds["light"], "themes should use different backgrounds")
}

func TestTheme_PersistsAcrossReload(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	initialTheme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	newTheme := clickThemeToggle(t, page, initialTheme)

	_, err = page.Reload()
	require.NoError(t, err)
	waitVisible(t, page.Locator(".top"))
	persisted, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	assert.Equal(t, newTheme, persisted, "theme should persist after reload")
	clickThemeToggle(t, page, newTheme)
}

func TestSort_DefaultIsCrontabOrder(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	value, err := page.Locator("select[name=sort]").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "default", value)
	names := rowNames(t, page)
	require.Len(t, names, totalJobs)
	assert.Equal(t, jobFiveMin, names[0])
	assert.Equal(t, jobTemplated, names[totalJobs-1])
}

func TestSort_SelectChangesOrderAndPersists(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	selectSort(t, page, "nextrun")
	waitVisible(t, page.Locator("th.c-next.sorted"))
	names := rowNames(t, page)
	require.Len(t, names, totalJobs)
	assert.Equal(t, jobFiveMin, names[0], "the */5 job runs next")

	_, err := page.Reload()
	require.NoError(t, err)
	waitForJobsLoaded(t, page)
	value, err := page.Locator("select[name=sort]").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "nextrun", value, "sort should persist across reload")

	selectSort(t, page, "lastrun")
	selectSort(t, page, "default")
	assert.Equal(t, jobFiveMin, rowNames(t, page)[0])
}

func TestSort_LastRunHeaderShowsDescendingArrow(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)

	selectSort(t, page, "lastrun")
	require.Eventually(t, func() bool {
		return evalBool(t, page, `() => document.querySelector('th.c-last').classList.contains('sorted')`)
	}, 5*time.Second, 100*time.Millisecond, "the last run header is marked sorted")
	arrow, err := page.Evaluate(`() => getComputedStyle(document.querySelector('th.c-last'), '::after').content`)
	require.NoError(t, err)
	assert.Equal(t, `" ↓"`, arrow, "most recent first is a descending sort")
	assert.False(t, evalBool(t, page, `() => document.querySelector('th.c-next').classList.contains('sorted')`))

	selectSort(t, page, "default")
}

func TestSort_OpenSelectSurvivesPoll(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	sel := page.Locator("select[name=sort]")
	require.NoError(t, sel.Focus())

	_, err := page.ExpectResponse(jobsRe, func() error { return nil }, playwright.PageExpectResponseOptions{Timeout: new(8000.0)})
	require.NoError(t, err)
	assert.True(t, activeElementIn(t, page, "#jobs-controls"), "a poll must not replace the focused sort select")
}

func TestFilter_DefaultShowsAll(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	active, err := page.Locator(".tabs .tab.on").TextContent()
	require.NoError(t, err)
	assert.Contains(t, active, "All")
	count, err := page.Locator("tr.row").Count()
	require.NoError(t, err)
	assert.Equal(t, totalJobs, count)
}

func TestFilter_TabsFilterAndPersist(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)

	clickTab(t, page, "Failed")
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(rowNames(t, page), ","), jobFailing)
	}, 5*time.Second, 100*time.Millisecond)
	assert.True(t, evalBool(t, page, `() => [...document.querySelectorAll('tr.row')].every(r => r.dataset.state === 'fail')`),
		"the Failed tab should list only failed jobs")

	_, err := page.Reload()
	require.NoError(t, err)
	waitForJobsLoaded(t, page)
	active, err := page.Locator(".tabs .tab.on").TextContent()
	require.NoError(t, err)
	assert.Contains(t, active, "Failed", "filter should persist across reload")

	clickTab(t, page, "All")
	count, err := page.Locator("tr.row").Count()
	require.NoError(t, err)
	assert.Equal(t, totalJobs, count)
}

func TestFilter_TabModesSurviveCookiesChangedElsewhere(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	require.NoError(t, page.Context().AddCookies([]playwright.OptionalCookie{
		{Name: "filter-mode", Value: "failed", URL: &baseURL},
		{Name: "sort-mode", Value: "lastrun", URL: &baseURL},
	}))
	waitForPoll(t, page)
	assert.Len(t, rowNames(t, page), totalJobs, "a poll keeps this tab's All filter")
	active, err := page.Locator(".tabs .tab.on").TextContent()
	require.NoError(t, err)
	assert.Contains(t, active, "All")

	clickTab(t, page, "Succeeded")
	value, err := page.Locator("select[name=sort]").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "default", value, "a filter change keeps this tab's sort")
	clickTab(t, page, "All")
}

func TestFilter_MatchCountFollowsFilter(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	text, err := page.Locator("#match-count").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "8 jobs", strings.TrimSpace(text))

	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	clickTab(t, page, "Failed")
	shown, err := page.Locator("tr.row").Count()
	require.NoError(t, err)
	require.GreaterOrEqual(t, shown, 1)
	text, err = page.Locator("#match-count").TextContent()
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(shown)+" of 8 jobs", strings.TrimSpace(text))
	clickTab(t, page, "All")
}
