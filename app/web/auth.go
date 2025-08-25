package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/web/enums"
)

// handleLoginForm displays the login form
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
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
		Theme: s.getTheme(r),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] failed to render login template: %v", err)
		http.Error(w, "Failed to render login page", http.StatusInternalServerError)
	}
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

	// password is valid, set auth cookie
	// create a simple signed token (hash of password hash + secret)
	// this is safe because the password hash itself is the secret
	token := s.generateAuthToken()

	http.SetCookie(w, &http.Cookie{
		Name:     "cronn-auth",
		Value:    token,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60, // 7 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})

	// redirect to dashboard
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout logs the user out by clearing the auth cookie
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// clear the auth cookie by setting MaxAge to -1
	http.SetCookie(w, &http.Cookie{
		Name:     "cronn-auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1, // delete cookie
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})

	// tell HTMX to perform a full page refresh instead of swapping content
	w.Header().Set("HX-Refresh", "true")

	// redirect to login page
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// renderLoginError renders the login form with an error message
func (s *Server) renderLoginError(w http.ResponseWriter, r *http.Request, errorMsg string) {
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
	w.WriteHeader(http.StatusUnauthorized)
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("[ERROR] failed to render login template with error: %v", err)
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

		// check auth cookie
		cookie, err := r.Cookie("cronn-auth")
		if err == nil && s.validateAuthToken(cookie.Value) {
			next.ServeHTTP(w, r)
			return
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

// generateAuthToken generates a simple auth token
func (s *Server) generateAuthToken() string {
	// use the password hash itself as the token
	// this is safe because it's already a one-way hash
	h := sha256.Sum256([]byte(s.passwordHash + "cronn-auth-token"))
	return hex.EncodeToString(h[:])
}

// validateAuthToken checks if the auth token is valid
func (s *Server) validateAuthToken(token string) bool {
	expected := s.generateAuthToken()
	return token == expected
}
