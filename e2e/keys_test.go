//go:build e2e

package e2e

import (
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func blurFocus(t *testing.T, page playwright.Page) {
	t.Helper()
	_, err := page.Evaluate(`() => document.activeElement && document.activeElement.blur()`)
	require.NoError(t, err)
}

func press(t *testing.T, page playwright.Page, key string) {
	t.Helper()
	require.NoError(t, page.Keyboard().Press(key))
}

func cursorID(t *testing.T, page playwright.Page) string {
	t.Helper()
	res, err := page.Evaluate(`() => { const r = document.querySelectorAll('#rows .it.cur'); return r.length === 1 ? r[0].dataset.jobId : String(r.length) }`)
	require.NoError(t, err)
	return res.(string)
}

func typeJump(t *testing.T, page playwright.Page, text string) {
	t.Helper()
	press(t, page, "/")
	require.True(t, focusOn(t, page, "search"))
	typed := regexp.MustCompile(`/api/jobs/jump\?q=` + regexp.QuoteMeta(url.QueryEscape(text)) + `&`)
	_, err := page.ExpectResponse(typed, func() error { return page.Keyboard().Type(text) })
	require.NoError(t, err)
	waitVisible(t, page.Locator("#jdrop a.ji.cur"))
}

func TestKeys_IndexCursorMovesOpensAndRuns(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	ids := rowIDs(t, page)
	blurFocus(t, page)

	press(t, page, "j")
	assert.Equal(t, ids[0], cursorID(t, page))
	press(t, page, "j")
	assert.Equal(t, ids[1], cursorID(t, page))
	press(t, page, "k")
	assert.Equal(t, ids[0], cursorID(t, page))
	press(t, page, "k")
	assert.Equal(t, ids[0], cursorID(t, page), "the cursor stops at the first row")

	waitForPoll(t, page)
	assert.Equal(t, ids[0], cursorID(t, page), "the cursor survives a poll that replaces the rows")

	press(t, page, "r")
	waitVisible(t, page.Locator(`#run-dialog[open][data-job-id="` + ids[0] + `"]`))
	press(t, page, "Escape")
	waitHidden(t, page.Locator("#run-dialog"))
	require.Eventually(t, func() bool { return focusOn(t, page, "run-"+ids[0]) }, 3*time.Second, 50*time.Millisecond,
		"focus returns to the row's Run")

	blurFocus(t, page)
	press(t, page, "Enter")
	waitJobPage(t, page, ids[0])
}

func TestKeys_SearchFocusAndEnterOpensFirstMatch(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	hourly := jobID(t, page, jobHourly)
	blurFocus(t, page)

	press(t, page, "/")
	require.True(t, focusOn(t, page, "search"))
	require.NoError(t, page.Keyboard().Type("hourly"))
	assert.Equal(t, "0", cursorID(t, page), "keys typed into the field move nothing")
	count, err := page.Locator("#run-dialog").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "r typed into the field opens nothing")
	press(t, page, "Enter")
	waitJobPage(t, page, hourly)

	navigateToDashboard(t, page)
	blurFocus(t, page)
	press(t, page, "/")
	press(t, page, "Escape")
	assert.False(t, focusOn(t, page, "search"), "Esc on an empty search leaves it")
	press(t, page, "j")
	assert.NotEqual(t, "0", cursorID(t, page), "keys act again after Esc")

	press(t, page, "Control+k")
	assert.True(t, focusOn(t, page, "search"), "Ctrl+K focuses the search")
	blurFocus(t, page)
	press(t, page, "Meta+k")
	assert.True(t, focusOn(t, page, "search"), "Cmd+K focuses the search")
}

func TestKeys_IgnoredWhileDialogOpen(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	theme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)

	openSettings(t, page)
	press(t, page, "t")
	press(t, page, "j")
	assert.Never(t, func() bool {
		v, e := page.Locator("html").GetAttribute("data-theme")
		return e != nil || v != theme
	}, 700*time.Millisecond, 100*time.Millisecond, "t does nothing under an open dialog")
	assert.Equal(t, "0", cursorID(t, page))
	press(t, page, "Escape")
	waitHidden(t, page.Locator("dialog.settings-modal"))
}

func TestKeys_ThemeAndShortcutsDialog(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	theme, err := page.Locator("html").GetAttribute("data-theme")
	require.NoError(t, err)
	blurFocus(t, page)

	press(t, page, "t")
	require.Eventually(t, func() bool {
		v, e := page.Locator("html").GetAttribute("data-theme")
		return e == nil && v != theme
	}, 5*time.Second, 50*time.Millisecond, "t switches the theme")
	waitForJobsLoaded(t, page)
	blurFocus(t, page)
	press(t, page, "t")
	require.Eventually(t, func() bool {
		v, e := page.Locator("html").GetAttribute("data-theme")
		return e == nil && v == theme
	}, 5*time.Second, 50*time.Millisecond)
	waitForJobsLoaded(t, page)

	blurFocus(t, page)
	press(t, page, "?")
	dlg := page.Locator("#keys-dialog[open]")
	waitVisible(t, dlg)
	assert.Contains(t, pageText(t, page, "#keys-dialog"), "Enable or disable the schedule")
	press(t, page, "Escape")
	waitHidden(t, dlg)

	require.NoError(t, page.Locator("#gear-menu summary").Click())
	require.NoError(t, page.Locator("#open-keys").Click())
	waitVisible(t, dlg)
	require.NoError(t, page.Locator("#keys-dialog .modal-close").Click())
	waitHidden(t, dlg)
	press(t, page, "?")
	waitVisible(t, dlg)
	press(t, page, "Escape")
}

func TestKeys_JobPage(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	ids := rowIDs(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)
	runJob(t, id)
	gotoJob(t, page, id, "?sort=default&rev=0&filter=all")
	blurFocus(t, page)

	runs, err := page.Locator("#jruns .jr").EvaluateAll(`els => els.map(e => e.dataset.run)`)
	require.NoError(t, err)
	list := runs.([]any)
	require.GreaterOrEqual(t, len(list), 2)
	require.Equal(t, list[0], selectedRun(page))
	press(t, page, "]")
	require.Eventually(t, func() bool { return selectedRun(page) == list[1] }, 5*time.Second, 50*time.Millisecond, "] selects the older run")
	press(t, page, "[")
	require.Eventually(t, func() bool { return selectedRun(page) == list[0] }, 5*time.Second, 50*time.Millisecond, "[ selects the newer run")

	blurFocus(t, page)
	press(t, page, "r")
	waitVisible(t, page.Locator(`#run-dialog[open][data-job-id="` + id + `"]`))
	press(t, page, "Escape")
	waitHidden(t, page.Locator("#run-dialog"))
	require.Eventually(t, func() bool { return focusOn(t, page, "run-now") }, 3*time.Second, 50*time.Millisecond)

	blurFocus(t, page)
	press(t, page, "Shift+E")
	waitVisible(t, page.Locator(`#toggle-`+id+`[aria-checked="false"]`))
	assert.False(t, jobStatus(t, id).Enabled)
	blurFocus(t, page)
	press(t, page, "Shift+E")
	waitVisible(t, page.Locator(`#toggle-`+id+`[aria-checked="true"]`))
	assert.True(t, jobStatus(t, id).Enabled)

	at := 0
	for i, v := range ids {
		if v == id {
			at = i
		}
	}
	require.Less(t, at, len(ids)-1)
	blurFocus(t, page)
	press(t, page, "j")
	waitJobPage(t, page, ids[at+1])
	blurFocus(t, page)
	press(t, page, "k")
	waitJobPage(t, page, id)

	press(t, page, "/")
	require.True(t, focusOn(t, page, "search"))
	press(t, page, "Escape")
	assert.False(t, focusOn(t, page, "search"), "Esc leaves the jump field")
	assert.Contains(t, page.URL(), "/jobs/"+id, "Esc in the jump field stays on the job page")
	press(t, page, "Escape")
	waitForJobsLoaded(t, page)
	assert.NotContains(t, page.URL(), "/jobs/")
}

func TestKeys_JumpListArrowsEnterAndShiftEnter(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	weekday := jobID(t, page, jobWeekday)
	setEnabled(t, weekday, true)
	gotoJob(t, page, weekday, "")

	typeJump(t, page, "job")
	items, err := page.Locator("#jdrop a.ji").EvaluateAll(`els => els.map(e => e.dataset.jobId)`)
	require.NoError(t, err)
	list := items.([]any)
	require.GreaterOrEqual(t, len(list), 2)
	current := func() string {
		v, e := page.Locator("#jdrop a.ji.cur").GetAttribute("data-job-id")
		if e != nil {
			return ""
		}
		return v
	}
	assert.Equal(t, list[0], current())
	press(t, page, "ArrowDown")
	assert.Equal(t, list[1], current())
	press(t, page, "ArrowUp")
	assert.Equal(t, list[0], current())
	press(t, page, "ArrowDown")
	press(t, page, "Enter")
	waitJobPage(t, page, list[1].(string))

	require.NotEqual(t, weekday, list[1])
	typeJump(t, page, "weekday")
	press(t, page, "Shift+Enter")
	waitVisible(t, page.Locator(`#run-dialog[open][data-job-id="` + weekday + `"]`))
	assert.Contains(t, pageText(t, page, "#run-dialog h4"), jobWeekday)
	assert.Contains(t, page.URL(), list[1].(string), "Shift+Enter stays on this page")
	press(t, page, "Escape")
	waitHidden(t, page.Locator("#run-dialog"))
}

func TestKeys_WorkAfterWrapToggle(t *testing.T) {
	// a focused wrap checkbox counted as a text field and swallowed every shortcut
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	gotoJob(t, page, jobID(t, page, jobHourly), "")

	require.NoError(t, page.Locator(".wraptg").Click())
	require.True(t, focusOn(t, page, "wrap"))
	press(t, page, "?")
	waitVisible(t, page.Locator("#keys-dialog[open]"))
	press(t, page, "Escape")
	waitHidden(t, page.Locator("#keys-dialog"))
}
