package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

type profileData struct {
	Page
	Profile        *models.User
	Tab            string
	Logs           []*models.Log
	Reviews        []*models.Log
	Lists          []*models.List
	Favorites      []models.Game
	Stats          *store.UserStats
	Badges         []models.Badge
	Links          []models.SocialLink
	IsMe           bool
	IsFollowing    bool
	Pending        bool
	FollowsYou     bool
	Private        bool
	Blocked        bool
	Friends        []*models.User
	Followers      []*models.User
	Following      []*models.User
	CountFollowers int
	CountFollowing int
	Activity       []models.Activity
	PageNum        int
	Pages          int
	Total          int
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
	page := atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
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
		Badges:         badgesOrEmpty(s.st.BadgesFor(r.Context(), p.ID)),
		Links:          s.st.SocialLinksFor(r.Context(), p.ID),
	}
	if canView {
		data.Stats, _ = s.st.StatsFor(r.Context(), p.ID)
		data.Favorites = s.st.FavoriteGames(r.Context(), p.ID, 5)
		data.Friends, _ = s.st.MutualUsers(r.Context(), p.ID, 12)
		switch tab {
		case "reviews":
			data.Reviews, data.Total, _ = s.st.UserLogs(r.Context(), p.ID, true, 15, (page-1)*15)
		case "logs":
			data.Logs, data.Total, _ = s.st.UserLogs(r.Context(), p.ID, false, 15, (page-1)*15)
		case "lists":
			data.Lists, data.Total = s.st.ListsForUser(r.Context(), p.ID, isMe, 30, (page-1)*30)
		case "activity":
			data.Activity, _ = s.st.UserActivities(r.Context(), p.ID, 30, 0)
		default:
			data.Logs, _, _ = s.st.UserLogs(r.Context(), p.ID, false, 6, 0)
			data.Lists, _ = s.st.ListsForUser(r.Context(), p.ID, isMe, 4, 0)
			data.Reviews, _, _ = s.st.UserLogs(r.Context(), p.ID, true, 2, 0)
			data.Activity, _ = s.st.UserActivities(r.Context(), p.ID, 8, 0)
		}
		if data.Total > 0 {
			data.Pages = (data.Total + 14) / 15
		}
	}
	data.Page.Title = p.Username
	s.render(w, r, "profile", data)
}

func badgesOrEmpty(b []models.Badge, err error) []models.Badge { return b }

func (s *Server) atProfile(w http.ResponseWriter, r *http.Request) {
	s.redirect(w, r, "/users/"+r.PathValue("name"))
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
	recap, logs, _ := s.st.YearRecap(r.Context(), p.ID, year)
	s.render(w, r, "year", struct {
		Page
		Profile *models.User
		Year    int
		Recap   map[string]any
		Logs    []*models.Log
	}{s.page(r, "Play in "+strconv.Itoa(year)), p, year, recap, logs})
}
