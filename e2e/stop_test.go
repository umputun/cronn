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
	dlg := page.Locator("dialog.stop-dialog[open]")
	waitVisible(t, dlg)
	_, err := page.ExpectResponse(stopRe, func() error {
		return dlg.Locator("button.danger").Click()
	}, playwright.PageExpectResponseOptions{Timeout: new(4000.0)})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		count, e := page.Locator("dialog.stop-dialog").Count()
		return e == nil && count == 0
	}, 3*time.Second, 50*time.Millisecond, "the dialog closes after the stop")
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

	stopBtn := row(page, jobLive).Locator(".stop-btn")
	waitVisible(t, stopBtn)
	count, err := row(page, jobLive).Locator(".run-btn").Count()
	require.NoError(t, err)
	assert.Zero(t, count, "a running job shows Stop in place of Run")

	clickAndAwait(t, page, stopBtn, stopFormRe)
	title, err := page.Locator("dialog.stop-dialog[open] h4").TextContent()
	require.NoError(t, err)
	assert.Equal(t, "Stop “"+jobLive+"”?", title)
	confirmStop(t, page)
	toast, err := page.Locator("#toasts").TextContent()
	require.NoError(t, err)
	assert.Contains(t, toast, "Stop requested: "+jobLive)
	requireStopped(t, id, started)
	waitVisible(t, row(page, jobLive).Locator(".run-btn"))
}

func TestStop_FromInspectorEndsTheSelectedRun(t *testing.T) {
	page := newPageSized(t, 1440, 900)
	navigateToDashboard(t, page)
	id := startLive(t, page)
	started := time.Now()

	openInspector(t, page, jobLive)
	stopBtn := page.Locator("#live-out .stop-btn")
	waitVisible(t, stopBtn)
	clickAndAwait(t, page, stopBtn, stopFormRe)
	start, err := page.Locator("dialog.stop-dialog[open] input[name=start]").InputValue()
	require.NoError(t, err)
	assert.Equal(t, "live:"+start, selectedRun(page), "the dialog targets the run the inspector shows")
	confirmStop(t, page)
	requireStopped(t, id, started)
}

func TestStop_StaleConfirmLeavesNoError(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := startLive(t, page)
	started := time.Now()

	clickAndAwait(t, page, row(page, jobLive).Locator(".stop-btn"), stopFormRe)
	start, err := page.Locator("dialog.stop-dialog[open] input[name=start]").InputValue()
	require.NoError(t, err)
	resp, err := http.PostForm(baseURL+"/api/jobs/"+id+"/stop", url.Values{"start": {start}}) //nolint:noctx // test helper
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	requireStopped(t, id, started)

	confirmStop(t, page)
	toast, err := page.Locator("#toasts").TextContent()
	require.NoError(t, err)
	assert.NotContains(t, toast, "Stop requested", "a run that already ended is not reported as stopped again")
}
