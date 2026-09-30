//go:build e2e

// Package e2e provides end-to-end browser tests for the Cronn web UI.
//
// Test organization:
// - e2e_test.go: TestMain, shared helpers, constants, core dashboard tests
// - auth_test.go: authentication tests (login/logout)
// - controls_test.go: theme, sort select and filter tabs
// - search_test.go: search and the empty state
// - modals_test.go: settings dialog
// - manual_test.go: run form (dialog and bottom sheet)
// - inspector_test.go: job inspector (runs, output, selection, focus)
// - layout_test.go: table structure, footer, polling, responsive layouts
// - toggle_test.go: enable/disable jobs
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDBPath  = "/tmp/cronn-e2e.db"
	testCrontab = "e2e/testdata/test-crontab.yml"
)

var (
	e2ePort     = envOr("E2E_PORT", "18080")
	authPort    = envOr("E2E_AUTH_PORT", "18081")
	baseURL     = "http://localhost:" + e2ePort
	authBaseURL = "http://localhost:" + authPort
)

// auth server constants (separate server for auth tests to avoid rate limiting main tests)
const (
	authDBPath   = "/tmp/cronn-e2e-auth.db"
	testPassword = "testpass123"                                                  //nolint:gosec // test password for e2e tests
	passwordHash = "$2y$10$ZcZnRH/ya6JUmBRGE8qlBupIFUYgvOewRXtpkB8HecWtUnryAHr0S" //nolint:gosec // bcrypt hash of testpass123 for e2e tests
)

const (
	jobFiveMin   = "Five minute job"
	jobHourly    = "Hourly job"
	jobWeekday   = "Weekday report"
	jobUnnamed   = `echo "job3: daily at midnight"`
	jobFailing   = "Vendor feed import"
	jobSilent    = "Silent failure"
	jobSlow      = "Slow report"
	jobTemplated = "Templated job"
	totalJobs    = 8
)

var (
	pw        *playwright.Playwright
	browser   playwright.Browser // single browser instance for all tests
	serverCmd *exec.Cmd
)

func TestMain(m *testing.M) {
	// clean old test data
	_ = os.Remove(testDBPath)

	for _, port := range []string{e2ePort, authPort} {
		if err := checkPortFree(port); err != nil {
			fmt.Printf("port %s is in use, set E2E_PORT / E2E_AUTH_PORT to free ports: %v\n", port, err)
			os.Exit(1)
		}
	}

	// create test crontab
	if err := createTestCrontab(); err != nil {
		fmt.Printf("failed to create test crontab: %v\n", err)
		os.Exit(1)
	}

	// build test binary
	ctx := context.Background()
	build := exec.CommandContext(ctx, "go", "build", "-o", "/tmp/cronn-e2e", "./app")
	build.Dir = ".."
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Printf("failed to build: %v\n", err)
		os.Exit(1)
	}

	// start server with test config (no auth - auth tests use separate server)
	serverCmd = exec.CommandContext(ctx, "/tmp/cronn-e2e",
		"-f", "../"+testCrontab,
		"--log.enabled",
		"--web.enabled",
		"--web.address=:"+e2ePort,
		"--web.db-path="+testDBPath,
		"--web.hostname=e2e-test",
	)
	serverCmd.Stdout = os.Stdout
	serverCmd.Stderr = os.Stderr
	if err := serverCmd.Start(); err != nil {
		fmt.Printf("failed to start server: %v\n", err)
		os.Exit(1)
	}

	// wait for server readiness
	if err := waitForServer(baseURL+"/ping", 30*time.Second); err != nil {
		fmt.Printf("server not ready: %v\n", err)
		_ = serverCmd.Process.Kill()
		os.Exit(1)
	}

	// install playwright browsers
	if err := playwright.Install(&playwright.RunOptions{
		Browsers: []string{"chromium"},
	}); err != nil {
		fmt.Printf("failed to install playwright: %v\n", err)
		_ = serverCmd.Process.Kill()
		os.Exit(1)
	}

	// start playwright
	var err error
	pw, err = playwright.Run()
	if err != nil {
		fmt.Printf("failed to start playwright: %v\n", err)
		_ = serverCmd.Process.Kill()
		os.Exit(1)
	}

	// launch browser once for all tests (contexts per test provide isolation)
	headless := os.Getenv("E2E_HEADLESS") != "false"
	var slowMo float64
	if !headless {
		slowMo = 50 // slow down visible browser for easier observation
	}
	browser, err = pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: new(headless),
		SlowMo:   new(slowMo),
	})
	if err != nil {
		fmt.Printf("failed to launch browser: %v\n", err)
		_ = pw.Stop()
		_ = serverCmd.Process.Kill()
		os.Exit(1)
	}

	// run tests
	code := m.Run()

	// cleanup
	_ = browser.Close()
	_ = pw.Stop()
	_ = serverCmd.Process.Kill()
	_ = os.Remove(testDBPath)
	_ = os.Remove("../" + testCrontab)

	os.Exit(code)
}

func createTestCrontab() error {
	content := `jobs:
  - spec: "*/5 * * * *"
    command: 'echo "job1: every 5 minutes"'
    name: "Five minute job"
  - spec: "0 * * * *"
    command: 'echo "job2: hourly"'
    name: "Hourly job"
  - spec: "0 0 * * *"
    command: 'echo "job3: daily at midnight"'
  - spec: "30 8 * * 1-5"
    command: 'echo "job4: weekdays at 8:30"'
    name: "Weekday report"
  - spec: "0 0 1 1 *"
    command: "sh -c 'echo fetching; echo boom >&2; exit 3'"
    name: "Vendor feed import"
  - spec: "0 0 1 1 *"
    command: "sh -c 'exit 7'"
    name: "Silent failure"
  - spec: "0 0 1 1 *"
    command: "sleep 3"
    name: "Slow report"
  - spec: "0 0 1 1 *"
    command: "echo run {{.YYYYMMDD}}"
    name: "Templated job"
`
	if err := os.MkdirAll(filepath.Dir("../"+testCrontab), 0o750); err != nil {
		return fmt.Errorf("failed to create test crontab dir: %w", err)
	}
	if err := os.WriteFile("../"+testCrontab, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write test crontab: %w", err)
	}
	return nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func checkPortFree(port string) error {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return err
	}
	return ln.Close()
}

func waitForServer(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("server not ready after %v", timeout)
		default:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody) // #nosec G107 - test url
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func newPage(t *testing.T) playwright.Page {
	t.Helper()
	// create isolated context (incognito-like) for complete test isolation
	// browser is shared, contexts provide isolation (cookies, storage)
	ctx, err := browser.NewContext()
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctx.Close() })

	page, err := ctx.NewPage()
	require.NoError(t, err)
	return page
}

func newPageSized(t *testing.T, width, height int) playwright.Page {
	t.Helper()
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{Width: width, Height: height},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctx.Close() })
	page, err := ctx.NewPage()
	require.NoError(t, err)
	return page
}

func navigateToDashboard(t *testing.T, page playwright.Page) {
	t.Helper()
	_, err := page.Goto(baseURL)
	require.NoError(t, err)
	waitVisible(t, page.Locator(".top"))
	waitForJobsLoaded(t, page)
}

func waitForJobsLoaded(t *testing.T, page playwright.Page) {
	t.Helper()
	err := page.Locator("tr.row").First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: new(5000.0),
	})
	require.NoError(t, err, "jobs should load within 5 seconds")
}

func row(page playwright.Page, name string) playwright.Locator {
	return page.Locator("tr.row").Filter(playwright.LocatorFilterOptions{
		Has: page.Locator(".job-open", playwright.PageLocatorOptions{HasText: name}),
	})
}

func jobID(t *testing.T, page playwright.Page, name string) string {
	t.Helper()
	id, err := row(page, name).GetAttribute("data-job-id")
	require.NoError(t, err)
	require.NotEmpty(t, id)
	return id
}

type apiJob struct {
	ID         string    `json:"id"`
	Command    string    `json:"command"`
	LastRun    time.Time `json:"last_run"`
	LastStatus string    `json:"last_status"`
	IsRunning  bool      `json:"is_running"`
	Enabled    bool      `json:"enabled"`
}

func jobStatus(t *testing.T, id string) apiJob {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/status") //nolint:noctx // test helper
	require.NoError(t, err)
	defer resp.Body.Close()
	var status struct {
		Jobs []apiJob `json:"jobs"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	for _, j := range status.Jobs {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("job %s not found in status API", id)
	return apiJob{}
}

func runJob(t *testing.T, id string) {
	t.Helper()
	prev := jobStatus(t, id).LastRun
	resp, err := http.Post(baseURL+"/api/jobs/"+id+"/run", "application/x-www-form-urlencoded", http.NoBody) //nolint:noctx // test helper
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Eventually(t, func() bool {
		j := jobStatus(t, id)
		return j.LastRun.After(prev) && !j.IsRunning
	}, 15*time.Second, 100*time.Millisecond, "job should finish")
}

func setEnabled(t *testing.T, id string, enabled bool) {
	t.Helper()
	if jobStatus(t, id).Enabled == enabled {
		return
	}
	resp, err := http.Post(baseURL+"/api/jobs/"+id+"/toggle", "application/x-www-form-urlencoded", http.NoBody) //nolint:noctx // test helper
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func evalBool(t *testing.T, page playwright.Page, expr string, args ...any) bool {
	t.Helper()
	var arg any
	if len(args) > 0 {
		arg = args[0]
	}
	res, err := page.Evaluate(expr, arg)
	require.NoError(t, err)
	b, ok := res.(bool)
	require.True(t, ok, "expression %q should return a bool, got %T", expr, res)
	return b
}

func activeElementIn(t *testing.T, page playwright.Page, selector string) bool {
	t.Helper()
	return evalBool(t, page, `(sel) => { const el = document.querySelector(sel); return !!el && el.contains(document.activeElement); }`, selector)
}

func noHorizontalScroll(t *testing.T, page playwright.Page) bool {
	t.Helper()
	return evalBool(t, page, `() => document.documentElement.scrollWidth <= document.documentElement.clientWidth`)
}

// waitVisible waits for locator to become visible
func waitVisible(t *testing.T, loc playwright.Locator) {
	t.Helper()
	require.NoError(t, loc.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}))
}

// waitHidden waits for locator to become hidden
func waitHidden(t *testing.T, loc playwright.Locator) {
	t.Helper()
	require.NoError(t, loc.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateHidden,
	}))
}

var (
	inspectorRe = regexp.MustCompile(`/api/jobs/[^/]+/inspector(\?.*)?$`)
	outputRe    = regexp.MustCompile(`/api/jobs/[^/]+/executions/\d+/output`)
	runFormRe   = regexp.MustCompile(`/api/jobs/[^/]+/run-form`)
	settingsRe  = regexp.MustCompile(`/api/settings/modal`)
	jobsRe      = regexp.MustCompile(`/api/jobs(\?.*)?$`)
)

func clickAndAwait(t *testing.T, page playwright.Page, loc playwright.Locator, urlRe *regexp.Regexp) {
	t.Helper()
	var lastErr error
	for range 6 {
		_, lastErr = page.ExpectResponse(urlRe, func() error {
			return loc.Click(playwright.LocatorClickOptions{Timeout: new(3000.0)})
		}, playwright.PageExpectResponseOptions{Timeout: new(4000.0)})
		if lastErr == nil {
			return
		}
	}
	require.NoError(t, lastErr)
}

func openInspector(t *testing.T, page playwright.Page, name string) playwright.Locator {
	t.Helper()
	// one click, and only this job's open response: a retry or the live poll would hide a dropped click
	id := jobID(t, page, name)
	openRe := regexp.MustCompile(`/api/jobs/` + id + `/inspector\?.*selected-job=`)
	_, err := page.ExpectResponse(openRe, func() error {
		return row(page, name).Locator(".job-open").Click(playwright.LocatorClickOptions{Timeout: new(3000.0)})
	}, playwright.PageExpectResponseOptions{Timeout: new(4000.0)})
	require.NoError(t, err)
	insp := page.Locator("#inspector .insp")
	waitVisible(t, insp)
	require.Eventually(t, func() bool {
		got, e := insp.GetAttribute("data-job-id")
		return e == nil && got == id
	}, 3*time.Second, 50*time.Millisecond, "the inspector shows the job that was clicked")
	return insp
}

func clickThemeToggle(t *testing.T, page playwright.Page, prev string) string {
	t.Helper()
	require.NoError(t, page.Locator(".iconbtn.theme").Click())
	var cur string
	require.Eventually(t, func() bool {
		v, e := page.Locator("html").GetAttribute("data-theme")
		if e != nil {
			return false
		}
		cur = v
		return v != prev
	}, 5*time.Second, 50*time.Millisecond)
	return cur
}

func countOf(t *testing.T, page playwright.Page, mode string) int {
	t.Helper()
	text, err := page.Locator("#count-" + mode).TextContent()
	require.NoError(t, err)
	n, err := strconv.Atoi(strings.TrimSpace(text))
	require.NoError(t, err, "count %s should be an integer, got %q", mode, text)
	return n
}

// --- dashboard tests ---

func TestDashboard_PageLoads(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	title, err := page.Title()
	require.NoError(t, err)
	assert.Equal(t, "Cronn Dashboard", title)

	text, err := page.Locator(".host").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "e2e-test")
}

func TestDashboard_ShowsJobsByName(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	count, err := page.Locator("tr.row").Count()
	require.NoError(t, err)
	assert.Equal(t, totalJobs, count)

	for _, name := range []string{jobFiveMin, jobHourly, jobWeekday, jobFailing, jobSlow} {
		visible, err := row(page, name).IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "row for %q should be visible", name)
	}

	cmd, err := row(page, jobHourly).Locator(".cmd").TextContent()
	require.NoError(t, err)
	assert.Equal(t, `echo "job2: hourly"`, cmd)

	unnamed, err := row(page, jobUnnamed).Locator(".job-open").TextContent()
	require.NoError(t, err)
	assert.Equal(t, jobUnnamed, unnamed, "an unnamed job shows its command as its name")
}

func TestDashboard_ShowsReadableSchedule(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	text, err := row(page, jobHourly).Locator("td.c-sched").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Every hour")
	assert.Contains(t, text, "0 * * * *")

	text, err = row(page, jobWeekday).Locator("td.c-sched").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "At 08:30, Monday through Friday")
}

func TestDashboard_FilterTabCountsAddUp(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	all := countOf(t, page, "all")
	sum := countOf(t, page, "failed") + countOf(t, page, "running") + countOf(t, page, "success") +
		countOf(t, page, "idle") + countOf(t, page, "disabled")
	assert.Equal(t, totalJobs, all)
	assert.Equal(t, all, sum, "tab counts should add up to all jobs")
}

func TestDashboard_FailingAlertOpensInspector(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	id := jobID(t, page, jobFailing)
	setEnabled(t, id, true)
	runJob(t, id)

	alert := page.Locator("#failing-alert .alert")
	require.Eventually(t, func() bool {
		txt, err := alert.TextContent()
		return err == nil && strings.Contains(txt, "failing")
	}, 10*time.Second, 200*time.Millisecond, "alert should appear after the failed run")

	clickAndAwait(t, page, alert.Locator("button.go"), inspectorRe)
	waitVisible(t, page.Locator("#inspector .insp"))
	heading, err := page.Locator("#inspector h3").TextContent()
	require.NoError(t, err)
	assert.NotEmpty(t, heading)
}

func TestDashboard_HasSearchBox(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	visible, err := page.Locator("#search").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "search input should be visible")

	placeholder, err := page.Locator("#search").GetAttribute("placeholder")
	require.NoError(t, err)
	assert.Equal(t, "Filter by name or command", placeholder)
}
