//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var runPostRe = regexp.MustCompile(`/api/jobs/[^/]+/run$`)

// openConfirm clicks a Run control and returns the confirmation dialog it opens
func openConfirm(t *testing.T, page playwright.Page, btn playwright.Locator) playwright.Locator {
	t.Helper()
	clickAndAwait(t, page, btn, runConfirmRe)
	dlg := page.Locator("#run-dialog[open]")
	waitVisible(t, dlg)
	return dlg
}

func startJob(t *testing.T, id string) {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/jobs/"+id+"/run", "application/x-www-form-urlencoded", http.NoBody) //nolint:noctx // test helper
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Eventually(t, func() bool { return jobStatus(t, id).IsRunning }, 5*time.Second, 50*time.Millisecond)
}

func focusOn(t *testing.T, page playwright.Page, id string) bool {
	t.Helper()
	return evalBool(t, page, `(id) => document.activeElement === document.getElementById(id)`, id)
}

func focusRingShown(t *testing.T, page playwright.Page) bool {
	t.Helper()
	return evalBool(t, page, `() => getComputedStyle(document.activeElement).outlineStyle !== 'none'`)
}

func countRunPosts(page playwright.Page) *atomic.Int32 {
	posts := &atomic.Int32{}
	page.OnRequest(func(r playwright.Request) {
		if r.Method() == http.MethodPost && runPostRe.MatchString(r.URL()) {
			posts.Add(1)
		}
	})
	return posts
}

func TestManualRun_ConfirmShowsTheCommand(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)

	dlg := openConfirm(t, page, page.Locator("#run-"+id))
	assert.Equal(t, "Run “"+jobHourly+"” now?", pageText(t, page, "#run-dialog h4"))
	assert.Equal(t, `echo "job2: hourly"`, pageText(t, page, "#cf-cmd"))
	count, err := dlg.Locator("#cf-edited, #cf-date").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a plain run has no edit or date note")
	assert.True(t, evalBool(t, page, `() => document.querySelector('#run-dialog').matches(':modal')`))
	assert.True(t, focusOn(t, page, "cf-run"), "focus moves to Run once")
	assert.True(t, strings.HasSuffix(page.URL(), "/"), "a Run click does not open the job page")
	require.NoError(t, page.Keyboard().Press("Escape"))
}

func TestManualRun_CancelAndEscStartNothing(t *testing.T) {
	page := newPage(t)
	posts := countRunPosts(page)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)

	openConfirm(t, page, page.Locator("#run-"+id))
	waitForPoll(t, page)
	require.NoError(t, page.Locator("#cf-cancel").Click())
	waitHidden(t, page.Locator("#run-dialog"))
	require.Eventually(t, func() bool { return focusOn(t, page, "run-"+id) }, 3*time.Second, 50*time.Millisecond,
		"focus returns to the re-rendered row button after Cancel")

	openConfirm(t, page, page.Locator("#run-"+id))
	waitForPoll(t, page)
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("#run-dialog"))
	require.Eventually(t, func() bool { return focusOn(t, page, "run-"+id) }, 3*time.Second, 50*time.Millisecond,
		"focus returns to the row button after Esc")

	gotoJob(t, page, id, "")
	openConfirm(t, page, page.Locator("#run-now"))
	waitForFacts(t, page)
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("#run-dialog"))
	require.Eventually(t, func() bool { return focusOn(t, page, "run-now") }, 3*time.Second, 50*time.Millisecond,
		"focus returns to Run now after a poll re-rendered it")

	assert.Zero(t, posts.Load(), "closing the dialog starts nothing")
}

func TestManualRun_EscAfterMouseLeavesNoFocusRing(t *testing.T) {
	// a mouse user who closed the run dialog with Esc got the keyboard focus ring on the row
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)

	openConfirm(t, page, page.Locator("#run-"+id))
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("#run-dialog"))
	require.True(t, focusOn(t, page, "run-"+id))
	assert.False(t, focusRingShown(t, page), "Esc after a mouse open shows no ring")
	_, err := page.Evaluate(`() => document.activeElement.setAttribute('data-before-poll', '')`)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return focusOn(t, page, "run-"+id) && evalBool(t, page, `() => !document.activeElement.hasAttribute('data-before-poll')`)
	}, 8*time.Second, 50*time.Millisecond, "a poll replaces the row and focus moves to the new button")
	assert.False(t, focusRingShown(t, page), "the re-rendered button shows no ring either")
}

func TestManualRun_EscReturnsFocusToAnUnfocusedOpener(t *testing.T) {
	// safari does not focus a clicked button, so focus fell back to the jump field and opened its list
	page := newPage(t)
	navigateToDashboard(t, page)
	gotoJob(t, page, jobID(t, page, jobHourly), "")
	blurFocus(t, page)

	clicked := make(chan error, 1)
	_, err := page.ExpectResponse(runConfirmRe, func() error {
		_, e := page.Evaluate(`() => document.getElementById('run-now').click()`)
		clicked <- e
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, <-clicked)
	waitVisible(t, page.Locator("#run-dialog[open]"))
	require.False(t, focusOn(t, page, "run-now"))
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("#run-dialog"))
	require.Eventually(t, func() bool { return focusOn(t, page, "run-now") }, 3*time.Second, 50*time.Millisecond)
	visible, err := page.Locator("#jdrop").IsVisible()
	require.NoError(t, err)
	assert.False(t, visible, "the jump list stays closed")
}

func TestManualRun_BackdropClickKeepsDialogOpen(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	openConfirm(t, page, page.Locator("#run-"+jobID(t, page, jobHourly)))
	require.NoError(t, page.Mouse().Click(5, 5))
	assert.Never(t, func() bool {
		visible, err := page.Locator("#run-dialog[open]").IsVisible()
		return err != nil || !visible
	}, 500*time.Millisecond, 50*time.Millisecond, "a stray click outside does not dismiss the confirmation")
	require.NoError(t, page.Keyboard().Press("Escape"))
}

func TestManualRun_FromRowLandsOnLiveOutputAndBackToTheList(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobLive)
	setEnabled(t, id, true)
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 40*time.Second, 200*time.Millisecond)
	search(t, page, "tail")
	clickSort(t, page, "lastrun", "down")

	openConfirm(t, page, page.Locator("#run-"+id))
	require.NoError(t, page.Locator("#cf-run").Click())
	require.NoError(t, page.WaitForURL(regexp.MustCompile(`/jobs/`+id+`\?after=`)))
	waitJobPage(t, page, id)
	waitVisible(t, page.Locator("#live-out .log"))
	assert.True(t, strings.HasPrefix(selectedRun(page), "live:"), "the page lands on the started run")
	require.Eventually(t, func() bool { return !strings.Contains(page.URL(), "after=") }, 3*time.Second, 50*time.Millisecond,
		"the address drops the wait once the run is found, so a reload shows the job")
	assert.Contains(t, page.URL(), "search=tail")
	assert.Contains(t, pageText(t, page, "#joutput .obar"), "manual")

	stopRunFromBar(t, page)
	require.NoError(t, page.Locator("#back-link").Click())
	waitForJobsLoaded(t, page)
	value, err := page.Locator("#search").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "tail", value)
	assert.Equal(t, []string{jobLive}, rowNames(t, page), "All jobs returns to the filtered grid")
	waitVisible(t, page.Locator(`#sort-lastrun.on[data-dir="down"]`))
}

func TestManualRun_EditedCommandAndDateLandOnRecordedRun(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobDated)
	setEnabled(t, id, true)
	gotoJob(t, page, id, "")

	require.NoError(t, page.Locator("#cmdin").Fill("echo dated {{.YYYYMMDD}} edited"))
	require.NoError(t, page.Locator("#datein").Fill("20260102"))
	assert.Equal(t, "Run edited", pageText(t, page, "#run-now .lbl"))
	openConfirm(t, page, page.Locator("#run-now"))
	assert.Equal(t, "echo dated {{.YYYYMMDD}} edited", pageText(t, page, "#cf-cmd"))
	waitVisible(t, page.Locator("#cf-edited"))
	assert.Equal(t, "20260102", pageText(t, page, "#cf-date"))

	require.NoError(t, page.Locator("#cf-run").Click())
	require.Eventually(t, func() bool {
		lines, err := page.Locator("#joutput .log div").AllTextContents()
		return err == nil && len(lines) == 1 && lines[0] == "dated 20260102 edited"
	}, 10*time.Second, 100*time.Millisecond, "a sub-second run lands on its recorded output")
	assert.NotContains(t, page.URL(), "after=")
	assert.Contains(t, pageText(t, page, "#jruns .jr.sel"), "manual")
	val, err := page.Locator("#cmdin").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "echo dated {{.YYYYMMDD}}", val, "the landed page shows the job's own command again")
}

func TestManualRun_RejectionKeepsDialogOpen(t *testing.T) {
	page := newPage(t)
	posts := countRunPosts(page)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobSlow)
	setEnabled(t, id, true)
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 10*time.Second, 100*time.Millisecond)
	gotoJob(t, page, id, "")

	openConfirm(t, page, page.Locator("#run-now"))
	startJob(t, id)
	_, err := page.ExpectResponse(runPostRe, func() error { return page.Locator("#cf-run").Click() })
	require.NoError(t, err)
	errBox := page.Locator("#run-dialog[open] .dlg-err:not(.net-error)")
	waitVisible(t, errBox)
	assert.Contains(t, pageText(t, page, "#run-dialog .dlg-err:not(.net-error)"), "Not started: Job already running")
	assert.Equal(t, int32(1), posts.Load())
	require.NoError(t, page.Keyboard().Press("Escape"))
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 10*time.Second, 100*time.Millisecond)
}

func TestManualRun_NetworkFailureShowsMessage(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	gotoJob(t, page, jobID(t, page, jobHourly), "")

	require.NoError(t, page.Locator("#cmdin").Fill(`echo "edited"`))
	dlg := openConfirm(t, page, page.Locator("#run-now"))
	require.NoError(t, page.Route("**/api/jobs/*/run", func(r playwright.Route) { _ = r.Abort() }))
	t.Cleanup(func() { _ = page.Unroute("**/api/jobs/*/run") })
	require.NoError(t, page.Locator("#cf-run").Click())

	waitVisible(t, dlg.Locator(".net-error"))
	val, err := page.Locator("#cmdin").InputValue()
	require.NoError(t, err)
	assert.Equal(t, `echo "edited"`, val)
	require.NoError(t, page.Keyboard().Press("Escape"))
}

func TestManualRun_DialogFitsPhoneScreens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{390, 844}, {390, 460}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			page := newPageSized(t, size.w, size.h)
			navigateToDashboard(t, page)
			gotoJob(t, page, jobID(t, page, jobTemplated), "")

			dlg := openConfirm(t, page, page.Locator("#run-now"))
			box, err := dlg.BoundingBox()
			require.NoError(t, err)
			assert.GreaterOrEqual(t, box.X, 0.0)
			assert.LessOrEqual(t, box.X+box.Width, float64(size.w), "the dialog fits the width")
			for _, sel := range []string{"#cf-cancel", "#cf-run"} {
				b, err := page.Locator(sel).BoundingBox()
				require.NoError(t, err)
				assert.GreaterOrEqual(t, b.Y, 0.0)
				assert.LessOrEqual(t, b.Y+b.Height, float64(size.h), "%s must stay on screen", sel)
			}
			assert.True(t, noHorizontalScroll(t, page))
			require.NoError(t, page.Keyboard().Press("Escape"))
		})
	}
}
