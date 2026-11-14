package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	flag := r.FormValue("flag")
	on := r.FormValue("on") != "0"
	switch flag {
	case "played", "playing", "backlog", "wishlist", "liked":
	default:
		s.notFound(w, r)
		return
	}
	if err := s.st.SetStatusFlag(r.Context(), u.ID, g.ID, flag, on); err != nil {
		s.serverError(w, r, err)
		return
	}
	if flag == "liked" {
		s.st.AddActivity(r.Context(), u.ID, "liked", &g.ID, nil, nil, "", "public")
	} else {
		s.st.AddActivity(r.Context(), u.ID, "status", &g.ID, nil, nil, flag, "public")
	}
	if s.isHTMX(r) {
		st, _ := s.st.StatusFor(r.Context(), u.ID, g.ID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		csrf, _ := r.Context().Value(ctxCSRF).(string)
		w.Write([]byte(statusBarHTML(g, st, true, csrf)))
		return
	}
	s.redirect(w, r, "/games/"+g.Slug)
}

func statusBarHTML(g *models.Game, st models.Status, loggedIn bool, csrf string) string {
	if !loggedIn {
		return `<a class="btn btn-sm" href="/login">Log in to track this game</a>`
	}
	flags := []struct {
		key, label string
		on         bool
	}{
		{"played", "Played", st.Played},
		{"playing", "Playing", st.Playing},
		{"backlog", "Backlog", st.Backlog},
		{"wishlist", "Wishlist", st.Wishlist},
		{"liked", "Liked", st.Liked},
	}
	var b strings.Builder
	b.WriteString(`<form class="status-bar" hx-post="/games/` + g.Slug + `/status" hx-target="this" hx-swap="outerHTML">`)
	b.WriteString(`<input type="hidden" name="_csrf" value="` + csrf + `">`)
	for _, f := range flags {
		on := "0"
		cls := ""
		if f.on {
			on = "1"
			cls = " on"
		}
		b.WriteString(`<button type="submit" name="flag" value="` + f.key + `" data-on="` + on + `" class="` + strings.TrimSpace(cls) + `">` + f.label + `</button>`)
	}
	b.WriteString(`</form>`)
	return b.String()
}

func (s *Server) likeGame(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	st, _ := s.st.StatusFor(r.Context(), u.ID, g.ID)
	s.st.SetStatusFlag(r.Context(), u.ID, g.ID, "liked", !st.Liked)
	likes, users := s.st.GameLikes(r.Context(), g.ID, 5)
	if s.isHTMX(r) {
		s.renderFragment(w, "game", "game_likes", map[string]any{"Game": g, "Likes": likes, "Likers": users, "User": u})
		return
	}
	s.redirect(w, r, "/games/"+g.Slug)
}

type logFormData struct {
	Page
	Game      *models.Game
	Log       *models.Log
	Platforms []models.Platform
	Editions  []models.Game
	Error     string
	Action    string
}

func (s *Server) logForm(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	g, _ = s.st.GameDetail(r.Context(), g)
	u := currentUser(r)
	data := logFormData{Page: s.page(r, "Log "+g.Title), Game: g, Platforms: s.st.Platforms(r.Context())}
	if l, err := s.st.LatestLogFor(r.Context(), u.ID, g.ID); err == nil {
		data.Log = l
	}
	if r.URL.Query().Get("review") == "1" {
		data.Log = &models.Log{UserID: u.ID, GameID: g.ID}
	}
	data.Action = "/games/" + g.Slug + "/log"
	s.render(w, r, "log_form", data)
}

func (s *Server) logEditForm(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	g, _ := s.st.GameByID(r.Context(), l.GameID)
	g, _ = s.st.GameDetail(r.Context(), g)
	s.render(w, r, "log_form", logFormData{
		Page: s.page(r, "Edit log"), Game: g, Log: l, Platforms: s.st.Platforms(r.Context()), Action: "/logs/" + itoa(id) + "/edit",
	})
}

func logInputFrom(r *http.Request, u *models.User, gameID int64) store.LogInput {
	in := store.LogInput{
		UserID:       u.ID,
		GameID:       gameID,
		Rating:       atof(r.FormValue("rating")),
		Review:       strings.TrimSpace(r.FormValue("review")),
		Spoiler:      r.FormValue("spoiler") == "1",
		Status:       r.FormValue("status"),
		Replay:       r.FormValue("replay") == "1",
		Mastered:     r.FormValue("mastered") == "1",
		Difficulty:   r.FormValue("difficulty"),
		Ownership:    r.FormValue("ownership"),
		Storefront:   r.FormValue("storefront"),
		StartedAt:    r.FormValue("started_at"),
		FinishedAt:   r.FormValue("finished_at"),
		Playtime:     atof(r.FormValue("playtime")),
		SyncSessions: r.FormValue("sync_sessions") == "1",
		Language:     r.FormValue("language"),
		Draft:        r.FormValue("draft") == "1",
	}
	if v := r.FormValue("platform_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.PlatformID = &id
		}
	}
	if v := r.FormValue("played_on_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.PlayedOnID = &id
		}
	}
	if v := r.FormValue("edition_game_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.EditionGameID = &id
		}
	}
	if in.Rating < 0 {
		in.Rating = 0
	}
	return in
}

func (s *Server) logSubmit(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	in := logInputFrom(r, u, g.ID)
	if in.Status == "" {
		in.Status = "played"
	}
	l, err := s.st.CreateLog(r.Context(), in)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.st.SetStatusFlag(r.Context(), u.ID, g.ID, statusFlag(in.Status), !in.Draft)
	if in.Status != "wishlist" && in.Status != "backlog" {
		s.st.SetStatusFlag(r.Context(), u.ID, g.ID, "played", true)
	}
	if !in.Draft {
		s.st.AddActivity(r.Context(), u.ID, "log", &g.ID, &l.ID, nil, "", "public")
		s.st.CheckBadges(r.Context(), u.ID)
		if in.Review != "" {
			s.notifyMentions(r, u, "log", l.ID, in.Review, "/users/"+u.Username+"/reviews/"+itoa(l.ID))
		}
	}
	s.flash(w, r, "success", "Your log for "+g.Title+" was saved.")
	if in.Review != "" && !in.Draft {
		s.redirect(w, r, "/users/"+u.Username+"/reviews/"+itoa(l.ID))
		return
	}
	s.redirect(w, r, "/games/"+g.Slug)
}

func statusFlag(status string) string {
	switch status {
	case "playing":
		return "playing"
	case "backlog":
		return "backlog"
	case "wishlist":
		return "wishlist"
	default:
		return "played"
	}
}

func (s *Server) logEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	in := logInputFrom(r, u, l.GameID)
	if _, err := s.st.UpdateLog(r.Context(), id, in); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Log updated.")
	s.redirect(w, r, "/users/"+u.Username+"/reviews/"+itoa(id))
}

func (s *Server) logDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || (l.UserID != u.ID && !u.CanModerate()) {
		s.notFound(w, r)
		return
	}
	if l.UserID != u.ID {
		if err := s.st.ModDeleteLog(r.Context(), id); err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.st.LogModAction(r.Context(), u.ID, "remove_review", &l.UserID, r.FormValue("reason"), nil); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.flash(w, r, "success", "Log removed.")
		s.redirect(w, r, "/users/"+l.User.Username)
		return
	}
	s.st.DeleteLog(r.Context(), id, l.UserID)
	s.st.RefreshGameStats(r.Context(), l.GameID)
	s.flash(w, r, "success", "Log deleted.")
	s.redirect(w, r, "/users/"+u.Username)
}

func (s *Server) likeLog(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	wasLiked := s.st.HasLikedLog(r.Context(), id, u.ID)
	if err := s.st.LikeLog(r.Context(), id, u.ID, !wasLiked); err != nil {
		s.serverError(w, r, err)
		return
	}
	liked := !wasLiked
	if liked && l.UserID != u.ID {
		s.notifyContext(r, l.UserID, u.ID, "like", "log", id, s.absolute("/users/"+u.Username+"/reviews/"+itoa(id)))
	}
	if s.isHTMX(r) {
		count := s.st.CountLogLikes(r.Context(), id)
		s.renderFragment(w, "partials", "like_button", map[string]any{"Count": count, "On": liked, "Action": "/logs/" + itoa(id) + "/like"})
		return
	}
	s.redirect(w, r, "/users/"+l.User.Username+"/reviews/"+itoa(id))
}

type sessionsData struct {
	Page
	Log      *models.Log
	Game     *models.Game
	Sessions []models.Session
	Error    string
}

func (s *Server) sessionList(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	g, _ := s.st.GameByID(r.Context(), l.GameID)
	sessions, _ := s.st.SessionsFor(r.Context(), id)
	s.render(w, r, "sessions", sessionsData{Page: s.page(r, "Play sessions"), Log: l, Game: g, Sessions: sessions})
}

func (s *Server) sessionCreate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	days := []string{}
	for _, d := range r.Form["days"] {
		if _, err := time.Parse("2006-01-02", d); err == nil {
			days = append(days, d)
		}
	}
	_, err = s.st.CreateSession(r.Context(), store.SessionInput{
		LogID:       id,
		Note:        strings.TrimSpace(r.FormValue("note")),
		NotePrivacy: r.FormValue("privacy"),
		Playtime:    atof(r.FormValue("playtime")),
		Days:        days,
		Tags:        nonEmpty(r.Form["tags"]),
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Session added.")
	s.redirect(w, r, "/logs/"+itoa(id)+"/sessions")
}

func (s *Server) sessionDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	if err := s.st.DeleteSession(r.Context(), id, u.ID); err != nil {
		s.notFound(w, r)
		return
	}
	s.redirect(w, r, r.Header.Get("Referer"))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
