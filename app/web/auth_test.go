package web

import (
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
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		handler := server.routes()
		req := httptest.NewRequest("GET", "/", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
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
