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
	if subjectType != "log" && subjectType != "list" && subjectType != "profile" && subjectType != "post" {
		s.notFound(w, r)
		return
	}
	maxChars := s.instanceConfig(r.Context()).MaxCommentChars
	if maxChars <= 0 {
		maxChars = 5000
	}
	if body == "" || len(body) > maxChars {
		s.flash(w, r, "error", "Comments need between 1 and "+strconv.Itoa(maxChars)+" characters.")
		s.redirect(w, r, back)
		return
	}
	if owner, ok := s.st.CommentSubjectOwner(r.Context(), subjectType, subjectID); !ok {
		s.notFound(w, r)
		return
	} else if !s.st.CommentAllowed(r.Context(), u.ID, owner) {
		s.flash(w, r, "error", "This user does not accept comments from you.")
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
	if subjectType == "post" {
		if p, err := s.st.PostByID(r.Context(), subjectID, u.ID); err == nil {
			inReplyTo := p.APID
			if parent != nil {
				if pc, err := s.st.CommentByID(r.Context(), *parent); err == nil && pc.APID != "" {
					inReplyTo = pc.APID
				}
			}
			apID := s.absolute("/posts/" + itoa(p.ID) + "#c" + itoa(id))
			s.st.SetCommentAPID(r.Context(), id, apID, inReplyTo)
			s.notifyContext(r, p.UserID, u.ID, "comment", "post", p.ID, "/posts/"+itoa(p.ID))
			s.deliverCommentActivity(r, u, p, id, body, inReplyTo, apID)
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
	c, _ := s.st.CommentByID(r.Context(), id)
	if err := s.st.DeleteComment(r.Context(), id, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if c != nil && c.SubjectType == "post" && c.APID != "" {
		s.deliverCommentDelete(r, u, c)
	}
	s.redirect(w, r, back)
}

func (s *Server) reportPage(w http.ResponseWriter, r *http.Request) {
	targetType := r.URL.Query().Get("target_type")
	targetID, _ := strconv.ParseInt(r.URL.Query().Get("target_id"), 10, 64)
	allowed := map[string]bool{"user": true, "log": true, "list": true, "comment": true, "post": true, "remote": true}
	if !allowed[targetType] {
		s.notFound(w, r)
		return
	}
	returnTo := safeReturnTo(r.URL.Query().Get("return_to"))
	if returnTo == "" {
		returnTo = "/"
	}
	targetURL, targetName := r.URL.Query().Get("target_url"), ""
	switch targetType {
	case "post":
		p, err := s.st.PostByID(r.Context(), targetID, currentUser(r).ID)
		if err != nil {
			s.notFound(w, r)
			return
		}
		targetURL = s.absolute("/posts/" + itoa(p.ID))
		targetName = postTitle(p)
	case "user":
		if u, err := s.st.UserByID(r.Context(), targetID); err == nil {
			targetName = u.Username
			targetURL = s.absolute("/users/" + u.Username)
		}
	case "log":
		if l, err := s.st.LogByID(r.Context(), targetID); err == nil {
			targetName = l.Name
			if l.User != nil {
				targetURL = s.absolute("/users/" + l.User.Username + "/reviews/" + itoa(l.ID))
			}
		}
	case "list":
		if l, err := s.st.ListByID(r.Context(), targetID, false); err == nil {
			targetName = l.Title
		}
	}
	if targetURL == "" {
		targetURL = r.URL.Query().Get("target_url")
	}
	instance := ""
	if host := hostOf(targetURL); host != "" && host != hostOf(s.cfg.BaseURL) {
		instance = host
	}
	s.render(w, r, "report", reportData{
		Page: s.page(r, "Report"), TargetType: targetType, TargetID: targetID,
		TargetURL: targetURL, TargetName: targetName, TargetInstance: instance,
		CanForward: instance != "" && targetType != "user", ReturnTo: returnTo,
	})
}

type reportData struct {
	Page
	TargetType     string
	TargetID       int64
	TargetURL      string
	TargetName     string
	TargetInstance string
	CanForward     bool
	ReturnTo       string
}

func (s *Server) reportCreate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	targetType := r.FormValue("target_type")
	allowed := map[string]bool{"user": true, "log": true, "list": true, "comment": true, "post": true, "remote": true}
	if !allowed[targetType] {
		s.notFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("target_id"), 10, 64)
	reason := strings.TrimSpace(r.FormValue("reason"))
	details := strings.TrimSpace(r.FormValue("details"))
	targetURL := r.FormValue("target_url")
	if targetType == "post" {
		p, err := s.st.PostByID(r.Context(), id, u.ID)
		if err != nil {
			s.notFound(w, r)
			return
		}
		targetURL = s.absolute("/posts/" + itoa(p.ID))
	}
	if reason == "" {
		s.flash(w, r, "error", "Choose a reason for the report.")
		s.redirect(w, r, safeReturnTo(r.FormValue("return_to")))
		return
	}
	forward := r.FormValue("forward") == "1" && hostOf(targetURL) != "" && hostOf(targetURL) != hostOf(s.cfg.BaseURL)
	reportID, err := s.st.CreateReportDetailed(r.Context(), &u.ID, targetType, id, targetURL, reason, details, "", "", false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if forward {
		if err := s.forwardFlag(r, u, targetURL, reason, details, reportID); err == nil {
			s.st.MarkReportForwarded(r.Context(), reportID)
		}
	}
	s.flash(w, r, "success", "Thanks — a moderator will review this report.")
	back := safeReturnTo(r.FormValue("return_to"))
	if back == "" {
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
