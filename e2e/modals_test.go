//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openSettings(t *testing.T, page playwright.Page) playwright.Locator {
	t.Helper()
	require.NoError(t, page.Locator("#gear-menu summary").Click())
	clickAndAwait(t, page, page.Locator("#open-settings"), settingsRe)
	dlg := page.Locator("dialog.settings-modal[open]")
	waitVisible(t, dlg)
	return dlg
}

func TestSettings_OpensAsModalDialogWithAbout(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	dlg := openSettings(t, page)
	text, err := dlg.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Settings & About")
	assert.Contains(t, text, ":"+e2ePort)
	assert.True(t, evalBool(t, page, `() => document.querySelector('dialog.settings-modal').matches(':modal')`))
	href, err := page.Locator("#github-link").GetAttribute("href")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/umputun/cronn", href)
	open, err := page.Locator("#gear-menu").GetAttribute("open")
	require.NoError(t, err)
	assert.Empty(t, open, "the menu closes once Settings is chosen")
}

func TestSettings_ClosesWithButtonEscAndBackdrop(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	dlg := openSettings(t, page)
	require.NoError(t, dlg.Locator(".modal-close").Click())
	waitHidden(t, page.Locator("dialog.settings-modal"))

	openSettings(t, page)
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("dialog.settings-modal"))

	openSettings(t, page)
	require.NoError(t, page.Mouse().Click(5, 300))
	waitHidden(t, page.Locator("dialog.settings-modal"))
	require.Eventually(t, func() bool {
		count, err := page.Locator("#dialog-slot dialog").Count()
		return err == nil && count == 0
	}, 3*time.Second, 50*time.Millisecond, "a closed dialog is removed from the slot")
}

func TestSettings_OpensFromJobPage(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)
	gotoJob(t, page, jobID(t, page, jobHourly), "")
	openSettings(t, page)
	require.NoError(t, page.Keyboard().Press("Escape"))
	waitHidden(t, page.Locator("dialog.settings-modal"))
}
