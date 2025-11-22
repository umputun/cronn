package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_handleNeighbors(t *testing.T) {
	// create minimal templates for testing
	tmpl := template.Must(template.New("neighbors").Parse(`
		{{define "neighbors-list"}}{{if .Neighbors}}<div class="neighbors-list">{{range .Neighbors}}<a href="{{.URL}}" class="neighbor-item">{{.Name}}</a>{{end}}</div>{{else}}<div class="neighbors-error">No neighbors configured</div>{{end}}{{end}}
		{{define "neighbors-error"}}<div class="neighbors-error">{{.Error}}</div>{{end}}
	`))
	templates := map[string]*template.Template{"partials/jobs.html": tmpl}

	t.Run("returns error message when neighbors not configured", func(t *testing.T) {
		srv := &Server{neighborsURL: "", templates: templates}
		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), "Neighbors not configured")
	})

	t.Run("fetches from HTTP URL and returns HTML", func(t *testing.T) {
		neighbors := []NeighborInstance{
			{Name: "server1", URL: "http://server1.example.com"},
			{Name: "server2", URL: "http://server2.example.com"},
		}

		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(neighbors)
		}))
		defer mockServer.Close()

		srv := &Server{neighborsURL: mockServer.URL, templates: templates}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), "neighbors-list")
		assert.Contains(t, w.Body.String(), "server1")
		assert.Contains(t, w.Body.String(), "server2")
		assert.Contains(t, w.Body.String(), "http://server1.example.com")

		// verify caching
		assert.NotNil(t, srv.neighborsCache)
		assert.Equal(t, neighbors, srv.neighborsCache)
	})

	t.Run("returns cached data within TTL", func(t *testing.T) {
		cachedNeighbors := []NeighborInstance{
			{Name: "cached", URL: "http://cached.example.com"},
		}

		srv := &Server{
			neighborsURL:       "http://should-not-be-called.example.com",
			neighborsCache:     cachedNeighbors,
			neighborsCacheTime: time.Now(),
			templates:          templates,
		}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "cached")
		assert.Contains(t, w.Body.String(), "http://cached.example.com")
	})

	t.Run("fetches from file:// URL", func(t *testing.T) {
		neighbors := []NeighborInstance{
			{Name: "file-server1", URL: "http://file-server1.example.com"},
		}

		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "neighbors.json")
		data, err := json.Marshal(neighbors)
		require.NoError(t, err)
		err = os.WriteFile(filePath, data, 0o600)
		require.NoError(t, err)

		srv := &Server{neighborsURL: "file://" + filePath, templates: templates}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "file-server1")
	})

	t.Run("returns error HTML when HTTP fetch fails", func(t *testing.T) {
		srv := &Server{neighborsURL: "http://nonexistent.invalid", templates: templates}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Failed to load neighbors")
	})

	t.Run("returns error HTML when file read fails", func(t *testing.T) {
		srv := &Server{neighborsURL: "file:///nonexistent/path/neighbors.json", templates: templates}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Failed to load neighbors")
	})

	t.Run("returns error HTML when JSON is invalid", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "invalid.json")
		err := os.WriteFile(filePath, []byte("not valid json"), 0o600)
		require.NoError(t, err)

		srv := &Server{neighborsURL: "file://" + filePath, templates: templates}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Failed to load neighbors")
	})

	t.Run("filters out invalid URLs", func(t *testing.T) {
		neighbors := []NeighborInstance{
			{Name: "valid", URL: "http://valid.example.com"},
			{Name: "javascript-xss", URL: "javascript:alert('xss')"},
			{Name: "also-valid", URL: "https://also-valid.example.com"},
		}

		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(neighbors)
		}))
		defer mockServer.Close()

		srv := &Server{neighborsURL: mockServer.URL, templates: templates}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "valid")
		assert.Contains(t, body, "also-valid")
		assert.NotContains(t, body, "javascript")
		assert.NotContains(t, body, "xss")
	})
}

func TestServer_fetchNeighbors(t *testing.T) {
	t.Run("fetches from HTTP URL", func(t *testing.T) {
		neighbors := []NeighborInstance{
			{Name: "test", URL: "http://test.example.com"},
		}

		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(neighbors)
		}))
		defer mockServer.Close()

		srv := &Server{neighborsURL: mockServer.URL}
		result, err := srv.fetchNeighbors()

		require.NoError(t, err)
		assert.Equal(t, neighbors, result)
	})

	t.Run("fetches from file URL", func(t *testing.T) {
		neighbors := []NeighborInstance{
			{Name: "file-test", URL: "http://file-test.example.com"},
		}

		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "neighbors.json")
		data, err := json.Marshal(neighbors)
		require.NoError(t, err)
		err = os.WriteFile(filePath, data, 0o600)
		require.NoError(t, err)

		srv := &Server{neighborsURL: "file://" + filePath}
		result, err := srv.fetchNeighbors()

		require.NoError(t, err)
		assert.Equal(t, neighbors, result)
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "invalid.json")
		err := os.WriteFile(filePath, []byte("{invalid}"), 0o600)
		require.NoError(t, err)

		srv := &Server{neighborsURL: "file://" + filePath}
		_, err = srv.fetchNeighbors()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse neighbors JSON")
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		srv := &Server{neighborsURL: "file:///nonexistent/file.json"}
		_, err := srv.fetchNeighbors()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read neighbors file")
	})

	t.Run("returns error for unsupported scheme", func(t *testing.T) {
		srv := &Server{neighborsURL: "ftp://server.example.com/neighbors.json"}
		_, err := srv.fetchNeighbors()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported scheme")
	})
}
