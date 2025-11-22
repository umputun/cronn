package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	log "github.com/go-pkgz/lgr"
)

// NeighborInstance represents a neighboring cronn instance
type NeighborInstance struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// neighborsTemplateData holds data for neighbors template
type neighborsTemplateData struct {
	Neighbors []NeighborInstance
	Error     string
}

// handleNeighbors returns HTML fragment with list of neighbor instances
func (s *Server) handleNeighbors(w http.ResponseWriter, _ *http.Request) {
	tmpl := s.templates["partials/jobs.html"]
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if s.neighborsURL == "" {
		if err := tmpl.ExecuteTemplate(w, "neighbors-error", neighborsTemplateData{Error: "Neighbors not configured"}); err != nil {
			log.Printf("[ERROR] failed to execute neighbors-error template: %v", err)
		}
		return
	}

	// check cache (5 minute TTL)
	s.neighborsMu.RLock()
	if s.neighborsCache != nil && time.Since(s.neighborsCacheTime) < 5*time.Minute {
		neighbors := s.neighborsCache
		s.neighborsMu.RUnlock()
		s.renderNeighbors(w, tmpl, neighbors)
		return
	}
	s.neighborsMu.RUnlock()

	// fetch from external URL
	neighbors, err := s.fetchNeighbors()
	if err != nil {
		log.Printf("[ERROR] failed to fetch neighbors from %s: %v", s.neighborsURL, err)
		if err := tmpl.ExecuteTemplate(w, "neighbors-error", neighborsTemplateData{Error: "Failed to load neighbors"}); err != nil {
			log.Printf("[ERROR] failed to execute neighbors-error template: %v", err)
		}
		return
	}

	// update cache
	s.neighborsMu.Lock()
	s.neighborsCache = neighbors
	s.neighborsCacheTime = time.Now()
	s.neighborsMu.Unlock()

	s.renderNeighbors(w, tmpl, neighbors)
}

// renderNeighbors filters neighbors by valid URL and renders the template
func (s *Server) renderNeighbors(w http.ResponseWriter, tmpl *template.Template, neighbors []NeighborInstance) {
	// filter neighbors with valid http/https URLs to prevent XSS
	valid := make([]NeighborInstance, 0, len(neighbors))
	for _, n := range neighbors {
		u, err := url.Parse(n.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			log.Printf("[WARN] skipping neighbor with invalid URL: %s", n.URL)
			continue
		}
		valid = append(valid, n)
	}

	if err := tmpl.ExecuteTemplate(w, "neighbors-list", neighborsTemplateData{Neighbors: valid}); err != nil {
		log.Printf("[ERROR] failed to execute neighbors-list template: %v", err)
	}
}

// fetchNeighbors fetches neighbor instances from the configured URL (supports http://, https://, file://)
func (s *Server) fetchNeighbors() ([]NeighborInstance, error) {
	var body []byte
	var err error

	u, parseErr := url.Parse(s.neighborsURL)
	if parseErr != nil {
		return nil, fmt.Errorf("invalid neighbors URL %q: %w", s.neighborsURL, parseErr)
	}

	switch u.Scheme {
	case "file":
		body, err = readNeighborsFile(u.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to read neighbors file %s: %w", u.Path, err)
		}
	case "http", "https":
		body, err = s.fetchNeighborsHTTP()
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported scheme in neighbors URL: %q", u.Scheme)
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
