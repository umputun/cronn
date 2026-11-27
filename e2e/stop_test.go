//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	stopFormRe = regexp.MustCompile(`/api/jobs/[^/]+/stop-form`)
	stopRe     = regexp.MustCompile(`/api/jobs/[^/]+/stop$`)
)

func lastExecution(t *testing.T, id string) (status string, exitCode int, output string) {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/jobs/" + id + "/history") //nolint:noctx // test helper
	require.NoError(t, err)
	defer resp.Body.Close()
	var hist struct {
		Executions []struct {
			ID       int    `json:"id"`
			Status   string `json:"status"`
			ExitCode int    `json:"exit_code"`
		} `json:"executions"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&hist))
	require.NotEmpty(t, hist.Executions)
	last := hist.Executions[0]
	logs, err := http.Get(baseURL + "/api/v1/jobs/" + id + "/executions/" + strconv.Itoa(last.ID) + "/logs") //nolint:noctx // test helper
	require.NoError(t, err)
	defer logs.Body.Close()
	var body struct {
		Output string `json:"output"`
	}
	require.NoError(t, json.NewDecoder(logs.Body).Decode(&body))
	return last.Status, last.ExitCode, body.Output
}

func confirmStop(t *testing.T, page playwright.Page) {
	t.Helper()
	dlg := page.Locator("#stop-dialog[open]")
	waitVisible(t, dlg)
	_, err := page.ExpectResponse(stopRe, func() error {
		return page.Locator("#sf-stop").Click()
	}, playwright.PageExpectResponseOptions{Timeout: new(4000.0)})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		count, e := page.Locator("#stop-dialog").Count()
		return e == nil && count == 0
	}, 3*time.Second, 50*time.Millisecond, "the dialog closes after the stop")
}

// stopRunFromBar stops the run in progress shown on a job page and waits for Run to come back
func stopRunFromBar(t *testing.T, page playwright.Page) {
	t.Helper()
	clickAndAwait(t, page, page.Locator("#stop-run"), stopFormRe)
	confirmStop(t, page)
	waitVisible(t, page.Locator("#run-now"))
}

func TestStop_DisabledRunningJobKeepsStopOnJobPage(t *testing.T) {
	// turning the schedule off hid Stop on the job page while the run went on
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := startLive(t, page)
	started := time.Now()
	gotoJob(t, page, id, "")
	waitVisible(t, page.Locator("#stop-run"))

	setEnabled(t, id, false)
	t.Cleanup(func() { setEnabled(t, id, true) })
	require.Eventually(t, func() bool { return pageText(t, page, "#job-badge") == "Disabled" }, 8*time.Second, 100*time.Millisecond)
	waitVisible(t, page.Locator("#stop-run"))
	clickAndAwait(t, page, page.Locator("#stop-run"), stopFormRe)
	confirmStop(t, page)
	requireStopped(t, id, started)
}

func requireStopped(t *testing.T, id string, started time.Time) {
	t.Helper()
	require.Eventually(t, func() bool { return !jobStatus(t, id).IsRunning }, 10*time.Second, 100*time.Millisecond)
	assert.Less(t, time.Since(started), 25*time.Second, "stopped well before its 30s run ends")
	status, code, output := lastExecution(t, id)
	assert.Equal(t, "failed", status)
	assert.Equal(t, -1, code)
	assert.True(t, strings.HasSuffix(output, "stopped from the dashboard"), output)
}

func TestStop_FromRowEndsTheRun(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := startLive(t, page)
	started := time.Now()

	stopBtn := row(page, jobLive).Locator("#stop-" + id)
	waitVisible(t, stopBtn)
	count, err := row(page, jobLive).Locator("#run-" + id).Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a running job shows Stop in place of Run")

	clickAndAwait(t, page, stopBtn, stopFormRe)
	title, err := page.Locator("#stop-dialog[open] h4").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "Stop “"+jobLive+"”?", title)
	confirmStop(t, page)
	toast, err := page.Locator("#toasts").TextContent()
	require.NoError(t, err)
	assert.Contains(t, toast, "Stop requested: "+jobLive)
	requireStopped(t, id, started)
	waitVisible(t, row(page, jobLive).Locator("#run-"+id))
}

func TestStop_FromCommandBarEndsTheSelectedRun(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := startLive(t, page)
	started := time.Now()
	gotoJob(t, page, id, "")

	waitVisible(t, page.Locator("#stop-run"))
	clickAndAwait(t, page, page.Locator("#stop-run"), stopFormRe)
	start, err := page.Locator("#stop-dialog[open] input[name=start]").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "live:"+start, selectedRun(page), "the dialog targets the run the page shows")
	confirmStop(t, page)
	requireStopped(t, id, started)
	waitVisible(t, page.Locator("#run-now"))
	require.Eventually(t, func() bool {
		count, e := page.Locator("#live-out").Count()
		return e == nil && count == 0
	}, 8*time.Second, 100*time.Millisecond, "the stopped run turns into its recorded output without a reload")
	assert.Contains(t, pageText(t, page, "#joutput"), "stopped from the dashboard")
}

func TestStop_StaleConfirmLeavesNoError(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := startLive(t, page)
	started := time.Now()

	clickAndAwait(t, page, row(page, jobLive).Locator("#stop-"+id), stopFormRe)
	start, err := page.Locator("#stop-dialog[open] input[name=start]").InputValue()
	require.NoError(t, err)
	resp, err := http.PostForm(baseURL+"/api/jobs/"+id+"/stop", url.Values{"start": {start}}) //nolint:noctx // test helper
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	requireStopped(t, id, started)

	confirmStop(t, page)
	toast, err := page.Locator("#toasts").TextContent()
	require.NoError(t, err)
	assert.NotContains(t, toast, "Stop requested", "a run that already ended is not reported as stopped again")
}
