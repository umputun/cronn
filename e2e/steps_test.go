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

func textOf(t *testing.T, loc playwright.Locator) string {
	t.Helper()
	text, err := loc.TextContent()
	require.NoError(t, err)
	return strings.Join(strings.Fields(text), " ")
}

func isOpen(t *testing.T, loc playwright.Locator) bool {
	t.Helper()
	open, err := loc.Evaluate(`(el) => el.open`, nil)
	require.NoError(t, err)
	b, ok := open.(bool)
	require.True(t, ok)
	return b
}

// selectFromStep picks the run dialog's start step and waits for its fields to be redrawn
func selectFromStep(t *testing.T, page playwright.Page, step string) {
	t.Helper()
	_, err := page.ExpectResponse(runFormRe, func() error {
		if _, e := page.Locator("#rf-from").SelectOption(playwright.SelectOptionValues{Values: &[]string{step}}); e != nil {
			return fmt.Errorf("failed to select start step %s: %w", step, e)
		}
		return nil
	}, playwright.PageExpectResponseOptions{Timeout: new(4000.0)})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		v, e := page.Locator("#rf-from").InputValue()
		return e == nil && v == step
	}, 3*time.Second, 50*time.Millisecond)
}

func TestSteps_TableAndDialogShowSteps(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	r := row(page, jobPipeline)
	assert.Equal(t, "3 steps", textOf(t, r.Locator(".tag.steps")))
	assert.Equal(t, "Extract → sh -c 'echo transform start; sleep 8; echo transform done' → echo load",
		textOf(t, r.Locator(".cmd")))

	dlg := openRunForm(t, page, jobPipeline)
	options, err := dlg.Locator("#rf-from option").Count()
	require.NoError(t, err)
	assert.Equal(t, 3, options)
	steps, err := dlg.Locator(".rf-step textarea").Count()
	require.NoError(t, err)
	assert.Equal(t, 3, steps)
	first, err := dlg.Locator("#rf-step-1").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "echo extract {{.YYYYMMDD}}", first)
	visible, err := dlg.Locator("#rf-date").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "a step with templates gets the date field")
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("dialog.run-dialog"))
}

func TestSteps_FailedRunShowsFailedStep(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobBroken)
	setEnabled(t, id, true)
	runJob(t, id)

	openInspector(t, page, jobBroken)
	assert.Contains(t, inspectorText(t, page, ".chips"), "Last run failed · step 2 · exit 3")
	steps, err := page.Locator("#inspector .steps-def li").AllTextContents()
	require.NoError(t, err)
	assert.Equal(t, []string{"echo fetching", "sh -c 'echo boom >&2; exit 3'", "echo never"}, steps)
	assert.Equal(t, "step 2 · exit 3", textOf(t, page.Locator("#inspector .run.sel .ex")))

	failed := page.Locator(`#insp-output details.step.failed[data-step="2"]`)
	waitVisible(t, failed)
	assert.True(t, isOpen(t, failed), "the failed step starts expanded")
	assert.Contains(t, textOf(t, failed), "boom")
	assert.Contains(t, textOf(t, failed.Locator(".sr")), "exit 3")
	assert.False(t, isOpen(t, page.Locator(`#insp-output details.step.success[data-step="1"]`)))
	assert.Equal(t, "skipped", textOf(t, page.Locator(`#insp-output .step.skipped[data-step="3"] .sr`)))

	require.Eventually(t, func() bool {
		return strings.Contains(textOf(t, row(page, jobBroken).Locator(".c-last")), "step 2/3 · exit 3")
	}, 8*time.Second, 100*time.Millisecond, "the row shows the step the run failed at")
}

func TestSteps_RerunFromFailedStep(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobBroken)
	setEnabled(t, id, true)
	if jobStatus(t, id).LastStatus != "failed" {
		runJob(t, id)
	}

	openInspector(t, page, jobBroken)
	clickAndAwait(t, page, page.Locator("#insp-output .rerun .btn"), runFormRe)
	dlg := page.Locator("dialog.run-dialog[open]")
	waitVisible(t, dlg)

	from, err := dlg.Locator("#rf-from").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "2", from, "the dialog starts at the failed step")
	count, err := dlg.Locator("#rf-step-1").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "steps before the start step are not shown")
	step2, err := dlg.Locator("#rf-step-2").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "sh -c 'echo boom >&2; exit 3'", step2)

	require.NoError(t, dlg.Locator("#rf-step-2").Fill("echo fixed"))
	selectFromStep(t, page, "1")
	waitVisible(t, dlg.Locator("#rf-step-1"))
	step2, err = dlg.Locator("#rf-step-2").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "echo fixed", step2, "changing the start step keeps the edits")

	selectFromStep(t, page, "2")
	prev := jobStatus(t, id).LastRun
	require.NoError(t, dlg.Locator("button[type=submit]").Click())
	waitHidden(t, page.Locator("dialog.run-dialog"))
	require.Eventually(t, func() bool {
		j := jobStatus(t, id)
		return j.LastRun.After(prev) && !j.IsRunning && j.LastStatus == "success"
	}, 15*time.Second, 100*time.Millisecond, "the re-run from step 2 succeeds")

	require.Eventually(t, func() bool {
		return strings.Contains(textOf(t, page.Locator("#inspector .runs .run").First()), "from 2 · 3/3 · exit 0")
	}, 10*time.Second, 100*time.Millisecond, "the runs list shows where the re-run started")
}

func TestSteps_LiveRunShowsCurrentStep(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobPipeline)
	setEnabled(t, id, true)
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 20*time.Second, 200*time.Millisecond)
	startJob(t, id)

	openInspector(t, page, jobPipeline)
	running := page.Locator(`#live-out details.step.running[data-step="2"]`)
	require.NoError(t, running.WaitFor(playwright.LocatorWaitForOptions{Timeout: new(8000.0)}))
	assert.True(t, isOpen(t, running), "the running step starts expanded")
	assert.Contains(t, textOf(t, running.Locator("pre")), "transform start")
	assert.Contains(t, textOf(t, page.Locator("#inspector .run.sel .ex")), "running · 2/3")
	require.Eventually(t, func() bool {
		return strings.Contains(textOf(t, row(page, jobPipeline).Locator(".c-last")), "step 2/3")
	}, 8*time.Second, 100*time.Millisecond, "the row shows the running step")

	extract := page.Locator(`#live-out details.step[data-step="1"]`)
	require.False(t, isOpen(t, extract), "a finished step starts collapsed")
	require.NoError(t, extract.Locator("summary").Click())
	require.True(t, isOpen(t, extract))
	// mark the rendered output, so the check below sees only the one the next poll swaps in
	_, err := page.Evaluate(`() => { document.querySelector('#insp-output .out').dataset.seen = '1' }`, nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return evalBool(t, page, `() => { const out = document.querySelector('#insp-output .out');
			const step = out && out.querySelector('details.step[data-step="1"]');
			return !!step && out.dataset.seen !== '1' && step.open }`)
	}, 8*time.Second, 50*time.Millisecond, "a step the reader expanded stays expanded across polls")

	require.Eventually(t, func() bool {
		return strings.Contains(textOf(t, page.Locator("#inspector .runs .run").First()), "3/3 · exit 0")
	}, 20*time.Second, 200*time.Millisecond, "the finished run shows all steps done")
}
