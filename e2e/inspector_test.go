//go:build e2e

package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func inspectorText(t *testing.T, page playwright.Page, sel string) string {
	t.Helper()
	text, err := page.Locator("#inspector " + sel).TextContent()
	require.NoError(t, err)
	return strings.Join(strings.Fields(text), " ")
}

func TestInspector_FailedRunShowsExitCodeAndOutput(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)

	openInspector(t, page, jobFailing)
	assert.Contains(t, inspectorText(t, page, ".chips"), "Last run failed · exit 3")
	assert.Contains(t, inspectorText(t, page, ".cmdbox"), "sh -c 'echo fetching; echo boom >&2; exit 3'")
	out := inspectorText(t, page, "#insp-output")
	assert.Contains(t, out, "exit 3")
	assert.Contains(t, out, "fetching")
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "Executed:", "a manual run shows the command it actually executed")
	assert.Contains(t, inspectorText(t, page, ".runs-h"), "latest 50 shown")
}

func TestInspector_SilentFailureShowsExitCode(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobSilent)
	setEnabled(t, id, true)
	runJob(t, id)

	openInspector(t, page, jobSilent)
	out := inspectorText(t, page, "#insp-output")
	assert.Contains(t, out, "No output captured. The run failed with exit code 7.")
	assert.Contains(t, out, "exit 7")
}

func TestInspector_JobWithoutRunsShowsOneEmptyMessage(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	openInspector(t, page, jobTemplated)
	assert.Equal(t, "No runs recorded yet.", inspectorText(t, page, ".runs-none"))
	assert.Empty(t, inspectorText(t, page, "#insp-output"))
}

func TestInspector_OpensByKeyboardWithFocusInside(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	btn := row(page, jobHourly).Locator(".job-open")
	require.NoError(t, btn.Focus())
	_, err := page.ExpectResponse(inspectorRe, func() error { return page.Keyboard().Press("Enter") })
	require.NoError(t, err)
	waitVisible(t, page.Locator("#inspector .insp"))
	require.Eventually(t, func() bool { return activeElementIn(t, page, "#inspector") }, 3*time.Second, 50*time.Millisecond,
		"opening the inspector moves focus into it")
}

func TestInspector_RunAndOverflowButtonsDoNotOpenInspector(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	openRunForm(t, page, jobHourly)
	require.NoError(t, page.Keyboard().Press("Escape"))
	count, err := page.Locator("#inspector .insp").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "clicking Run must not open the inspector")
}

func TestInspector_SelectionSurvivesPollsAndClearsOnNewJob(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	failID := jobID(t, page, jobFailing)
	setEnabled(t, failID, true)
	runJob(t, failID)
	runJob(t, failID)

	openInspector(t, page, jobFailing)
	runs := page.Locator("#inspector .runs .run")
	n, err := runs.Count()
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 2)

	second := runs.Nth(1)
	secondID, err := second.GetAttribute("id")
	require.NoError(t, err)
	clickAndAwait(t, page, second, outputRe)
	require.Eventually(t, func() bool {
		cls, e := page.Locator("#" + secondID).GetAttribute("class")
		return e == nil && strings.Contains(cls, "sel")
	}, 8*time.Second, 100*time.Millisecond, "the picked run becomes selected")

	waitForPoll(t, page)
	waitForPoll(t, page)
	cls, err := page.Locator("#" + secondID).GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, cls, "sel", "run selection survives polls")
	rowCls, err := row(page, jobFailing).GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, rowCls, "sel", "job selection survives polls")
	selectedRun, err := page.Locator("#selected-run").InputValue()
	require.NoError(t, err)
	assert.Equal(t, strings.TrimPrefix(secondID, "exec-"), selectedRun)

	openInspector(t, page, jobHourly)
	require.Eventually(t, func() bool {
		cls, e := row(page, jobFailing).GetAttribute("class")
		return e == nil && !strings.Contains(cls, "sel")
	}, 8*time.Second, 100*time.Millisecond, "choosing another job moves the selection")
	selectedJob, err := page.Locator("#selected-job").InputValue()
	require.NoError(t, err)
	assert.Equal(t, jobID(t, page, jobHourly), selectedJob)
}

func TestInspector_CloseWorksWithServerUnreachable(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)

	openInspector(t, page, jobHourly)
	require.NoError(t, page.Route("**/api/**", func(r playwright.Route) { _ = r.Abort() }))
	require.NoError(t, page.Locator("#inspector .x").Click())
	waitHidden(t, page.Locator("#inspector .insp"))
	selected, err := page.Locator("#selected-job").InputValue()
	require.NoError(t, err)
	assert.Empty(t, selected)
	assert.True(t, focusOnRow(t, page, id), "closing returns focus to the job's row")
	require.NoError(t, page.Unroute("**/api/**"))
}

func TestInspector_DockedOnWideScreens(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)

	insp := openInspector(t, page, jobHourly)
	box, err := insp.BoundingBox()
	require.NoError(t, err)
	tbl, err := page.Locator("table.jobs").BoundingBox()
	require.NoError(t, err)
	assert.LessOrEqual(t, tbl.X+tbl.Width, box.X+1, "the table sits beside the docked inspector")
	visible, err := page.Locator(".scrim").IsVisible()
	require.NoError(t, err)
	assert.False(t, visible)
}

func TestInspector_OverlayAndFullScreenLayouts(t *testing.T) {
	tbl := []struct {
		w, h       int
		fullScreen bool
	}{
		{820, 1180, false}, {820, 460, false}, {390, 844, true}, {390, 460, true},
	}
	for _, tt := range tbl {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			page := newPageSized(t, tt.w, tt.h)
			navigateToDashboard(t, page)
			id := jobID(t, page, jobFailing)
			setEnabled(t, id, true)
			runJob(t, id)

			insp := openInspector(t, page, jobFailing)
			box, err := insp.BoundingBox()
			require.NoError(t, err)
			assert.InDelta(t, 0, box.Y, 1)
			assert.InDelta(t, float64(tt.h), box.Height, 1, "the inspector takes the screen height")
			if tt.fullScreen {
				assert.InDelta(t, float64(tt.w), box.Width, 1, "full screen on phones")
			} else {
				assert.Less(t, box.Width, float64(tt.w), "an overlay drawer on tablets")
			}

			back := page.Locator("#inspector .back-btn")
			visible, err := back.IsVisible()
			require.NoError(t, err)
			assert.True(t, visible)
			_, err = insp.Evaluate(`el => { el.scrollTop = el.scrollHeight; }`, nil)
			require.NoError(t, err)
			bar, err := page.Locator("#inspector .insp-back").BoundingBox()
			require.NoError(t, err)
			assert.InDelta(t, 0, bar.Y, 1, "the Back bar stays at the top after scrolling")
			b, err := back.BoundingBox()
			require.NoError(t, err)
			assert.GreaterOrEqual(t, b.Y, bar.Y)
			assert.LessOrEqual(t, b.Y+b.Height, bar.Y+bar.Height, "Back sits inside the sticky bar")
			assert.GreaterOrEqual(t, b.Height, 43.5)

			require.Eventually(t, func() bool { return activeElementIn(t, page, "#inspector") }, 3*time.Second, 50*time.Millisecond)
			for i := range 12 {
				require.NoError(t, page.Keyboard().Press("Tab"))
				assert.False(t, activeElementIn(t, page, "#jobs-container"), "tab %d reached a covered table control", i)
				assert.False(t, activeElementIn(t, page, ".top"), "tab %d reached the covered header", i)
			}

			require.NoError(t, back.Click())
			waitHidden(t, page.Locator("#inspector .insp"))
			assert.True(t, noHorizontalScroll(t, page))
			assert.False(t, evalBool(t, page, `() => document.querySelector('#jobs-container').inert`), "closing restores the page")
		})
	}
}

var liveRe = regexp.MustCompile(`/inspector\?.*part=live`)

func focusOnHeading(t *testing.T, page playwright.Page) bool {
	t.Helper()
	return evalBool(t, page, `() => document.activeElement === document.querySelector('#inspector [data-focus]')`)
}

func tableInert(t *testing.T, page playwright.Page) bool {
	t.Helper()
	return evalBool(t, page, `() => document.querySelector('#jobs-container').inert`)
}

func TestInspector_ResizeReconcilesCoveredPage(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	openInspector(t, page, jobHourly)
	require.False(t, tableInert(t, page), "a docked inspector covers nothing")

	require.NoError(t, page.SetViewportSize(820, 1180))
	require.Eventually(t, func() bool { return tableInert(t, page) }, 3*time.Second, 50*time.Millisecond,
		"narrowing turns the inspector into an overlay over the table")
	for i := range 10 {
		require.NoError(t, page.Keyboard().Press("Tab"))
		assert.False(t, activeElementIn(t, page, "#jobs-container"), "tab %d reached a covered table control", i)
	}

	require.NoError(t, page.SetViewportSize(1440, 900))
	require.Eventually(t, func() bool { return !tableInert(t, page) }, 3*time.Second, 50*time.Millisecond,
		"widening docks the inspector and frees the table")
	btn := row(page, jobWeekday).Locator(".job-open")
	require.NoError(t, btn.Focus())
	assert.True(t, activeElementIn(t, page, "#jobs-container"), "the table takes focus again")
}

func TestInspector_RunFromOverlayReturnsFocusToHeading(t *testing.T) {
	page := newPageSized(t, 820, 460)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)
	setEnabled(t, id, true)
	openInspector(t, page, jobHourly)

	clickAndAwait(t, page, page.Locator("#insp-run"), runFormRe)
	waitVisible(t, page.Locator("dialog.run-dialog[open]"))
	_, err := page.ExpectResponse(liveRe, func() error { return nil }, playwright.PageExpectResponseOptions{Timeout: new(8000.0)})
	require.NoError(t, err)
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("dialog.run-dialog"))
	require.Eventually(t, func() bool { return focusOnHeading(t, page) }, 3*time.Second, 50*time.Millisecond,
		"Esc returns focus to the inspector, not to the covered row or the page body")

	clickAndAwait(t, page, page.Locator("#insp-run"), runFormRe)
	dlg := page.Locator("dialog.run-dialog[open]")
	waitVisible(t, dlg)
	_, err = page.ExpectResponse(`**/run`, func() error { return dlg.Locator("button[form=run-form]").Click() })
	require.NoError(t, err)
	waitHidden(t, page.Locator("dialog.run-dialog"))
	require.Eventually(t, func() bool { return focusOnHeading(t, page) }, 3*time.Second, 50*time.Millisecond,
		"an accepted run returns focus to the inspector")
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 10*time.Second, 100*time.Millisecond)
}

func TestInspector_RunFromAlertWithJobFilteredOut(t *testing.T) {
	page := newPageSized(t, 820, 1180)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	clickTab(t, page, "Succeeded")

	alert := page.Locator("#failing-alert .alert")
	waitVisible(t, alert)
	clickAndAwait(t, page, alert.Locator("button.go"), inspectorRe)
	waitVisible(t, page.Locator("#inspector .insp"))
	opened, err := page.Locator("#inspector .insp").GetAttribute("data-job-id")
	require.NoError(t, err)
	count, err := page.Locator(`tr.row[data-job-id="` + opened + `"]`).Count()
	require.NoError(t, err)
	require.Zero(t, count, "the failing job is outside the Succeeded tab")

	clickAndAwait(t, page, page.Locator("#insp-run"), runFormRe)
	waitVisible(t, page.Locator("dialog.run-dialog[open]"))
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("dialog.run-dialog"))
	require.Eventually(t, func() bool { return focusOnHeading(t, page) }, 3*time.Second, 50*time.Millisecond,
		"with no row to return to, focus goes to the inspector")
}

func TestInspector_RemovedJobClosesPanel(t *testing.T) {
	page := newPageSized(t, 820, 1180)
	navigateToDashboard(t, page)
	openInspector(t, page, jobHourly)
	require.True(t, tableInert(t, page))

	gone := baseURL + "/api/jobs/gone/inspector?part=live"
	require.NoError(t, page.Route(liveRe, func(r playwright.Route) {
		_ = r.Continue(playwright.RouteContinueOptions{URL: &gone})
	}))
	require.NoError(t, page.Locator("#inspector .insp").WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateDetached, Timeout: new(8000.0)}))
	require.NoError(t, page.Unroute(liveRe))

	count, err := page.Locator("#inspector").Count()
	require.NoError(t, err)
	assert.Equal(t, 1, count, "the inspector slot survives")
	assert.False(t, tableInert(t, page), "the page is usable again")
	selected, err := page.Locator("#selected-job").InputValue()
	require.NoError(t, err)
	assert.Empty(t, selected)
	require.Eventually(t, func() bool { return activeElementIn(t, page, "#search-box") }, 3*time.Second, 50*time.Millisecond,
		"focus lands on search, since the job's row is gone")

	openInspector(t, page, jobWeekday)
}
