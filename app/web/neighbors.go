package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	log "github.com/go-pkgz/lgr"
)

// NeighborInstance represents a neighboring cronn instance
type NeighborInstance struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// handleNeighbors returns the list of neighbor instances fetched from external URL
func (s *Server) handleNeighbors(w http.ResponseWriter, _ *http.Request) {
	if s.neighborsURL == "" {
		http.Error(w, "Neighbors not configured", http.StatusNotFound)
		return
	}

	// check cache (5 minute TTL)
	s.neighborsMu.RLock()
	if s.neighborsCache != nil && time.Since(s.neighborsCacheTime) < 5*time.Minute {
		neighbors := s.neighborsCache
		s.neighborsMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(neighbors); err != nil {
			log.Printf("[ERROR] failed to encode neighbors: %v", err)
		}
		return
	}
	s.neighborsMu.RUnlock()

	// fetch from external URL
	neighbors, err := s.fetchNeighbors()
	if err != nil {
		log.Printf("[ERROR] failed to fetch neighbors from %s: %v", s.neighborsURL, err)
		http.Error(w, "Failed to fetch neighbors", http.StatusBadGateway)
		return
	}

	// update cache
	s.neighborsMu.Lock()
	s.neighborsCache = neighbors
	s.neighborsCacheTime = time.Now()
	s.neighborsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(neighbors); err != nil {
		log.Printf("[ERROR] failed to encode neighbors: %v", err)
	}
}

// fetchNeighbors fetches neighbor instances from the configured URL (supports http://, https://, file://)
func (s *Server) fetchNeighbors() ([]NeighborInstance, error) {
	var body []byte
	var err error

	// handle file:// uRLs by reading from local filesystem
	if len(s.neighborsURL) > 7 && s.neighborsURL[:7] == "file://" {
		filePath := s.neighborsURL[7:]
		body, err = readNeighborsFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read neighbors file %s: %w", filePath, err)
		}
	} else {
		// http/https URL
		body, err = s.fetchNeighborsHTTP()
		if err != nil {
			return nil, err
		}
	}

	var neighbors []NeighborInstance
	if err := json.Unmarshal(body, &neighbors); err != nil {
		return nil, fmt.Errorf("failed to parse neighbors JSON: %w", err)
	}

	return neighbors, nil
}

// fetchNeighborsHTTP fetches neighbors from an HTTP/HTTPS URL
func (s *Server) fetchNeighborsHTTP() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.neighborsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch neighbors: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("[WARN] failed to close response body: %v", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return body, nil
}

// readNeighborsFile reads neighbors from a local file with size limit
func readNeighborsFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 - path comes from trusted config
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("[WARN] failed to close file: %v", closeErr)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}
