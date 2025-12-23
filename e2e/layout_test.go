//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- list view tests ---

func TestListView_ShowsTableStructure(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// switch to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	// verify table headers
	visible, err := page.Locator(".jobs-table th:has-text('Status')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Status header")

	visible, err = page.Locator(".jobs-table th:has-text('Schedule')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Schedule header")

	visible, err = page.Locator(".jobs-table th:has-text('Command')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Command header")

	visible, err = page.Locator(".jobs-table th:has-text('Next Run')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Next Run header")

	visible, err = page.Locator(".jobs-table th:has-text('Last Run')").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "should show Last Run header")
}

func TestListView_InfoButtonOpensModal(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// switch to list view
	require.NoError(t, page.Locator(".view-toggle").Click())
	waitVisible(t, page.Locator(".jobs-table"))

	// click info button on first job row
	require.NoError(t, page.Locator(".job-row .info-btn").First().Click())
	waitVisible(t, page.Locator("#job-modal"))

	// verify modal is visible
	assert.True(t, isModalVisible(t, page, "#job-modal"), "job modal should be visible from list view")
}

// --- footer tests ---

func TestFooter_ShowsLinks(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify footer is visible
	visible, err := page.Locator(".footer").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "footer should be visible")

	// verify GitHub link
	visible, err = page.Locator(".footer a[href='https://github.com/umputun/cronn']").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "GitHub link should be visible")
}

func TestFooter_ShowsCopyright(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify copyright text
	text, err := page.Locator(".footer").TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Umputun")
}

func TestFooter_UsesFlexboxForCentering(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify footer uses flexbox for cross-browser vertical centering
	result, err := page.Evaluate("() => getComputedStyle(document.querySelector('.footer')).display")
	require.NoError(t, err)
	assert.Equal(t, "flex", result, "footer should use flexbox for cross-browser centering")

	// verify flex alignment
	result, err = page.Evaluate("() => getComputedStyle(document.querySelector('.footer')).alignItems")
	require.NoError(t, err)
	assert.Equal(t, "center", result, "footer should vertically center items")
}

// --- htmx polling tests ---

func TestHTMX_AutoRefreshIsConfigured(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify jobs container has htmx polling configured
	trigger, err := page.Locator("#jobs-container").GetAttribute("hx-trigger")
	require.NoError(t, err)
	assert.Contains(t, trigger, "every 5s", "jobs container should have 5s polling")
}

// --- responsive tests ---

func TestResponsive_MobileLayout(t *testing.T) {
	page := newPage(t)

	// set mobile viewport before navigation
	err := page.SetViewportSize(375, 667)
	require.NoError(t, err)

	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify page loads on mobile
	visible, err := page.Locator(".header").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "header should be visible on mobile")

	// verify jobs container is visible
	visible, err = page.Locator("#jobs-container").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "jobs container should be visible on mobile")
}

// --- job status display tests ---

func TestJobStatus_ShowsIdleState(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify at least one job has idle status (since no jobs have run yet)
	count, err := page.Locator(".status-indicator.idle").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should show idle status indicators for unrun jobs")
}

func TestJobStatus_HistoryButtonDisabledForIdleJobs(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// verify history button is disabled for idle jobs
	count, err := page.Locator(".job-card .history-btn[disabled]").Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "history button should be disabled for idle jobs")
}
