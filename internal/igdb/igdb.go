// Package igdb is a small IGDB API client. Game data is stored locally; the
// API is only contacted by the sync command and by explicit admin refreshes.
// See docs/igdb.md for attribution requirements.
package igdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"luduslog/internal/models"
)

type Client struct {
	ClientID     string
	ClientSecret string
	HTTP         *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func New(clientID, secret string) *Client {
	return &Client{ClientID: clientID, ClientSecret: secret, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiry) {
		return nil
	}
	form := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://id.twitch.tv/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("igdb auth: %s: %s", resp.Status, b)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	c.token = out.AccessToken
	c.expiry = time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second)
	return nil
}

type apiNamed struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	URL     string `json:"url"`
	VideoID string `json:"video_id"`
}

type apiReleaseDate struct {
	Platform *apiNamed `json:"platform"`
	Date     int64     `json:"date"`
	Human    string    `json:"human"`
	Region   int       `json:"region"`
	Status   int       `json:"status"`
}

type apiTimeToBeat struct {
	GameID     int64 `json:"game_id"`
	Hastily    int   `json:"hastily"`
	Normally   int   `json:"normally"`
	Completely int   `json:"completely"`
}

type apiGameType struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
}

type apiGame struct {
	ID               int64        `json:"id"`
	Name             string       `json:"name"`
	Slug             string       `json:"slug"`
	Summary          string       `json:"summary"`
	FirstReleaseDate int64        `json:"first_release_date"`
	Category         int          `json:"category"`
	GameType         *apiGameType `json:"game_type"`
	VersionParent    int64        `json:"version_parent"`
	ParentGame       int64        `json:"parent_game"`
	Cover            *struct {
		URL string `json:"url"`
	} `json:"cover"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Platforms         []apiNamed       `json:"platforms"`
	ReleaseDates      []apiReleaseDate `json:"release_dates"`
	Themes            []apiNamed       `json:"themes"`
	GameModes         []apiNamed       `json:"game_modes"`
	Screenshots       []apiNamed       `json:"screenshots"`
	Videos            []apiNamed       `json:"videos"`
	Alternative       []apiNamed       `json:"alternative_names"`
	Collections       []apiNamed       `json:"collections"`
	Remakes           []apiNamed       `json:"remakes"`
	Ports             []apiNamed       `json:"ports"`
	Bundles           []apiNamed       `json:"bundles"`
	ExpandedGames     []apiNamed       `json:"expanded_games"`
	InvolvedCompanies []struct {
		Developer bool `json:"developer"`
		Publisher bool `json:"publisher"`
		Company   struct {
			Name string `json:"name"`
		} `json:"company"`
	} `json:"involved_companies"`
	AggregatedRating float64 `json:"aggregated_rating"`
	RatingCount      int     `json:"rating_count"`
}

const gameFields = `fields id,name,slug,summary,first_release_date,category,version_parent,parent_game,cover.url,
genres.name,platforms.id,platforms.name,release_dates.platform.id,release_dates.platform.name,release_dates.date,
release_dates.region,release_dates.human,release_dates.status,themes.name,game_modes.name,screenshots.url,
videos.video_id,alternative_names.name,collections.name,game_type.id,game_type.type,
remakes.id,remakes.name,remakes.slug,ports.id,ports.name,ports.slug,bundles.id,bundles.name,bundles.slug,
expanded_games.id,expanded_games.name,expanded_games.slug,involved_companies.developer,
involved_companies.publisher,involved_companies.company.name,aggregated_rating,rating_count;`

// Games fetches up to limit games ordered by rating count.
func (c *Client) Games(ctx context.Context, limit int) ([]models.Game, error) {
	return c.GamesRange(ctx, limit, 0, nil)
}

// GamesRange fetches a total of limit games starting at offset. IGDB caps a
// single request at 500 rows, so larger catalogs are paged here.
func (c *Client) GamesRange(ctx context.Context, limit, offset int, progress func(done, total int)) ([]models.Game, error) {
	if limit <= 0 {
		return nil, nil
	}
	var out []models.Game
	for done := 0; done < limit; {
		pageSize := limit - done
		if pageSize > 500 {
			pageSize = 500
		}
		// Variant records (editions, remasters, ports) are imported too. Their
		// parent_id keeps them out of the games index and search results while
		// letting the parent page list them as editions.
		body := fmt.Sprintf(`%s
sort rating_count desc; where rating_count > 5; limit %d; offset %d;`, gameFields, pageSize, offset+done)
		games, err := c.query(ctx, body)
		if err != nil {
			return out, err
		}
		out = append(out, games...)
		done += len(games)
		if progress != nil {
			progress(done, limit)
		}
		if len(games) < pageSize {
			break
		}
		if done < limit {
			if err := sleepContext(ctx, 250*time.Millisecond); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// Search looks up games by title for the on-demand catalog flow.
func (c *Client) Search(ctx context.Context, q string, limit int) ([]models.Game, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(q)
	body := fmt.Sprintf(`%s
search "%s"; limit %d;`, gameFields, escaped, limit)
	return c.query(ctx, body)
}

// GameBySlug fetches one game by its IGDB slug.
func (c *Client) GameBySlug(ctx context.Context, slug string) (*models.Game, error) {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(strings.TrimSpace(slug))
	body := fmt.Sprintf(`%s
where slug = "%s"; limit 1;`, gameFields, escaped)
	games, err := c.query(ctx, body)
	if err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("igdb: no game with slug %q", slug)
	}
	return &games[0], nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// GamesByIDs fetches games by IGDB id in batches. Backloggd game ids are IGDB
// ids, so an export can populate its catalog without a title search.
func (c *Client) GamesByIDs(ctx context.Context, ids []int64) ([]models.Game, error) {
	seen := make(map[int64]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	var out []models.Game
	for start := 0; start < len(unique); start += 100 {
		end := start + 100
		if end > len(unique) {
			end = len(unique)
		}
		parts := make([]string, end-start)
		for i, id := range unique[start:end] {
			parts[i] = strconv.FormatInt(id, 10)
		}
		body := fmt.Sprintf(`%s
where id = (%s); limit 100;`, gameFields, strings.Join(parts, ","))
		games, err := c.query(ctx, body)
		if err != nil {
			return out, err
		}
		out = append(out, games...)
		if end < len(unique) {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	return out, nil
}

func (c *Client) query(ctx context.Context, body string) ([]models.Game, error) {
	var raw []apiGame
	if err := c.post(ctx, "games", body, &raw); err != nil {
		return nil, err
	}
	times, err := c.timeToBeat(ctx, raw)
	if err != nil {
		return nil, err
	}
	out := make([]models.Game, 0, len(raw))
	for _, r := range raw {
		g := mapGame(r)
		if t, ok := times[r.ID]; ok {
			g.TimeToBeat = timeToBeatModel(t)
		}
		out = append(out, g)
	}
	return out, nil
}

func (c *Client) timeToBeat(ctx context.Context, games []apiGame) (map[int64]apiTimeToBeat, error) {
	ids := make([]int64, len(games))
	for i, g := range games {
		ids[i] = g.ID
	}
	return c.timeToBeatByIDs(ctx, ids)
}

func (c *Client) timeToBeatByIDs(ctx context.Context, ids []int64) (map[int64]apiTimeToBeat, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	body := fmt.Sprintf(`fields game_id,hastily,normally,completely; where game_id = (%s); limit %d;`, strings.Join(parts, ","), len(ids))
	var raw []apiTimeToBeat
	if err := c.post(ctx, "game_time_to_beats", body, &raw); err != nil {
		return nil, err
	}
	out := make(map[int64]apiTimeToBeat, len(raw))
	for _, t := range raw {
		out[t.GameID] = t
	}
	return out, nil
}

func timeToBeatModel(t apiTimeToBeat) *models.TimeToBeat {
	if t.Hastily <= 0 && t.Normally <= 0 && t.Completely <= 0 {
		return nil
	}
	return &models.TimeToBeat{Hastily: t.Hastily, Normally: t.Normally, Completely: t.Completely}
}

func (c *Client) post(ctx context.Context, endpoint, body string, out any) error {
	if err := c.authenticate(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.igdb.com/v4/"+endpoint, bytes.NewBufferString(body))
	if err != nil {
		return err
	}
	req.Header.Set("Client-ID", c.ClientID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	for attempt := 0; ; attempt++ {
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			wait := time.Duration(1<<attempt) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if n, err := strconv.Atoi(ra); err == nil && n > 0 && n < 60 {
					wait = time.Duration(n) * time.Second
				}
			}
			if err := sleepContext(ctx, wait); err != nil {
				return err
			}
			req = req.Clone(ctx)
			if req.GetBody != nil {
				req.Body, _ = req.GetBody()
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("igdb %s: %s: %s", endpoint, resp.Status, b)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
}

func mapGame(r apiGame) models.Game {
	g := models.Game{
		IGDBID: r.ID, Slug: r.Slug, Title: r.Name, Summary: r.Summary,
		ReleaseStatus: "released", RatingCount: r.RatingCount,
	}
	// category is deprecated and now returns 0 for most records; game_type is
	// its replacement and tells updates and ports apart from plain editions.
	if r.GameType != nil {
		g.Category = categoryName(r.GameType.ID)
	} else {
		g.Category = categoryName(r.Category)
	}
	if g.Slug == "" {
		g.Slug = Slugify(r.Name)
	}
	if r.FirstReleaseDate > 0 {
		t := time.Unix(r.FirstReleaseDate, 0).UTC()
		g.ReleaseDate = t.Format("2006-01-02")
		g.ReleaseYear = t.Year()
		if t.After(time.Now().UTC()) {
			g.ReleaseStatus = "upcoming"
		}
	}
	if r.Cover != nil && r.Cover.URL != "" {
		g.CoverURL = imageURL(r.Cover.URL, "t_cover_big_2x")
	}
	for _, gen := range r.Genres {
		g.Genres = appendUnique(g.Genres, gen.Name)
	}
	for _, p := range r.Platforms {
		g.Platforms = append(g.Platforms, models.Platform{IGDBID: p.ID, Name: p.Name})
	}
	for _, rd := range r.ReleaseDates {
		x := models.ReleaseDate{Region: regionName(rd.Region), Human: rd.Human, Status: releaseStatusName(rd.Status)}
		if rd.Platform != nil {
			x.PlatformID = rd.Platform.ID
			x.Platform = rd.Platform.Name
		}
		if rd.Date > 0 {
			t := time.Unix(rd.Date, 0).UTC()
			x.Date = t.Format("2006-01-02")
			if x.Human == "" {
				x.Human = t.Format("Jan 2, 2006")
			}
		}
		if x.Platform != "" || x.Date != "" {
			g.ReleaseDates = append(g.ReleaseDates, x)
		}
	}
	for _, x := range r.Themes {
		g.Themes = appendUnique(g.Themes, x.Name)
	}
	for _, x := range r.GameModes {
		g.Modes = appendUnique(g.Modes, x.Name)
	}
	for _, x := range r.Screenshots {
		if x.URL != "" {
			g.Screenshots = appendUnique(g.Screenshots, imageURL(x.URL, "t_screenshot_big"))
		}
	}
	for _, x := range r.Videos {
		if x.VideoID != "" {
			g.Videos = appendUnique(g.Videos, x.VideoID)
		}
	}
	for _, x := range r.Alternative {
		g.AltNames = appendUnique(g.AltNames, x.Name)
	}
	if len(r.Collections) > 0 {
		g.Franchise = r.Collections[0].Name
	}
	// version_parent is set on editions and remasters; parent_game is the
	// broader DLC/expansion link. Both point at the base game.
	if r.ParentGame > 0 {
		g.ParentIGDBID = r.ParentGame
	} else if r.VersionParent > 0 {
		g.ParentIGDBID = r.VersionParent
	}
	if g.ParentIGDBID > 0 {
		g.EditionName = editionName(r.Name)
	}
	g.Remakes = namedGames(r.Remakes)
	g.Ports = namedGames(r.Ports)
	g.Bundles = namedGames(r.Bundles)
	g.Updates = namedGames(r.ExpandedGames)
	for _, ic := range r.InvolvedCompanies {
		role := ""
		switch {
		case ic.Developer:
			role = "developer"
		case ic.Publisher:
			role = "publisher"
		}
		if role != "" {
			g.Companies = appendUniqueCompany(g.Companies, models.CompanyRole{Name: ic.Company.Name, Role: role})
		}
	}
	return g
}

// editionName turns "Base Game - Complete Edition" into "Complete Edition" so
// the parent page can label a variant without a second IGDB lookup.
func editionName(title string) string {
	if i := strings.LastIndex(title, " - "); i >= 0 && i+3 < len(title) {
		return strings.TrimSpace(title[i+3:])
	}
	if i := strings.LastIndex(title, ": "); i >= 0 && i+2 < len(title) {
		return strings.TrimSpace(title[i+2:])
	}
	return strings.TrimSpace(title)
}

func namedGames(refs []apiNamed) []models.Game {
	out := make([]models.Game, 0, len(refs))
	for _, r := range refs {
		if r.ID <= 0 {
			continue
		}
		g := models.Game{IGDBID: r.ID, Title: r.Name, Slug: r.Slug}
		if g.Slug == "" {
			g.Slug = Slugify(r.Name)
		}
		out = append(out, g)
	}
	return out
}

func imageURL(raw, size string) string {
	u := strings.Replace(raw, "t_thumb", size, 1)
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	return u
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func appendUniqueCompany(list []models.CompanyRole, v models.CompanyRole) []models.CompanyRole {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func categoryName(c int) string {
	switch c {
	case 1:
		return "dlc_addon"
	case 2:
		return "expansion"
	case 3:
		return "bundle"
	case 4:
		return "standalone_expansion"
	case 5:
		return "mod"
	case 6:
		return "episode"
	case 7:
		return "season"
	case 8:
		return "remake"
	case 9:
		return "remaster"
	case 10:
		return "expanded_game"
	case 11:
		return "port"
	case 12:
		return "fork"
	case 13:
		return "pack"
	case 14:
		return "update"
	default:
		return "main_game"
	}
}

func regionName(r int) string {
	switch r {
	case 1:
		return "Europe"
	case 2:
		return "North America"
	case 3:
		return "Australia"
	case 4:
		return "New Zealand"
	case 5:
		return "Japan"
	case 6:
		return "China"
	case 7:
		return "Asia"
	case 8:
		return "Worldwide"
	default:
		return ""
	}
}

func releaseStatusName(s int) string {
	switch s {
	case 1:
		return "alpha"
	case 2:
		return "beta"
	case 3:
		return "early_access"
	case 4:
		return "offline"
	case 5:
		return "cancelled"
	case 6:
		return "rumored"
	case 7:
		return "delisted"
	default:
		return "released"
	}
}

// Slugify turns a title into a URL slug.
func Slugify(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
