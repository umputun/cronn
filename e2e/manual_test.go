//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openRunForm(t *testing.T, page playwright.Page, name string) playwright.Locator {
	t.Helper()
	clickAndAwait(t, page, row(page, name).Locator(".run-btn"), runFormRe)
	dlg := page.Locator("dialog.run-dialog[open]")
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

func waitForPoll(t *testing.T, page playwright.Page) {
	t.Helper()
	_, err := page.ExpectResponse(jobsRe, func() error { return nil }, playwright.PageExpectResponseOptions{Timeout: new(8000.0)})
	require.NoError(t, err)
}

func focusOnRow(t *testing.T, page playwright.Page, id string) bool {
	t.Helper()
	return evalBool(t, page, `(id) => document.activeElement === document.querySelector('[data-job-id="' + id + '"] .job-open')`, id)
}

func TestManualRun_DialogOpensWithCommand(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	dlg := openRunForm(t, page, jobHourly)
	title, err := dlg.Locator("h4").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "Run “"+jobHourly+"” now", title)
	cmd, err := dlg.Locator("#rf-cmd").InputValue()
	require.NoError(t, err)
	assert.Equal(t, `echo "job2: hourly"`, cmd)
	count, err := dlg.Locator("#rf-date").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a command without templates has no date field")
	assert.True(t, evalBool(t, page, `() => document.querySelector('dialog.run-dialog').matches(':modal')`))
	assert.True(t, activeElementIn(t, page, "dialog.run-dialog"), "focus should move into the dialog")
}

func TestManualRun_TemplatedCommandHasDateField(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	dlg := openRunForm(t, page, jobTemplated)
	visible, err := dlg.Locator("#rf-date").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible)
	hint, err := dlg.Locator(".hint").TextContent()
	require.NoError(t, err)
	assert.Contains(t, hint, "a supplied date is used at 00:00")
}

func TestManualRun_CancelAndEscCloseWithoutRequestAndRestoreFocus(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)

	var posts atomic.Int32
	page.OnRequest(func(r playwright.Request) {
		if r.Method() == http.MethodPost && strings.Contains(r.URL(), "/run") {
			posts.Add(1)
		}
	})

	openRunForm(t, page, jobHourly)
	waitForPoll(t, page)
	require.NoError(t, page.Locator("dialog.run-dialog .rf-f form[method=dialog] button").Click())
	waitHidden(t, page.Locator("dialog.run-dialog"))
	require.Eventually(t, func() bool { return focusOnRow(t, page, id) }, 3*time.Second, 50*time.Millisecond,
		"focus returns to the re-rendered row after Cancel")

	openRunForm(t, page, jobHourly)
	waitForPoll(t, page)
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("dialog.run-dialog"))
	require.Eventually(t, func() bool { return focusOnRow(t, page, id) }, 3*time.Second, 50*time.Millisecond,
		"focus returns to the re-rendered row after Esc")

	assert.Zero(t, posts.Load(), "closing the dialog sends no run request")
}

func TestManualRun_BackdropClickKeepsDialogOpen(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	openRunForm(t, page, jobHourly)
	require.NoError(t, page.Mouse().Click(5, 5))
	assert.Never(t, func() bool {
		visible, err := page.Locator("dialog.run-dialog[open]").IsVisible()
		return err != nil || !visible
	}, 500*time.Millisecond, 50*time.Millisecond, "a stray click outside must not discard edits")
	require.NoError(t, page.Keyboard().Press("Escape"))
}

func TestManualRun_AcceptedClosesDialogShowsToastRestoresFocus(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobHourly)
	setEnabled(t, id, true)

	dlg := openRunForm(t, page, jobHourly)
	_, err := page.ExpectResponse(`**/run`, func() error { return dlg.Locator("button[form=run-form]").Click() })
	require.NoError(t, err)
	waitHidden(t, page.Locator("dialog.run-dialog"))

	toast := page.Locator("#toasts .t")
	waitVisible(t, toast)
	text, err := toast.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Run request accepted")
	assert.Contains(t, text, jobHourly)
	require.Eventually(t, func() bool { return focusOnRow(t, page, id) }, 3*time.Second, 50*time.Millisecond,
		"focus returns to the row after an accepted run empties the dialog slot")
	require.Eventually(t, func() bool { return jobStatus(t, id).LastStatus == "success" }, 10*time.Second, 100*time.Millisecond)
}

func TestManualRun_RejectionKeepsFormAndEdits(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobSlow)
	setEnabled(t, id, true)
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 10*time.Second, 100*time.Millisecond)

	dlg := openRunForm(t, page, jobSlow)
	require.NoError(t, dlg.Locator("#rf-cmd").Fill("sleep 1"))
	startJob(t, id)

	_, err := page.ExpectResponse(`**/run`, func() error { return dlg.Locator("button[form=run-form]").Click() })
	require.NoError(t, err)
	errBox := page.Locator("dialog.run-dialog[open] .dlg-err:not(.net-error)")
	waitVisible(t, errBox)
	text, err := errBox.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Not started: Job already running")
	cmd, err := page.Locator("dialog.run-dialog[open] #rf-cmd").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "sleep 1", cmd, "the edited command survives the rejection")
	count, err := page.Locator("#toasts .t").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a rejection shows no acceptance toast")

	require.NoError(t, page.Keyboard().Press("Escape"))
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 10*time.Second, 100*time.Millisecond)
}

func TestManualRun_NetworkFailureShowsMessageAndKeepsEdits(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	dlg := openRunForm(t, page, jobHourly)
	require.NoError(t, dlg.Locator("#rf-cmd").Fill(`echo "edited"`))
	require.NoError(t, page.Route("**/api/jobs/*/run", func(r playwright.Route) { _ = r.Abort() }))
	require.NoError(t, dlg.Locator("button[form=run-form]").Click())

	waitVisible(t, dlg.Locator(".net-error"))
	cmd, err := dlg.Locator("#rf-cmd").InputValue()
	require.NoError(t, err)
	assert.Equal(t, `echo "edited"`, cmd)
	require.NoError(t, page.Unroute("**/api/jobs/*/run"))
	require.NoError(t, page.Keyboard().Press("Escape"))
}

func TestManualRun_BottomSheetOnPhone(t *testing.T) {
	for _, size := range []struct{ w, h int }{{390, 844}, {390, 460}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			page := newPageSized(t, size.w, size.h)
			navigateToDashboard(t, page)

			dlg := openRunForm(t, page, jobTemplated)
			box, err := dlg.BoundingBox()
			require.NoError(t, err)
			assert.InDelta(t, float64(size.h), box.Y+box.Height, 1, "the sheet sits on the bottom edge")
			assert.InDelta(t, float64(size.w), box.Width, 1, "the sheet spans the width")

			for _, sel := range []string{".rf-f form[method=dialog] button", "button[form=run-form]"} {
				b, err := dlg.Locator(sel).BoundingBox()
				require.NoError(t, err)
				assert.GreaterOrEqual(t, b.Y, 0.0)
				assert.LessOrEqual(t, b.Y+b.Height, float64(size.h), "%s must stay on screen", sel)
				assert.GreaterOrEqual(t, b.Height, 43.5, "%s must be a 44px target", sel)
			}
			require.NoError(t, page.Keyboard().Press("Escape"))
		})
	}
}
