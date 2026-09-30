//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openSettings(t *testing.T, page playwright.Page) playwright.Locator {
	t.Helper()
	clickAndAwait(t, page, page.Locator(`.top button[aria-label="Settings and about"]`), settingsRe)
	dlg := page.Locator("dialog.settings-modal[open]")
	waitVisible(t, dlg)
	return dlg
}

func TestSettings_OpensAsModalDialog(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	dlg := openSettings(t, page)
	text, err := dlg.TextContent()
	require.NoError(t, err)
	assert.Contains(t, text, "Settings & About")
	assert.Contains(t, text, ":"+e2ePort)
	assert.True(t, evalBool(t, page, `() => document.querySelector('dialog.settings-modal').matches(':modal')`))
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
	require.NoError(t, page.Mouse().Click(5, 5))
	waitHidden(t, page.Locator("dialog.settings-modal"))
	require.Eventually(t, func() bool {
		count, err := page.Locator("#dialog-slot dialog").Count()
		return err == nil && count == 0
	}, 3*time.Second, 50*time.Millisecond, "a closed dialog is removed from the slot")
}
