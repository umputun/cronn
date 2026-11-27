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

func TestGrid_ShowsColumnsOnWideScreens(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)

	headers, err := page.Locator("#ihead > *:visible").AllTextContents()
	require.NoError(t, err)
	joined := strings.ToLower(strings.Join(headers, "|"))
	for _, h := range []string{"job", "history", "schedule", "last run", "next"} {
		assert.Contains(t, joined, h)
	}
	visible, err := row(page, jobHourly).Locator(".strip").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible)
}

func TestGrid_NarrowLayoutHidesSchedule(t *testing.T) {
	page := newPageSized(t, 820, 1180)
	navigateToDashboard(t, page)

	visible, err := row(page, jobHourly).Locator(".sched").IsVisible()
	require.NoError(t, err)
	assert.False(t, visible, "the schedule column folds away")
	visible, err = row(page, jobHourly).Locator(".strip").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "the history stays")
}

func TestGrid_TouchTargetsOnTouchScreens(t *testing.T) {
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{Width: 390, Height: 844}, HasTouch: new(true), IsMobile: new(true),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctx.Close() })
	page, err := ctx.NewPage()
	require.NoError(t, err)
	navigateToDashboard(t, page)

	small, err := page.Evaluate(`() => [...document.querySelectorAll('.ibtn, .btn')]
		.filter(el => el.offsetParent !== null)
		.filter(el => el.getBoundingClientRect().height < 35.5)
		.map(el => el.id || el.className)`)
	require.NoError(t, err)
	assert.Empty(t, small, "buttons are at least 36px tall on a touch screen")
	box, err := row(page, jobHourly).BoundingBox()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, box.Height, 47.5, "rows are at least 48px tall")
}

func TestResponsive_PlainHostLabelIsNotSizedAsControl(t *testing.T) {
	page := newPageSized(t, 390, 844)
	navigateToDashboard(t, page)

	box, err := page.Locator(".tb-host.static").BoundingBox()
	require.NoError(t, err)
	assert.Less(t, box.Height, 30.0, "a hostname without a server selector is a label, not a touch target")
}

func TestResponsive_NoHorizontalScroll(t *testing.T) {
	jobPage := ""
	for _, size := range []struct{ w, h int }{{320, 640}, {390, 844}, {600, 900}, {820, 1180}, {1024, 768}, {1440, 900}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			page := newPageSized(t, size.w, size.h)
			navigateToDashboard(t, page)
			assert.True(t, noHorizontalScroll(t, page), "the index must not scroll sideways")
			if jobPage == "" {
				jobPage = jobID(t, page, jobFailing)
			}
			gotoJob(t, page, jobPage, "")
			assert.True(t, noHorizontalScroll(t, page), "the job page must not scroll sideways")
		})
	}
}

func TestResponsive_PhoneRowsAreTwoLineBlocks(t *testing.T) {
	page := newPageSized(t, 390, 844)
	navigateToDashboard(t, page)

	visible, err := page.Locator("#ihead").IsVisible()
	require.NoError(t, err)
	assert.False(t, visible)
	nm, err := row(page, jobHourly).Locator(".nm").BoundingBox()
	require.NoError(t, err)
	last, err := row(page, jobHourly).Locator(".last").BoundingBox()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, last.Y, nm.Y+nm.Height-1, "the times sit on a second line under the name")
	visible, err = page.Locator("#search").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "search stays visible on phones")
}

func TestLayout_KeyboardFocusRing(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)

	require.NoError(t, page.Locator("#open-"+id).Focus())
	require.NoError(t, page.Keyboard().Press("Tab"))
	assert.True(t, focusRingShown(t, page), "Tab shows the ring on the next control")
	require.NoError(t, page.Keyboard().Press("Shift+Tab"))
	require.True(t, focusOn(t, page, "open-"+id))
	assert.True(t, focusRingShown(t, page), "Shift+Tab shows the ring on the row")

	require.NoError(t, page.Locator("#search").Focus())
	assert.False(t, focusRingShown(t, page), "the search input keeps its own border styling in place of the ring")

	require.NoError(t, page.Locator(".tb-live").Click())
	require.NoError(t, page.Locator("#open-"+id).Focus())
	assert.False(t, focusRingShown(t, page), "a mouse press ends keyboard mode")
}

func TestHTMX_PollingIsConfigured(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	trigger, err := page.Locator("#rows").GetAttribute("hx-trigger")
	require.NoError(t, err)
	assert.Equal(t, "every 5s [!uiHeld], refresh-jobs from:body", trigger)
	scripts, err := page.Evaluate(`() => [...document.querySelectorAll("script[src]")].map(s => s.src.split("/").pop())`)
	require.NoError(t, err)
	assert.ElementsMatch(t, []any{"htmx.min.js", "ui.js"}, scripts)
}

func TestHTMX_FailedPollShowsOnLiveIndicatorAndRecovers(t *testing.T) {
	tbl := []struct {
		name string
		page bool
	}{{"index", false}, {"job page", true}}
	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			page := newPageSized(t, 1440, 900)
			navigateToDashboard(t, page)
			re := jobsRe
			if tt.page {
				gotoJob(t, page, jobID(t, page, jobHourly), "")
				re = factsRe
			}
			failed := page.Locator(".tb-live .lv-failed")
			require.NoError(t, page.Route(re, func(r playwright.Route) {
				_ = r.Fulfill(playwright.RouteFulfillOptions{Status: new(500), Body: "boom"})
			}))
			require.NoError(t, failed.WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible, Timeout: new(8000.0)}))
			if !tt.page {
				assert.Len(t, rowIDs(t, page), totalJobs, "the last rows stay while polls fail")
			}
			require.NoError(t, page.Unroute(re))
			require.NoError(t, failed.WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateHidden, Timeout: new(8000.0)}))
		})
	}
}

func TestHTMX_ControlRequestFailureDoesNotMarkPollFailed(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	runJob(t, id)
	gotoJob(t, page, id, "")

	require.NoError(t, page.Route(outputRe, func(r playwright.Route) {
		_ = r.Fulfill(playwright.RouteFulfillOptions{Status: new(500), Body: "boom"})
	}))
	t.Cleanup(func() { _ = page.Unroute(outputRe) })
	_, err := page.ExpectResponse(outputRe, func() error { return page.Locator("#facts .strip a").First().Click() })
	require.NoError(t, err)
	assert.Never(t, func() bool {
		v, e := page.Locator(".tb-live .lv-failed").IsVisible()
		return e == nil && v
	}, 1*time.Second, 100*time.Millisecond, "only the poll itself decides the notice")
}

func TestHTMX_HeldSortClickActsAfterPoll(t *testing.T) {
	// a poll that replaced the header between press and release swallowed the click
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)

	btn := page.Locator("#sort-lastrun")
	require.NoError(t, btn.Hover())
	_, err := page.Evaluate(`() => { document.querySelector('#rows .it').dataset.held = '1' }`)
	require.NoError(t, err)
	require.NoError(t, page.Mouse().Down())
	assert.Never(t, func() bool {
		count, e := page.Locator("#rows [data-held]").Count()
		return e != nil || count == 0
	}, 6*time.Second, 200*time.Millisecond, "no poll replaces the rows while a button is pressed")
	_, err = page.ExpectResponse(`**/api/sort-mode`, func() error { return page.Mouse().Up() })
	require.NoError(t, err)
	waitVisible(t, page.Locator(`#sort-lastrun.on[data-dir="down"]`))
	clickSort(t, page, "default", "down")
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
	text, err := row(page, jobTemplated).Locator(".last").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "never ran")
	assert.NotContains(t, text, "exit")
}
