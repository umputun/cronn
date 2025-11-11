package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/didip/tollbooth/v8"
	"github.com/didip/tollbooth/v8/limiter"
	"golang.org/x/crypto/bcrypt"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/web/enums"
)

var loginLimiter = func() *limiter.Limiter {
	// allow 5 login attempts per minute (much more reasonable than per second)
	lmt := tollbooth.NewLimiter(5.0/60.0, &limiter.ExpirableOptions{ // 5 per 60 seconds = 0.083 per second
		DefaultExpirationTTL: time.Hour,
	})
	lmt.SetBurst(5) // allow up to 5 attempts in a burst
	lmt.SetMessage("Too many login attempts. Please try again later.")
	return lmt
}()

// handleLoginForm displays the login form
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.renderLoginTemplate(w, r, "", http.StatusOK)
}

// handleLogin processes the login form submission
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	password := r.FormValue("password")
	if password == "" {
		s.renderLoginError(w, r, "Password is required")
		return
	}

	// validate password against bcrypt hash
	if err := bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(password)); err != nil {
		s.renderLoginError(w, r, "Invalid password")
		return
	}

	// password is valid, create secure session
	token, err := s.createSession()
	if err != nil {
		log.Printf("[ERROR] failed to create session: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// use __Host- prefix for enhanced security over HTTPS
	cookieName := "cronn-auth"
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	if secure {
		cookieName = "__Host-cronn-auth"
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   24 * 60 * 60, // 24 hours
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	})

	// redirect to dashboard
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout logs the user out by clearing the auth cookie
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// invalidate session and clear cookie
	// try both cookie names for compatibility
	if cookie, err := r.Cookie("__Host-cronn-auth"); err == nil {
		s.invalidateSession(cookie.Value)
	} else if cookie, err := r.Cookie("cronn-auth"); err == nil {
		s.invalidateSession(cookie.Value)
	}

	// clear both possible cookie names to ensure logout works
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	// always clear the non-secure cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "cronn-auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1, // delete cookie
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	})

	// always attempt to clear the secure __Host- cookie
	// browser will only honor this on HTTPS connection
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-cronn-auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1, // delete cookie
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
	})

	// tell HTMX to perform a full page refresh instead of swapping content
	w.Header().Set("HX-Refresh", "true")

	// redirect to login page
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// renderLoginError renders the login form with an error message
func (s *Server) renderLoginError(w http.ResponseWriter, r *http.Request, errorMsg string) {
	s.renderLoginTemplate(w, r, errorMsg, http.StatusUnauthorized)
}

// renderLoginTemplate renders the login template with optional error message
func (s *Server) renderLoginTemplate(w http.ResponseWriter, r *http.Request, errorMsg string, statusCode int) {
	tmpl := s.templates["login"]
	if tmpl == nil {
		log.Printf("[ERROR] Login template not found in templates map")
		http.Error(w, "Login template not found", http.StatusInternalServerError)
		return
	}
	log.Printf("[DEBUG] Using login template: %v", tmpl.Name())

	data := struct {
		Error string
		Theme enums.Theme
	}{
		Error: errorMsg,
		Theme: s.getTheme(r),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if statusCode != http.StatusOK {
		w.WriteHeader(statusCode)
	}
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] failed to render login template: %v", err)
		http.Error(w, "Failed to render login page", http.StatusInternalServerError)
	}
}

// authMiddleware checks for auth cookie or falls back to basic auth
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// skip auth for login page and static resources
		if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		// check auth cookie (try both names for compatibility)
		for _, cookieName := range []string{"__Host-cronn-auth", "cronn-auth"} {
			if cookie, err := r.Cookie(cookieName); err == nil && s.validateSession(cookie.Value) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// fallback to basic auth for API clients
		username, password, ok := r.BasicAuth()
		if ok && username == "cronn" {
			if err := bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(password)); err == nil {
				next.ServeHTTP(w, r)
				return
			}
		}

		// no valid auth, redirect to login
		if r.Header.Get("Accept") == "" || strings.Contains(r.Header.Get("Accept"), "text/html") {
			// browser request, redirect to login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		} else {
			// API request, return 401
			w.Header().Set("WWW-Authenticate", `Basic realm="Cronn Dashboard"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}
	})
}

// createSession generates a cryptographically secure session token
func (s *Server) createSession() (string, error) {
	// generate 32 random bytes
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	token := hex.EncodeToString(bytes)

	// store session with expiration
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	if s.sessions == nil {
		s.sessions = make(map[string]session)
	}

	s.sessions[token] = session{
		token:     token,
		createdAt: time.Now(),
	}

	// cleanup expired sessions
	s.cleanupExpiredSessions()

	return token, nil
}

// validateSession checks if a session token is valid and not expired
func (s *Server) validateSession(token string) bool {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	sess, exists := s.sessions[token]
	if !exists {
		return false
	}

	// check if session is expired
	if time.Since(sess.createdAt) > s.loginTTL {
		delete(s.sessions, token)
		return false
	}

	return true
}

// invalidateSession removes a session from the store
func (s *Server) invalidateSession(token string) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	delete(s.sessions, token)
}

// cleanupExpiredSessions removes expired sessions (called with lock held)
func (s *Server) cleanupExpiredSessions() {
	now := time.Now()
	for token, sess := range s.sessions {
		if now.Sub(sess.createdAt) > s.loginTTL {
			delete(s.sessions, token)
		}
	}
}
