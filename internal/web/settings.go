package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"luduslog/internal/auth"
	"luduslog/internal/backloggd"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

type settingsData struct {
	Page
	Tab      string
	Error    string
	Links    []models.SocialLink
	Blocks   []*models.User
	Folders  []models.Folder
	Sessions []models.Session
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	tab := r.URL.Query().Get("tab")
	data := settingsData{Page: s.page(r, "Settings"), Tab: tab, Links: s.st.SocialLinksFor(r.Context(), u.ID)}
	if tab == "blocks" {
		data.Blocks, _ = s.st.BlockedUsers(r.Context(), u.ID)
	}
	if tab == "folders" {
		data.Folders, _ = s.st.Folders(r.Context(), u.ID)
	}
	s.render(w, r, "settings", data)
}

func (s *Server) settingsProfile(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	links := parseLinks(r.FormValue("links"))
	err := s.st.UpdateProfile(r.Context(), u.ID, store.ProfileUpdate{
		Bio:              strings.TrimSpace(r.FormValue("bio")),
		SocialLinks:      links,
		Avatar:           strings.TrimSpace(r.FormValue("avatar")),
		Header:           strings.TrimSpace(r.FormValue("header")),
		PrivateProfile:   u.PrivateProfile,
		HideActivity:     u.HideActivity,
		ManuallyApproves: r.FormValue("manually_approves") == "1",
		HideSocialGraph:  r.FormValue("hide_social_graph") == "1",
		AdultOptIn:       u.AdultOptIn,
		StarScale:        u.StarScale,
		CoverMode:        u.CoverMode,
		TimeDisplay:      u.TimeDisplay,
		InfiniteScroll:   u.InfiniteScroll,
		Theme:            u.Theme,
		HighContrast:     u.HighContrast,
		Timezone:         u.Timezone,
		FadePlayed:       u.FadePlayed,
		JournalView:      u.JournalView,
		CommentPrivacy:   u.CommentPrivacy,
		NotePrivacy:      u.NotePrivacy,
		Federation:       u.Federation,
		FedStatuses:      u.FedStatuses,
		FedReviews:       u.FedReviews,
		FedLists:         u.FedLists,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Profile updated.")
	s.redirect(w, r, "/settings")
}

func parseLinks(raw string) []models.SocialLink {
	var out []models.SocialLink
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		site, url := "website", line
		if parts := strings.SplitN(line, ":", 2); len(parts) == 2 && !strings.HasPrefix(line, "http") {
			site, url = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
		out = append(out, models.SocialLink{Site: site, URL: url})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func (s *Server) settingsPreferences(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	scale, _ := strconv.Atoi(r.FormValue("star_scale"))
	if scale != 5 && scale != 10 {
		scale = 5
	}
	theme := r.FormValue("theme")
	if theme != "system" && theme != "dark" && theme != "light" {
		theme = "system"
	}
	cover := r.FormValue("cover_mode")
	if cover != "any" && cover != "mine" && cover != "none" {
		cover = "any"
	}
	err := s.st.UpdateProfile(r.Context(), u.ID, store.ProfileUpdate{
		Bio: u.Bio, SocialLinks: s.st.SocialLinksFor(r.Context(), u.ID), Avatar: u.Avatar, Header: u.Header,
		PrivateProfile: u.PrivateProfile, HideActivity: u.HideActivity,
		ManuallyApproves: u.ManuallyApproves, HideSocialGraph: u.HideSocialGraph, AdultOptIn: u.AdultOptIn,
		StarScale: scale, CoverMode: cover, TimeDisplay: r.FormValue("time_display"),
		InfiniteScroll: r.FormValue("infinite_scroll") == "1", Theme: theme,
		HighContrast: r.FormValue("high_contrast") == "1", Timezone: r.FormValue("timezone"),
		FadePlayed: r.FormValue("fade_played") == "1", JournalView: r.FormValue("journal_view"),
		CommentPrivacy: r.FormValue("comment_privacy"), NotePrivacy: r.FormValue("note_privacy"),
		Federation: u.Federation, FedStatuses: u.FedStatuses, FedReviews: u.FedReviews, FedLists: u.FedLists,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Preferences saved.")
	s.redirect(w, r, "/settings?tab=preferences")
}

func (s *Server) settingsPrivacy(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	err := s.st.UpdateProfile(r.Context(), u.ID, store.ProfileUpdate{
		Bio: u.Bio, SocialLinks: s.st.SocialLinksFor(r.Context(), u.ID), Avatar: u.Avatar, Header: u.Header,
		PrivateProfile:   r.FormValue("private_profile") == "1",
		HideActivity:     r.FormValue("hide_activity") == "1",
		ManuallyApproves: r.FormValue("manually_approves") == "1",
		HideSocialGraph:  r.FormValue("hide_social_graph") == "1",
		AdultOptIn:       r.FormValue("adult_optin") == "1",
		StarScale:        u.StarScale, CoverMode: u.CoverMode, TimeDisplay: u.TimeDisplay,
		InfiniteScroll: u.InfiniteScroll, Theme: u.Theme, HighContrast: u.HighContrast,
		Timezone: u.Timezone, FadePlayed: u.FadePlayed, JournalView: u.JournalView,
		CommentPrivacy: r.FormValue("comment_privacy"), NotePrivacy: r.FormValue("note_privacy"),
		Federation: u.Federation, FedStatuses: u.FedStatuses, FedReviews: u.FedReviews, FedLists: u.FedLists,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Privacy settings saved.")
	s.redirect(w, r, "/settings?tab=privacy")
}

func (s *Server) settingsFederation(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	err := s.st.UpdateProfile(r.Context(), u.ID, store.ProfileUpdate{
		Bio: u.Bio, SocialLinks: s.st.SocialLinksFor(r.Context(), u.ID), Avatar: u.Avatar, Header: u.Header,
		PrivateProfile: u.PrivateProfile, HideActivity: u.HideActivity, ManuallyApproves: u.ManuallyApproves,
		HideSocialGraph: u.HideSocialGraph, AdultOptIn: u.AdultOptIn, StarScale: u.StarScale,
		CoverMode: u.CoverMode, TimeDisplay: u.TimeDisplay, InfiniteScroll: u.InfiniteScroll,
		Theme: u.Theme, HighContrast: u.HighContrast, Timezone: u.Timezone, FadePlayed: u.FadePlayed,
		JournalView: u.JournalView, CommentPrivacy: u.CommentPrivacy, NotePrivacy: u.NotePrivacy,
		Federation:  r.FormValue("federation") == "1",
		FedStatuses: r.FormValue("fed_statuses") == "1",
		FedReviews:  r.FormValue("fed_reviews") == "1",
		FedLists:    r.FormValue("fed_lists") == "1",
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Federation settings saved.")
	s.redirect(w, r, "/settings?tab=federation")
}

func (s *Server) settingsAccount(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	action := r.FormValue("action")
	switch action {
	case "email":
		email := strings.TrimSpace(r.FormValue("email"))
		if !strings.Contains(email, "@") {
			s.flash(w, r, "error", "Enter a valid email address.")
			s.redirect(w, r, "/settings?tab=account")
			return
		}
		s.st.UpdateEmail(r.Context(), u.ID, email)
		if s.mail.Configured() {
			if token, err := s.auth.CreateEmailToken(r.Context(), u.ID, "verify", email, 48*time.Hour); err == nil {
				s.mail.Send(email, "Verify your new email", "Confirm your address:\n"+s.absolute("/verify?token="+token))
			}
		} else {
			s.st.SetEmailVerified(r.Context(), u.ID)
		}
		s.flash(w, r, "success", "Email updated.")
	case "username":
		name := strings.TrimSpace(r.FormValue("username"))
		if !usernameRE.MatchString(name) {
			s.flash(w, r, "error", "Usernames are 3–24 characters: letters, numbers and underscores.")
			s.redirect(w, r, "/settings?tab=account")
			return
		}
		if s.st.UsernameChangedRecently(u) {
			s.flash(w, r, "error", "You can change your username once every 30 days.")
			s.redirect(w, r, "/settings?tab=account")
			return
		}
		exists, _ := s.st.UserExists(r.Context(), name, "x@invalid")
		if exists {
			s.flash(w, r, "error", "That username is taken.")
			s.redirect(w, r, "/settings?tab=account")
			return
		}
		if err := s.st.ChangeUsername(r.Context(), u.ID, name); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.flash(w, r, "success", "Username changed. Old URLs redirect through your profile.")
		s.redirect(w, r, "/settings?tab=account")
		return
	case "password":
		if !auth.VerifyPassword(u.PasswordHash, r.FormValue("current")) {
			s.flash(w, r, "error", "Current password is incorrect.")
			s.redirect(w, r, "/settings?tab=account")
			return
		}
		next := r.FormValue("password")
		if len(next) < 10 {
			s.flash(w, r, "error", "New password must be at least 10 characters.")
			s.redirect(w, r, "/settings?tab=account")
			return
		}
		hash, err := auth.HashPassword(next)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		s.st.SetUserPassword(r.Context(), u.ID, hash)
		sess := currentSession(r)
		s.auth.DestroyUserSessions(r.Context(), u.ID, sess.Token)
		s.flash(w, r, "success", "Password changed. Other sessions were signed out.")
	}
	s.redirect(w, r, "/settings?tab=account")
}

func (s *Server) settingsDeactivate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if !auth.VerifyPassword(u.PasswordHash, r.FormValue("password")) {
		s.flash(w, r, "error", "Password is incorrect.")
		s.redirect(w, r, "/settings?tab=account")
		return
	}
	now := store.Now()
	s.st.SetDeactivated(r.Context(), u.ID, &now)
	s.auth.DestroyUserSessions(r.Context(), u.ID, "")
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	s.redirect(w, r, "/")
}

func (s *Server) blocksPage(w http.ResponseWriter, r *http.Request) {
	s.redirect(w, r, "/settings?tab=blocks")
}

type libraryData struct {
	Page
	Entries   []models.LibraryEntry
	Platforms []models.Platform
	Game      *models.Game
}

func (s *Server) libraryPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	entries, _ := s.st.LibraryFor(r.Context(), u.ID)
	s.render(w, r, "library", libraryData{Page: s.page(r, "Library"), Entries: entries, Platforms: s.st.Platforms(r.Context())})
}

func (s *Server) librarySave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	gameID, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
	if gameID == 0 {
		if g, err := s.st.GameBySlug(r.Context(), r.FormValue("game_slug")); err == nil {
			gameID = g.ID
		}
	}
	var platform *int64
	if v := r.FormValue("platform_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			platform = &id
		}
	}
	if gameID > 0 {
		s.st.SaveLibraryEntry(r.Context(), u.ID, gameID, platform, r.FormValue("ownership"), r.FormValue("storefront"), r.FormValue("edition"))
		s.flash(w, r, "success", "Library updated.")
	}
	s.redirect(w, r, "/library")
}

func (s *Server) libraryRemove(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	gameID, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
	s.st.RemoveLibraryEntry(r.Context(), u.ID, gameID)
	s.flash(w, r, "success", "Removed from library.")
	s.redirect(w, r, "/library")
}

func (s *Server) exportData(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	logs, _, _ := s.st.UserLogs(r.Context(), u.ID, false, 10000, 0)
	lists, _ := s.st.ListsForUser(r.Context(), u.ID, true, 1000, 0)
	library, _ := s.st.LibraryFor(r.Context(), u.ID)
	type exportLog struct {
		Game       string  `json:"game"`
		Rating     float64 `json:"rating"`
		Review     string  `json:"review"`
		Status     string  `json:"status"`
		Playtime   float64 `json:"playtime"`
		StartedAt  string  `json:"started_at"`
		FinishedAt string  `json:"finished_at"`
	}
	out := map[string]any{
		"user":        map[string]any{"username": u.Username, "bio": u.Bio, "created_at": u.CreatedAt},
		"exported_at": time.Now().UTC(),
		"logs":        []exportLog{},
		"lists":       lists,
		"library":     library,
	}
	var el []exportLog
	for _, l := range logs {
		name := ""
		if l.Game != nil {
			name = l.Game.Title
		}
		el = append(el, exportLog{name, l.Rating, l.Review, l.Status, l.Playtime, l.StartedAt, l.FinishedAt})
	}
	out["logs"] = el
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="luduslog-`+u.Username+`.json"`)
	json.NewEncoder(w).Encode(out)
}

func (s *Server) importData(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	file, header, err := r.FormFile("file")
	if err != nil {
		s.flash(w, r, "error", "Choose a Backloggd JSON or CSV export.")
		s.redirect(w, r, "/settings?tab=import")
		return
	}
	defer file.Close()
	entries, err := backloggd.Parse(io.LimitReader(file, 16<<20), header.Filename)
	if err != nil {
		s.flash(w, r, "error", "Could not read that export: "+err.Error())
		s.redirect(w, r, "/settings?tab=import")
		return
	}
	// Uploads cannot wait for hundreds of IGDB requests. Games that are not
	// in the catalog are reported; the CLI importer can fetch them.
	res, err := backloggd.Import(r.Context(), s.st, entries, backloggd.ImportOptions{UserID: u.ID})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := res.Summary()
	if len(res.Missing) > 0 {
		msg += ". Run `luduslog import backloggd` from the command line to fetch missing games from IGDB."
	}
	level := "success"
	if len(res.Errors) > 0 {
		level = "error"
		msg += ". First error: " + res.Errors[0]
	}
	s.flash(w, r, level, msg)
	s.redirect(w, r, "/users/"+u.Username)
}
