package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

type homeData struct {
	Page
	Trending    []models.Game
	Sleepers    []models.Game
	Anticipated []models.Game
	Reviews     []*models.Log
	Lists       []*models.List
	Feed        []models.Activity
	News        []models.News
	GamesCount  int
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	trending, _ := s.st.RecentlyTrending(r.Context(), 12)
	sleepers, _ := s.st.SleeperHits(r.Context(), 6)
	anticipated, _ := s.st.RecentlyAnticipated(r.Context(), 6)
	reviews, _ := s.st.RecentReviews(r.Context(), 4)
	lists, _ := s.st.PopularLists(r.Context(), 4)
	var feed []models.Activity
	if u != nil {
		feed, _ = s.st.ActivityFeed(r.Context(), u.ID, 0, 12, 0, true)
	}
	var news []models.News
	if nrows, nerr := s.st.DB.QueryContext(r.Context(), `SELECT id,title,body,created_at FROM news ORDER BY created_at DESC LIMIT 3`); nerr == nil {
		defer nrows.Close()
		for nrows.Next() {
			var n models.News
			var created string
			if nrows.Scan(&n.ID, &n.Title, &n.Body, &created) == nil {
				n.CreatedAt = parseTimeSafe(created)
				news = append(news, n)
			}
		}
	}
	data := homeData{
		Page:        s.page(r, ""),
		Trending:    trending,
		Sleepers:    sleepers,
		Anticipated: anticipated,
		Reviews:     reviews,
		Lists:       lists,
		Feed:        feed,
		News:        news,
	}
	s.render(w, r, "home", data)
}

type gamesData struct {
	Page
	Games       []models.Game
	Total       int
	Facets      store.Facets
	Q           string
	Genres      []string
	Themes      []string
	Platforms   []string
	Categories  []string
	Years       []string
	YearFrom    string
	YearTo      string
	Release     string
	Company     string
	Sort        string
	Dir         string
	MinRating   string
	View        string
	FriendsOnly bool
	HideOwned   bool
	NoStatus    bool
	PageNum     int
	Pages       int
	BaseQuery   string
	Friends     []*models.User
	LoggedIn    bool
}

func (s *Server) gamesIndex(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	q := r.URL.Query()
	f := store.GameFilter{
		Q:               strings.TrimSpace(q.Get("q")),
		Genres:          nonEmpty(q["genre"]),
		Themes:          nonEmpty(q["theme"]),
		Categories:      nonEmpty(q["category"]),
		YearFrom:        atoi(q.Get("from")),
		YearTo:          atoi(q.Get("to")),
		Release:         q.Get("release"),
		Company:         q.Get("company"),
		MinRating:       atof(q.Get("min_rating")),
		Statuses:        nonEmpty(q["status"]),
		Ownership:       q.Get("ownership"),
		Sort:            q.Get("sort"),
		Dir:             q.Get("dir"),
		FriendsOnly:     q.Get("friends") == "1",
		HideOwned:       q.Get("hide_owned") == "1",
		NoStatus:        q.Get("no_status") == "1",
		ExcludeEditions: true,
		Limit:           60,
	}
	for _, p := range q["platform"] {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			f.Platforms = append(f.Platforms, id)
		}
	}
	if u != nil {
		f.UserID = u.ID
		f.IncludeAdult = u.AdultOptIn
	}
	page := atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	f.Offset = (page - 1) * f.Limit
	games, total, err := s.st.ListGames(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	facets := s.st.GameFacets(r.Context())
	pages := (total + f.Limit - 1) / f.Limit
	base := url.Values{}
	for k, v := range q {
		if k == "page" {
			continue
		}
		for _, x := range v {
			base.Add(k, x)
		}
	}
	data := gamesData{
		Page:        s.page(r, "Games"),
		Games:       games,
		Total:       total,
		Facets:      facets,
		Q:           f.Q,
		Genres:      f.Genres,
		Themes:      f.Themes,
		Categories:  f.Categories,
		YearFrom:    q.Get("from"),
		YearTo:      q.Get("to"),
		Release:     f.Release,
		Company:     f.Company,
		Sort:        f.Sort,
		Dir:         f.Dir,
		MinRating:   q.Get("min_rating"),
		FriendsOnly: f.FriendsOnly,
		HideOwned:   f.HideOwned,
		NoStatus:    f.NoStatus,
		PageNum:     page,
		Pages:       pages,
		BaseQuery:   base.Encode(),
		LoggedIn:    u != nil,
	}
	for _, p := range f.Platforms {
		data.Platforms = append(data.Platforms, strconv.FormatInt(p, 10))
	}
	if u != nil {
		data.Friends, _ = s.st.Following(r.Context(), u.ID, 50, 0)
	}
	s.render(w, r, "games", data)
}

type gameData struct {
	Page
	Game         *models.Game
	Status       models.Status
	UserLog      *models.Log
	UserRating   float64
	Counts       map[string]int
	Histogram    map[int]int
	HistogramMax int
	Reviews      []*models.Log
	ReviewTotal  int
	Lists        []*models.List
	Friends      []friendInteraction
	Covers       map[int64]string
	MyCover      string
	Platforms    []models.Platform
	AdultBlocked bool
	LoggedIn     bool
	Owned        *models.LibraryEntry
	InLists      []*models.List
	Hidden       bool
}

type friendInteraction struct {
	User   *models.User
	Rating float64
	Status models.Status
}

func (s *Server) gamePage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	g, err := s.st.GameBySlug(r.Context(), slug)
	if err == store.ErrNotFound {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	u := currentUser(r)
	if g.Adult && (u == nil || !u.AdultOptIn) {
		s.render(w, r, "adult_gate", struct {
			Page
			Game *models.Game
		}{s.page(r, g.Title), g})
		return
	}
	g, err = s.st.GameDetail(r.Context(), g)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := gameData{
		Page:      s.page(r, g.Title),
		Game:      g,
		Counts:    s.st.GameStatusCounts(r.Context(), g.ID),
		Histogram: s.st.RatingDistribution(r.Context(), g.ID, 5),
		Covers:    s.st.PlayerCovers(r.Context(), g.ID),
		Platforms: s.st.Platforms(r.Context()),
	}
	for _, n := range data.Histogram {
		if n > data.HistogramMax {
			data.HistogramMax = n
		}
	}
	data.Reviews, data.ReviewTotal, _ = s.st.ReviewsForGame(r.Context(), g.ID, "likes", 4, 0)
	data.Lists, _ = s.st.ListsContainingGame(r.Context(), g.ID, 4)
	if u != nil {
		data.LoggedIn = true
		data.Status, _ = s.st.StatusFor(r.Context(), u.ID, g.ID)
		data.UserRating = s.st.RatingForUser(r.Context(), u.ID, g.ID)
		data.MyCover = s.st.CoverFor(r.Context(), u.ID, g.ID)
		data.Hidden = s.st.IsGameHidden(r.Context(), u.ID, g.ID)
		if l, err := s.st.LatestLogFor(r.Context(), u.ID, g.ID); err == nil {
			data.UserLog = l
		}
		if e, err := s.st.LibraryEntry(r.Context(), u.ID, g.ID); err == nil {
			data.Owned = e
		}
		for _, fi := range s.st.FriendsWhoInteracted(r.Context(), u.ID, g.ID, 8) {
			data.Friends = append(data.Friends, friendInteraction{fi.User, fi.Rating, fi.Status})
		}
	}
	s.render(w, r, "game", data)
}

type reviewsData struct {
	Page
	Reviews   []*models.Log
	Total     int
	Tab       string
	Sort      string
	PageNum   int
	Pages     int
	Base      string
	BaseQuery string
	Game      *models.Game
	LoggedIn  bool
}

func (s *Server) reviewsPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "all"
	}
	page := atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	var viewer int64
	if u != nil {
		viewer = u.ID
	}
	reviews, total, err := s.st.ReviewsFeed(r.Context(), viewer, tab, 0, 30, (page-1)*30)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	base := url.Values{"tab": {tab}}
	data := reviewsData{
		Page:      s.page(r, "Reviews"),
		Reviews:   reviews,
		Total:     total,
		Tab:       tab,
		PageNum:   page,
		Pages:     (total + 29) / 30,
		Base:      "/reviews",
		BaseQuery: base.Encode(),
		LoggedIn:  u != nil,
	}
	s.render(w, r, "reviews", data)
}

func (s *Server) gameReviews(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err == store.ErrNotFound {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	u := currentUser(r)
	if g.Adult && (u == nil || !u.AdultOptIn) {
		s.render(w, r, "adult_gate", struct {
			Page
			Game *models.Game
		}{s.page(r, g.Title), g})
		return
	}
	sortKey := r.URL.Query().Get("sort")
	storeSort := "likes"
	switch sortKey {
	case "newest":
		storeSort = "new"
	case "rating":
		storeSort = "rating"
	case "oldest":
		storeSort = "oldest"
	default:
		sortKey = "popular"
	}
	page := atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	reviews, total, err := s.st.ReviewsForGame(r.Context(), g.ID, storeSort, 20, (page-1)*20)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	base := url.Values{"sort": {sortKey}}
	data := reviewsData{
		Page:      s.page(r, "Reviews of "+g.Title),
		Reviews:   reviews,
		Total:     total,
		Sort:      sortKey,
		Game:      g,
		PageNum:   page,
		Pages:     (total + 19) / 20,
		Base:      "/games/" + g.Slug + "/reviews",
		BaseQuery: base.Encode(),
		LoggedIn:  u != nil,
	}
	s.render(w, r, "reviews", data)
}

func (s *Server) listsDiscover(w http.ResponseWriter, r *http.Request) {
	lists, _ := s.st.PopularLists(r.Context(), 40)
	s.render(w, r, "lists_discover", struct {
		Page
		Lists []*models.List
	}{s.page(r, "Lists"), lists})
}

type searchData struct {
	Page
	Q         string
	Tab       string
	PageNum   int
	Pages     int
	Total     int
	BaseQuery string
	Games     []models.Game
	Remote    []models.Game
	Users     []*models.User
	Lists     []models.List
	Reviews   []*models.Log
}

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	q := strings.TrimSpace(query.Get("q"))
	tab := query.Get("tab")
	switch tab {
	case "games", "reviews", "people", "lists":
	default:
		tab = "games"
	}
	page := atoi(query.Get("page"))
	if page < 1 {
		page = 1
	}
	u := currentUser(r)
	includeAdult := u != nil && u.AdultOptIn
	data := searchData{Page: s.page(r, "Search"), Q: q, Tab: tab, PageNum: page}
	data.SearchTab = tab
	base := url.Values{"q": {q}, "tab": {tab}}
	data.BaseQuery = base.Encode()
	if q != "" {
		const limit = 30
		offset := (page - 1) * limit
		var total int
		var err error
		switch tab {
		case "reviews":
			data.Reviews, total, err = s.st.SearchReviews(r.Context(), q, limit, offset, includeAdult)
		case "people":
			data.Users, total, err = s.st.SearchUsers(r.Context(), q, limit, offset)
		case "lists":
			data.Lists, total, err = s.st.SearchLists(r.Context(), q, limit, offset)
		default:
			data.Games, total, err = s.st.SearchGames(r.Context(), q, limit, offset, includeAdult)
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data.Total = total
		data.Pages = (total + limit - 1) / limit
		if tab == "games" && page == 1 && total == 0 && s.igdb != nil {
			data.Remote, _ = s.igdb.Search(r.Context(), q, 6)
		}
	}
	s.render(w, r, "search", data)
}

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "about", struct{ Page }{s.page(r, "About")})
}

func nonEmpty(vals []string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
