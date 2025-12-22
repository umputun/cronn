//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearch_FiltersByCommand(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// count initial jobs
	initialCount, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	require.GreaterOrEqual(t, initialCount, 2, "need at least 2 jobs to test search")

	// search for specific job
	require.NoError(t, page.Locator(".search-input").Fill("job1"))

	// wait for filter to apply (debounce + HTMX)
	assert.Eventually(t, func() bool {
		count, e := page.Locator(".job-card").Count()
		return e == nil && count < initialCount
	}, 5*time.Second, 100*time.Millisecond)

	// verify filtered results
	filteredCount, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.Less(t, filteredCount, initialCount, "filtered count should be less than initial")
	assert.GreaterOrEqual(t, filteredCount, 1, "should find at least one matching job")
}

func TestSearch_NoResults(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// search for non-existent job
	require.NoError(t, page.Locator(".search-input").Fill("nonexistentjob12345"))

	// wait for filter to apply
	assert.Eventually(t, func() bool {
		count, e := page.Locator(".job-card").Count()
		return e == nil && count == 0
	}, 5*time.Second, 100*time.Millisecond)

	// verify no results
	count, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.Equal(t, 0, count, "should show no jobs for non-matching search")
}

func TestSearch_ClearRestoresAll(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	waitForJobsLoaded(t, page)

	// count initial jobs
	initialCount, err := page.Locator(".job-card").Count()
	require.NoError(t, err)

	// search for something
	require.NoError(t, page.Locator(".search-input").Fill("job1"))

	// wait for filter to apply
	assert.Eventually(t, func() bool {
		count, e := page.Locator(".job-card").Count()
		return e == nil && count < initialCount
	}, 5*time.Second, 100*time.Millisecond)

	// clear search
	require.NoError(t, page.Locator(".search-input").Fill(""))

	// wait for all jobs to return
	assert.Eventually(t, func() bool {
		count, e := page.Locator(".job-card").Count()
		return e == nil && count == initialCount
	}, 5*time.Second, 100*time.Millisecond)

	// verify all jobs are back
	finalCount, err := page.Locator(".job-card").Count()
	require.NoError(t, err)
	assert.Equal(t, initialCount, finalCount, "clearing search should restore all jobs")
}
