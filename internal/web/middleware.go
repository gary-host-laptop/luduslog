package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
	ctxCSRF
	ctxFlash
)

const (
	sessionCookie = "luduslog_session"
	csrfCookie    = "luduslog_csrf"
	flashCookie   = "luduslog_flash"
)

func (s *Server) secureCookies() bool { return strings.HasPrefix(s.cfg.BaseURL, "https://") }

func randomID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 20 {
		return c.Value
	}
	token := randomID(24)
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: token, Path: "/", HttpOnly: false,
		Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 3600,
	})
	return token
}

func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrf := s.ensureCSRF(w, r)
		ctx := context.WithValue(r.Context(), ctxCSRF, csrf)

		var sess *authSession
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if as, err := s.auth.LookupSession(r.Context(), c.Value); err == nil {
				if u, err := s.st.UserByID(r.Context(), as.UserID); err == nil && u.DeactivatedAt == nil {
					sess = &authSession{Token: c.Value, CSRF: as.CSRF}
					ctx = context.WithValue(ctx, ctxUser, u)
					ctx = context.WithValue(ctx, ctxSession, sess)
					if sess.CSRF != "" {
						ctx = context.WithValue(ctx, ctxCSRF, sess.CSRF)
					}
				} else {
					s.auth.DestroySession(r.Context(), c.Value)
					http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
				}
			}
		}
		// One-shot flash message.
		if c, err := r.Cookie(flashCookie); err == nil && c.Value != "" {
			var f []Flash
			if raw, err := base64.RawURLEncoding.DecodeString(c.Value); err == nil {
				json.Unmarshal(raw, &f)
			}
			ctx = context.WithValue(ctx, ctxFlash, f)
			http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		}
		if csrf != "" {
			ctx = context.WithValue(ctx, ctxCSRF, ctx.Value(ctxCSRF))
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type authSession struct {
	Token string
	CSRF  string
}

func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if strings.HasSuffix(r.URL.Path, "/inbox") {
				next.ServeHTTP(w, r)
				return
			}
			token := r.FormValue("_csrf")
			if token == "" {
				token = r.Header.Get("X-CSRF-Token")
			}
			c, err := r.Cookie(csrfCookie)
			expected, _ := r.Context().Value(ctxCSRF).(string)
			if err != nil || token == "" || (token != c.Value && token != expected) {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://images.igdb.com https://*.igdb.com; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if s.log != nil && !strings.HasPrefix(r.URL.Path, "/static/") {
			s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start).String())
		}
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setFlash(w http.ResponseWriter, kind, text string) {
	raw, _ := json.Marshal([]Flash{{Kind: kind, Text: text}})
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: base64.RawURLEncoding.EncodeToString(raw), Path: "/",
		HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: 60,
	})
}

func (s *Server) flash(w http.ResponseWriter, r *http.Request, kind, text string) {
	s.setFlash(w, kind, text)
}

func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r); u == nil {
			nextURL := r.URL.RequestURI()
			http.Redirect(w, r, "/login?next="+urlQuery(nextURL), http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireMod(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r); !u.CanModerate() {
			s.notFound(w, r)
			return
		}
		next(w, r)
	})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r); !u.IsAdmin() {
			s.notFound(w, r)
			return
		}
		next(w, r)
	})
}

func currentUser(r *http.Request) *models.User {
	u, _ := r.Context().Value(ctxUser).(*models.User)
	return u
}

func currentSession(r *http.Request) *authSession {
	sess, _ := r.Context().Value(ctxSession).(*authSession)
	return sess
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func urlQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "%", "%25"), "#", "%23")
}

// w(r) is never used; keep takeFlash for compatibility with the page helper.
func (s *Server) takeFlash(w http.ResponseWriter) []Flash { return nil }

var _ = store.ErrNotFound
