//go:build e2e

package e2e

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clickSort clicks a sort column header and waits for it to show the expected direction
func clickSort(t *testing.T, page playwright.Page, mode, dir string) {
	t.Helper()
	_, err := page.ExpectResponse(`**/api/sort-mode`, func() error { return page.Locator("#sort-" + mode).Click() })
	require.NoError(t, err)
	waitVisible(t, page.Locator("#sort-"+mode+`.on[data-dir="`+dir+`"]`))
}

// clickPill clicks a filter pill and waits for it to become the active one
func clickPill(t *testing.T, page playwright.Page, mode string) {
	t.Helper()
	_, err := page.ExpectResponse(`**/api/filter-mode`, func() error { return page.Locator("#pill-" + mode).Click() })
	require.NoError(t, err)
	waitVisible(t, page.Locator("#pill-"+mode+".on"))
}

// stableRows returns the grid order and the job state it was rendered from. A scheduled run can
// land between the render and the API read, so the read brackets a fresh poll and repeats when
// the state moved inside it
func stableRows(t *testing.T, page playwright.Page) ([]string, map[string]apiJob) {
	t.Helper()
	for range 3 {
		before := allJobs(t)
		_, err := page.Evaluate(`() => { document.querySelector('#rows .it').dataset.stale = '1' }`)
		require.NoError(t, err)
		_, err = page.WaitForFunction(`() => !document.querySelector('#rows [data-stale]')`, nil,
			playwright.PageWaitForFunctionOptions{Timeout: new(8000.0)})
		require.NoError(t, err)
		ids := rowIDs(t, page)
		after := allJobs(t)
		if reflect.DeepEqual(before, after) {
			return ids, after
		}
	}
	t.Fatal("job state kept changing during the read")
	return nil, nil
}

// assertSorted checks the grid order: jobs with a value first, ascending or descending, the rest
// after them in either direction
func assertSorted(t *testing.T, page playwright.Page, value func(apiJob) (time.Time, bool), desc bool) {
	t.Helper()
	ids, jobs := stableRows(t, page)
	require.Len(t, ids, totalJobs)
	var prev time.Time
	seenEmpty := false
	for i, id := range ids {
		v, ok := value(jobs[id])
		if !ok {
			seenEmpty = true
			continue
		}
		assert.False(t, seenEmpty, "row %d (%s) has a value after a row without one", i, id)
		if !prev.IsZero() {
			outOfOrder := v.Before(prev)
			if desc {
				outOfOrder = v.After(prev)
			}
			assert.False(t, outOfOrder, "row %d (%s) out of order", i, id)
		}
		prev = v
	}
}

func lastRunOf(j apiJob) (time.Time, bool) { return j.LastRun, !j.LastRun.IsZero() }

func nextRunOf(j apiJob) (time.Time, bool) { return j.NextRun, j.Enabled && !j.NextRun.IsZero() }

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

func TestTheme_BothThemesRenderIndexAndJobPage(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	theme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	initial := theme
	id := jobID(t, page, jobHourly)

	backgrounds := map[string]string{}
	for range 2 {
		gotoJob(t, page, id, "")
		bg, err := page.Evaluate(`() => getComputedStyle(document.body).backgroundColor`)
		require.NoError(t, err)
		backgrounds[theme] = bg.(string)
		visible, err := page.Locator("#cmdin").IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "job page should render in %s theme", theme)
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
	waitVisible(t, page.Locator(".tb"))
	persisted, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	assert.Equal(t, newTheme, persisted, "theme should persist after reload")
	clickThemeToggle(t, page, newTheme)
}

func TestSort_DefaultIsCrontabOrder(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	waitVisible(t, page.Locator(`#sort-default.on[data-dir="down"]`))
	names := rowNames(t, page)
	require.Len(t, names, totalJobs)
	assert.Equal(t, jobFiveMin, names[0])
	assert.Equal(t, jobTemplated, names[totalJobs-1])
}

func TestSort_HeaderSortsEachColumnBothWays(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	failing := jobID(t, page, jobFailing)
	setEnabled(t, failing, true)
	runJob(t, failing)
	silent := jobID(t, page, jobSilent)
	setEnabled(t, silent, false)
	t.Cleanup(func() { setEnabled(t, silent, true) })

	clickSort(t, page, "default", "up")
	names := rowNames(t, page)
	assert.Equal(t, jobTemplated, names[0], "crontab order flipped")
	assert.Equal(t, jobFiveMin, names[totalJobs-1])

	clickSort(t, page, "lastrun", "down")
	assertSorted(t, page, lastRunOf, true)
	assert.Equal(t, failing, rowIDs(t, page)[0], "the job that ran last is first")
	clickSort(t, page, "lastrun", "up")
	assertSorted(t, page, lastRunOf, false)

	clickSort(t, page, "nextrun", "down")
	assertSorted(t, page, nextRunOf, false)
	ids := rowIDs(t, page)
	assert.Equal(t, silent, ids[len(ids)-1], "a disabled job is last")
	clickSort(t, page, "nextrun", "up")
	assertSorted(t, page, nextRunOf, true)
	ids = rowIDs(t, page)
	assert.Equal(t, silent, ids[len(ids)-1], "a disabled job stays last when flipped")

	_, err := page.Reload()
	require.NoError(t, err)
	waitForJobsLoaded(t, page)
	waitVisible(t, page.Locator(`#sort-nextrun.on[data-dir="up"]`))

	clickSort(t, page, "default", "down")
	assert.Equal(t, jobFiveMin, rowNames(t, page)[0])
}

func TestSort_NeverRanJobsStayLast(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	for _, dir := range []string{"down", "up"} {
		clickSort(t, page, "lastrun", dir)
		texts, err := page.Locator(".it .last").AllTextContents()
		require.NoError(t, err)
		seenNever := false
		for _, txt := range texts {
			never := strings.TrimSpace(txt) == "never ran"
			assert.False(t, seenNever && !never, "%s: a job that ran is listed after one that never ran", dir)
			seenNever = seenNever || never
		}
	}
	clickSort(t, page, "default", "down")
}

func TestFilter_DefaultShowsAll(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	waitVisible(t, page.Locator("#pill-all.on"))
	count, err := page.Locator(".it").Count()
	require.NoError(t, err)
	assert.Equal(t, totalJobs, count)
}

func TestFilter_TabsFilterAndPersist(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)

	clickPill(t, page, "failed")
	require.Eventually(t, func() bool {
		return strings.Contains(strings.Join(rowNames(t, page), ","), jobFailing)
	}, 5*time.Second, 100*time.Millisecond)
	assert.True(t, evalBool(t, page, `() => [...document.querySelectorAll('.it')].every(r => r.dataset.state === 'fail')`),
		"the Failing pill should list only failed jobs")

	_, err := page.Reload()
	require.NoError(t, err)
	waitForJobsLoaded(t, page)
	waitVisible(t, page.Locator("#pill-failed.on"))

	clickPill(t, page, "all")
	count, err := page.Locator(".it").Count()
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
	waitVisible(t, page.Locator("#pill-all.on"))

	clickPill(t, page, "success")
	waitVisible(t, page.Locator(`#sort-default.on`))
	clickPill(t, page, "all")
}

func TestFilter_MatchCountFollowsFilter(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	text, err := page.Locator("#match-count").TextContent()
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(totalJobs)+" jobs", strings.TrimSpace(text))

	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	clickPill(t, page, "failed")
	shown, err := page.Locator(".it").Count()
	require.NoError(t, err)
	require.GreaterOrEqual(t, shown, 1)
	text, err = page.Locator("#match-count").TextContent()
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(shown)+" of "+strconv.Itoa(totalJobs), strings.TrimSpace(text))
	clickPill(t, page, "all")
}

func TestMenu_FilterAndSortWhenPillsDoNotFit(t *testing.T) {
	page := newPageSized(t, 820, 1180)
	navigateToDashboard(t, page)

	pillsVisible, err := page.Locator("#pills").IsVisible()
	require.NoError(t, err)
	assert.False(t, pillsVisible, "the pills fold into the menu")
	menu := page.Locator("#fdd")
	require.NoError(t, menu.Locator("summary").Click())
	waitVisible(t, page.Locator("#menu-filter-idle"))
	idle, err := page.Locator("#mcount-idle").TextContent()
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(countOf(t, page, "idle")), strings.TrimSpace(idle))

	_, err = page.ExpectResponse(`**/api/filter-mode`, func() error { return page.Locator("#menu-filter-idle").Click() })
	require.NoError(t, err)
	assert.True(t, evalBool(t, page, `() => [...document.querySelectorAll('.it')].every(r => r.dataset.state === 'never')`))
	assert.False(t, evalBool(t, page, `() => document.querySelector('#fdd').open`), "a choice closes the menu")
	summary, err := menu.Locator("summary").TextContent()
	require.NoError(t, err)
	assert.Contains(t, summary, "Never")

	require.NoError(t, menu.Locator("summary").Click())
	_, err = page.ExpectResponse(`**/api/sort-mode`, func() error { return page.Locator("#menu-sort-nextrun").Click() })
	require.NoError(t, err)
	require.NoError(t, menu.Locator("summary").Click())
	waitVisible(t, page.Locator(`#menu-sort-nextrun.on [data-dir="down"]`))
	_, err = page.ExpectResponse(`**/api/sort-mode`, func() error { return page.Locator("#menu-sort-nextrun").Click() })
	require.NoError(t, err)
	require.NoError(t, menu.Locator("summary").Click())
	waitVisible(t, page.Locator(`#menu-sort-nextrun.on [data-dir="up"]`))

	_, err = page.ExpectResponse(`**/api/filter-mode`, func() error { return page.Locator("#menu-filter-all").Click() })
	require.NoError(t, err)
	require.NoError(t, menu.Locator("summary").Click())
	_, err = page.ExpectResponse(`**/api/sort-mode`, func() error { return page.Locator("#menu-sort-default").Click() })
	require.NoError(t, err)
	assert.Equal(t, jobFiveMin, rowNames(t, page)[0])
}

func TestMenu_SortsOnPhone(t *testing.T) {
	page := newPageSized(t, 390, 844)
	navigateToDashboard(t, page)

	headVisible, err := page.Locator("#ihead").IsVisible()
	require.NoError(t, err)
	assert.False(t, headVisible, "the grid header is hidden on a phone")
	require.NoError(t, page.Locator("#fdd summary").Click())
	_, err = page.ExpectResponse(`**/api/sort-mode`, func() error { return page.Locator("#menu-sort-default").Click() })
	require.NoError(t, err)
	assert.Equal(t, jobTemplated, rowNames(t, page)[0], "a second click on the sorted column flips it")
	require.NoError(t, page.Locator("#fdd summary").Click())
	_, err = page.ExpectResponse(`**/api/sort-mode`, func() error { return page.Locator("#menu-sort-default").Click() })
	require.NoError(t, err)
	assert.Equal(t, jobFiveMin, rowNames(t, page)[0])
}

func TestMenu_ClosesOnOutsideClickAndEsc(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	gear := page.Locator("#gear-menu")
	require.NoError(t, gear.Locator("summary").Click())
	waitVisible(t, page.Locator("#open-settings"))
	require.NoError(t, page.Locator("#ilist").Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: 5, Y: 5}}))
	waitHidden(t, page.Locator("#open-settings"))

	require.NoError(t, gear.Locator("summary").Click())
	waitVisible(t, page.Locator("#open-settings"))
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("#open-settings"))
}
