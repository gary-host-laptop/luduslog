package web

import (
	"context"
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
	case "playing", "backlog", "wishlist":
	default:
		s.notFound(w, r)
		return
	}
	if err := s.st.SetStatusFlag(r.Context(), u.ID, g.ID, flag, on); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.st.AddActivity(r.Context(), u.ID, "status", &g.ID, nil, nil, flag, "public")
	s.redirect(w, r, "/games/"+g.Slug)
}

func (s *Server) likeGame(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	liked, err := s.st.ToggleGameLike(r.Context(), u.ID, g.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if liked {
		s.st.AddActivity(r.Context(), u.ID, "liked", &g.ID, nil, nil, "", "public")
	}
	if s.isHTMX(r) {
		likes, _ := s.st.GameLikes(r.Context(), g.ID, 0)
		csrf, _ := r.Context().Value(ctxCSRF).(string)
		s.renderFragment(w, "game", "game_like", map[string]any{
			"Slug": g.Slug, "Liked": liked, "Likes": likes, "CSRF": csrf,
		})
		return
	}
	s.redirect(w, r, "/games/"+g.Slug)
}

// rateGame stores a half-step 5-star rating (1–10) without opening the log
// form. Clearing only removes the rating; it never lowers the play counter.
func (s *Server) rateGame(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	rating := 0.0
	if r.FormValue("clear") != "1" {
		rating = atof(strings.TrimSpace(r.FormValue("rating")))
		if rating <= 0 || rating > 10 {
			s.flash(w, r, "error", "Choose a rating between 1 and 10.")
			s.redirect(w, r, "/games/"+g.Slug)
			return
		}
	}
	if err := s.st.RateGame(r.Context(), u.ID, g.ID, rating); err != nil {
		s.serverError(w, r, err)
		return
	}
	if rating > 0 {
		if err := s.st.EnsurePlayed(r.Context(), u.ID, g.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.st.AddActivity(r.Context(), u.ID, "rated", &g.ID, nil, nil, "", "public")
		s.flash(w, r, "success", "Rating saved.")
	} else {
		s.flash(w, r, "success", "Rating cleared.")
	}
	s.redirect(w, r, "/games/"+g.Slug)
}

type logFormData struct {
	Page
	Game      *models.Game
	Log       *models.Log
	Platforms []models.Platform
	Editions  []models.Game
	Played    int
	Error     string
	Action    string
}

func playedLogStatus(status string) bool {
	return status == "played" || status == "replayed" || status == "completed"
}

// logFormPlayed shows the real counter, defaulting a fresh play-like log to 1.
func (s *Server) logFormPlayed(ctx context.Context, userID, gameID int64, l *models.Log) int {
	st, _ := s.st.StatusFor(ctx, userID, gameID)
	if st.Played > 0 {
		return st.Played
	}
	if l == nil || l.Status == "" || playedLogStatus(l.Status) {
		return 1
	}
	return 0
}

func playedCountFrom(r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.FormValue("played"))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if n < 0 {
		n = 0
	}
	return n, true
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
	data.Played = s.logFormPlayed(r.Context(), u.ID, g.ID, data.Log)
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
		Page: s.page(r, "Edit log"), Game: g, Log: l, Platforms: s.st.Platforms(r.Context()),
		Played: s.logFormPlayed(r.Context(), u.ID, l.GameID, l), Action: "/logs/" + itoa(id) + "/edit",
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
	played, hasPlayed := playedCountFrom(r)
	if !in.Draft {
		switch in.Status {
		case "playing", "backlog", "wishlist":
			s.st.SetStatusFlag(r.Context(), u.ID, g.ID, in.Status, true)
		case "played", "replayed", "completed":
			if !hasPlayed {
				s.st.EnsurePlayed(r.Context(), u.ID, g.ID)
			}
		}
		if hasPlayed {
			if playedLogStatus(in.Status) && played < 1 {
				played = 1
			}
			if err := s.st.SetPlayedCount(r.Context(), u.ID, g.ID, played); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
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
	played, hasPlayed := playedCountFrom(r)
	if !in.Draft {
		if playedLogStatus(in.Status) && !hasPlayed {
			s.st.EnsurePlayed(r.Context(), u.ID, l.GameID)
		}
		if hasPlayed {
			if playedLogStatus(in.Status) && played < 1 {
				played = 1
			}
			if err := s.st.SetPlayedCount(r.Context(), u.ID, l.GameID, played); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
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
