package web

import (
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"luduslog/internal/auth"
	"luduslog/internal/models"
)

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_]{3,24}$`)

type authPage struct {
	Page
	Identifier string
	Next       string
	Error      string
	Code       int
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		s.redirect(w, r, "/")
		return
	}
	s.render(w, r, "login", authPage{Page: s.page(r, "Log in"), Next: r.URL.Query().Get("next")})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	identifier := strings.TrimSpace(r.FormValue("identifier"))
	password := r.FormValue("password")
	next := r.FormValue("next")
	ip := s.clientIP(r)
	if !s.auth.RateLimit(r.Context(), "login:"+ip, 12, 15*time.Minute) {
		page := authPage{Page: s.page(r, "Log in"), Identifier: identifier, Next: next, Error: "Too many attempts. Try again in a few minutes."}
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, r, "login", page)
		return
	}
	var user *models.User
	var err error
	if strings.Contains(identifier, "@") {
		user, err = s.st.UserByEmail(r.Context(), identifier)
	} else {
		user, err = s.st.UserByUsername(r.Context(), identifier)
	}
	if err != nil || !auth.VerifyPassword(user.PasswordHash, password) {
		page := authPage{Page: s.page(r, "Log in"), Identifier: identifier, Next: next, Error: "Incorrect username or password."}
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, "login", page)
		return
	}
	if user.DeactivatedAt != nil {
		page := authPage{Page: s.page(r, "Log in"), Identifier: identifier, Error: "This account is deactivated."}
		w.WriteHeader(http.StatusForbidden)
		s.render(w, r, "login", page)
		return
	}
	if !user.Approved {
		page := authPage{Page: s.page(r, "Log in"), Identifier: identifier, Error: "Your account is waiting for approval by an administrator."}
		w.WriteHeader(http.StatusForbidden)
		s.render(w, r, "login", page)
		return
	}
	if s.mail.Configured() && !user.EmailVerified {
		page := authPage{Page: s.page(r, "Log in"), Identifier: identifier, Error: "Please verify your email address. Check your inbox for the link."}
		w.WriteHeader(http.StatusForbidden)
		s.render(w, r, "login", page)
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.st.AddActivity(r.Context(), user.ID, "joined", nil, nil, nil, "", "public")
	if next != "" && strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		s.redirect(w, r, next)
		return
	}
	s.redirect(w, r, "/")
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *models.User) error {
	sess, err := s.auth.CreateSession(r.Context(), u.ID, r.UserAgent(), s.clientIP(r))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sess.Token, Path: "/", HttpOnly: true,
		Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
		Expires: sess.ExpiresAt, MaxAge: int((30 * 24 * time.Hour).Seconds()),
	})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.auth.DestroySession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	s.redirect(w, r, "/")
}

func (s *Server) registerForm(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Registration == "closed" {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, "Registration closed"), "Registration is currently closed on this instance."})
		return
	}
	s.render(w, r, "register", authPage{Page: s.page(r, "Create an account")})
}

func (s *Server) registerSubmit(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	emailAddr := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")
	invite := strings.TrimSpace(r.FormValue("invite_code"))
	page := authPage{Page: s.page(r, "Create an account"), Identifier: username}
	fail := func(msg string, code int) {
		page.Error = msg
		w.WriteHeader(code)
		s.render(w, r, "register", page)
	}
	if s.cfg.Registration == "closed" {
		fail("Registration is closed on this instance.", http.StatusForbidden)
		return
	}
	if !s.auth.RateLimit(r.Context(), "register:"+s.clientIP(r), 5, time.Hour) {
		fail("Too many registration attempts. Try again later.", http.StatusTooManyRequests)
		return
	}
	if !usernameRE.MatchString(username) {
		fail("Usernames are 3–24 characters: letters, numbers and underscores.", http.StatusBadRequest)
		return
	}
	if _, err := mail.ParseAddress(emailAddr); err != nil || !strings.Contains(emailAddr, ".") {
		fail("Enter a valid email address.", http.StatusBadRequest)
		return
	}
	if len(password) < 10 {
		fail("Passwords must be at least 10 characters.", http.StatusBadRequest)
		return
	}
	if password != confirm {
		fail("Passwords do not match.", http.StatusBadRequest)
		return
	}
	if s.cfg.Registration == "invite" && (s.cfg.InviteCode == "" || invite != s.cfg.InviteCode) {
		fail("That invite code is not valid.", http.StatusForbidden)
		return
	}
	userExists, emailExists := s.st.UserExists(r.Context(), username, emailAddr)
	if userExists {
		fail("That username is taken.", http.StatusConflict)
		return
	}
	if emailExists {
		fail("An account with that email already exists.", http.StatusConflict)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	admin := s.st.CountUsers(r.Context()) == 0
	u, err := s.st.CreateUser(r.Context(), username, emailAddr, hash, admin)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	verified := !s.mail.Configured()
	if verified {
		s.st.SetEmailVerified(r.Context(), u.ID)
	}
	if s.cfg.Registration == "approval" {
		s.st.SetApproved(r.Context(), u.ID, false)
	}
	if !verified {
		token, err := s.auth.CreateEmailToken(r.Context(), u.ID, "verify", emailAddr, 48*time.Hour)
		if err == nil {
			s.mail.Send(emailAddr, "Verify your "+s.cfg.Name+" email",
				"Welcome to "+s.cfg.Name+"!\n\nVerify your address by opening:\n"+s.absolute("/verify?token="+token))
		}
	}
	if s.cfg.Registration == "approval" {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, "Account pending"), "Thanks for signing up. An administrator needs to approve your account before you can log in."})
		return
	}
	if !verified {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, "Check your email"), "We sent a verification link to " + emailAddr + ". Open it to finish signing up."})
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Welcome to "+s.cfg.Name+", "+u.Username+"!")
	s.redirect(w, r, "/")
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	userID, _, err := s.auth.ConsumeEmailToken(r.Context(), token, "verify")
	if err != nil {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, "Invalid link"), "That verification link is invalid or has expired."})
		return
	}
	s.st.SetEmailVerified(r.Context(), userID)
	if u, err := s.st.UserByID(r.Context(), userID); err == nil && u.Approved {
		s.startSession(w, r, u)
	}
	s.flash(w, r, "success", "Your email is verified.")
	s.redirect(w, r, "/settings")
}

func (s *Server) forgotForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "forgot", authPage{Page: s.page(r, "Reset your password")})
}

func (s *Server) forgotSubmit(w http.ResponseWriter, r *http.Request) {
	emailAddr := strings.TrimSpace(r.FormValue("email"))
	if s.auth.RateLimit(r.Context(), "forgot:"+s.clientIP(r), 5, time.Hour) {
		if u, err := s.st.UserByEmail(r.Context(), emailAddr); err == nil {
			if token, err := s.auth.CreateEmailToken(r.Context(), u.ID, "reset", emailAddr, time.Hour); err == nil {
				s.mail.Send(emailAddr, "Reset your "+s.cfg.Name+" password",
					"Reset your password by opening:\n"+s.absolute("/reset?token="+token)+"\n\nThis link expires in one hour.")
			}
		}
	}
	s.render(w, r, "message", struct {
		Page
		Message string
	}{s.page(r, "Check your email"), "If an account exists for that address, we sent a password reset link."})
}

func (s *Server) resetForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "reset", authPage{Page: s.page(r, "Choose a new password")})
}

func (s *Server) resetSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")
	page := authPage{Page: s.page(r, "Choose a new password"), Error: ""}
	if len(password) < 10 {
		page.Error = "Passwords must be at least 10 characters."
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "reset", page)
		return
	}
	if password != confirm {
		page.Error = "Passwords do not match."
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "reset", page)
		return
	}
	userID, _, err := s.auth.ConsumeEmailToken(r.Context(), token, "reset")
	if err != nil {
		page.Error = "That reset link is invalid or has expired."
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "reset", page)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.st.SetUserPassword(r.Context(), userID, hash)
	s.auth.DestroyUserSessions(r.Context(), userID, "")
	s.flash(w, r, "success", "Your password was changed. You can log in now.")
	s.redirect(w, r, "/login")
}
