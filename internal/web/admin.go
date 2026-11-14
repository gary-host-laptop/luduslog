package web

import (
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
		{"games", `SELECT COUNT(*) FROM games WHERE deleted_at IS NULL`},
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
	s.render(w, r, "admin", adminData{Page: s.page(r, "Admin"), Stats: stats, Jobs: jobs, Tab: "overview"})
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

func (s *Server) adminToggleAdmin(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	target, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if target.ID == u.ID {
		s.flash(w, r, "error", "You cannot change your own admin flag.")
		s.redirect(w, r, "/admin/users")
		return
	}
	s.st.SetAdmin(r.Context(), id, !target.IsAdmin)
	s.st.LogModAction(r.Context(), u.ID, "toggle_admin", "user", id, "")
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
	if target.DeactivatedAt != nil {
		s.st.SetDeactivated(r.Context(), id, nil)
		s.st.SetApproved(r.Context(), id, true)
		s.st.LogModAction(r.Context(), u.ID, "unsuspend", "user", id, r.FormValue("reason"))
	} else {
		now := store.Now()
		s.st.SetDeactivated(r.Context(), id, &now)
		s.st.LogModAction(r.Context(), u.ID, "suspend", "user", id, r.FormValue("reason"))
	}
	s.redirect(w, r, "/admin/users")
}

func (s *Server) adminReports(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	reports, err := s.st.Reports(r.Context(), status)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "admin_reports", adminData{Page: s.page(r, "Reports · Admin"), Reports: reports, Tab: "reports"})
}

func (s *Server) adminResolveReport(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	status := r.FormValue("status")
	if status != "resolved" && status != "dismissed" && status != "open" {
		status = "resolved"
	}
	s.st.ResolveReport(r.Context(), id, status)
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
