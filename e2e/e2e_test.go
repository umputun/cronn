//go:build e2e

// Package e2e provides end-to-end browser tests for the Cronn web UI.
//
// Test organization:
// - e2e_test.go: TestMain, shared helpers, constants, core dashboard tests
// - auth_test.go: authentication tests (login/logout)
// - controls_test.go: UI controls tests (view mode, theme, sort, filter)
// - search_test.go: search functionality tests
// - modals_test.go: modal tests (job details, settings)
// - manual_test.go: manual job execution tests
// - layout_test.go: layout tests (list view, footer, htmx, responsive, job status)
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	baseURL     = "http://localhost:18080"
	testDBPath  = "/tmp/cronn-e2e.db"
	testCrontab = "e2e/testdata/test-crontab"
)

// auth server constants (separate server for auth tests to avoid rate limiting main tests)
const (
	authBaseURL  = "http://localhost:18081"
	authDBPath   = "/tmp/cronn-e2e-auth.db"
	testPassword = "testpass123"                                                  //nolint:gosec // test password for e2e tests
	passwordHash = "$2y$10$ZcZnRH/ya6JUmBRGE8qlBupIFUYgvOewRXtpkB8HecWtUnryAHr0S" //nolint:gosec // bcrypt hash of testpass123 for e2e tests
)

var (
	pw        *playwright.Playwright
	serverCmd *exec.Cmd
)

func TestMain(m *testing.M) {
	// clean old test data
	_ = os.Remove(testDBPath)

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
		"--web.address=:18080",
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

	// run tests
	code := m.Run()

	// cleanup
	_ = pw.Stop()
	_ = serverCmd.Process.Kill()
	_ = os.Remove(testDBPath)
	_ = os.Remove("../" + testCrontab)

	os.Exit(code)
}

func createTestCrontab() error {
	content := `# test crontab for e2e tests
*/5 * * * * echo "job1: every 5 minutes"
0 * * * * echo "job2: hourly"
0 0 * * * echo "job3: daily at midnight"
30 8 * * 1-5 echo "job4: weekdays at 8:30"
`
	if err := os.WriteFile("../"+testCrontab, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write test crontab: %w", err)
	}
	return nil
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
	headless := os.Getenv("E2E_HEADLESS") != "false"
	slowMo := 0.0
	if !headless {
		slowMo = 50 // 50ms slowdown for UI mode
	}
	brow, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
		SlowMo:   playwright.Float(slowMo),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = brow.Close() })

	// create isolated context (incognito-like) for complete test isolation
	ctx, err := brow.NewContext()
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctx.Close() })

	page, err := ctx.NewPage()
	require.NoError(t, err)
	return page
}

// navigateToDashboard navigates to the dashboard and waits for it to load
// Used by non-auth tests (main server runs without authentication)
func navigateToDashboard(t *testing.T, page playwright.Page) {
	t.Helper()

	_, err := page.Goto(baseURL)
	require.NoError(t, err)

	// wait for header to be visible (confirms dashboard loaded)
	err = page.Locator(".header").WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	})
	require.NoError(t, err)
}

// waitForJobsLoaded waits for HTMX to load jobs (either cards or table rows)
func waitForJobsLoaded(t *testing.T, page playwright.Page) {
	t.Helper()
	// wait for either job cards or job rows to appear (depending on view mode)
	err := page.Locator(".job-card, .job-row").First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(5000),
	})
	require.NoError(t, err, "jobs should load within 5 seconds")
}

// isModalVisible checks if a modal element is displayed (style.display !== 'none')
func isModalVisible(t *testing.T, page playwright.Page, selector string) bool {
	t.Helper()
	result, err := page.Evaluate("(selector) => { const el = document.querySelector(selector); return el && el.style.display !== 'none'; }", selector)
	require.NoError(t, err)
	if result == nil {
		return false
	}
	visible, ok := result.(bool)
	if !ok {
		return false
	}
	return visible
}

// --- dashboard tests ---

func TestDashboard_PageLoads(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	title, err := page.Title()
	require.NoError(t, err)
	assert.Equal(t, "Cronn Dashboard", title)

	// verify header is present (already checked in navigateToDashboard)
	visible, err := page.Locator(".header").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "header should be visible")

	// verify hostname badge is present
	visible, err = page.Locator(".hostname-badge").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "hostname badge should be visible")

	// verify hostname shows test value
	text, err := page.Locator(".hostname-badge").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "e2e-test")
}

func TestDashboard_ShowsJobs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify jobs container is present
	visible, err := page.Locator("#jobs-container").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "jobs container should be visible")

	// verify we have job cards (at least one)
	count, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have at least one job card")
}

func TestDashboard_ShowsStatsBar(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify stats bar shows job count
	visible, err := page.Locator(".stats-bar").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "stats bar should be visible")
}

func TestDashboard_HasSearchBox(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify search input exists (class is search-input, name is search)
	visible, err := page.Locator(".search-input").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "search input should be visible")

	// verify placeholder text
	placeholder, err := page.Locator("input[name='search']").GetAttribute("placeholder")
	require.NoError(t, err)
	assert.Equal(t, "Search commands...", placeholder)
}
