package web

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

type profileData struct {
	Page
	Profile           *models.User
	Tab               string
	Logs              []*models.Log
	Reviews           []*models.Log
	Shelves           []shelfSection
	Widgets           []overviewWidget
	Lists             []*models.List
	Favorites         []models.Game
	Stats             *store.UserStats
	YearlyGoalPercent int
	FeaturedBadge     *models.Badge
	Trophies          []models.Badge
	Earned            int
	Links             []models.SocialLink
	IsMe              bool
	IsFollowing       bool
	Pending           bool
	FollowsYou        bool
	Private           bool
	Blocked           bool
	Followers         []*models.User
	Following         []*models.User
	CountFollowers    int
	CountFollowing    int
	Stream            timelineStream
	PageNum           int
	Pages             int
	Total             int

	GameRows      []store.UserGameRow
	GameSort      string
	GameStatus    string
	GamePlayed    string
	GameRating    string
	GamePlatform  string
	GameYear      string
	GameTag       string
	GameOwnership string
	GamePlaytime  string
	GameMastered  bool
	GameReviewed  bool
	GamePlatforms []string
	GameYears     []int
	GameTags      []string
	GameFiltered  int
}

type shelfSection struct {
	Label string
	Count int
	Games []store.ShelfGame
}

type overviewWidget struct {
	Kind      string
	Favorites []models.Game
	Review    *models.Log
	Playtime  float64
	List      *models.List
}

type calendarDay struct {
	Day     int
	InMonth bool
	Played  bool
}

type journalCalendar struct {
	Month string
	Prev  string
	Next  string
	Weeks [][]calendarDay
}

// buildJournalCalendar lays out one month (Monday first) and marks the days a
// session was logged on.
func buildJournalCalendar(days []store.PlayDay, at time.Time) *journalCalendar {
	played := make(map[string]bool, len(days))
	for _, d := range days {
		played[d.Day] = true
	}
	first := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	offset := (int(first.Weekday()) + 6) % 7 // Monday-first grid
	start := first.AddDate(0, 0, -offset)
	cal := &journalCalendar{
		Month: first.Format("January 2006"),
		Prev:  first.AddDate(0, -1, 0).Format("2006-01"),
		Next:  first.AddDate(0, 1, 0).Format("2006-01"),
	}
	for w := 0; w < 6; w++ {
		row := make([]calendarDay, 0, 7)
		for d := 0; d < 7; d++ {
			day := start.AddDate(0, 0, w*7+d)
			row = append(row, calendarDay{
				Day:     day.Day(),
				InMonth: day.Month() == first.Month(),
				Played:  played[day.Format("2006-01-02")],
			})
		}
		cal.Weeks = append(cal.Weeks, row)
	}
	return cal
}

func timelineFilter(raw string) string {
	switch raw {
	case "reviews", "logs", "sessions", "shelves", "lists", "social":
		return raw
	default:
		return "all"
	}
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := s.st.UserByUsername(r.Context(), name)
	if err == store.ErrNotFound {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if acceptsActivityJSON(r) && p.Federation {
		s.apActor(w, r, p)
		return
	}
	viewer := currentUser(r)
	var viewerID int64
	if viewer != nil {
		viewerID = viewer.ID
	}
	if strings.EqualFold(p.Username, name) == false {
		// usernames are case-insensitive for lookup but canonical in URLs
	}
	isMe := viewer != nil && viewer.ID == p.ID
	blocked := false
	if viewer != nil && !isMe {
		blocked = s.st.IsBlocked(r.Context(), viewer.ID, p.ID)
	}
	if blocked {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, p.Username), "This profile is unavailable."})
		return
	}
	following, approved := false, false
	if viewer != nil && !isMe {
		following, approved = s.st.IsFollowing(r.Context(), viewer.ID, p.ID)
	}
	canView := !p.PrivateProfile || isMe || approved
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "overview"
	}
	if tab == "activity" || tab == "logs" {
		query := url.Values{}
		for key, values := range r.URL.Query() {
			if key != "tab" {
				query[key] = values
			}
		}
		if tab == "logs" {
			query.Set("kind", "logs")
		}
		target := "/users/" + p.Username
		if encoded := query.Encode(); encoded != "" {
			target += "?" + encoded
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	page := atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	var badges []models.Badge
	if canView {
		s.st.CheckBadges(r.Context(), p.ID)
		badges, _ = s.st.BadgesFor(r.Context(), p.ID)
	}
	data := profileData{
		Page:           s.page(r, p.Username),
		Profile:        p,
		Tab:            tab,
		IsMe:           isMe,
		IsFollowing:    following,
		Pending:        following && !approved,
		FollowsYou:     viewer != nil && s.st.FollowsYou(r.Context(), viewer.ID, p.ID),
		Private:        !canView,
		CountFollowers: s.st.CountFollowers(r.Context(), p.ID),
		CountFollowing: s.st.CountFollowing(r.Context(), p.ID),
		FeaturedBadge:  featuredBadge(badges, p.FeaturedBadge),
		Links:          s.st.SocialLinksFor(r.Context(), p.ID),
	}
	if canView {
		data.Stats, _ = s.st.StatsFor(r.Context(), p.ID)
		if data.Stats != nil && p.YearlyGoal > 0 {
			data.YearlyGoalPercent = data.Stats.ThisYear * 100 / p.YearlyGoal
			if data.YearlyGoalPercent > 100 {
				data.YearlyGoalPercent = 100
			}
		}
		data.Favorites = s.st.FavoriteGames(r.Context(), p.ID, 5)
		if tab == "trophies" {
			data.Trophies, _ = s.st.BadgeCatalogue(r.Context(), p.ID)
			for _, trophy := range data.Trophies {
				if trophy.EarnedAt != nil {
					data.Earned++
				}
			}
		}
		switch tab {
		case "reviews":
			data.Reviews, data.Total, _ = s.st.UserLogs(r.Context(), p.ID, true, 15, (page-1)*15)
		case "games":
			rows, _ := s.st.UserGameRows(r.Context(), p.ID)
			if viewer == nil || !viewer.AdultOptIn {
				visible := rows[:0]
				for _, row := range rows {
					if !row.Game.Adult {
						visible = append(visible, row)
					}
				}
				rows = visible
			}
			data.GameRows, data.GamePlatforms, data.GameYears, data.GameTags = filterGameRows(rows, r)
			data.GameFiltered = len(data.GameRows)
			data.GameSort = r.URL.Query().Get("sort")
			data.GameStatus = r.URL.Query().Get("status")
			data.GamePlayed = r.URL.Query().Get("played")
			data.GameRating = r.URL.Query().Get("rating")
			data.GamePlatform = r.URL.Query().Get("platform")
			data.GameYear = r.URL.Query().Get("year")
			data.GameTag = r.URL.Query().Get("tag")
			data.GameOwnership = r.URL.Query().Get("ownership")
			data.GamePlaytime = r.URL.Query().Get("playtime")
			data.GameMastered = r.URL.Query().Get("mastered") == "1"
			data.GameReviewed = r.URL.Query().Get("review") == "1"
		case "lists":
			data.Lists, _ = s.st.VisibleListsFor(r.Context(), p.ID, viewerID)
			data.Total = len(data.Lists)
		default:
			data.Widgets = s.overviewWidgets(r, p, viewer, data.Favorites)
			counts := s.st.UserShelfCounts(r.Context(), p.ID)
			shelves := []struct {
				key, label string
				count      int
			}{
				{"played", "Played", counts["played"]},
				{"playing", "Playing", counts["playing"]},
				{"backlog", "Backlog", counts["backlog"]},
			}
			for _, shelf := range shelves {
				games, _ := s.st.ShelfGames(r.Context(), p.ID, shelf.key, 3)
				data.Shelves = append(data.Shelves, shelfSection{Label: shelf.label, Count: shelf.count, Games: games})
			}
			data.Stream = s.timelineStream(r, viewerID, []int64{p.ID}, "/users/"+p.Username, true, 15)
		}
		if data.Total > 0 {
			data.Pages = (data.Total + 14) / 15
		}
	}
	data.Page.Title = p.Username
	s.render(w, r, "profile", data)
}

// filterGameRows applies the profile Games-tab filters and returns the option
// lists discovered from the unfiltered rows.
func filterGameRows(rows []store.UserGameRow, r *http.Request) (out []store.UserGameRow, platforms []string, years []int, tags []string) {
	q := r.URL.Query()
	status := q.Get("status")
	played := q.Get("played")
	rating, _ := strconv.Atoi(q.Get("rating"))
	platform := q.Get("platform")
	year, _ := strconv.Atoi(q.Get("year"))
	tag := strings.ToLower(strings.TrimSpace(q.Get("tag")))
	ownership := q.Get("ownership")
	playtimeMin, _ := strconv.ParseFloat(q.Get("playtime"), 64)
	mastered := q.Get("mastered") == "1"
	reviewed := q.Get("review") == "1"

	seenPlatform := map[string]bool{}
	seenYear := map[int]bool{}
	seenTag := map[string]bool{}
	for _, row := range rows {
		if row.Platform != "" && !seenPlatform[row.Platform] {
			seenPlatform[row.Platform] = true
			platforms = append(platforms, row.Platform)
		}
		if row.PlayedYear > 0 && !seenYear[row.PlayedYear] {
			seenYear[row.PlayedYear] = true
			years = append(years, row.PlayedYear)
		}
		for _, t := range row.Tags {
			if t != "" && !seenTag[t] {
				seenTag[t] = true
				tags = append(tags, t)
			}
		}
	}
	sort.Strings(platforms)
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	sort.Strings(tags)

	for _, row := range rows {
		switch status {
		case "played":
			if row.Played < 1 {
				continue
			}
		case "playing":
			if !row.Playing {
				continue
			}
		case "backlog":
			if !row.Backlog {
				continue
			}
		case "wishlist":
			if !row.Wishlist {
				continue
			}
		}
		if played != "" && row.PlayedStatus != played {
			continue
		}
		if rating > 0 && row.Rating < float64(rating) {
			continue
		}
		if platform != "" && row.Platform != platform {
			continue
		}
		if year > 0 && row.PlayedYear != year {
			continue
		}
		if tag != "" {
			found := false
			for _, t := range row.Tags {
				if strings.EqualFold(t, tag) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if ownership != "" && row.Ownership != ownership {
			continue
		}
		if playtimeMin > 0 && row.Playtime < playtimeMin {
			continue
		}
		if mastered && !row.Mastered {
			continue
		}
		if reviewed && !row.HasReview {
			continue
		}
		out = append(out, row)
	}
	switch q.Get("sort") {
	case "rating":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Rating > out[j].Rating })
	case "playtime":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Playtime > out[j].Playtime })
	case "title":
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Game.Title) < strings.ToLower(out[j].Game.Title)
		})
	}
	return out, platforms, years, tags
}

type userLogCard struct {
	Log          *models.Log
	Sessions     []models.Session
	SessionCount int
	SessionHours float64
}

type userGameLogData struct {
	Page
	Profile      *models.User
	Game         *models.Game
	Status       models.Status
	Rating       float64
	Playtime     float64
	Logs         []*models.Log
	Cards        []userLogCard
	IsMe         bool
	Sort         string
	StatusFilter string
	Replay       bool
	Mastered     bool
	Drafts       bool
	Reviewed     bool
	Filtered     int
}

// userGameLogsPage is the stable destination for a person's log of one game.
// It follows the profile's privacy rules, gates adult games, and only shows
// drafts to their owner.
func (s *Server) userGameLogsPage(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	viewer := currentUser(r)
	var viewerID int64
	if viewer != nil {
		viewerID = viewer.ID
	}
	isMe := viewerID != 0 && viewerID == p.ID
	if viewer != nil && !isMe && s.st.IsBlocked(r.Context(), viewer.ID, p.ID) {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, p.Username), "This profile is unavailable."})
		return
	}
	canView := !p.PrivateProfile || isMe
	if !canView && viewer != nil {
		_, approved := s.st.IsFollowing(r.Context(), viewer.ID, p.ID)
		canView = approved
	}
	if !canView {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, p.Username), "This profile is private."})
		return
	}
	if g.Adult && (viewer == nil || !viewer.AdultOptIn) {
		s.render(w, r, "adult_gate", struct {
			Page
			Game *models.Game
		}{s.page(r, g.Title), g})
		return
	}

	logs, err := s.st.UserGameLogs(r.Context(), p.ID, g.ID, isMe)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	status, _ := s.st.StatusFor(r.Context(), p.ID, g.ID)
	var playtime float64
	for _, l := range logs {
		playtime += l.Playtime
	}
	q := r.URL.Query()
	data := userGameLogData{
		Page:         s.page(r, p.Username+" · "+g.Title),
		Profile:      p,
		Game:         g,
		Status:       status,
		Rating:       s.st.RatingForUser(r.Context(), p.ID, g.ID),
		Playtime:     playtime,
		IsMe:         isMe,
		Sort:         q.Get("sort"),
		StatusFilter: q.Get("status"),
		Replay:       q.Get("replay") == "1",
		Mastered:     q.Get("mastered") == "1",
		Drafts:       q.Get("drafts") == "1",
		Reviewed:     q.Get("review") == "1",
	}
	for _, l := range logs {
		if data.StatusFilter != "" && l.Status != data.StatusFilter {
			continue
		}
		if data.Replay && !l.Replay {
			continue
		}
		if data.Mastered && !l.Mastered {
			continue
		}
		if data.Drafts && !l.Draft {
			continue
		}
		if data.Reviewed && l.Review == "" {
			continue
		}
		data.Logs = append(data.Logs, l)
	}
	switch data.Sort {
	case "oldest":
		sort.SliceStable(data.Logs, func(i, j int) bool { return data.Logs[i].CreatedAt.Before(data.Logs[j].CreatedAt) })
	case "rating":
		sort.SliceStable(data.Logs, func(i, j int) bool { return data.Logs[i].Rating > data.Logs[j].Rating })
	case "playtime":
		sort.SliceStable(data.Logs, func(i, j int) bool { return data.Logs[i].Playtime > data.Logs[j].Playtime })
	}
	data.Filtered = len(data.Logs)
	for _, l := range data.Logs {
		card := userLogCard{Log: l}
		card.Sessions, _ = s.st.SessionsForViewer(r.Context(), l.ID, viewerID)
		card.SessionCount = len(card.Sessions)
		for _, sess := range card.Sessions {
			card.SessionHours += sess.Playtime
		}
		data.Cards = append(data.Cards, card)
	}
	s.render(w, r, "user_game_logs", data)
}

// overviewWidgets resolves the configured slots in order and silently drops
// content that was deleted, lost visibility, or is adult-gated for the viewer.
func (s *Server) overviewWidgets(r *http.Request, p *models.User, viewer *models.User, favorites []models.Game) []overviewWidget {
	var out []overviewWidget
	for _, kind := range []string{p.OverviewWidget1, p.OverviewWidget2} {
		switch kind {
		case "favourites":
			if len(favorites) > 0 {
				out = append(out, overviewWidget{Kind: "favourites", Favorites: favorites})
			}
		case "review":
			if p.FeaturedReviewID == nil {
				continue
			}
			review, err := s.st.LogByID(r.Context(), *p.FeaturedReviewID)
			if err != nil || review.UserID != p.ID || review.Review == "" || review.Draft || review.Game == nil {
				continue
			}
			if review.Game.Adult && (viewer == nil || !viewer.AdultOptIn) {
				continue
			}
			playtime, _ := s.st.PlaytimeForGame(r.Context(), p.ID, review.GameID)
			out = append(out, overviewWidget{Kind: "review", Review: review, Playtime: playtime})
		case "list":
			if p.FeaturedListID == nil {
				continue
			}
			list, err := s.st.ListByID(r.Context(), *p.FeaturedListID, false)
			if err != nil || list.UserID != p.ID || !s.listVisibleTo(r, list, p, viewer) {
				continue
			}
			if (viewer == nil || !viewer.AdultOptIn) && s.st.ListHasAdultGames(r.Context(), list.ID) {
				continue
			}
			covers, _ := s.st.ListCoverGames(r.Context(), []int64{list.ID}, 5)
			list.CoverGames = covers[list.ID]
			out = append(out, overviewWidget{Kind: "list", List: list})
		}
	}
	return out
}

func (s *Server) listVisibleTo(r *http.Request, list *models.List, owner *models.User, viewer *models.User) bool {
	if viewer != nil && viewer.ID == owner.ID {
		return true
	}
	switch list.Privacy {
	case "private":
		return false
	case "personal":
		if viewer == nil {
			return false
		}
		following, approved := s.st.IsFollowing(r.Context(), viewer.ID, owner.ID)
		return following && approved
	default:
		return true
	}
}

func featuredBadge(badges []models.Badge, key string) *models.Badge {
	if key == "" {
		return nil
	}
	for i := range badges {
		if badges[i].Key == key {
			b := badges[i]
			return &b
		}
	}
	return nil
}

type reviewData struct {
	Page
	Log         *models.Log
	Comments    []*models.Comment
	Liked       bool
	Likes       int
	IsOwner     bool
	Related     []models.Game
	MoreBy      []*models.Log
	CanReply    bool
	SubjectType string
	SubjectID   int64
}

func (s *Server) reviewPage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil || l.Review == "" || l.Draft {
		s.notFound(w, r)
		return
	}
	name := r.PathValue("name")
	if !strings.EqualFold(l.User.Username, name) {
		s.notFound(w, r)
		return
	}
	viewer := currentUser(r)
	if l.User.PrivateProfile && (viewer == nil || (viewer.ID != l.UserID && !(func() bool { ok, _ := s.st.IsFollowing(r.Context(), viewer.ID, l.UserID); return ok })())) {
		s.notFound(w, r)
		return
	}
	comments, _ := s.st.CommentsFor(r.Context(), "log", id)
	data := reviewData{
		Page:        s.page(r, l.Name),
		Log:         l,
		Comments:    comments,
		Likes:       s.st.CountLogLikes(r.Context(), id),
		IsOwner:     viewer != nil && viewer.ID == l.UserID,
		CanReply:    viewer != nil,
		SubjectType: "log",
		SubjectID:   id,
		MoreBy:      reviewsByUser(s.st, r, l.UserID, 3),
	}
	if viewer != nil {
		data.Liked = s.st.HasLikedLog(r.Context(), id, viewer.ID)
	}
	if l.Game != nil {
		data.Related, _ = s.st.RelatedGames(r.Context(), l.GameID, 6)
	}
	s.render(w, r, "review", data)
}

func reviewsByUser(st *store.Store, r *http.Request, userID int64, n int) []*models.Log {
	logs, _, _ := st.UserLogs(r.Context(), userID, true, n, 0)
	return logs
}

func (s *Server) reviewByID(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	l, err := s.st.LogByID(r.Context(), id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.redirect(w, r, "/users/"+l.User.Username+"/reviews/"+itoa(id))
}

func (s *Server) followersRoute(w http.ResponseWriter, r *http.Request) {
	if acceptsActivityJSON(r) {
		s.apFollowers(w, r)
		return
	}
	s.followersPage(w, r)
}

func (s *Server) followingRoute(w http.ResponseWriter, r *http.Request) {
	if acceptsActivityJSON(r) {
		s.apFollowing(w, r)
		return
	}
	s.followingPage(w, r)
}

func acceptsActivityJSON(r *http.Request) bool {
	a := r.Header.Get("Accept")
	return strings.Contains(a, "application/activity+json") || strings.Contains(a, `application/ld+json`)
}

func (s *Server) followersPage(w http.ResponseWriter, r *http.Request) {
	s.followList(w, r, true)
}

func (s *Server) followingPage(w http.ResponseWriter, r *http.Request) {
	s.followList(w, r, false)
}

func (s *Server) followList(w http.ResponseWriter, r *http.Request, followers bool) {
	p, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	viewer := currentUser(r)
	if p.HideSocialGraph && (viewer == nil || viewer.ID != p.ID) {
		s.render(w, r, "message", struct {
			Page
			Message string
		}{s.page(r, p.Username), "This user keeps their social graph private."})
		return
	}
	page := atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	var users []*models.User
	var total int
	if followers {
		users, _ = s.st.Followers(r.Context(), p.ID, 50, (page-1)*50)
		total = s.st.CountFollowers(r.Context(), p.ID)
	} else {
		users, _ = s.st.Following(r.Context(), p.ID, 50, (page-1)*50)
		total = s.st.CountFollowing(r.Context(), p.ID)
	}
	title := "Followers"
	if !followers {
		title = "Following"
	}
	s.render(w, r, "follows", struct {
		Page
		Profile   *models.User
		Users     []*models.User
		Title     string
		Followers bool
		PageNum   int
		Pages     int
	}{s.page(r, title+" of "+p.Username), p, users, title, followers, page, (total + 49) / 50})
}

func (s *Server) followUser(w http.ResponseWriter, r *http.Request) {
	target, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	if target.ID == u.ID {
		s.redirect(w, r, "/users/"+u.Username)
		return
	}
	if err := s.st.Follow(r.Context(), u.ID, target.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.notifyContext(r, target.ID, u.ID, "follow", "user", u.ID, s.absolute("/users/"+u.Username))
	s.q.Enqueue(r.Context(), "deliver", `{"kind":"follow","actor":`+itoa(u.ID)+`,"target":`+itoa(target.ID)+`}`, 0)
	s.flash(w, r, "success", "Follow request sent to "+target.Username+".")
	s.redirect(w, r, "/users/"+target.Username)
}

func (s *Server) unfollowUser(w http.ResponseWriter, r *http.Request) {
	target, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	s.st.Unfollow(r.Context(), u.ID, target.ID)
	s.flash(w, r, "success", "Unfollowed "+target.Username+".")
	s.redirect(w, r, "/users/"+target.Username)
}

func (s *Server) blockUser(w http.ResponseWriter, r *http.Request) {
	target, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	s.st.Block(r.Context(), u.ID, target.ID)
	s.flash(w, r, "success", "You blocked "+target.Username+".")
	s.redirect(w, r, "/users/"+target.Username)
}

func (s *Server) unblockUser(w http.ResponseWriter, r *http.Request) {
	target, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	u := currentUser(r)
	s.st.Unblock(r.Context(), u.ID, target.ID)
	s.flash(w, r, "success", "You unblocked "+target.Username+".")
	s.redirect(w, r, "/users/"+target.Username)
}

func (s *Server) trophiesPage(w http.ResponseWriter, r *http.Request) {
	s.redirect(w, r, "/users/"+r.PathValue("name")+"?tab=trophies")
}

func (s *Server) statsPage(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	viewer := currentUser(r)
	if p.PrivateProfile && (viewer == nil || viewer.ID != p.ID) {
		s.notFound(w, r)
		return
	}
	st, _ := s.st.StatsFor(r.Context(), p.ID)
	s.st.CheckBadges(r.Context(), p.ID)
	badges, _ := s.st.BadgesFor(r.Context(), p.ID)
	s.render(w, r, "stats", struct {
		Page
		Profile *models.User
		Stats   *store.UserStats
		Badges  []models.Badge
		IsMe    bool
	}{s.page(r, "Stats of "+p.Username), p, st, badges, viewer != nil && viewer.ID == p.ID})
}

func (s *Server) yearPage(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	viewer := currentUser(r)
	if p.PrivateProfile && (viewer == nil || viewer.ID != p.ID) {
		s.notFound(w, r)
		return
	}
	year := atoi(r.PathValue("year"))
	if year < 2000 || year > time.Now().Year() {
		s.notFound(w, r)
		return
	}
	if viewer != nil && viewer.ID == p.ID {
		s.st.AwardBadge(r.Context(), p.ID, "year_in_review")
	}
	recap, logs, _ := s.st.YearRecap(r.Context(), p.ID, year)
	s.render(w, r, "year", struct {
		Page
		Profile *models.User
		Year    int
		Recap   map[string]any
		Logs    []*models.Log
	}{s.page(r, "Play in "+strconv.Itoa(year)), p, year, recap, logs})
}
