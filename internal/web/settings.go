package web

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
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
	Tab                string
	Error              string
	Links              []models.SocialLink
	Blocks             []*models.User
	Folders            []models.Folder
	FolderLists        map[int64][]*models.List
	MyLists            []*models.List
	Sessions           []models.Session
	Favorites          []models.Game
	FavoriteSearch     []models.Game
	FavoriteQuery      string
	FeaturedReviews    []*models.Log
	FeaturedLists      []*models.List
	Badges             []models.Badge
	ActivityVisibility map[string]string
	SoleAdmin          bool
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	tab := r.URL.Query().Get("tab")
	if tab == "favourites" {
		http.Redirect(w, r, "/settings#favourites", http.StatusSeeOther)
		return
	}
	data := settingsData{Page: s.page(r, "Settings"), Tab: tab, Links: s.st.SocialLinksFor(r.Context(), u.ID)}
	if tab == "blocks" {
		data.Blocks, _ = s.st.BlockedUsers(r.Context(), u.ID)
	}
	if tab == "privacy" {
		data.ActivityVisibility, _ = s.st.ActivityVisibility(r.Context(), u.ID)
	}
	if tab == "folders" {
		data.Folders, _ = s.st.Folders(r.Context(), u.ID)
		data.FolderLists = map[int64][]*models.List{}
		for _, f := range data.Folders {
			data.FolderLists[f.ID], _ = s.st.FolderLists(r.Context(), f.ID)
		}
		data.MyLists, _ = s.st.ListsForUser(r.Context(), u.ID, true, 100, 0)
	}
	if tab == "account" {
		data.SoleAdmin = u.IsAdmin() && s.st.AdminCount(r.Context()) <= 1
	}
	if tab == "" || tab == "profile" {
		s.st.CheckBadges(r.Context(), u.ID)
		data.Badges, _ = s.st.BadgesFor(r.Context(), u.ID)
		data.Favorites = s.st.FavoriteGames(r.Context(), u.ID, 5)
		data.FeaturedReviews, _ = s.st.PublishedReviewsForUser(r.Context(), u.ID, 100)
		data.FeaturedLists, _ = s.st.ListsForUser(r.Context(), u.ID, true, 100, 0)
		data.FavoriteQuery = strings.TrimSpace(r.URL.Query().Get("q"))
		if data.FavoriteQuery != "" {
			results, _, _ := s.st.SearchGames(r.Context(), data.FavoriteQuery, 8, 0, u.AdultOptIn)
			picked := make(map[int64]bool, len(data.Favorites))
			for _, g := range data.Favorites {
				picked[g.ID] = true
			}
			for _, g := range results {
				if !picked[g.ID] {
					data.FavoriteSearch = append(data.FavoriteSearch, g)
				}
			}
		}
	}
	s.render(w, r, "settings", data)
}

func (s *Server) settingsProfile(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		s.flash(w, r, "error", "The upload was too large. Images must be 5 MiB or smaller.")
		s.redirect(w, r, "/settings")
		return
	}
	avatar, err := s.readImageUpload(r, "avatar_upload", "avatar")
	if err != nil {
		s.flash(w, r, "error", err.Error())
		s.redirect(w, r, "/settings")
		return
	}
	header, err := s.readImageUpload(r, "header_upload", "header")
	if err != nil {
		s.flash(w, r, "error", err.Error())
		s.redirect(w, r, "/settings")
		return
	}
	oldAvatar, oldHeader := u.Avatar, u.Header
	newAvatar, newHeader := oldAvatar, oldHeader
	if avatar.set {
		newAvatar, err = s.saveUpload(avatar, "avatar", u.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	} else if r.FormValue("remove_avatar") == "1" {
		newAvatar = ""
	}
	if header.set {
		newHeader, err = s.saveUpload(header, "header", u.ID)
		if err != nil {
			if avatar.set {
				s.deleteUpload(newAvatar, "avatar", u.ID)
			}
			s.serverError(w, r, err)
			return
		}
	} else if r.FormValue("remove_header") == "1" {
		newHeader = ""
	}
	links := parseLinks(r.FormValue("links"))
	err = s.st.UpdateProfile(r.Context(), u.ID, store.ProfileUpdate{
		Bio:                 strings.TrimSpace(r.FormValue("bio")),
		SocialLinks:         links,
		Avatar:              newAvatar,
		Header:              newHeader,
		PrivateProfile:      u.PrivateProfile,
		HideActivity:        u.HideActivity,
		ManuallyApproves:    r.FormValue("manually_approves") == "1",
		HideSocialGraph:     r.FormValue("hide_social_graph") == "1",
		AdultOptIn:          u.AdultOptIn,
		StarScale:           u.StarScale,
		CoverMode:           u.CoverMode,
		TimeDisplay:         u.TimeDisplay,
		InfiniteScroll:      u.InfiniteScroll,
		Theme:               u.Theme,
		HighContrast:        u.HighContrast,
		Timezone:            u.Timezone,
		FadePlayed:          u.FadePlayed,
		JournalView:         u.JournalView,
		DefaultPlayedStatus: u.DefaultPlayedStatus,
		CommentPrivacy:      u.CommentPrivacy,
		NotePrivacy:         u.NotePrivacy,
		Federation:          u.Federation,
		FedStatuses:         u.FedStatuses,
		FedReviews:          u.FedReviews,
		FedLists:            u.FedLists,
	})
	if err != nil {
		if newAvatar != oldAvatar {
			s.deleteUpload(newAvatar, "avatar", u.ID)
		}
		if newHeader != oldHeader {
			s.deleteUpload(newHeader, "header", u.ID)
		}
		s.serverError(w, r, err)
		return
	}
	if oldAvatar != newAvatar {
		s.deleteUpload(oldAvatar, "avatar", u.ID)
	}
	if oldHeader != newHeader {
		s.deleteUpload(oldHeader, "header", u.ID)
	}
	s.flash(w, r, "success", "Profile updated.")
	s.redirect(w, r, "/settings")
}

func (s *Server) settingsOverviewWidgets(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	w1 := strings.ToLower(strings.TrimSpace(r.FormValue("overview_widget_1")))
	w2 := strings.ToLower(strings.TrimSpace(r.FormValue("overview_widget_2")))
	allowed := map[string]bool{"": true, "favourites": true, "review": true, "list": true}
	if !allowed[w1] || !allowed[w2] {
		s.flash(w, r, "error", "Choose a valid Overview widget for each slot.")
		s.redirect(w, r, "/settings#overview-widgets")
		return
	}
	if w1 != "" && w1 == w2 {
		s.flash(w, r, "error", "Choose two different Overview widgets.")
		s.redirect(w, r, "/settings#overview-widgets")
		return
	}

	var reviewID, listID *int64
	if w1 == "review" || w2 == "review" {
		id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("featured_review_id")), 10, 64)
		if err != nil || id <= 0 {
			s.flash(w, r, "error", "Choose a published review to feature.")
			s.redirect(w, r, "/settings#overview-widgets")
			return
		}
		review, err := s.st.LogByID(r.Context(), id)
		if err != nil || review.UserID != u.ID || review.Review == "" || review.Draft {
			s.flash(w, r, "error", "Choose one of your published reviews.")
			s.redirect(w, r, "/settings#overview-widgets")
			return
		}
		reviewID = &id
	}
	if w1 == "list" || w2 == "list" {
		id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("featured_list_id")), 10, 64)
		if err != nil || id <= 0 {
			s.flash(w, r, "error", "Choose a list to feature.")
			s.redirect(w, r, "/settings#overview-widgets")
			return
		}
		list, err := s.st.ListByID(r.Context(), id, false)
		if err != nil || list.UserID != u.ID {
			s.flash(w, r, "error", "Choose one of your visible lists.")
			s.redirect(w, r, "/settings#overview-widgets")
			return
		}
		listID = &id
	}
	if err := s.st.SetOverviewWidgets(r.Context(), u.ID, w1, w2, reviewID, listID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Overview widgets saved.")
	s.redirect(w, r, "/settings#overview-widgets")
}

func (s *Server) settingsTrophy(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	key := strings.TrimSpace(r.FormValue("badge"))
	if key != "" {
		s.st.CheckBadges(r.Context(), u.ID)
		badges, err := s.st.BadgesFor(r.Context(), u.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		earned := false
		for _, b := range badges {
			if b.Key == key {
				earned = true
				break
			}
		}
		if !earned {
			s.flash(w, r, "error", "Choose one of your earned trophies.")
			s.redirect(w, r, "/settings#trophy")
			return
		}
	}
	if err := s.st.SetFeaturedBadge(r.Context(), u.ID, key); err != nil {
		s.serverError(w, r, err)
		return
	}
	if key == "" {
		s.flash(w, r, "success", "Trophy highlight cleared.")
	} else {
		s.flash(w, r, "success", "Trophy highlighted on your profile.")
	}
	s.redirect(w, r, "/settings#trophy")
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
	yearlyGoal := 0
	if raw := strings.TrimSpace(r.FormValue("yearly_goal")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 10000 {
			s.flash(w, r, "error", "Yearly goal must be a whole number between 0 and 10000.")
			s.redirect(w, r, "/settings?tab=preferences")
			return
		}
		yearlyGoal = n
	}
	err := s.st.UpdateProfile(r.Context(), u.ID, store.ProfileUpdate{
		Bio: u.Bio, SocialLinks: s.st.SocialLinksFor(r.Context(), u.ID), Avatar: u.Avatar, Header: u.Header,
		PrivateProfile: u.PrivateProfile, HideActivity: u.HideActivity,
		ManuallyApproves: u.ManuallyApproves, HideSocialGraph: u.HideSocialGraph, AdultOptIn: u.AdultOptIn,
		StarScale: scale, CoverMode: cover, TimeDisplay: r.FormValue("time_display"),
		InfiniteScroll: r.FormValue("infinite_scroll") == "1", Theme: theme,
		HighContrast: r.FormValue("high_contrast") == "1", Timezone: r.FormValue("timezone"),
		FadePlayed: r.FormValue("fade_played") == "1", JournalView: u.JournalView,
		DefaultPlayedStatus: store.NormalizePlayedStatus(r.FormValue("default_played_status")),
		CommentPrivacy:      u.CommentPrivacy, NotePrivacy: u.NotePrivacy,
		Federation: u.Federation, FedStatuses: u.FedStatuses, FedReviews: u.FedReviews, FedLists: u.FedLists,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.st.SetYearlyGoal(r.Context(), u.ID, yearlyGoal); err != nil {
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
		DefaultPlayedStatus: u.DefaultPlayedStatus,
		CommentPrivacy:      r.FormValue("comment_privacy"), NotePrivacy: r.FormValue("note_privacy"),
		Federation: u.Federation, FedStatuses: u.FedStatuses, FedReviews: u.FedReviews, FedLists: u.FedLists,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	visibility := make(map[string]string, len(store.ActivityKinds))
	for _, kind := range store.ActivityKinds {
		value := strings.ToLower(strings.TrimSpace(r.FormValue("activity_" + kind)))
		switch value {
		case "everyone", "followers", "private":
			visibility[kind] = value
		default:
			s.flash(w, r, "error", "Choose a valid visibility for every activity type.")
			s.redirect(w, r, "/settings?tab=privacy")
			return
		}
	}
	if err := s.st.SetActivityVisibility(r.Context(), u.ID, visibility); err != nil {
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
		JournalView: u.JournalView, DefaultPlayedStatus: u.DefaultPlayedStatus,
		CommentPrivacy: u.CommentPrivacy, NotePrivacy: u.NotePrivacy,
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

func (s *Server) settingsFavoritesAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
	if id <= 0 {
		s.flash(w, r, "error", "Choose a game first.")
		s.redirect(w, r, "/settings#favourites")
		return
	}
	if err := s.st.AddFavoriteGame(r.Context(), u.ID, id); err != nil {
		switch {
		case errors.Is(err, store.ErrFavoriteLimit):
			s.flash(w, r, "error", "You can choose up to five favourite games.")
		case errors.Is(err, store.ErrNotFound):
			s.flash(w, r, "error", "That game is not in the catalog.")
		default:
			s.serverError(w, r, err)
			return
		}
		s.redirect(w, r, "/settings#favourites")
		return
	}
	s.flash(w, r, "success", "Favourite added.")
	s.redirect(w, r, "/settings#favourites")
}

func (s *Server) settingsFavoritesRemove(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	id, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
	if id > 0 {
		if err := s.st.RemoveFavoriteGame(r.Context(), u.ID, id); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.flash(w, r, "success", "Favourite removed.")
	s.redirect(w, r, "/settings#favourites")
}

func (s *Server) settingsFavoritesReorder(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	current := s.st.FavoriteGames(r.Context(), u.ID, 5)
	ids := make([]int64, 0, len(current))
	for _, g := range current {
		ids = append(ids, g.ID)
	}
	var next []int64
	if raw := strings.TrimSpace(r.FormValue("order")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil {
				s.flash(w, r, "error", "That order could not be read.")
				s.redirect(w, r, "/settings#favourites")
				return
			}
			next = append(next, id)
		}
	} else {
		id, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
		dir := r.FormValue("direction")
		if dir != "up" && dir != "down" {
			s.notFound(w, r)
			return
		}
		at := -1
		for i, cur := range ids {
			if cur == id {
				at = i
				break
			}
		}
		swap := at - 1
		if dir == "down" {
			swap = at + 1
		}
		if at < 0 || swap < 0 || swap >= len(ids) {
			s.redirect(w, r, "/settings#favourites")
			return
		}
		next = append([]int64(nil), ids...)
		next[at], next[swap] = next[swap], next[at]
	}
	if err := s.st.ReorderFavoriteGames(r.Context(), u.ID, next); err != nil {
		if errors.Is(err, store.ErrInvalidOrder) {
			s.flash(w, r, "error", "The favourite order changed. Try again.")
			s.redirect(w, r, "/settings#favourites")
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "Favourite order saved.")
	s.redirect(w, r, "/settings#favourites")
}

// settingsDelete permanently deletes the signed-in account after password and
// typed-username confirmation. Deactivation stays available separately.
func (s *Server) settingsDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if !auth.VerifyPassword(u.PasswordHash, r.FormValue("password")) {
		s.flash(w, r, "error", "Password is incorrect.")
		s.redirect(w, r, "/settings?tab=account")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.FormValue("username")), u.Username) {
		s.flash(w, r, "error", "Type your username exactly to confirm.")
		s.redirect(w, r, "/settings?tab=account")
		return
	}
	if err := s.st.DeleteUser(r.Context(), u.ID); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			s.flash(w, r, "error", "The instance needs at least one administrator. Promote another account first.")
			s.redirect(w, r, "/settings?tab=account")
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.deleteUserUploads(u.ID)
	s.auth.DestroyUserSessions(r.Context(), u.ID, "")
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	s.flash(w, r, "success", "Account permanently deleted.")
	s.redirect(w, r, "/")
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

// safeReturnTo keeps redirects local after a library save.
func safeReturnTo(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	return ""
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
	if to := safeReturnTo(r.FormValue("return_to")); to != "" {
		s.redirect(w, r, to)
		return
	}
	s.redirect(w, r, "/library")
}

func (s *Server) libraryRemove(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	gameID, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
	s.st.RemoveLibraryEntry(r.Context(), u.ID, gameID)
	s.flash(w, r, "success", "Removed from library.")
	if to := safeReturnTo(r.FormValue("return_to")); to != "" {
		s.redirect(w, r, to)
		return
	}
	s.redirect(w, r, "/library")

}

func gameTitle(g *models.Game) string {
	if g == nil {
		return ""
	}
	return g.Title
}

func gameSlug(g *models.Game) string {
	if g == nil {
		return ""
	}
	return g.Slug
}

// exportLog is the round-trippable log shape. It keeps every editable field
// plus the journal, so a LudusLog export can be imported without data loss.
type exportLog struct {
	Game          string          `json:"game"`
	GameSlug      string          `json:"game_slug"`
	Name          string          `json:"name"`
	Rating        float64         `json:"rating"`
	Review        string          `json:"review"`
	Spoiler       bool            `json:"spoiler"`
	Status        string          `json:"status"`
	Replay        bool            `json:"replay"`
	Mastered      bool            `json:"mastered"`
	Difficulty    string          `json:"difficulty"`
	Platform      string          `json:"platform"`
	PlayedOn      string          `json:"played_on"`
	Ownership     string          `json:"ownership"`
	Storefront    string          `json:"storefront"`
	Edition       string          `json:"edition"`
	StartedAt     string          `json:"started_at"`
	FinishedAt    string          `json:"finished_at"`
	TimeToFinish  float64         `json:"time_to_finish"`
	TimeToMaster  float64         `json:"time_to_master"`
	Playtime      float64         `json:"playtime"`
	SyncSessions  bool            `json:"sync_sessions"`
	PrimaryRating bool            `json:"primary_rating"`
	Language      string          `json:"language"`
	Draft         bool            `json:"draft"`
	CreatedAt     time.Time       `json:"created_at"`
	Sessions      []exportSession `json:"sessions"`
}

type exportSession struct {
	Note        string   `json:"note"`
	NotePrivacy string   `json:"note_privacy"`
	Playtime    float64  `json:"playtime"`
	Days        []string `json:"days"`
	Tags        []string `json:"tags"`
}

type exportLibrary struct {
	Game       string `json:"game"`
	GameSlug   string `json:"game_slug"`
	Platform   string `json:"platform"`
	Ownership  string `json:"ownership"`
	Storefront string `json:"storefront"`
	Edition    string `json:"edition"`
}

func (s *Server) exportData(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	logs, _, _ := s.st.UserLogs(r.Context(), u.ID, false, 10000, 0)
	lists, _ := s.st.ListsForUser(r.Context(), u.ID, true, 1000, 0)
	library, _ := s.st.LibraryFor(r.Context(), u.ID)
	if r.URL.Query().Get("format") == "csv" {
		s.exportCSV(w, u, logs, lists, library)
		return
	}
	out := map[string]any{
		"format":      "luduslog",
		"version":     2,
		"user":        map[string]any{"username": u.Username, "bio": u.Bio, "created_at": u.CreatedAt},
		"exported_at": time.Now().UTC(),
		"logs":        []exportLog{},
		"lists":       lists,
		"library":     []exportLibrary{},
	}
	var el []exportLog
	for _, l := range logs {
		e := exportLog{
			Game: gameTitle(l.Game), GameSlug: gameSlug(l.Game), Name: l.Name, Rating: l.Rating, Review: l.Review,
			Spoiler: l.Spoiler, Status: l.Status, Replay: l.Replay, Mastered: l.Mastered, Difficulty: l.Difficulty,
			Platform: l.PlatformName, PlayedOn: l.PlayedOnName, Ownership: l.Ownership, Storefront: l.Storefront,
			Edition: l.EditionName, StartedAt: l.StartedAt, FinishedAt: l.FinishedAt, TimeToFinish: l.TimeToFinish,
			TimeToMaster: l.TimeToMaster, Playtime: l.Playtime, SyncSessions: l.SyncSessions,
			PrimaryRating: l.PrimaryRating, Language: l.Language, Draft: l.Draft, CreatedAt: l.CreatedAt,
		}
		sessions, _ := s.st.SessionsFor(r.Context(), l.ID)
		for _, sess := range sessions {
			e.Sessions = append(e.Sessions, exportSession{Note: sess.Note, NotePrivacy: sess.NotePrivacy, Playtime: sess.Playtime, Days: sess.Days, Tags: sess.Tags})
		}
		el = append(el, e)
	}
	var libs []exportLibrary
	for _, entry := range library {
		libs = append(libs, exportLibrary{
			Game: gameTitle(entry.Game), GameSlug: gameSlug(entry.Game), Platform: entry.Platform,
			Ownership: entry.Ownership, Storefront: entry.Storefront, Edition: entry.Edition,
		})
	}
	out["logs"] = el
	out["library"] = libs
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="luduslog-`+u.Username+`.json"`)
	json.NewEncoder(w).Encode(out)
}

// exportCSV writes one flat file: logs, lists and library entries share a row
// shape, distinguished by the kind column. encoding/csv handles quoting.
func (s *Server) exportCSV(w http.ResponseWriter, u *models.User, logs []*models.Log, lists []*models.List, library []models.LibraryEntry) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="luduslog-`+u.Username+`.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write([]string{"kind", "game", "list", "rating", "status", "playtime_hours", "started_at", "finished_at", "platform", "ownership", "storefront", "edition", "notes"})
	for _, l := range logs {
		game, name := "", ""
		if l.Game != nil {
			game, name = l.Game.Slug, l.Game.Title
		}
		if name == "" {
			name = game
		}
		cw.Write([]string{
			"log", name, "", strconv.FormatFloat(l.Rating, 'f', -1, 64), l.Status,
			strconv.FormatFloat(l.Playtime, 'f', -1, 64), l.StartedAt, l.FinishedAt,
			l.PlatformName, l.Ownership, l.Storefront, l.EditionName, l.Review,
		})
	}
	for _, l := range lists {
		cw.Write([]string{"list", "", l.Title, "", l.Privacy, "", "", "", "", "", "", "", l.Description})
	}
	for _, e := range library {
		game := ""
		if e.Game != nil {
			game = e.Game.Title
		}
		cw.Write([]string{"library", game, "", "", "", "", "", "", e.Platform, e.Ownership, e.Storefront, e.Edition, ""})
	}
}

// luduslogExport is the envelope written by exportData.
type luduslogExport struct {
	Format  string          `json:"format"`
	Version int             `json:"version"`
	Logs    []exportLog     `json:"logs"`
	Library []exportLibrary `json:"library"`
}

func parseLuduslogExport(data []byte) (luduslogExport, bool) {
	var payload luduslogExport
	if !bytes.Contains(data, []byte(`"luduslog"`)) || json.Unmarshal(data, &payload) != nil {
		return payload, false
	}
	return payload, payload.Format == "luduslog"
}

func (s *Server) platformIDByName(ctx context.Context, name string) *int64 {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	var id int64
	if err := s.st.DB.QueryRowContext(ctx, `SELECT id FROM platforms WHERE lower(name)=lower(?)`, strings.TrimSpace(name)).Scan(&id); err != nil {
		return nil
	}
	return &id
}

// resolveExportGame finds a catalog game by slug first, then by exact title.
func (s *Server) resolveExportGame(ctx context.Context, slug, title string) *models.Game {
	if slug != "" {
		if g, err := s.st.GameBySlug(ctx, slug); err == nil {
			return g
		}
	}
	if title == "" {
		return nil
	}
	games, _, err := s.st.SearchGames(ctx, title, 10, 0, true)
	if err != nil {
		return nil
	}
	for i := range games {
		if strings.EqualFold(games[i].Title, title) {
			return &games[i]
		}
	}
	return nil
}

func (s *Server) importLuduslogExport(r *http.Request, u *models.User, payload luduslogExport) (added, updated, sessions, missing int, err error) {
	ctx := r.Context()
	existing := map[int64][]*models.Log{}
	claimed := map[int64]bool{}
	type flags struct{ played, playing, backlog, wishlist bool }
	shelf := map[int64]*flags{}
	for _, e := range payload.Logs {
		g := s.resolveExportGame(ctx, e.GameSlug, e.Game)
		if g == nil {
			missing++
			continue
		}
		primary := e.PrimaryRating && e.Rating > 0 && !e.Draft
		in := store.LogInput{
			UserID: u.ID, GameID: g.ID, Name: e.Name, Rating: e.Rating, Review: e.Review,
			Spoiler: e.Spoiler, Status: store.NormalizePlayedStatus(e.Status), Replay: e.Replay,
			Mastered: e.Mastered, Difficulty: e.Difficulty, PlatformID: s.platformIDByName(ctx, e.Platform),
			PlayedOnID: s.platformIDByName(ctx, e.PlayedOn), Ownership: e.Ownership, Storefront: e.Storefront,
			StartedAt: e.StartedAt, FinishedAt: e.FinishedAt, TimeToFinish: e.TimeToFinish,
			TimeToMaster: e.TimeToMaster, Playtime: e.Playtime, SyncSessions: e.SyncSessions,
			Language: e.Language, Draft: e.Draft, PrimaryRating: &primary,
		}
		logs, ok := existing[g.ID]
		if !ok {
			logs, _ = s.st.UserGameLogs(ctx, u.ID, g.ID, true)
			existing[g.ID] = logs
		}
		var match *models.Log
		for _, candidate := range logs {
			if !claimed[candidate.ID] && candidate.Name == e.Name {
				match = candidate
				break
			}
		}
		if match == nil {
			for _, candidate := range logs {
				if !claimed[candidate.ID] {
					match = candidate
					break
				}
			}
		}
		var logID int64
		if match != nil {
			claimed[match.ID] = true
			logID = match.ID
			if _, err := s.st.UpdateLog(ctx, logID, in); err != nil {
				return added, updated, sessions, missing, err
			}
			if err := s.st.DeleteSessionsForLog(ctx, logID); err != nil {
				return added, updated, sessions, missing, err
			}
			updated++
		} else {
			l, err := s.st.CreateLog(ctx, in)
			if err != nil {
				return added, updated, sessions, missing, err
			}
			logID = l.ID
			claimed[l.ID] = true
			existing[g.ID] = append(existing[g.ID], l)
			added++
		}
		for _, sess := range e.Sessions {
			if _, err := s.st.CreateSession(ctx, store.SessionInput{
				LogID: logID, Note: sess.Note, NotePrivacy: sess.NotePrivacy, Playtime: sess.Playtime,
				Days: sess.Days, Tags: sess.Tags,
			}); err != nil {
				return added, updated, sessions, missing, err
			}
			sessions++
		}
		f := shelf[g.ID]
		if f == nil {
			f = &flags{}
			shelf[g.ID] = f
		}
		f.played = f.played || playedLogStatus(in.Status)
		f.playing = f.playing || in.Status == "playing"
		f.backlog = f.backlog || in.Status == "backlog"
		f.wishlist = f.wishlist || in.Status == "wishlist"
		if in.Ownership != "" {
			s.st.SaveLibraryEntry(ctx, u.ID, g.ID, in.PlatformID, in.Ownership, in.Storefront, e.Edition)
		}
	}
	for gameID, f := range shelf {
		if err := s.st.SetStatuses(ctx, u.ID, gameID, f.played, f.playing, f.backlog, f.wishlist, false); err != nil {
			return added, updated, sessions, missing, err
		}
	}
	for _, entry := range payload.Library {
		g := s.resolveExportGame(ctx, entry.GameSlug, entry.Game)
		if g == nil {
			missing++
			continue
		}
		if err := s.st.SaveLibraryEntry(ctx, u.ID, g.ID, s.platformIDByName(ctx, entry.Platform), entry.Ownership, entry.Storefront, entry.Edition); err != nil {
			return added, updated, sessions, missing, err
		}
	}
	return added, updated, sessions, missing, nil
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
	data, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if payload, ok := parseLuduslogExport(data); ok {
		added, updated, sessions, missing, err := s.importLuduslogExport(r, u, payload)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		msg := fmt.Sprintf("Restored %d logs (%d new) and %d play sessions", added+updated, added, sessions)
		if missing > 0 {
			msg += fmt.Sprintf("; %d games were not in the catalog", missing)
		}
		s.flash(w, r, "success", msg+".")
		s.redirect(w, r, "/users/"+u.Username)
		return
	}
	entries, err := backloggd.Parse(bytes.NewReader(data), header.Filename)
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
