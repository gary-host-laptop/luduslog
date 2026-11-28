package web

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

// maxLogsPerGame keeps one game's journal navigable and mirrors the cap a
// Backloggd log list has.
const maxLogsPerGame = 100

var logTabs = map[string]bool{"play": true, "journal": true, "library": true, "review": true}

// Old editor links used log/time/details for what is now the Play tab.
func logTab(raw string) string {
	switch raw {
	case "log", "time", "details":
		return "play"
	}
	if logTabs[raw] {
		return raw
	}
	return "play"
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	_ = r.ParseForm()
	shelf, newForm := r.Form["shelf"]
	selected := ""
	if newForm {
		selected = shelf[0]
	} else if flag := r.FormValue("flag"); flag != "" {
		// Older status forms toggle one flag; removal clears the shelf.
		if r.FormValue("on") != "0" {
			selected = flag
		}
	}
	selected = strings.ToLower(strings.TrimSpace(selected))
	if selected == "__remove__" {
		selected = ""
	}
	if !store.ValidShelf(selected) {
		s.notFound(w, r)
		return
	}
	if err := s.st.SetShelf(r.Context(), u.ID, g.ID, selected); err != nil {
		s.serverError(w, r, err)
		return
	}
	target := selected + ":1"
	if selected == "" {
		target = "shelf:0"
	}
	s.st.AddActivity(r.Context(), u.ID, "status", &g.ID, nil, nil, target, "public")
	label := "Added to " + selected
	if selected == "" {
		label = "Removed from shelf"
	} else if selected == "playing" {
		label = "Started playing"
	}
	s.deliverStatusActivity(r, u, g, label)
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
	} else {
		s.st.RemoveActivity(r.Context(), u.ID, "liked", g.ID)
	}
	if s.isHTMX(r) {
		likes, _ := s.st.GameLikes(r.Context(), g.ID, 0)
		csrf, _ := r.Context().Value(ctxCSRF).(string)
		s.renderFragment(w, "game", "game_like_fragment", map[string]any{
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
		s.st.AddActivity(r.Context(), u.ID, "rated", &g.ID, nil, nil, strconv.FormatFloat(rating, 'f', -1, 64), "public")
		s.flash(w, r, "success", "Rating saved.")
	} else {
		s.st.RemoveActivity(r.Context(), u.ID, "rated", g.ID)
		s.flash(w, r, "success", "Rating cleared.")
	}
	s.redirect(w, r, "/games/"+g.Slug)
}

type logFormData struct {
	Page
	Game              *models.Game
	Log               *models.Log
	Logs              []*models.Log
	Platforms         []models.Platform
	PlayedOnPlatforms []models.Platform
	Played            int
	Error             string
	Action            string
	Tab               string
	Sessions          []models.Session
	Library           *models.LibraryEntry
	Cover             string
	GameStatus        models.Status
	DefaultStatus     string
	LogCount          int
	MaxLogs           int
	IsNew             bool
	Lists             []*models.List
}

// logPlatformOptions limits the platform pickers to systems the game actually
// released on, keeping a saved value visible if the catalog no longer lists it.
func logPlatformOptions(g *models.Game, l *models.Log) []models.Platform {
	opts := append([]models.Platform{}, g.Platforms...)
	if l == nil {
		return opts
	}
	has := func(id int64) bool {
		for _, p := range opts {
			if p.ID == id {
				return true
			}
		}
		return false
	}
	if l.PlatformID != nil && !has(*l.PlatformID) {
		opts = append(opts, models.Platform{ID: *l.PlatformID, Name: l.PlatformName})
	}
	if l.PlayedOnID != nil && !has(*l.PlayedOnID) {
		opts = append(opts, models.Platform{ID: *l.PlayedOnID, Name: l.PlayedOnName})
	}
	return opts
}

func playedLogStatus(status string) bool {
	return status == "played" || status == "replayed" || status == "completed"
}

// Old defaults used the combined game/played status select. Map those game
// values to played so the new log-only select always has a valid selection.
func playedStatusForForm(v string) string {
	switch v {
	case "playing", "backlog", "wishlist":
		return "played"
	}
	return store.NormalizePlayedStatus(v)
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

// formDuration reads the paired hours/minutes fields used by the editor. When
// neither half is present it falls back to the older decimal-hours field so
// API clients and bookmarks keep working.
func formDuration(r *http.Request, name string) float64 {
	_ = r.ParseForm()
	_, hasHours := r.Form[name+"_hours"]
	_, hasMinutes := r.Form[name+"_minutes"]
	if !hasHours && !hasMinutes {
		return atof(r.FormValue(name))
	}
	hours := atof(r.FormValue(name + "_hours"))
	minutes := atof(r.FormValue(name + "_minutes"))
	if hours < 0 {
		hours = 0
	}
	if minutes < 0 {
		minutes = 0
	}
	return (hours*60 + minutes) / 60
}

func formDurationPresent(r *http.Request, name string) bool {
	_ = r.ParseForm()
	if _, ok := r.Form[name+"_hours"]; ok {
		return true
	}
	if _, ok := r.Form[name+"_minutes"]; ok {
		return true
	}
	_, ok := r.Form[name]
	return ok
}

// logEditorData fills the shared parts of the create and edit editors: every
// log for the game, the selected log's journal and library state, and the
// header status the game carries across all logs.
func (s *Server) logEditorData(r *http.Request, u *models.User, g *models.Game, selected *models.Log, tab string) logFormData {
	ctx := r.Context()
	g, _ = s.st.GameDetail(ctx, g)
	data := logFormData{
		Page:          s.page(r, "Log "+g.Title),
		Game:          g,
		Tab:           logTab(tab),
		DefaultStatus: playedStatusForForm(u.DefaultPlayedStatus),
		MaxLogs:       maxLogsPerGame,
	}
	data.Logs, _ = s.st.UserGameLogs(ctx, u.ID, g.ID, true)
	// The editor opens on the original playthrough. Only this view is
	// reordered; the store keeps newest-first for the log page and export.
	sort.SliceStable(data.Logs, func(i, j int) bool {
		if data.Logs[i].CreatedAt.Equal(data.Logs[j].CreatedAt) {
			return data.Logs[i].ID < data.Logs[j].ID
		}
		return data.Logs[i].CreatedAt.Before(data.Logs[j].CreatedAt)
	})
	data.LogCount = len(data.Logs)
	data.Lists, _ = s.st.ListsForUser(ctx, u.ID, true, 100, 0)
	if selected == nil && len(data.Logs) > 0 {
		selected = data.Logs[0]
	}
	data.Log = selected
	data.IsNew = selected == nil || selected.ID == 0
	if !data.IsNew {
		data.Sessions, _ = s.st.SessionsFor(ctx, selected.ID)
		data.Library, _ = s.st.LibraryEntry(ctx, u.ID, g.ID)
		data.Cover = s.st.CoverFor(ctx, u.ID, g.ID)
	}
	data.Platforms = logPlatformOptions(g, selected)
	data.PlayedOnPlatforms = s.st.Platforms(ctx)
	data.Played = s.logFormPlayed(ctx, u.ID, g.ID, selected)
	data.GameStatus, _ = s.st.StatusFor(ctx, u.ID, g.ID)
	return data
}

func (s *Server) logForm(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	q := r.URL.Query()
	newLog := q.Get("new") == "1" || q.Get("review") == "1"
	var selected *models.Log
	if !newLog {
		if raw := q.Get("log"); raw != "" {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
				if l, err := s.st.LogByID(r.Context(), id); err == nil && l.UserID == u.ID && l.GameID == g.ID {
					selected = l
				}
			}
		}
	}
	data := s.logEditorData(r, u, g, selected, q.Get("tab"))
	if newLog {
		data.Log = &models.Log{UserID: u.ID, GameID: g.ID, Status: data.DefaultStatus}
		data.IsNew = true
		data.Sessions = nil
		data.Library = nil
		data.Cover = ""
		data.Platforms = logPlatformOptions(data.Game, nil)
		data.Played = s.logFormPlayed(r.Context(), u.ID, g.ID, nil)
	}
	if data.IsNew {
		data.Action = "/games/" + g.Slug + "/log"
	} else {
		data.Action = "/logs/" + itoa(data.Log.ID) + "/edit"
	}
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
	data := s.logEditorData(r, u, g, l, r.URL.Query().Get("tab"))
	data.Page = s.page(r, "Edit log")
	data.Action = "/logs/" + itoa(id) + "/edit"
	s.render(w, r, "log_form", data)
}

func logInputFrom(r *http.Request, u *models.User, gameID int64) store.LogInput {
	in := store.LogInput{
		UserID:       u.ID,
		GameID:       gameID,
		Name:         strings.TrimSpace(r.FormValue("name")),
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
		Playtime:     formDuration(r, "playtime"),
		TimeToFinish: formDuration(r, "time_to_finish"),
		TimeToMaster: formDuration(r, "time_to_master"),
		SyncSessions: r.FormValue("sync_sessions") == "1",
		Language:     r.FormValue("language"),
		Draft:        r.FormValue("draft") == "1",
	}
	in.Status = store.NormalizePlayedStatus(in.Status)
	if values, ok := r.Form["primary_rating"]; ok {
		primary := false
		for _, v := range values {
			if v == "1" {
				primary = true
			}
		}
		in.PrimaryRating = &primary
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

// applyGameShelfForm saves the single game-level shelf from the Play tab.
// Older clients without the marker leave the shelf untouched. The return value
// tells the caller whether an explicit shelf was supplied.
func (s *Server) applyGameShelfForm(r *http.Request, userID, gameID int64) bool {
	if r.FormValue("game_shelf_form") != "1" {
		return false
	}
	shelf := ""
	if r.FormValue("game_shelf_remove") != "1" {
		shelf = strings.ToLower(strings.TrimSpace(r.FormValue("game_shelf")))
	}
	if !store.ValidShelf(shelf) {
		shelf = ""
	}
	_ = s.st.SetShelf(r.Context(), userID, gameID, shelf)
	return true
}

// syncLogLibrary keeps the ownership library in step with a saved log: an
// ownership value on the log means the game is in the user's library.
func (s *Server) syncLogLibrary(ctx context.Context, userID, gameID int64, in store.LogInput) {
	if in.Ownership == "" {
		return
	}
	edition := ""
	if in.EditionGameID != nil {
		if eg, err := s.st.GameByID(ctx, *in.EditionGameID); err == nil {
			edition = eg.EditionName
			if edition == "" {
				edition = eg.Title
			}
		}
	}
	s.st.SaveLibraryEntry(ctx, userID, gameID, in.PlatformID, in.Ownership, in.Storefront, edition)
}

func (s *Server) logSubmit(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	in := logInputFrom(r, u, g.ID)
	if strings.TrimSpace(r.FormValue("status")) == "" {
		in.Status = u.DefaultPlayedStatus
	}
	if existing, _ := s.st.UserGameLogs(r.Context(), u.ID, g.ID, true); len(existing) >= maxLogsPerGame && !in.Draft {
		s.flash(w, r, "error", "This game already has the maximum number of logs (100). Delete an old log first.")
		s.redirect(w, r, "/games/"+g.Slug+"/log")
		return
	}
	l, err := s.st.CreateLog(r.Context(), in)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	shelfSet := s.applyGameShelfForm(r, u.ID, g.ID)
	played, hasPlayed := playedCountFrom(r)
	if !in.Draft {
		if !shelfSet && playedLogStatus(in.Status) && !hasPlayed {
			// Older clients sent a combined play status without a shelf. A log
			// still means the game was played, but it never picks a shelf.
			s.st.EnsurePlayed(r.Context(), u.ID, g.ID)
		}
		if hasPlayed {
			if err := s.st.SetPlayedCount(r.Context(), u.ID, g.ID, played); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
	}
	if !in.Draft {
		s.syncLogLibrary(r.Context(), u.ID, g.ID, in)
		s.st.CheckBadges(r.Context(), u.ID)
		if in.Review != "" {
			s.notifyMentions(r, u, "log", l.ID, in.Review, "/users/"+u.Username+"/reviews/"+itoa(l.ID))
		}
	}
	s.flash(w, r, "success", "Your log for "+g.Title+" was saved.")
	s.deliverLogActivity(r, u, l)
	if in.Review != "" && !in.Draft {
		s.redirect(w, r, "/users/"+u.Username+"/reviews/"+itoa(l.ID))
		return
	}
	s.redirect(w, r, "/users/"+u.Username+"/logs/"+g.Slug)
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
	if strings.TrimSpace(r.FormValue("status")) == "" {
		in.Status = u.DefaultPlayedStatus
	}
	// Older forms and API clients may not send the title or manual timing
	// fields; an absent field must not wipe stored values.
	if _, ok := r.Form["name"]; !ok {
		in.Name = l.Name
	}
	if !formDurationPresent(r, "time_to_finish") {
		in.TimeToFinish = l.TimeToFinish
	}
	if !formDurationPresent(r, "time_to_master") {
		in.TimeToMaster = l.TimeToMaster
	}
	updated, err := s.st.UpdateLog(r.Context(), id, in)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	shelfSet := s.applyGameShelfForm(r, u.ID, l.GameID)
	played, hasPlayed := playedCountFrom(r)
	if !in.Draft {
		if !shelfSet && playedLogStatus(in.Status) && !hasPlayed {
			s.st.EnsurePlayed(r.Context(), u.ID, l.GameID)
		}
		if hasPlayed {
			if err := s.st.SetPlayedCount(r.Context(), u.ID, l.GameID, played); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
	}
	if !in.Draft {
		s.syncLogLibrary(r.Context(), u.ID, l.GameID, in)
	}
	s.flash(w, r, "success", "Log updated.")
	s.deliverLogActivity(r, u, updated)
	if to := safeReturnTo(r.FormValue("return_to")); to != "" {
		s.redirect(w, r, to)
		return
	}
	if in.Review != "" && !in.Draft {
		s.redirect(w, r, "/users/"+u.Username+"/reviews/"+itoa(id))
		return
	}
	g, _ := s.st.GameByID(r.Context(), l.GameID)
	slug := ""
	if g != nil {
		slug = g.Slug
	}
	s.redirect(w, r, "/users/"+u.Username+"/logs/"+slug)
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
		s.deleteCanonicalPost(r, u, "log", id)
		if err := s.st.LogModAction(r.Context(), u.ID, "remove_review", &l.UserID, r.FormValue("reason"), nil); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.flash(w, r, "success", "Log removed.")
		s.redirect(w, r, "/users/"+l.User.Username)
		return
	}
	s.st.DeleteLog(r.Context(), id, l.UserID)
	s.deleteCanonicalPost(r, u, "log", id)
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
	sessions, _ := s.st.SessionsForViewer(r.Context(), id, u.ID)
	s.render(w, r, "sessions", sessionsData{Page: s.page(r, "Play sessions"), Log: l, Game: g, Sessions: sessions})
}

func sessionDays(r *http.Request) []string {
	days := []string{}
	for _, d := range r.Form["days"] {
		if _, err := time.Parse("2006-01-02", d); err == nil {
			days = append(days, d)
		}
	}
	return days
}

func sessionTags(r *http.Request) []string {
	var tags []string
	for _, raw := range r.Form["tags"] {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	return tags
}

// sessionUpdate edits a session after confirming the log belongs to the user.
func (s *Server) sessionUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	sess, err := s.st.SessionByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	err = s.st.UpdateSessionFor(r.Context(), id, u.ID, store.SessionInput{
		Note:        strings.TrimSpace(r.FormValue("note")),
		NotePrivacy: store.NormalizeNotePrivacy(r.FormValue("privacy")),
		Playtime:    atof(r.FormValue("playtime")),
		Days:        sessionDays(r),
		Tags:        sessionTags(r),
	})
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.flash(w, r, "success", "Session updated.")
	s.redirect(w, r, sessionReturn(r, "/logs/"+itoa(sess.LogID)+"/sessions"))
}

func (s *Server) sessionCreate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	_, err = s.st.CreateSession(r.Context(), store.SessionInput{
		LogID:       id,
		Note:        strings.TrimSpace(r.FormValue("note")),
		NotePrivacy: store.NormalizeNotePrivacy(r.FormValue("privacy")),
		Playtime:    atof(r.FormValue("playtime")),
		Days:        sessionDays(r),
		Tags:        sessionTags(r),
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Session added.")
	s.redirect(w, r, sessionReturn(r, "/logs/"+itoa(id)+"/sessions"))
}

// gameSessionCreate logs a session straight from a game page, creating the
// minimal container log first when the user has never logged that game.
func (s *Server) gameSessionCreate(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	l, err := s.st.SessionLogFor(r.Context(), u.ID, g.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	_, err = s.st.CreateSession(r.Context(), store.SessionInput{
		LogID:       l.ID,
		Note:        strings.TrimSpace(r.FormValue("note")),
		NotePrivacy: store.NormalizeNotePrivacy(r.FormValue("privacy")),
		Playtime:    atof(r.FormValue("playtime")),
		Days:        sessionDays(r),
		Tags:        sessionTags(r),
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Session added.")
	s.redirect(w, r, sessionReturn(r, "/logs/"+itoa(l.ID)+"/sessions"))
}

// sessionReturn keeps journal edits inside the editor when it sent a local
// return path, and falls back to the standalone sessions page otherwise.
func sessionReturn(r *http.Request, fallback string) string {
	if to := safeReturnTo(r.FormValue("return_to")); to != "" {
		return to
	}
	if ref := safeReturnTo(r.Header.Get("Referer")); ref != "" && strings.Contains(ref, "/edit") {
		return ref
	}
	return fallback
}

func (s *Server) sessionDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	sess, err := s.st.SessionByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.st.DeleteSession(r.Context(), id, u.ID); err != nil {
		s.notFound(w, r)
		return
	}
	s.deleteCanonicalPost(r, u, "session", id)
	s.redirect(w, r, sessionReturn(r, "/logs/"+itoa(sess.LogID)+"/sessions"))
}

// logRename is the editor's log-bar rename action. It changes only the
// display title so the rest of an in-progress edit cannot be lost.
func (s *Server) logRename(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	if err := s.st.RenameLog(r.Context(), id, u.ID, strings.TrimSpace(r.FormValue("name"))); err != nil {
		s.notFound(w, r)
		return
	}
	s.flash(w, r, "success", "Log renamed.")
	s.redirect(w, r, "/logs/"+itoa(id)+"/edit")
}

// logCover stores a custom cover URL for the game in this user's library.
// An empty value clears it and falls back to the catalog cover.
func (s *Server) logCover(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	url := strings.TrimSpace(r.FormValue("cover_url"))
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		s.flash(w, r, "error", "Cover URLs must start with http:// or https://.")
		s.redirect(w, r, "/logs/"+itoa(id)+"/edit?tab=library")
		return
	}
	if err := s.st.SetCover(r.Context(), u.ID, l.GameID, url); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Cover updated.")
	s.redirect(w, r, "/logs/"+itoa(id)+"/edit?tab=library")
}

// logDeleteAll removes every trace of the game in this user's account.
func (s *Server) logDeleteAll(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	g, _ := s.st.GameByID(r.Context(), l.GameID)
	if err := s.st.DeleteGameData(r.Context(), u.ID, l.GameID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "All your logs, sessions and library data for this game were deleted.")
	if g != nil {
		s.redirect(w, r, "/games/"+g.Slug)
		return
	}
	s.redirect(w, r, "/games")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
