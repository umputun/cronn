//go:build e2e

package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var liveOutRe = regexp.MustCompile(`/api/jobs/[^/]+/live-output\?`)

func pageText(t *testing.T, page playwright.Page, sel string) string {
	t.Helper()
	text, err := page.Locator(sel).First().TextContent()
	require.NoError(t, err)
	return strings.Join(strings.Fields(text), " ")
}

func startLive(t *testing.T, page playwright.Page) string {
	t.Helper()
	id := jobID(t, page, jobLive)
	setEnabled(t, id, true)
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 40*time.Second, 200*time.Millisecond)
	startJob(t, id)
	return id
}

func liveText(t *testing.T, page playwright.Page) string {
	t.Helper()
	text, err := page.Locator("#live-out .log").TextContent()
	require.NoError(t, err)
	return text
}

func selectedRun(page playwright.Page) string {
	input := page.Locator("#joutput input[name=selected-run]")
	if count, err := input.Count(); err != nil || count == 0 {
		return ""
	}
	val, err := input.InputValue()
	if err != nil {
		return ""
	}
	return val
}

func delayRoute(t *testing.T, page playwright.Page, re *regexp.Regexp, d time.Duration) {
	t.Helper()
	require.NoError(t, page.Route(re, func(r playwright.Route) {
		go func() {
			time.Sleep(d)
			_ = r.Continue()
		}()
	}))
	t.Cleanup(func() { _ = page.Unroute(re) })
}

func waitForFacts(t *testing.T, page playwright.Page) {
	t.Helper()
	_, err := page.ExpectResponse(factsRe, func() error { return nil }, playwright.PageExpectResponseOptions{Timeout: new(8000.0)})
	require.NoError(t, err)
}

func TestJobPage_OpensFromRowWithoutConsoleErrors(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	errs := consoleErrors(page)
	navigateToDashboard(t, page)
	id := openJob(t, page, jobHourly)

	title, err := page.Title()
	require.NoError(t, err)
	assert.Equal(t, jobHourly+" · cronn", title)
	assert.Equal(t, jobHourly, pageText(t, page, "#job-heading"))
	val, err := page.Locator("#cmdin").InputValue()
	require.NoError(t, err)
	assert.Equal(t, `echo "job2: hourly"`, val)
	assert.Contains(t, pageText(t, page, "#facts"), "Every hour")
	waitForFacts(t, page)
	assert.Empty(t, *errs)
	got, err := page.Locator("#jp").GetAttribute("data-job-id")
	require.NoError(t, err)
	assert.Equal(t, id, got)
}

func TestJobPage_OpensByKeyboard(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)

	require.NoError(t, row(page, jobHourly).Locator(".nm").Focus())
	require.NoError(t, page.Keyboard().Press("Enter"))
	waitJobPage(t, page, id)
}

func TestJobPage_UnknownJobShowsNotFound(t *testing.T) {
	page := newPage(t)
	resp, err := page.Goto(baseURL + "/jobs/no-such-job")
	require.NoError(t, err)
	assert.Equal(t, 404, resp.Status())
	assert.Contains(t, pageText(t, page, ".missing"), "Job not found")
	require.NoError(t, page.Locator("#back-link").Click())
	waitForJobsLoaded(t, page)
}

func TestJobPage_FailedRunShowsExitCodeAndOutput(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)

	gotoJob(t, page, id, "")
	assert.Equal(t, "Failing", pageText(t, page, "#job-badge"))
	assert.Contains(t, pageText(t, page, "#fact-last"), "exit 3")
	out := pageText(t, page, "#joutput")
	assert.Contains(t, out, "exit 3")
	assert.Contains(t, out, "Executed: sh -c 'echo fetching; echo boom >&2; exit 3'", "a manual run shows the command it executed")
	lines, err := page.Locator("#joutput .log div").AllTextContents()
	require.NoError(t, err)
	assert.Equal(t, []string{"fetching", "boom"}, lines)
	assert.Contains(t, pageText(t, page, "#jruns h5"), "latest 50")
	assert.Contains(t, pageText(t, page, "#jruns .jr.sel"), "exit 3")
}

func TestJobPage_SilentFailureShowsExitCode(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobSilent)
	setEnabled(t, id, true)
	runJob(t, id)

	gotoJob(t, page, id, "")
	out := pageText(t, page, "#joutput")
	assert.Contains(t, out, "No output captured. The run failed with exit code 7.")
	assert.Contains(t, out, "exit 7")
}

func TestJobPage_JobWithoutRunsShowsEmptyMessages(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobTemplated)

	gotoJob(t, page, id, "")
	assert.Equal(t, "No runs yet.", pageText(t, page, "#jruns .noruns"))
	assert.Equal(t, "No runs yet.", pageText(t, page, "#joutput .empty-out"))
	assert.Equal(t, "Never ran", pageText(t, page, "#job-badge"))
	visible, err := page.Locator("#datein").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "a templated command gets a date field")
}

func TestJobPage_IndexBarOpensThatRun(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	runJob(t, id)
	waitForPoll(t, page)

	bars := row(page, jobFailing).Locator(".strip a")
	n, err := bars.Count()
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 2)
	href, err := bars.Nth(n - 2).GetAttribute("href")
	require.NoError(t, err)
	run := regexp.MustCompile(`run=(\d+)`).FindStringSubmatch(href)
	require.Len(t, run, 2, href)

	require.NoError(t, bars.Nth(n-2).Click())
	waitJobPage(t, page, id)
	assert.Equal(t, run[1], selectedRun(page), "the page opens the run of the clicked bar")
	cls, err := page.Locator(`#jruns .jr[data-run="` + run[1] + `"]`).GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, cls, "sel")
}

func TestJobPage_BarSelectionSurvivesPollAndKeepsEditedCommand(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	runJob(t, id)
	gotoJob(t, page, id, "")

	require.NoError(t, page.Locator("#cmdin").Fill("sh -c 'echo edited'"))
	bars := page.Locator("#facts .strip a")
	n, err := bars.Count()
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 2)
	older, err := bars.Nth(n - 2).GetAttribute("data-run")
	require.NoError(t, err)
	clickAndAwait(t, page, bars.Nth(n-2), outputRe)
	require.Eventually(t, func() bool { return selectedRun(page) == older }, 5*time.Second, 50*time.Millisecond)
	selected := page.Locator(`#facts .strip a.sel[data-run="` + older + `"]`)
	waitVisible(t, selected)
	waitVisible(t, page.Locator(`#jruns .jr.sel[data-run="`+older+`"]`))

	waitForFacts(t, page)
	waitForFacts(t, page)
	waitVisible(t, selected)
	assert.Equal(t, older, selectedRun(page), "the selected run survives polls")
	val, err := page.Locator("#cmdin").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "sh -c 'echo edited'", val, "a poll keeps the edited command")
	assert.Equal(t, "Run edited", pageText(t, page, "#run-now .lbl"), "the re-rendered button keeps the edited label")
}

func TestJobPage_RunClickDuringRunsReloadShowsThatRun(t *testing.T) {
	// the runs list reload replaced the clicked row in flight, and htmx dropped the row's output
	page := newPageSized(t, 1440, 900)
	var thrown []string
	page.OnPageError(func(e error) { thrown = append(thrown, e.Error()) })
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	runJob(t, id)
	gotoJob(t, page, id, "")

	rows := page.Locator("#jruns .jr")
	newer, err := rows.Nth(0).GetAttribute("data-run")
	require.NoError(t, err)
	older, err := rows.Nth(1).GetAttribute("data-run")
	require.NoError(t, err)
	delayRoute(t, page, regexp.MustCompile(`/api/jobs/[^/]+/runs\?`), 300*time.Millisecond)
	clickAndAwait(t, page, rows.Nth(1), outputRe)
	require.Eventually(t, func() bool { return selectedRun(page) == older }, 5*time.Second, 50*time.Millisecond)

	delayRoute(t, page, outputRe, 1500*time.Millisecond)
	require.NoError(t, rows.Nth(0).Click())
	require.Eventually(t, func() bool { return selectedRun(page) == newer }, 5*time.Second, 50*time.Millisecond)
	waitVisible(t, page.Locator(`#jruns .jr.sel[data-run="`+newer+`"]`))
	assert.Empty(t, thrown, "htmx threw on the answer to a replaced row")
}

func TestJobPage_HeldBarClickActsAfterPoll(t *testing.T) {
	// a poll that replaced the bar between press and release swallowed the click
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	runJob(t, id)
	gotoJob(t, page, id, "")

	bars := page.Locator("#facts .strip a")
	n, err := bars.Count()
	require.NoError(t, err)
	bar := bars.Nth(n - 2)
	run, err := bar.GetAttribute("data-run")
	require.NoError(t, err)
	_, err = bar.Evaluate(`el => el.dataset.held = '1'`, nil)
	require.NoError(t, err)
	require.NoError(t, bar.Hover())
	require.NoError(t, page.Mouse().Down())
	assert.Never(t, func() bool {
		count, e := page.Locator("#facts [data-held]").Count()
		return e != nil || count == 0
	}, 6*time.Second, 200*time.Millisecond, "no poll replaces the bar while it is pressed")
	_, err = page.ExpectResponse(outputRe, func() error { return page.Mouse().Up() })
	require.NoError(t, err)
	require.Eventually(t, func() bool { return selectedRun(page) == run }, 5*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		count, e := page.Locator("#facts [data-held]").Count()
		return e == nil && count == 0
	}, 8*time.Second, 100*time.Millisecond, "polling resumes after the release")
}

func TestJobPage_HeldRunAfterTypingActsAfterPoll(t *testing.T) {
	// leaving the command field cleared the press hold, so a poll replaced Run now under the pressed mouse
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)
	setEnabled(t, id, true)
	gotoJob(t, page, id, "")

	require.NoError(t, page.Locator("#cmdin").Click())
	btn := page.Locator("#run-now")
	_, err := btn.Evaluate(`el => el.dataset.held = '1'`, nil)
	require.NoError(t, err)
	require.NoError(t, btn.Hover())
	require.NoError(t, page.Mouse().Down())
	assert.Never(t, func() bool {
		count, e := page.Locator("#runact [data-held]").Count()
		return e != nil || count == 0
	}, 6*time.Second, 200*time.Millisecond, "no poll replaces Run now while it is pressed")
	_, err = page.ExpectResponse(runConfirmRe, func() error { return page.Mouse().Up() })
	require.NoError(t, err)
	waitVisible(t, page.Locator("#run-dialog[open]"))
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("#run-dialog"))
}

func TestJobPage_MultiLineCommandKeepsItsLines(t *testing.T) {
	// a single-line input dropped the newlines, so Run now ran the lines glued together
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobTwoStep)
	gotoJob(t, page, id, "")

	val, err := page.Locator("#cmdin").InputValue()
	require.NoError(t, err)
	assert.Contains(t, val, "echo step one\necho step two")
	waitForFacts(t, page)
	assert.Equal(t, "Run now", pageText(t, page, "#run-now .lbl"), "a poll does not mark the command edited")

	openConfirm(t, page, page.Locator("#run-now"))
	shown, err := page.Locator("#cf-cmd").InnerText()
	require.NoError(t, err)
	assert.Contains(t, shown, "echo step one\necho step two", "the confirmation shows the lines apart")
	count, err := page.Locator("#cf-edited").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "an untouched multi-line command is not an edit")
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("#run-dialog"))
}

func TestJobPage_TitleFollowsJobStatus(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobSlow)
	setEnabled(t, id, true)
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 10*time.Second, 100*time.Millisecond)
	gotoJob(t, page, id, "")

	badge := func(want string) func() bool {
		return func() bool {
			txt, err := page.Locator("#job-badge").TextContent()
			return err == nil && strings.TrimSpace(txt) == want
		}
	}
	require.Never(t, badge("Running"), 300*time.Millisecond, 50*time.Millisecond)
	startJob(t, id)
	_, err := page.Evaluate(`() => htmx.trigger(document.body, 'refresh-jobs')`)
	require.NoError(t, err)
	require.Eventually(t, badge("Running"), 3*time.Second, 100*time.Millisecond, "the title shows the run in progress")
	waitVisible(t, page.Locator("#jtitle .dot.running"))
	require.Eventually(t, badge("Healthy"), 15*time.Second, 100*time.Millisecond, "the title shows the finished run")
	count, err := page.Locator("#job-badge").Count()
	require.NoError(t, err)
	assert.Equal(t, 1, count, "the out-of-band title replaces the old one")
}

func TestJobPage_WrapToggleSurvivesRunChange(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	runJob(t, id)
	gotoJob(t, page, id, "")

	fits := `() => { const l = document.querySelector('#joutput .log'); return l.scrollWidth <= l.clientWidth }`
	_, err := page.Locator("#joutput .log div").First().Evaluate(`el => { el.textContent = 'long-log-line'.repeat(80) }`, nil)
	require.NoError(t, err)
	assert.True(t, evalBool(t, page, fits), "wrapped long lines fit the output width")
	require.NoError(t, page.Locator(".wraptg").Click())
	assert.False(t, evalBool(t, page, fits), "unwrapped long lines scroll sideways")

	clickAndAwait(t, page, page.Locator("#jruns .jr").Nth(1), outputRe)
	checked, err := page.Locator("#wrap").IsChecked()
	require.NoError(t, err)
	assert.False(t, checked, "another run keeps the wrap setting")
	assert.True(t, evalBool(t, page, `() => getComputedStyle(document.querySelector('#joutput .log div')).whiteSpace === 'pre'`))
	require.NoError(t, page.Locator(".wraptg").Click())
}

func TestJobPage_CopyCommandShowsToast(t *testing.T) {
	page := newPage(t)
	require.NoError(t, page.Context().GrantPermissions([]string{"clipboard-read", "clipboard-write"}))
	navigateToDashboard(t, page)
	gotoJob(t, page, jobID(t, page, jobHourly), "")

	require.NoError(t, page.Locator("#copy-cmd").Click())
	waitVisible(t, page.Locator("#toasts .t", playwright.PageLocatorOptions{HasText: "Command copied"}))
	clip, err := page.Evaluate(`() => navigator.clipboard.readText()`)
	require.NoError(t, err)
	assert.Equal(t, `echo "job2: hourly"`, clip)
}

func TestJobPage_PreviousNextWalk(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	ids := rowIDs(t, page)
	require.Len(t, ids, totalJobs)
	gotoJob(t, page, ids[0], "?sort=default&rev=0&filter=all")

	assert.Equal(t, "1 of "+strconv.Itoa(totalJobs), pageText(t, page, "#jpos"))
	count, err := page.Locator("#prev-job").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "the first job has no previous")
	require.NoError(t, page.Locator("#next-job").Click())
	waitJobPage(t, page, ids[1])
	assert.Equal(t, "2 of "+strconv.Itoa(totalJobs), pageText(t, page, "#jpos"))
	require.NoError(t, page.Locator("#prev-job").Click())
	waitJobPage(t, page, ids[0])

	gotoJob(t, page, ids[totalJobs-1], "?sort=default&rev=0&filter=all")
	count, err = page.Locator("#next-job").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "the last job has no next")

	gotoJob(t, page, ids[0], "?sort=default&rev=1&filter=all")
	assert.Equal(t, strconv.Itoa(totalJobs)+" of "+strconv.Itoa(totalJobs), pageText(t, page, "#jpos"), "the walk follows the reversed list")
}

func TestJobPage_RemovedJobShowsNotice(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	gotoJob(t, page, jobID(t, page, jobHourly), "")

	gone := baseURL + "/api/jobs/gone/facts"
	require.NoError(t, page.Route(factsRe, func(r playwright.Route) {
		_ = r.Continue(playwright.RouteContinueOptions{URL: &gone})
	}))
	t.Cleanup(func() { _ = page.Unroute(factsRe) })
	notice := page.Locator("#facts .removed")
	require.NoError(t, notice.WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible, Timeout: new(8000.0)}))
	assert.Contains(t, pageText(t, page, "#facts .removed"), "This job was removed from the crontab.")
	assert.Equal(t, "Removed", pageText(t, page, "#job-badge"))
	count, err := page.Locator("#run-now").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a removed job cannot be run")
	_, err = page.ExpectResponse(factsRe, func() error { return nil }, playwright.PageExpectResponseOptions{Timeout: new(7000.0)})
	assert.Error(t, err, "the facts poll stops")
}

func TestJobPage_LiveOutputFollowsRunningJob(t *testing.T) {
	page := newPageSized(t, 820, 900)
	navigateToDashboard(t, page)
	id := startLive(t, page)

	gotoJob(t, page, id, "")
	waitVisible(t, page.Locator("#live-out .log"))
	assert.True(t, strings.HasPrefix(selectedRun(page), "live:"), "a running job opens on its run in progress")
	waitVisible(t, page.Locator("#jruns .jr.sel .x.live"))
	assert.NotContains(t, liveText(t, page), "tick 7 line 1")

	require.Eventually(t, func() bool { return strings.Contains(liveText(t, page), "tick 7 line 1") },
		12*time.Second, 200*time.Millisecond, "a poll brings the lines printed since the page opened")
	assert.True(t, jobStatus(t, id).IsRunning, "new lines arrived while the job was still running")
	atEnd := `() => { const l = document.querySelector('#live-out .log');
		return l.scrollHeight > l.clientHeight && l.scrollHeight - l.scrollTop - l.clientHeight < 4; }`
	assert.True(t, evalBool(t, page, atEnd), "a log scrolled to its end keeps following new lines")
	nextPoll := func() {
		before := liveText(t, page)
		require.Eventually(t, func() bool { return liveText(t, page) != before }, 8*time.Second, 200*time.Millisecond)
	}

	require.NoError(t, page.Locator(".wraptg").Click())
	_, err := page.Evaluate(`() => { const l = document.querySelector('#live-out .log'); l.scrollTop = l.scrollHeight; l.scrollLeft = 120; }`)
	require.NoError(t, err)
	nextPoll()
	assert.True(t, evalBool(t, page, atEnd), "the unwrapped log still follows new lines")
	dims, err := page.Evaluate(`() => { const l = document.querySelector('#live-out .log'); return [l.scrollLeft, l.scrollWidth, l.clientWidth] }`)
	require.NoError(t, err)
	assert.True(t, evalBool(t, page, `() => document.querySelector('#live-out .log').scrollLeft === 120`),
		"an unwrapped log at its end keeps its sideways position, got %v", dims)

	_, err = page.Evaluate(`() => { document.querySelector('#live-out .log').scrollTop = 60; }`)
	require.NoError(t, err)
	nextPoll()
	assert.True(t, evalBool(t, page, `() => { const l = document.querySelector('#live-out .log');
		return l.scrollTop === 60 && l.scrollLeft === 120; }`), "a reader scrolled up keeps the place")
	require.NoError(t, page.Locator(".wraptg").Click())

	require.Eventually(t, func() bool {
		count, e := page.Locator("#live-out").Count()
		return e == nil && count == 0
	}, 35*time.Second, 200*time.Millisecond, "the finished run replaces the live output")
	assert.Contains(t, pageText(t, page, "#joutput"), "tick 30 line 6")
	execID := selectedRun(page)
	_, err = strconv.Atoi(execID)
	require.NoError(t, err, "the selection is now the recorded run, got %q", execID)
	require.Eventually(t, func() bool {
		first, e := page.Locator("#jruns .jr").First().GetAttribute("data-run")
		cls, _ := page.Locator("#jruns .jr").First().GetAttribute("class")
		return e == nil && first == execID && strings.Contains(cls, "sel")
	}, 8*time.Second, 100*time.Millisecond, "the recorded run heads the runs list, selected")
	waitVisible(t, page.Locator(`#facts .strip a.sel[data-run="`+execID+`"]`))
}

func TestJobPage_HistorySelectionWinsOverLivePoll(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobLive)
	if jobStatus(t, id).LastRun.IsZero() {
		startLive(t, page)
		require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 40*time.Second, 200*time.Millisecond)
	}
	startLive(t, page)
	gotoJob(t, page, id, "")
	waitVisible(t, page.Locator("#live-out"))
	history := page.Locator(`#jruns .jr:not([data-run^="live:"])`).First()
	historyRun, err := history.GetAttribute("data-run")
	require.NoError(t, err)
	historyShown := func() bool {
		count, e := page.Locator("#live-out").Count()
		return e == nil && count == 0 && selectedRun(page) == historyRun
	}

	delayRoute(t, page, outputRe, 6*time.Second)
	require.NoError(t, history.Click())
	require.Eventually(t, historyShown, 10*time.Second, 100*time.Millisecond, "a slow history response spans a poll tick and still wins")
	waitVisible(t, page.Locator(`#jruns .jr.sel[data-run="`+historyRun+`"]`))
	require.NoError(t, page.Unroute(outputRe))

	require.NoError(t, page.Locator(`#jruns .jr[data-run^="live:"]`).First().Click())
	waitVisible(t, page.Locator("#live-out"))
	delayRoute(t, page, liveOutRe, 4*time.Second)
	_, err = page.ExpectRequest(liveOutRe, func() error { return nil }, playwright.PageExpectRequestOptions{Timeout: new(8000.0)})
	require.NoError(t, err)
	require.NoError(t, page.Locator(`#jruns .jr[data-run="`+historyRun+`"]`).Click())
	require.Eventually(t, historyShown, 4*time.Second, 100*time.Millisecond)
	require.Never(t, func() bool { return !historyShown() }, 5*time.Second, 200*time.Millisecond,
		"the live poll that was in flight does not bring the live output back")
	require.NoError(t, page.Unroute(liveOutRe))
	stopRunFromBar(t, page)
}

func TestJobPage_RunsBesideOutputOnWideScreens(t *testing.T) {
	page := newPageSized(t, 1440, 460)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	gotoJob(t, page, id, "")

	runs, err := page.Locator("#jruns").BoundingBox()
	require.NoError(t, err)
	out, err := page.Locator("#joutput").BoundingBox()
	require.NoError(t, err)
	assert.LessOrEqual(t, runs.X+runs.Width, out.X+1, "the runs list sits beside the output")
	assert.Greater(t, out.Width, 900.0)
	assert.Greater(t, out.Y+out.Height, 400.0, "the output fills the short viewport")

	require.NoError(t, page.SetViewportSize(390, 844))
	runs, err = page.Locator("#jruns").BoundingBox()
	require.NoError(t, err)
	out, err = page.Locator("#joutput").BoundingBox()
	require.NoError(t, err)
	assert.LessOrEqual(t, runs.Y+runs.Height, out.Y+1, "a phone stacks the runs above the output")
	assert.True(t, noHorizontalScroll(t, page))
}
