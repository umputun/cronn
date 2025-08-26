package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Authentication(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// bcrypt hash for "testpass"
	passwordHash := "$2y$10$qOIpGITktzktHpcnWXiow.penxJmMcapV3G2ZRQaK0QRW7BSmAuJG" //nolint:gosec // test password hash

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		PasswordHash:   passwordHash,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	handler := server.routes()

	t.Run("without auth redirects to login", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/login", rec.Header().Get("Location"))
	})

	t.Run("with wrong password returns 401 for API", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs", http.NoBody)
		req.Header.Set("Accept", "application/json")
		req.SetBasicAuth("cronn", "wrongpass")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("with correct auth returns 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.SetBasicAuth("cronn", "testpass")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("login form displays", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/login", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Enter password")
	})

	t.Run("login with correct password sets cookie", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/", rec.Header().Get("Location"))

		// check cookie was set
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "cronn-auth", cookies[0].Name)
	})

	t.Run("login with wrong password shows error", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=wrongpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "Invalid password")
	})

	t.Run("without password hash configured allows access", func(t *testing.T) {
		cfg := Config{
			DBPath:         filepath.Join(tmpDir, "test2.db"),
			UpdateInterval: time.Minute,
			Version:        "test",
			JobsProvider:   createTestProvider(t, tmpDir),
			PasswordHash:   "", // no auth
		}
		noAuthServer, err := New(cfg)
		require.NoError(t, err)
		defer noAuthServer.store.Close()

		noAuthHandler := noAuthServer.routes()
		req := httptest.NewRequest("GET", "/", http.NoBody)
		rec := httptest.NewRecorder()
		noAuthHandler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("HTTPS login sets __Host- prefixed cookie", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-Proto", "https") // simulate HTTPS
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/", rec.Header().Get("Location"))

		// check __Host- prefixed cookie was set for HTTPS
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "__Host-cronn-auth", cookies[0].Name)
		assert.True(t, cookies[0].Secure)
		assert.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	})
}

func TestServer_CSRFProtection(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// bcrypt hash for "testpass"
	passwordHash := "$2y$10$qOIpGITktzktHpcnWXiow.penxJmMcapV3G2ZRQaK0QRW7BSmAuJG" //nolint:gosec // test password hash

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		PasswordHash:   passwordHash,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	handler := server.routes()

	t.Run("GET requests are allowed (safe methods)", func(t *testing.T) {
		// GET requests should work with cross-origin
		req := httptest.NewRequest("GET", "/api/jobs", http.NoBody)
		req.Header.Set("Origin", "https://evil.com")
		req.SetBasicAuth("cronn", "testpass")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// GET should be allowed even with cross-origin
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("POST requests without Sec-Fetch-Site are allowed (same-origin assumed)", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/theme", http.NoBody)
		req.SetBasicAuth("cronn", "testpass")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// should be allowed (same-origin assumed)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("POST requests with Sec-Fetch-Site: same-origin are allowed", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/theme", http.NoBody)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.SetBasicAuth("cronn", "testpass")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("POST requests with Sec-Fetch-Site: cross-site are blocked", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/theme", http.NoBody)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.SetBasicAuth("cronn", "testpass")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// should be blocked by CSRF protection
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("POST requests with mismatched Origin header are blocked", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/theme", http.NoBody)
		req.Host = "localhost:8080"
		req.Header.Set("Origin", "https://evil.com")
		req.SetBasicAuth("cronn", "testpass")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// should be blocked by CSRF protection
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("Login form POST is also protected", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// login should also be protected
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("Same-origin login works", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// same-origin login should work
		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/", rec.Header().Get("Location"))
	})
}

func TestServer_handleLogout(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	passwordHash := "$2y$10$qOIpGITktzktHpcnWXiow.penxJmMcapV3G2ZRQaK0QRW7BSmAuJG" //nolint:gosec // test password hash

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		PasswordHash:   passwordHash,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	handler := server.routes()

	t.Run("logout clears auth cookie and redirects", func(t *testing.T) {
		// first login to get a valid session
		loginReq := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		loginRec := httptest.NewRecorder()
		handler.ServeHTTP(loginRec, loginReq)

		// get the session cookie
		cookies := loginRec.Result().Cookies()
		require.Len(t, cookies, 1)
		sessionCookie := cookies[0]

		// now test logout
		logoutReq := httptest.NewRequest("GET", "/logout", http.NoBody)
		logoutReq.AddCookie(sessionCookie)
		logoutRec := httptest.NewRecorder()
		handler.ServeHTTP(logoutRec, logoutReq)

		// should redirect to login
		assert.Equal(t, http.StatusSeeOther, logoutRec.Code)
		assert.Equal(t, "/login", logoutRec.Header().Get("Location"))
		assert.Equal(t, "true", logoutRec.Header().Get("HX-Refresh"))

		// should clear both possible cookie names
		responseCookies := logoutRec.Result().Cookies()
		assert.Len(t, responseCookies, 2)

		// verify both cookies are being cleared (MaxAge = -1)
		cookieNames := []string{responseCookies[0].Name, responseCookies[1].Name}
		assert.Contains(t, cookieNames, "cronn-auth")
		assert.Contains(t, cookieNames, "__Host-cronn-auth")

		for _, cookie := range responseCookies {
			assert.Equal(t, -1, cookie.MaxAge)
			assert.Equal(t, "", cookie.Value)
		}
	})

	t.Run("logout with HTTPS cookie", func(t *testing.T) {
		// login with HTTPS to get __Host- cookie
		loginReq := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		loginReq.Header.Set("X-Forwarded-Proto", "https")
		loginReq.Header.Set("Sec-Fetch-Site", "same-origin") // CSRF protection
		loginReq.RemoteAddr = "192.168.100.100:8080"         // unique IP for rate limiting
		loginRec := httptest.NewRecorder()
		handler.ServeHTTP(loginRec, loginReq)

		cookies := loginRec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "__Host-cronn-auth", cookies[0].Name)

		// logout with HTTPS
		logoutReq := httptest.NewRequest("GET", "/logout", http.NoBody)
		logoutReq.Header.Set("X-Forwarded-Proto", "https")
		logoutReq.AddCookie(cookies[0])
		logoutRec := httptest.NewRecorder()
		handler.ServeHTTP(logoutRec, logoutReq)

		assert.Equal(t, http.StatusSeeOther, logoutRec.Code)
		assert.Equal(t, "/login", logoutRec.Header().Get("Location"))
	})

	t.Run("logout without session cookie still works", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/logout", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/login", rec.Header().Get("Location"))
	})
}

func TestServer_validateSession(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		PasswordHash:   "test",
		LoginTTL:       2 * time.Hour, // custom TTL for testing
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	t.Run("validates valid session", func(t *testing.T) {
		// create a session
		token, err := server.createSession()
		require.NoError(t, err)

		// validate it
		assert.True(t, server.validateSession(token))
	})

	t.Run("rejects invalid session", func(t *testing.T) {
		assert.False(t, server.validateSession("invalid-token"))
	})

	t.Run("rejects expired session with custom TTL", func(t *testing.T) {
		// create a session
		token, err := server.createSession()
		require.NoError(t, err)

		// manually expire it (3 hours ago, past our 2 hour TTL)
		server.sessionsMu.Lock()
		sess := server.sessions[token]
		sess.createdAt = time.Now().Add(-3 * time.Hour)
		server.sessions[token] = sess
		server.sessionsMu.Unlock()

		// should be invalid now
		assert.False(t, server.validateSession(token))

		// and should be removed from sessions map
		server.sessionsMu.Lock()
		_, exists := server.sessions[token]
		server.sessionsMu.Unlock()
		assert.False(t, exists)
	})

	t.Run("accepts session within custom TTL", func(t *testing.T) {
		// create a session
		token, err := server.createSession()
		require.NoError(t, err)

		// manually set to 1 hour ago (within our 2 hour TTL)
		server.sessionsMu.Lock()
		sess := server.sessions[token]
		sess.createdAt = time.Now().Add(-1 * time.Hour)
		server.sessions[token] = sess
		server.sessionsMu.Unlock()

		// should still be valid
		assert.True(t, server.validateSession(token))
	})
}

func TestServer_handleLoginEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	passwordHash := "$2y$10$qOIpGITktzktHpcnWXiow.penxJmMcapV3G2ZRQaK0QRW7BSmAuJG" //nolint:gosec // test password hash

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		PasswordHash:   passwordHash,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	t.Run("handleLogin with missing password field", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		server.handleLogin(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "Password is required")
	})

	t.Run("handleLogin with empty password", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password="))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		server.handleLogin(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "Password is required")
	})

	t.Run("handleLogin with malformed form data", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("not_a_form"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		server.handleLogin(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("handleLogin with correct password redirects", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		server.handleLogin(rec, req)

		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/", rec.Header().Get("Location"))

		// check cookie was set
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "cronn-auth", cookies[0].Name)
	})

	t.Run("handleLogin createSession error", func(t *testing.T) {
		// fill up sessions to trigger error
		server.sessionsMu.Lock()
		for i := 0; i < 10000; i++ {
			server.sessions[fmt.Sprintf("token-%d", i)] = session{createdAt: time.Now()}
		}
		server.sessionsMu.Unlock()

		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		server.handleLogin(rec, req)

		// should still succeed, just with potentially lower quality token
		assert.Equal(t, http.StatusSeeOther, rec.Code)

		// cleanup
		server.sessionsMu.Lock()
		server.sessions = make(map[string]session)
		server.sessionsMu.Unlock()
	})
}

func TestServer_renderLoginTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		PasswordHash:   "test",
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	t.Run("renderLoginTemplate success", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/login", http.NoBody)
		rec := httptest.NewRecorder()
		server.renderLoginTemplate(rec, req, "", http.StatusOK)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Enter password")
		assert.Contains(t, rec.Body.String(), "Cronn Dashboard")
	})

	t.Run("renderLoginTemplate with error message", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/login", http.NoBody)
		rec := httptest.NewRecorder()
		server.renderLoginTemplate(rec, req, "Test error message", http.StatusUnauthorized)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "Test error message")
	})

	t.Run("renderLoginTemplate template not found", func(t *testing.T) {
		// temporarily remove template
		originalTemplate := server.templates["login"]
		delete(server.templates, "login")
		defer func() {
			server.templates["login"] = originalTemplate
		}()

		req := httptest.NewRequest("GET", "/login", http.NoBody)
		rec := httptest.NewRecorder()
		server.renderLoginTemplate(rec, req, "", http.StatusOK)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("renderLoginTemplate template execution error", func(t *testing.T) {
		// create a template with an error
		badTemplate := template.Must(template.New("login").Parse(`{{.NonExistentField}}`))
		originalTemplate := server.templates["login"]
		server.templates["login"] = badTemplate
		defer func() {
			server.templates["login"] = originalTemplate
		}()

		req := httptest.NewRequest("GET", "/login", http.NoBody)
		rec := httptest.NewRecorder()
		server.renderLoginTemplate(rec, req, "", http.StatusOK)

		// should return 500 on template execution error
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestServer_LoginRateLimiting(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	passwordHash := "$2y$10$qOIpGITktzktHpcnWXiow.penxJmMcapV3G2ZRQaK0QRW7BSmAuJG" //nolint:gosec // test password hash

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		PasswordHash:   passwordHash,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	handler := server.routes()

	t.Run("login rate limiting blocks after 5 attempts", func(t *testing.T) {
		// use unique IP for this test
		testIP := "10.0.0.1:12345"

		// make attempts until rate limited (should happen within 6 attempts)
		var attempts int
		for attempts < 10 { // safety limit
			req := httptest.NewRequest("POST", "/login", strings.NewReader("password=wrongpass"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.RemoteAddr = testIP
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			attempts++
			if rec.Code == http.StatusTooManyRequests {
				// rate limiting is working
				assert.Contains(t, rec.Body.String(), "Too many login attempts")
				break
			}
			// should get 401 for wrong password before rate limit
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Contains(t, rec.Body.String(), "Invalid password")
		}

		// verify we actually hit rate limit
		assert.True(t, attempts <= 6, "Rate limiting should kick in within 6 attempts")

		// make one more request to confirm it's still rate limited
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=wrongpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.RemoteAddr = testIP
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// should still be rate limited (429 Too Many Requests)
		assert.Equal(t, http.StatusTooManyRequests, rec.Code)
		assert.Contains(t, rec.Body.String(), "Too many login attempts")
	})

	t.Run("successful login still works within rate limit", func(t *testing.T) {
		// use a different remote addr to simulate different IP for rate limiting
		// make a few failed attempts (within limit)
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("POST", "/login", strings.NewReader("password=wrongpass"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.RemoteAddr = "192.168.1.100:12345" // different IP than default
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		}

		// successful login should work
		req := httptest.NewRequest("POST", "/login", strings.NewReader("password=testpass"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.RemoteAddr = "192.168.1.100:12345" // same different IP
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/", rec.Header().Get("Location"))
	})
}
