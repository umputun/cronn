//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// authServer manages the auth-enabled server for auth tests
type authServer struct {
	cmd *exec.Cmd
}

// startAuthServer starts a server with authentication enabled on port 18081
func startAuthServer(t *testing.T) *authServer {
	t.Helper()

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "/tmp/cronn-e2e",
		"-f", "../"+testCrontab,
		"--log.enabled",
		"--web.enabled",
		"--web.address=:18081",
		"--web.db-path="+authDBPath,
		"--web.password-hash="+passwordHash,
		"--web.hostname=e2e-auth-test",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start auth server: %v", err)
	}

	// wait for server readiness
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, authBaseURL+"/ping", http.NoBody)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return &authServer{cmd: cmd}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Fatalf("auth server not ready after 10s")
	return nil
}

func (s *authServer) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// authLogin performs login with test credentials on auth server
func authLogin(t *testing.T, page playwright.Page) {
	t.Helper()
	_, err := page.Goto(authBaseURL + "/login")
	require.NoError(t, err)
	require.NoError(t, page.Locator("input[name='password']").Fill(testPassword))
	require.NoError(t, page.Locator("button[type='submit']").Click())
	require.NoError(t, page.WaitForURL(authBaseURL+"/"))
}

// ensureAuthLoggedIn logs in and waits for dashboard on auth server
func ensureAuthLoggedIn(t *testing.T, page playwright.Page) {
	t.Helper()
	_, err := page.Goto(authBaseURL)
	require.NoError(t, err)

	// check if login form is visible and fill it
	visible, _ := page.Locator("input[name='password']").IsVisible()
	if visible {
		require.NoError(t, page.Locator("input[name='password']").Fill(testPassword))
		require.NoError(t, page.Locator("button[type='submit']").Click())
	}

	waitVisible(t, page.Locator(".header"))
}

func TestAuth_LoginPageDisplays(t *testing.T) {
	srv := startAuthServer(t)
	defer srv.stop()

	page := newPage(t)
	_, err := page.Goto(authBaseURL + "/login")
	require.NoError(t, err)

	title, err := page.Title()
	require.NoError(t, err)
	assert.Equal(t, "Cronn - Login", title)

	// verify login form elements
	visible, err := page.Locator(".login-card").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "login card should be visible")

	visible, err = page.Locator("input[name='password']").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "password input should be visible")

	visible, err = page.Locator("button[type='submit']").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "submit button should be visible")
}

func TestAuth_LoginValid(t *testing.T) {
	srv := startAuthServer(t)
	defer srv.stop()

	page := newPage(t)
	authLogin(t, page)

	// verify we're redirected to dashboard
	url := page.URL()
	assert.Equal(t, authBaseURL+"/", url, "should be redirected to dashboard")

	// verify logout link is visible (indicates logged in)
	visible, err := page.Locator("a[href='/logout']").IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "logout link should be visible after login")
}

func TestAuth_LoginInvalid(t *testing.T) {
	srv := startAuthServer(t)
	defer srv.stop()

	page := newPage(t)
	_, err := page.Goto(authBaseURL + "/login")
	require.NoError(t, err)
	require.NoError(t, page.Locator("input[name='password']").Fill("wrongpassword"))
	require.NoError(t, page.Locator("button[type='submit']").Click())

	// wait for error message and verify content (not just visibility)
	errorElement := page.Locator(".error-message")
	waitVisible(t, errorElement)

	// verify we're still on login page
	url := page.URL()
	assert.Contains(t, url, "/login", "should stay on login page")

	// verify error message content
	errorText, err := errorElement.TextContent()
	require.NoError(t, err)
	assert.NotEmpty(t, errorText, "error message should have content")
	assert.Contains(t, errorText, "Invalid password", "error message should indicate invalid credentials")
}

func TestAuth_Logout(t *testing.T) {
	srv := startAuthServer(t)
	defer srv.stop()

	page := newPage(t)
	ensureAuthLoggedIn(t, page)

	// click logout link
	require.NoError(t, page.Locator("a[href='/logout']").Click())
	require.NoError(t, page.WaitForURL("**/login"))

	// verify we're redirected to login page
	url := page.URL()
	assert.Contains(t, url, "/login", "should be redirected to login page")
}

func TestAuth_ProtectedRouteRedirects(t *testing.T) {
	srv := startAuthServer(t)
	defer srv.stop()

	page := newPage(t)
	_, err := page.Goto(authBaseURL)
	require.NoError(t, err)
	require.NoError(t, page.WaitForURL("**/login"))

	// verify we're redirected to login
	url := page.URL()
	assert.Contains(t, url, "/login", "should be redirected to login page")
}
