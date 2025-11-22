package web

import (
	"encoding/json"
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
	t.Run("returns 404 when neighbors not configured", func(t *testing.T) {
		srv := &Server{neighborsURL: ""}
		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Neighbors not configured")
	})

	t.Run("fetches from HTTP URL and caches", func(t *testing.T) {
		neighbors := []NeighborInstance{
			{Name: "server1", URL: "http://server1.example.com"},
			{Name: "server2", URL: "http://server2.example.com"},
		}

		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(neighbors)
		}))
		defer mockServer.Close()

		srv := &Server{neighborsURL: mockServer.URL}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var result []NeighborInstance
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, neighbors, result)

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
		}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result []NeighborInstance
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, cachedNeighbors, result)
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

		srv := &Server{neighborsURL: "file://" + filePath}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result []NeighborInstance
		err = json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, neighbors, result)
	})

	t.Run("returns 502 when HTTP fetch fails", func(t *testing.T) {
		srv := &Server{neighborsURL: "http://nonexistent.invalid"}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusBadGateway, w.Code)
	})

	t.Run("returns 502 when file read fails", func(t *testing.T) {
		srv := &Server{neighborsURL: "file:///nonexistent/path/neighbors.json"}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusBadGateway, w.Code)
	})

	t.Run("returns 502 when JSON is invalid", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "invalid.json")
		err := os.WriteFile(filePath, []byte("not valid json"), 0o600)
		require.NoError(t, err)

		srv := &Server{neighborsURL: "file://" + filePath}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusBadGateway, w.Code)
	})

	t.Run("returns 502 when HTTP server returns non-200", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer mockServer.Close()

		srv := &Server{neighborsURL: mockServer.URL}

		req := httptest.NewRequest(http.MethodGet, "/api/neighbors", http.NoBody)
		w := httptest.NewRecorder()
		srv.handleNeighbors(w, req)

		assert.Equal(t, http.StatusBadGateway, w.Code)
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
}
