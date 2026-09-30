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

func TestTable_ShowsColumnsOnWideScreens(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)

	headers, err := page.Locator("table.jobs thead th:visible").AllTextContents()
	require.NoError(t, err)
	joined := strings.ToLower(strings.Join(headers, "|"))
	for _, h := range []string{"job", "schedule", "last run", "next"} {
		assert.Contains(t, joined, h)
	}
	assert.NotContains(t, joined, "when", "the merged column is only for narrow layouts")
}

func TestTable_NarrowLayoutFoldsScheduleAndLabelsTimes(t *testing.T) {
	page := newPageSized(t, 820, 1180)
	navigateToDashboard(t, page)

	visible, err := row(page, jobHourly).Locator("td.c-sched").IsVisible()
	require.NoError(t, err)
	assert.False(t, visible, "the schedule column folds away")
	fold, err := row(page, jobHourly).Locator(".fold-sched").TextContent()
	require.NoError(t, err)
	assert.Contains(t, fold, "Every hour")
	when, err := row(page, jobHourly).Locator("td.c-time").TextContent()
	require.NoError(t, err)
	assert.Contains(t, when, "Last")
	assert.Contains(t, when, "Next")
}

func TestTable_TouchTargetsBelowWideLayout(t *testing.T) {
	for _, size := range []struct{ w, h int }{{820, 1180}, {390, 844}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			page := newPageSized(t, size.w, size.h)
			navigateToDashboard(t, page)
			small, err := page.Evaluate(`() => [...document.querySelectorAll('.run-btn, .toggle-btn, .tab, select[name=sort], .iconbtn, #search-box')]
				.filter(el => el.offsetParent !== null)
				.filter(el => { const r = el.getBoundingClientRect(); return r.height < 43.5; })
				.map(el => el.className || el.id)`)
			require.NoError(t, err)
			assert.Empty(t, small, "controls should be at least 44px tall")
		})
	}
}

func TestResponsive_PlainHostLabelIsNotSizedAsControl(t *testing.T) {
	page := newPageSized(t, 390, 844)
	navigateToDashboard(t, page)

	box, err := page.Locator("span.host").BoundingBox()
	require.NoError(t, err)
	assert.Less(t, box.Height, 30.0, "a hostname without a server selector is a label, not a touch target")
}

func TestResponsive_NoHorizontalScroll(t *testing.T) {
	for _, size := range []struct{ w, h int }{{320, 640}, {390, 844}, {600, 900}, {820, 1180}, {1024, 768}, {1440, 900}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			page := newPageSized(t, size.w, size.h)
			navigateToDashboard(t, page)
			assert.True(t, noHorizontalScroll(t, page), "page must not scroll sideways")
		})
	}
}

func TestResponsive_PhoneRowsAreTwoLineBlocks(t *testing.T) {
	page := newPageSized(t, 390, 844)
	navigateToDashboard(t, page)

	visible, err := page.Locator("table.jobs thead").IsVisible()
	require.NoError(t, err)
	assert.False(t, visible)
	display, err := row(page, jobHourly).Evaluate(`el => getComputedStyle(el).display`, nil)
	require.NoError(t, err)
	assert.Equal(t, "grid", display)
	visible, err = page.Locator("#search").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "search stays visible on phones")
}

func TestFooter_ShowsLinksAndCopyright(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	text, err := page.Locator(".footer").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Umputun")
	href, err := page.Locator(".footer a[href*='github.com/umputun/cronn']").GetAttribute("href")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/umputun/cronn", href)
}

func TestHTMX_PollingIsConfigured(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	trigger, err := page.Locator("#jobs-container").GetAttribute("hx-trigger")
	require.NoError(t, err)
	assert.Equal(t, "load, every 5s, refresh-jobs from:body", trigger)
	scripts, err := page.Evaluate(`() => [...document.querySelectorAll("script[src]")].map(s => s.src.split("/").pop())`)
	require.NoError(t, err)
	assert.ElementsMatch(t, []any{"htmx.min.js", "ui.js"}, scripts)
}

func TestHTMX_FailedPollShowsNoticeAndRecovers(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	notice := page.Locator(".fresh-failed")

	require.NoError(t, page.Route(jobsRe, func(r playwright.Route) {
		_ = r.Fulfill(playwright.RouteFulfillOptions{Status: new(500), Body: "boom"})
	}))
	waitVisible(t, notice)
	count, err := page.Locator("tr.row").Count()
	require.NoError(t, err)
	assert.Equal(t, totalJobs, count, "the last table stays while polls fail")

	require.NoError(t, page.Unroute(jobsRe))
	require.NoError(t, notice.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateHidden, Timeout: new(8000.0),
	}))
}

func TestHTMX_InspectorRequestFailureDoesNotMarkPollFailed(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	require.NoError(t, page.Route(inspectorRe, func(r playwright.Route) {
		_ = r.Fulfill(playwright.RouteFulfillOptions{Status: new(500), Body: "boom"})
	}))
	_, err := page.ExpectResponse(inspectorRe, func() error { return row(page, jobHourly).Locator(".job-open").Click() })
	require.NoError(t, err)
	assert.Never(t, func() bool {
		v, e := page.Locator(".fresh-failed").IsVisible()
		return e == nil && v
	}, 1*time.Second, 100*time.Millisecond, "only the table poll decides the notice")
	require.NoError(t, page.Unroute(inspectorRe))
}

func TestJobStatus_NeverRanIsNotAWarning(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	state, err := row(page, jobTemplated).GetAttribute("data-state")
	require.NoError(t, err)
	require.Equal(t, "never", state, "no test runs the templated job")
	dotColor, err := row(page, jobTemplated).Locator(".dot").Evaluate(`el => getComputedStyle(el).backgroundColor`, nil)
	require.NoError(t, err)
	assert.Equal(t, "rgba(0, 0, 0, 0)", dotColor, "a job that never ran gets a hollow marker, not a warning color")
	text, err := row(page, jobTemplated).Locator("td.c-last").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "never ran")
	assert.NotContains(t, text, "exit")
}
