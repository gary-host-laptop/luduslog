package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

type adminData struct {
	Page
	Users   []*models.User
	Total   int
	Reports []*models.Report
	Domains []domainRow
	Jobs    []models.Job
	News    []models.News
	Actions []models.ModAction
	Audit   []store.AuditEntry
	Query   string
	Stats   map[string]int
	Tab     string
	Error   string
}

type domainRow struct {
	Domain    string
	Reason    string
	CreatedAt time.Time
}

func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	stats := map[string]int{}
	rows := []struct {
		key string
		sql string
	}{
		{"users", `SELECT COUNT(*) FROM users`},
		{"games", `SELECT COUNT(*) FROM games`},
		{"logs", `SELECT COUNT(*) FROM logs WHERE deleted_at IS NULL`},
		{"reviews", `SELECT COUNT(*) FROM logs WHERE review<>'' AND deleted_at IS NULL`},
		{"lists", `SELECT COUNT(*) FROM lists WHERE deleted_at IS NULL`},
		{"reports", `SELECT COUNT(*) FROM reports WHERE status='open'`},
		{"remote", `SELECT COUNT(*) FROM remote_actors`},
		{"deliveries", `SELECT COUNT(*) FROM deliveries WHERE attempts>0`},
	}
	for _, row := range rows {
		var n int
		s.st.DB.QueryRowContext(r.Context(), row.sql).Scan(&n)
		stats[row.key] = n
	}
	jobs, _ := s.st.Jobs(r.Context(), 20)
	data := adminData{Page: s.page(r, "Admin"), Stats: stats, Jobs: jobs, Tab: "overview"}
	if data.IsAdmin {
		data.Audit, _ = s.st.AuditEntries(r.Context(), 20)
	}
	s.render(w, r, "admin", data)
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	users, total, err := s.st.AllUsers(r.Context(), q, 50, (page-1)*50)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "admin_users", adminData{Page: s.page(r, "Users · Admin"), Users: users, Total: total, Query: q, Tab: "users"})
}

func (s *Server) adminSetRole(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	target, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	role := r.FormValue("role")
	if role != "user" && role != "mod" && role != "admin" {
		s.flash(w, r, "error", "Invalid role.")
		s.redirect(w, r, "/admin/users")
		return
	}
	if target.ID == u.ID {
		s.flash(w, r, "error", "You cannot change your own role.")
		s.redirect(w, r, "/admin/users")
		return
	}
	if target.Role == "admin" && role != "admin" && s.st.AdminCount(r.Context()) <= 1 {
		s.flash(w, r, "error", "The instance needs at least one administrator.")
		s.redirect(w, r, "/admin/users")
		return
	}
	if err := s.st.SetRole(r.Context(), id, role); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.st.Audit(r.Context(), u.ID, "set_role", target.Username+" -> "+role); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Set "+target.Username+"'s role to "+role+".")
	s.redirect(w, r, "/admin/users")
}

// adminSetCatalogEditor grants or revokes the trusted catalog editor privilege.
func (s *Server) adminSetCatalogEditor(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	target, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if err := s.st.SetCatalogEditor(r.Context(), id, enabled); err != nil {
		s.serverError(w, r, err)
		return
	}
	action := "revoke_catalog_editor"
	if enabled {
		action = "grant_catalog_editor"
	}
	if err := s.st.Audit(r.Context(), u.ID, action, target.Username); err != nil {
		s.serverError(w, r, err)
		return
	}
	if enabled {
		s.flash(w, r, "success", target.Username+" can now edit the game catalog.")
	} else {
		s.flash(w, r, "success", target.Username+" can no longer edit the game catalog.")
	}
	s.redirect(w, r, "/admin/users")
}

func (s *Server) adminSuspend(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	target, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if target.ID == u.ID {
		s.flash(w, r, "error", "You cannot suspend yourself.")
		s.redirect(w, r, "/admin/users")
		return
	}
	if target.IsAdmin() && !u.IsAdmin() {
		s.flash(w, r, "error", "Moderators cannot suspend administrators.")
		s.redirect(w, r, "/admin/users")
		return
	}
	action := "suspend"
	if target.DeactivatedAt != nil {
		action = "unsuspend"
		if err := s.st.SetDeactivated(r.Context(), id, nil); err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.st.SetApproved(r.Context(), id, true); err != nil {
			s.serverError(w, r, err)
			return
		}
	} else {
		now := store.Now()
		if err := s.st.SetDeactivated(r.Context(), id, &now); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if err := s.st.LogModAction(r.Context(), u.ID, action, &id, r.FormValue("reason"), nil); err != nil {
		s.flash(w, r, "error", "The action was applied but could not be logged: "+err.Error())
		s.redirect(w, r, "/admin/users")
		return
	}
	s.flash(w, r, "success", target.Username+" "+action+"ed.")
	s.redirect(w, r, "/admin/users")
}

// adminDeleteUser permanently removes another account. Administrators cannot
// delete themselves from this page; the store blocks removing the last admin.
func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	target, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if target.ID == u.ID {
		s.flash(w, r, "error", "You cannot delete your own account from the admin page.")
		s.redirect(w, r, "/admin/users")
		return
	}
	if err := s.st.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			s.flash(w, r, "error", "The instance needs at least one administrator.")
			s.redirect(w, r, "/admin/users")
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.deleteUserUploads(id)
	if err := s.st.Audit(r.Context(), u.ID, "delete_user", target.Username); err != nil {
		s.flash(w, r, "error", "The account was deleted but the audit entry failed: "+err.Error())
		s.redirect(w, r, "/admin/users")
		return
	}
	s.flash(w, r, "success", target.Username+"'s account was permanently deleted.")
	s.redirect(w, r, "/admin/users")
}

func (s *Server) adminReports(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	reports, err := s.st.Reports(r.Context(), status)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "admin_reports", adminData{Page: s.page(r, "Reports · Admin"), Reports: reports, Tab: "reports"})
}

func (s *Server) adminResolveReport(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	status := r.FormValue("status")
	if status != "resolved" && status != "dismissed" && status != "open" {
		status = "resolved"
	}
	if err := s.st.ResolveReport(r.Context(), id, status); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.st.LogModAction(r.Context(), u.ID, status+"_report", nil, r.FormValue("resolution"), &id); err != nil {
		s.flash(w, r, "error", "Report updated but the moderation log entry failed: "+err.Error())
		s.redirect(w, r, "/admin/reports")
		return
	}
	s.redirect(w, r, "/admin/reports")
}

func (s *Server) adminModLog(w http.ResponseWriter, r *http.Request) {
	actions, err := s.st.ModActions(r.Context(), 200)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "admin_modlog", adminData{Page: s.page(r, "Moderation log"), Actions: actions, Tab: "modlog"})
}

func (s *Server) adminRemoveContent(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id <= 0 {
		s.notFound(w, r)
		return
	}
	var owner *int64
	action := ""
	switch r.PathValue("type") {
	case "log", "review":
		l, err := s.st.LogByID(r.Context(), id)
		if err != nil {
			s.notFound(w, r)
			return
		}
		owner = &l.UserID
		action = "remove_review"
		if err := s.st.ModDeleteLog(r.Context(), id); err != nil {
			s.serverError(w, r, err)
			return
		}
	case "comment":
		c, err := s.st.CommentByID(r.Context(), id)
		if err != nil {
			s.notFound(w, r)
			return
		}
		owner = c.UserID
		action = "remove_comment"
		if err := s.st.ModDeleteComment(r.Context(), id); err != nil {
			s.serverError(w, r, err)
			return
		}
	case "list":
		l, err := s.st.ListByID(r.Context(), id, true)
		if err != nil {
			s.notFound(w, r)
			return
		}
		owner = &l.UserID
		action = "remove_list"
		if err := s.st.ModDeleteList(r.Context(), id); err != nil {
			s.serverError(w, r, err)
			return
		}
	case "post":
		p, err := s.st.PostByID(r.Context(), id, 0)
		if err != nil {
			s.notFound(w, r)
			return
		}
		owner = &p.UserID
		action = "remove_post"
		if err := s.st.DeletePost(r.Context(), id, p.UserID); err != nil {
			s.serverError(w, r, err)
			return
		}
	default:
		s.notFound(w, r)
		return
	}
	if err := s.st.LogModAction(r.Context(), u.ID, action, owner, r.FormValue("reason"), nil); err != nil {
		s.flash(w, r, "error", "Content removed but the moderation log entry failed: "+err.Error())
		s.redirect(w, r, "/admin/reports")
		return
	}
	s.flash(w, r, "success", "Content removed.")
	s.redirect(w, r, "/admin/reports")
}

func (s *Server) adminFederation(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.st.DB.QueryContext(r.Context(), `SELECT domain,reason,created_at FROM domain_blocks ORDER BY created_at DESC`)
	var domains []domainRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var d domainRow
			var created string
			rows.Scan(&d.Domain, &d.Reason, &created)
			d.CreatedAt = parseTimeSafe(created)
			domains = append(domains, d)
		}
	}
	s.render(w, r, "admin_federation", adminData{Page: s.page(r, "Federation · Admin"), Domains: domains, Tab: "federation"})
}

func parseTimeSafe(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (s *Server) adminDomainBlock(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(strings.TrimSpace(r.FormValue("domain")))
	domain = strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	domain = strings.TrimSuffix(domain, "/")
	if domain != "" {
		s.st.BlockDomain(r.Context(), domain, strings.TrimSpace(r.FormValue("reason")))
	}
	s.redirect(w, r, "/admin/federation")
}

func (s *Server) adminDomainUnblock(w http.ResponseWriter, r *http.Request) {
	s.st.UnblockDomain(r.Context(), strings.TrimSpace(r.FormValue("domain")))
	s.redirect(w, r, "/admin/federation")
}

func (s *Server) adminJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.st.Jobs(r.Context(), 100)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "admin_jobs", adminData{Page: s.page(r, "Jobs · Admin"), Jobs: jobs, Tab: "jobs"})
}

func (s *Server) adminNews(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.st.DB.QueryContext(r.Context(), `SELECT id,title,body,created_at FROM news ORDER BY created_at DESC LIMIT 50`)
	var news []models.News
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var n models.News
			var created string
			rows.Scan(&n.ID, &n.Title, &n.Body, &created)
			n.CreatedAt = parseTimeSafe(created)
			news = append(news, n)
		}
	}
	s.render(w, r, "admin_news", adminData{Page: s.page(r, "News · Admin"), News: news, Tab: "news"})
}

func (s *Server) adminNewsCreate(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	if title != "" && body != "" {
		s.st.DB.ExecContext(r.Context(), `INSERT INTO news(title,body,created_at) VALUES(?,?,?)`, title, body, store.Now())
	}
	s.redirect(w, r, "/admin/news")
}
