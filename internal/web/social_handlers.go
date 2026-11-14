package web

import (
	"net/http"
	"strconv"
	"strings"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func (s *Server) notifyContext(r *http.Request, target, actor int64, kind, objectType string, objectID int64, url string) {
	if target == actor {
		return
	}
	s.st.Notify(r.Context(), target, kind, &actor, objectType, objectID, "", url)
}

func (s *Server) notifyMentions(r *http.Request, u *models.User, objectType string, objectID int64, body, url string) {
	for _, m := range s.st.MentionUsers(r.Context(), body, 20) {
		if m.ID == u.ID {
			continue
		}
		s.st.Notify(r.Context(), m.ID, "mention", &u.ID, objectType, objectID, "", url)
	}
}

func (s *Server) commentCreate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	subjectType := r.FormValue("subject_type")
	subjectID, _ := strconv.ParseInt(r.FormValue("subject_id"), 10, 64)
	body := strings.TrimSpace(r.FormValue("body"))
	back := r.FormValue("return_to")
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/"
	}
	if subjectType != "log" && subjectType != "list" && subjectType != "profile" {
		s.notFound(w, r)
		return
	}
	if body == "" || len(body) > 5000 {
		s.flash(w, r, "error", "Comments need between 1 and 5000 characters.")
		s.redirect(w, r, back)
		return
	}
	var parent *int64
	if v := r.FormValue("parent_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			parent = &id
		}
	}
	id, err := s.st.CreateComment(r.Context(), &u.ID, subjectType, subjectID, parent, body, "", "", "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if subjectType == "log" {
		if l, err := s.st.LogByID(r.Context(), subjectID); err == nil {
			s.notifyContext(r, l.UserID, u.ID, "comment", "log", subjectID, r.URL.Path)
		}
	}
	s.notifyMentions(r, u, "comment", id, body, back)
	if parent != nil {
		if c, err := s.st.CommentByID(r.Context(), *parent); err == nil && c.UserID != nil {
			s.notifyContext(r, *c.UserID, u.ID, "reply", "comment", *parent, back)
		}
	}
	s.redirect(w, r, back)
}

func (s *Server) commentDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	back := r.FormValue("return_to")
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/"
	}
	if err := s.st.DeleteComment(r.Context(), id, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirect(w, r, back)
}

func (s *Server) reportCreate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	targetType := r.FormValue("target_type")
	allowed := map[string]bool{"user": true, "log": true, "list": true, "comment": true, "remote": true}
	if !allowed[targetType] {
		s.notFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("target_id"), 10, 64)
	reason := strings.TrimSpace(r.FormValue("reason"))
	details := strings.TrimSpace(r.FormValue("details"))
	if reason == "" {
		s.flash(w, r, "error", "Choose a reason for the report.")
		s.redirect(w, r, r.FormValue("return_to"))
		return
	}
	if err := s.st.CreateReport(r.Context(), &u.ID, targetType, id, r.FormValue("target_url"), reason, details); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Thanks — a moderator will review this report.")
	back := r.FormValue("return_to")
	if !strings.HasPrefix(back, "/") {
		back = "/"
	}
	s.redirect(w, r, back)
}

type notificationsData struct {
	Page
	Notifications []*models.Notification
	Total         int
	PageNum       int
	Pages         int
	UnreadCount   int
}

func (s *Server) notificationsPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	page := atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	items, total := s.st.Notifications(r.Context(), u.ID, 30, (page-1)*30)
	s.render(w, r, "notifications", notificationsData{
		Page: s.page(r, "Notifications"), Notifications: items, Total: total, PageNum: page,
		Pages: (total + 29) / 30, UnreadCount: s.st.UnreadNotifications(r.Context(), u.ID),
	})
}

func (s *Server) notificationsRead(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	s.st.MarkNotificationsRead(r.Context(), u.ID, id)
	if id > 0 {
		s.redirect(w, r, "/notifications")
		return
	}
	s.redirect(w, r, "/notifications")
}

var _ = store.ErrNotFound
