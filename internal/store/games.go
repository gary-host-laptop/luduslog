package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"luduslog/internal/models"
)

const gameCols = `g.id,COALESCE(g.igdb_id,0),g.slug,g.title,g.summary,g.cover_url,g.banner_url,COALESCE(g.release_date,''),
COALESCE(g.release_year,0),g.release_status,g.category,g.parent_id,g.edition_name,g.franchise,g.adult,g.restricted,
g.avg_rating,g.rating_count,g.weighted,g.popularity,g.plays,g.completions,g.backlogs,g.wishlists,g.playing,
g.likes,g.lists_count,g.reviews,g.updated_at,g.synced_at`

func scanGame(row scanner) (*models.Game, error) {
	var g models.Game
	var parent sql.NullInt64
	var updated string
	var synced sql.NullString
	var adult, restricted int
	err := row.Scan(&g.ID, &g.IGDBID, &g.Slug, &g.Title, &g.Summary, &g.CoverURL, &g.BannerURL, &g.ReleaseDate,
		&g.ReleaseYear, &g.ReleaseStatus, &g.Category, &parent, &g.EditionName, &g.Franchise, &adult, &restricted,
		&g.AvgRating, &g.RatingCount, &g.Weighted, &g.Popularity, &g.Plays, &g.Completions, &g.Backlogs,
		&g.Wishlists, &g.Playing, &g.Likes, &g.ListsCount, &g.Reviews, &updated, &synced)
	if err != nil {
		return nil, err
	}
	if parent.Valid {
		g.ParentID = &parent.Int64
	}
	g.Adult = adult == 1
	g.Restricted = restricted == 1
	g.UpdatedAt = parseTime(updated)
	g.SyncedAt = parseNullTime(synced)
	return &g, nil
}

func (s *Store) GameByID(ctx context.Context, id int64) (*models.Game, error) {
	g, err := scanGame(s.DB.QueryRowContext(ctx, `SELECT `+gameCols+` FROM games g WHERE g.id=?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Store) GameBySlug(ctx context.Context, slug string) (*models.Game, error) {
	g, err := scanGame(s.DB.QueryRowContext(ctx, `SELECT `+gameCols+` FROM games g WHERE g.slug=?`, slug))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

// SetGameIGDBID adopts a manually seeded row into its IGDB identity.
func (s *Store) SetGameIGDBID(ctx context.Context, id, igdbID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE games SET igdb_id=? WHERE id=?`, igdbID, id)
	return err
}

func (s *Store) GameByIGDB(ctx context.Context, igdbID int64) (*models.Game, error) {
	g, err := scanGame(s.DB.QueryRowContext(ctx, `SELECT `+gameCols+` FROM games g WHERE g.igdb_id=?`, igdbID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return g, err
}

// GameDetail fills the associations rendered on the game page.
func (s *Store) GameDetail(ctx context.Context, g *models.Game) (*models.Game, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id,COALESCE(p.igdb_id,0),p.name,p.abbrev,p.generation,p.family
		FROM game_platforms gp JOIN platforms p ON p.id=gp.platform_id WHERE gp.game_id=? ORDER BY p.generation DESC, p.name`, g.ID)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var p models.Platform
		if err := rows.Scan(&p.ID, &p.IGDBID, &p.Name, &p.Abbrev, &p.Generation, &p.Family); err != nil {
			continue
		}
		g.Platforms = append(g.Platforms, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return g, err
	}
	rows.Close()
	loadStrings := func(table, col string) []string {
		r, err := s.DB.QueryContext(ctx, `SELECT `+col+` FROM `+table+` WHERE game_id=? ORDER BY 1`, g.ID)
		if err != nil {
			return nil
		}
		defer r.Close()
		var out []string
		for r.Next() {
			var v string
			if err := r.Scan(&v); err != nil {
				continue
			}
			out = append(out, v)
		}
		return out
	}
	g.Genres = loadStrings("game_genres", "genre")
	g.Themes = loadStrings("game_themes", "theme")
	g.Modes = loadStrings("game_modes", "mode")
	g.AltNames = loadStrings("game_alt_names", "name")
	crows, err := s.DB.QueryContext(ctx, `SELECT name,role FROM game_companies WHERE game_id=? ORDER BY role,name`, g.ID)
	if err != nil {
		return g, err
	}
	for crows.Next() {
		var c models.CompanyRole
		if err := crows.Scan(&c.Name, &c.Role); err != nil {
			continue
		}
		g.Companies = append(g.Companies, c)
	}
	crows.Close()
	rrows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(platform_id,0),platform,region,COALESCE(date,''),human,status FROM game_release_dates WHERE game_id=? ORDER BY date`, g.ID)
	if err != nil {
		return g, err
	}
	for rrows.Next() {
		var rd models.ReleaseDate
		if err := rrows.Scan(&rd.PlatformID, &rd.Platform, &rd.Region, &rd.Date, &rd.Human, &rd.Status); err != nil {
			continue
		}
		g.ReleaseDates = append(g.ReleaseDates, rd)
	}
	rrows.Close()
	var media string
	s.DB.QueryRowContext(ctx, `SELECT media FROM games WHERE id=?`, g.ID).Scan(&media)
	var m struct {
		Screenshots []string `json:"screenshots"`
		Videos      []string `json:"videos"`
	}
	json.Unmarshal([]byte(media), &m)
	g.Screenshots = m.Screenshots
	g.Videos = m.Videos

	if g.ParentID != nil {
		if parent, err := s.GameByID(ctx, *g.ParentID); err == nil {
			parent.Editions, _ = s.Editions(ctx, parent.ID)
			g.Editions = parent.Editions
		}
	} else {
		g.Editions, _ = s.Editions(ctx, g.ID)
	}
	g.Contents, _ = s.BundleContents(ctx, g.ID)
	g.Bundles, _ = s.BundlesFor(ctx, g.ID)
	return g, nil
}

func (s *Store) Editions(ctx context.Context, parentID int64) ([]models.Game, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+gameCols+` FROM games g WHERE g.parent_id=? ORDER BY g.release_date, g.title`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (s *Store) BundleContents(ctx context.Context, gameID int64) ([]models.Game, error) {
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM game_relations r JOIN games g ON g.id=r.related_id
		WHERE r.game_id=? AND r.kind='bundle_content' ORDER BY g.release_date`, gameID)
}

func (s *Store) BundlesFor(ctx context.Context, gameID int64) ([]models.Game, error) {
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM game_relations r JOIN games g ON g.id=r.related_id
		WHERE r.game_id=? AND r.kind='bundle' ORDER BY g.title`, gameID)
}

func (s *Store) relatedGames(ctx context.Context, query string, args ...any) ([]models.Game, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// SeriesGames returns games sharing a franchise, or related by IGDB relation.
func (s *Store) SeriesGames(ctx context.Context, franchise string, limit int) ([]models.Game, error) {
	if franchise == "" {
		return nil, nil
	}
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM games g WHERE g.franchise=? AND g.parent_id IS NULL
		ORDER BY COALESCE(g.release_date,'9999') LIMIT ?`, franchise, limit)
}

func (s *Store) RelatedGames(ctx context.Context, gameID int64, limit int) ([]models.Game, error) {
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM game_relations r JOIN games g ON g.id=r.related_id
		WHERE r.game_id=? AND r.kind='related' ORDER BY g.title LIMIT ?`, gameID, limit)
}

// GameFilter is the full games-index filter surface.
type GameFilter struct {
	Q               string
	Genres          []string
	Themes          []string
	Platforms       []int64
	YearFrom        int
	YearTo          int
	Release         string // "" | released | upcoming
	Categories      []string
	Company         string
	Franchise       string
	MinRating       float64
	ObscurityMin    int
	ObscurityMax    int
	Mastered        bool
	HasMastered     bool
	Ownership       string
	PlayedYear      int
	CustomCover     bool
	Statuses        []string
	FriendsOnly     bool
	ExcludeEditions bool
	Adult           bool
	IncludeAdult    bool
	UserID          int64
	Sort            string
	Dir             string
	Limit           int
	Offset          int
	Seed            int64
	HideOwned       bool
	NoStatus        bool
}

func (f *GameFilter) normalize() {
	if f.Limit <= 0 {
		f.Limit = 60
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	if f.Sort == "" {
		f.Sort = "trending"
	}
	if f.Dir == "" {
		f.Dir = "desc"
	}
}

func (f GameFilter) where(ctx context.Context, s *Store) (string, []any) {
	var conds []string
	var args []any
	if !f.IncludeAdult {
		conds = append(conds, "g.adult=0")
	}
	if f.ExcludeEditions {
		conds = append(conds, "g.parent_id IS NULL")
	}
	if f.Q != "" {
		if match := ftsQuery(f.Q); match != "" {
			conds = append(conds, "g.id IN (SELECT rowid FROM games_fts WHERE games_fts MATCH ?)")
			args = append(args, match)
		} else {
			conds = append(conds, "1=0")
		}
	}
	for _, genre := range f.Genres {
		conds = append(conds, "EXISTS(SELECT 1 FROM game_genres gg WHERE gg.game_id=g.id AND gg.genre=?)")
		args = append(args, genre)
	}
	for _, theme := range f.Themes {
		conds = append(conds, "EXISTS(SELECT 1 FROM game_themes gt WHERE gt.game_id=g.id AND gt.theme=?)")
		args = append(args, theme)
	}
	for _, p := range f.Platforms {
		conds = append(conds, "EXISTS(SELECT 1 FROM game_platforms gp WHERE gp.game_id=g.id AND gp.platform_id=?)")
		args = append(args, p)
	}
	if f.YearFrom > 0 {
		conds = append(conds, "g.release_year>=?")
		args = append(args, f.YearFrom)
	}
	if f.YearTo > 0 {
		conds = append(conds, "g.release_year<=?")
		args = append(args, f.YearTo)
	}
	switch f.Release {
	case "released":
		conds = append(conds, "g.release_date IS NOT NULL AND g.release_date<=date('now')")
	case "upcoming":
		conds = append(conds, "g.release_date IS NOT NULL AND g.release_date>date('now')")
	}
	if len(f.Categories) > 0 {
		ph := make([]string, len(f.Categories))
		for i, c := range f.Categories {
			ph[i] = "?"
			args = append(args, c)
		}
		conds = append(conds, "g.category IN ("+strings.Join(ph, ",")+")")
	}
	if f.Company != "" {
		conds = append(conds, "EXISTS(SELECT 1 FROM game_companies gc WHERE gc.game_id=g.id AND gc.name=?)")
		args = append(args, f.Company)
	}
	if f.Franchise != "" {
		conds = append(conds, "g.franchise=?")
		args = append(args, f.Franchise)
	}
	if f.MinRating > 0 {
		conds = append(conds, "g.avg_rating>=?")
		args = append(args, f.MinRating)
	}
	if f.ObscurityMin > 0 {
		conds = append(conds, "g.plays>=?")
		args = append(args, f.ObscurityMin)
	}
	if f.ObscurityMax > 0 {
		conds = append(conds, "g.plays<=?")
		args = append(args, f.ObscurityMax)
	}
	if f.HasMastered {
		conds = append(conds, "EXISTS(SELECT 1 FROM logs l WHERE l.game_id=g.id AND l.mastered=1 AND l.deleted_at IS NULL)")
	}
	if f.UserID > 0 {
		if len(f.Statuses) > 0 {
			var parts []string
			for _, st := range f.Statuses {
				switch st {
				case "played":
					parts = append(parts, "st.played>0")
				case "playing", "backlog", "wishlist":
					parts = append(parts, "st."+st+"=1")
				}
			}
			if len(parts) > 0 {
				conds = append(conds, "EXISTS(SELECT 1 FROM statuses st WHERE st.game_id=g.id AND st.user_id=? AND ("+strings.Join(parts, " OR ")+"))")
				args = append(args, f.UserID)
			}
		}
		if f.Ownership != "" {
			conds = append(conds, "EXISTS(SELECT 1 FROM library_entries le WHERE le.game_id=g.id AND le.user_id=? AND le.ownership=?)")
			args = append(args, f.UserID, f.Ownership)
		}
		if f.PlayedYear > 0 {
			conds = append(conds, "EXISTS(SELECT 1 FROM logs l JOIN play_sessions ps ON ps.log_id=l.id JOIN play_session_days psd ON psd.session_id=ps.id WHERE l.game_id=g.id AND l.user_id=? AND substr(psd.day,1,4)=?)")
			args = append(args, f.UserID, strconv.Itoa(f.PlayedYear))
		}
		if f.CustomCover {
			conds = append(conds, "EXISTS(SELECT 1 FROM covers c WHERE c.game_id=g.id AND c.user_id=?)")
			args = append(args, f.UserID)
		}
		if f.HideOwned {
			conds = append(conds, "NOT EXISTS(SELECT 1 FROM statuses st WHERE st.game_id=g.id AND st.user_id=? AND (st.played>0 OR st.playing=1 OR st.backlog=1 OR st.wishlist=1))")
			args = append(args, f.UserID)
		}
		if f.NoStatus {
			conds = append(conds, "NOT EXISTS(SELECT 1 FROM statuses st WHERE st.game_id=g.id AND st.user_id=? AND (st.played>0 OR st.playing=1 OR st.backlog=1 OR st.wishlist=1))")
			args = append(args, f.UserID)
		}
	}
	if f.FriendsOnly && f.UserID > 0 {
		friends := s.FriendIDs(ctx, f.UserID)
		if len(friends) == 0 {
			conds = append(conds, "1=0")
		} else {
			ph := make([]string, len(friends))
			for i, id := range friends {
				ph[i] = "?"
				args = append(args, id)
			}
			conds = append(conds, "EXISTS(SELECT 1 FROM statuses st WHERE st.game_id=g.id AND st.user_id IN ("+strings.Join(ph, ",")+") AND (st.played>0 OR st.playing=1 OR st.backlog=1 OR st.wishlist=1))")
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

var sortColumns = map[string]string{
	"trending":  "g.popularity",
	"popular":   "g.popularity",
	"rating":    "g.weighted",
	"top":       "g.weighted",
	"release":   "COALESCE(g.release_date,'9999-12-31')",
	"name":      "g.sort_title",
	"plays":     "g.plays",
	"wishlists": "g.wishlists",
	"likes":     "g.likes",
	"reviews":   "g.reviews",
	"random":    "",
	"obscurity": "g.plays",
	"recent":    "g.updated_at",
}

// ListGames executes the games index query and returns games plus total count.
func (s *Store) ListGames(ctx context.Context, f GameFilter) ([]models.Game, int, error) {
	f.normalize()
	where, args := f.where(ctx, s)
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM games g`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "g.popularity DESC, g.title"
	col, ok := sortColumns[f.Sort]
	if !ok {
		col = sortColumns["trending"]
	}
	dir := "DESC"
	if strings.EqualFold(f.Dir, "asc") {
		dir = "ASC"
	}
	switch f.Sort {
	case "random":
		seed := f.Seed
		if seed == 0 {
			seed = 42
		}
		// A stable pseudo-random ordering for pagination.
		order = fmt.Sprintf("((g.id * %d) %% 100003), g.popularity DESC", seed)
	case "name":
		order = "g.sort_title " + dir + ", g.release_year"
	default:
		if col != "" {
			order = col + " " + dir + ", g.popularity DESC, g.title"
		}
	}
	query := `SELECT ` + gameCols + ` FROM games g` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *g)
	}
	return out, total, rows.Err()
}

// ftsQuery converts user input to a safe FTS5 prefix query. Only letters and
// numbers survive, so quotes, operators, punctuation and path characters can
// never change the meaning of the query.
func ftsQuery(q string) string {
	var b strings.Builder
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	fields := strings.Fields(b.String())
	if len(fields) > 12 {
		fields = fields[:12]
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		runes := []rune(f)
		if len(runes) > 64 {
			f = string(runes[:64])
		}
		parts = append(parts, f+"*")
	}
	return strings.Join(parts, " ")
}

// SearchGames searches the catalog, excluding editions from results.
func (s *Store) SearchGames(ctx context.Context, q string, limit, offset int, includeAdult bool) ([]models.Game, int, error) {
	match := ftsQuery(q)
	if match == "" {
		return nil, 0, nil
	}
	adult := ""
	if !includeAdult {
		adult = " AND g.adult=0"
	}
	var total int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM games g JOIN games_fts ON games_fts.rowid=g.id
		WHERE games_fts MATCH ? AND g.parent_id IS NULL`+adult, match).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+gameCols+` FROM games g JOIN games_fts ON games_fts.rowid=g.id
		WHERE games_fts MATCH ? AND g.parent_id IS NULL`+adult+`
		ORDER BY bm25(games_fts), g.popularity DESC, g.id DESC LIMIT ? OFFSET ?`, match, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *g)
	}
	return out, total, rows.Err()
}

// QuickSearch returns compact results for the header search box.
func (s *Store) QuickSearch(ctx context.Context, q string, limit int, includeAdult bool) ([]models.Game, []*models.User, []models.List, error) {
	games, _, err := s.SearchGames(ctx, q, limit, 0, includeAdult)
	if err != nil {
		return nil, nil, nil, err
	}
	users, _, _ := s.SearchUsers(ctx, q, 3, 0)
	lists, _, _ := s.SearchLists(ctx, q, 3, 0)
	return games, users, lists, nil
}

// SaveGame upserts an IGDB game and its associations.
func (s *Store) SaveGame(ctx context.Context, g *models.Game) (*models.Game, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := Now()
	search := buildSearchText(g)
	media, _ := json.Marshal(map[string]any{"screenshots": g.Screenshots, "videos": g.Videos})
	parent := nullableInt(g.ParentID)
	res, err := tx.ExecContext(ctx, `INSERT INTO games(igdb_id,slug,title,sort_title,summary,cover_url,banner_url,release_date,
		release_year,release_status,category,parent_id,edition_name,franchise,adult,restricted,search_text,media,created_at,updated_at,synced_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(igdb_id) DO UPDATE SET slug=excluded.slug,title=excluded.title,sort_title=excluded.sort_title,
		summary=excluded.summary,cover_url=CASE WHEN excluded.cover_url<>'' THEN excluded.cover_url ELSE games.cover_url END,
		banner_url=excluded.banner_url,release_date=excluded.release_date,release_year=excluded.release_year,
		release_status=excluded.release_status,category=excluded.category,parent_id=excluded.parent_id,
		edition_name=excluded.edition_name,franchise=excluded.franchise,adult=excluded.adult,restricted=excluded.restricted,
		search_text=excluded.search_text,media=excluded.media,updated_at=excluded.updated_at,synced_at=excluded.synced_at`,
		g.IGDBID, g.Slug, g.Title, sortTitle(g.Title), g.Summary, g.CoverURL, g.BannerURL, nullStr(g.ReleaseDate),
		nullInt(g.ReleaseYear), g.ReleaseStatus, g.Category, parent, g.EditionName, g.Franchise, b2i(g.Adult),
		b2i(g.Restricted), search, string(media), now, now, now)
	if err != nil {
		return nil, err
	}
	// LastInsertId is unreliable for an UPSERT update in SQLite. Prefer the
	// id the caller already knows, then fall back to a lookup.
	id, _ := res.LastInsertId()
	if g.ID > 0 {
		id = g.ID
	}
	if id == 0 && g.IGDBID != 0 {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM games WHERE igdb_id=?`, g.IGDBID).Scan(&id); err != nil {
			return nil, err
		}
	}
	if id == 0 && g.Slug != "" {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM games WHERE slug=?`, g.Slug).Scan(&id); err != nil {
			return nil, err
		}
	}
	if id == 0 {
		return nil, fmt.Errorf("games: could not resolve id for %q", g.Title)
	}
	g.ID = id
	for _, table := range []string{"game_platforms", "game_genres", "game_themes", "game_modes", "game_companies", "game_alt_names", "game_release_dates", "game_relations"} {
		tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE game_id=?`, id)
	}
	for _, p := range g.Platforms {
		pid, err := savePlatform(ctx, tx, p)
		if err != nil {
			return nil, fmt.Errorf("platform %q: %w", p.Name, err)
		}
		if pid > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_platforms(game_id,platform_id) VALUES(?,?)`, id, pid); err != nil {
				return nil, err
			}
		}
	}
	for _, v := range g.Genres {
		tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_genres(game_id,genre) VALUES(?,?)`, id, v)
	}
	for _, v := range g.Themes {
		tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_themes(game_id,theme) VALUES(?,?)`, id, v)
	}
	for _, v := range g.Modes {
		tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_modes(game_id,mode) VALUES(?,?)`, id, v)
	}
	for _, c := range g.Companies {
		tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_companies(game_id,name,role) VALUES(?,?,?)`, id, c.Name, c.Role)
	}
	for _, v := range g.AltNames {
		tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_alt_names(game_id,name) VALUES(?,?)`, id, v)
	}
	for _, rd := range g.ReleaseDates {
		var platformID any
		if rd.PlatformID > 0 {
			platformID = rd.PlatformID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_release_dates(game_id,platform_id,platform,region,date,human,status) VALUES(?,?,?,?,?,?,?)`,
			id, platformID, rd.Platform, rd.Region, nullStr(rd.Date), rd.Human, rd.Status); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GameByID(ctx, id)
}

func savePlatform(ctx context.Context, tx *sql.Tx, p models.Platform) (int64, error) {
	if p.Name == "" {
		return 0, nil
	}
	var id int64
	if p.IGDBID > 0 {
		err := tx.QueryRowContext(ctx, `SELECT id FROM platforms WHERE igdb_id=?`, p.IGDBID).Scan(&id)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE platforms SET name=?,abbrev=?,generation=?,family=? WHERE id=?`,
				p.Name, p.Abbrev, p.Generation, p.Family, id)
			return id, err
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
		// An older name-only row may already own this name. Adopt it instead of
		// violating the unique name index.
		err = tx.QueryRowContext(ctx, `SELECT id FROM platforms WHERE name=? COLLATE NOCASE`, p.Name).Scan(&id)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE platforms SET igdb_id=?,abbrev=?,generation=?,family=? WHERE id=?`,
				p.IGDBID, p.Abbrev, p.Generation, p.Family, id)
			return id, err
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO platforms(igdb_id,name,abbrev,generation,family) VALUES(?,?,?,?,?)`,
			p.IGDBID, p.Name, p.Abbrev, p.Generation, p.Family)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	err := tx.QueryRowContext(ctx, `SELECT id FROM platforms WHERE name=? COLLATE NOCASE`, p.Name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO platforms(name) VALUES(?) ON CONFLICT DO NOTHING`, p.Name)
	if err != nil {
		return 0, err
	}
	if id, _ := res.LastInsertId(); id > 0 {
		return id, nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM platforms WHERE name=? COLLATE NOCASE`, p.Name).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func buildSearchText(g *models.Game) string {
	var b strings.Builder
	b.WriteString(g.Title)
	b.WriteString(" ")
	b.WriteString(strings.Join(g.AltNames, " "))
	b.WriteString(" ")
	b.WriteString(g.Franchise)
	b.WriteString(" ")
	b.WriteString(g.Summary)
	for _, c := range g.Companies {
		b.WriteString(" ")
		b.WriteString(c.Name)
	}
	// Roman numerals ⇄ digits so "Final Fantasy VII" matches "Final Fantasy 7".
	b.WriteString(" ")
	b.WriteString(romanVariants(g.Title))
	return b.String()
}

func romanVariants(s string) string {
	var out []string
	for _, tok := range strings.Fields(s) {
		if v, ok := romanToInt(strings.ToUpper(tok)); ok && v > 0 {
			out = append(out, strconv.Itoa(v))
		}
		if n, err := strconv.Atoi(tok); err == nil && n > 0 && n < 100 {
			out = append(out, intToRoman(n))
		}
	}
	return strings.Join(out, " ")
}

func romanToInt(s string) (int, bool) {
	vals := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	total, prev := 0, 0
	for i := len(s) - 1; i >= 0; i-- {
		v, ok := vals[s[i]]
		if !ok {
			return 0, false
		}
		if v < prev {
			total -= v
		} else {
			total += v
			prev = v
		}
	}
	return total, total > 0
}

func intToRoman(n int) string {
	table := []struct {
		v int
		s string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}}
	var b strings.Builder
	for _, e := range table {
		for n >= e.v {
			b.WriteString(e.s)
			n -= e.v
		}
	}
	return b.String()
}

// sortTitle ignores leading articles.
func sortTitle(t string) string {
	lower := strings.ToLower(t)
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, a) {
			return t[len(a):]
		}
	}
	return t
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// execQueryer is satisfied by *sql.DB and *sql.Tx so stats can be refreshed
// inside a deletion transaction as well as on their own.
type execQueryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RefreshGameStats recomputes cached counters for one game.
func (s *Store) RefreshGameStats(ctx context.Context, id int64) error {
	return refreshGameStats(ctx, s.DB, id)
}

func refreshGameStats(ctx context.Context, q execQueryer, id int64) error {
	_, err := q.ExecContext(ctx, `UPDATE games SET
		avg_rating=COALESCE((SELECT AVG(l.rating) FROM logs l WHERE l.game_id=games.id AND l.rating>0 AND l.primary_rating=1 AND l.deleted_at IS NULL),0),
		rating_count=(SELECT COUNT(*) FROM logs l WHERE l.game_id=games.id AND l.rating>0 AND l.primary_rating=1 AND l.deleted_at IS NULL),
		reviews=(SELECT COUNT(*) FROM logs l WHERE l.game_id=games.id AND l.review<>'' AND l.deleted_at IS NULL),
		plays=(SELECT COUNT(*) FROM statuses st WHERE st.game_id=games.id AND st.played>0),
		playing=(SELECT COUNT(*) FROM statuses st WHERE st.game_id=games.id AND st.playing=1),
		backlogs=(SELECT COUNT(*) FROM statuses st WHERE st.game_id=games.id AND st.backlog=1),
		wishlists=(SELECT COUNT(*) FROM statuses st WHERE st.game_id=games.id AND st.wishlist=1),
		completions=(SELECT COUNT(DISTINCT l.user_id) FROM logs l WHERE l.game_id=games.id AND l.status='completed' AND l.deleted_at IS NULL),
		likes=(SELECT COUNT(*) FROM game_likes gl WHERE gl.game_id=games.id),
		lists_count=(SELECT COUNT(DISTINCT le.list_id) FROM list_entries le JOIN lists li ON li.id=le.list_id WHERE le.game_id=games.id AND li.privacy IN ('public','unlisted','personal') AND li.deleted_at IS NULL),
		popularity=(SELECT COUNT(*) FROM statuses st WHERE st.game_id=games.id AND (st.played>0 OR st.playing=1 OR st.backlog=1 OR st.wishlist=1))
			+ 2*(SELECT COUNT(*) FROM game_likes gl WHERE gl.game_id=games.id)
			+ 3*(SELECT COUNT(*) FROM logs l WHERE l.game_id=games.id AND l.review<>'' AND l.deleted_at IS NULL)
		WHERE id=?`, id)
	if err != nil {
		return err
	}
	var avg float64
	var count int
	q.QueryRowContext(ctx, `SELECT avg_rating,rating_count FROM games WHERE id=?`, id).Scan(&avg, &count)
	_, err = q.ExecContext(ctx, `UPDATE games SET weighted=? WHERE id=?`, WeightedRating(avg, count), id)
	return err
}

func (s *Store) RefreshAllGameStats(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM games`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.RefreshGameStats(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Facets returns filter sidebar values with counts.
type Facets struct {
	Genres     []Facet
	Themes     []Facet
	Platforms  []models.Platform
	Companies  []string
	Franchises []string
	Years      []int
	Categories []Facet
}

type Facet struct {
	Name  string
	Count int
}

func (s *Store) GameFacets(ctx context.Context) Facets {
	var f Facets
	for _, spec := range []struct {
		table string
		col   string
		dst   *[]Facet
	}{
		{"game_genres", "genre", &f.Genres},
		{"game_themes", "theme", &f.Themes},
		{"(SELECT game_id, category AS genre FROM games WHERE parent_id IS NULL)", "genre", &f.Categories},
	} {
		rows, err := s.DB.QueryContext(ctx, `SELECT `+spec.col+`, COUNT(*) FROM `+spec.table+` GROUP BY 1 ORDER BY 2 DESC, 1`)
		if err != nil {
			continue
		}
		for rows.Next() {
			var x Facet
			rows.Scan(&x.Name, &x.Count)
			*spec.dst = append(*spec.dst, x)
		}
		rows.Close()
	}
	prows, err := s.DB.QueryContext(ctx, `SELECT p.id,COALESCE(p.igdb_id,0),p.name,p.abbrev,p.generation,p.family FROM platforms p
		WHERE EXISTS(SELECT 1 FROM game_platforms gp WHERE gp.platform_id=p.id)
		ORDER BY p.generation DESC, p.name`)
	if err == nil {
		for prows.Next() {
			var p models.Platform
			if err := prows.Scan(&p.ID, &p.IGDBID, &p.Name, &p.Abbrev, &p.Generation, &p.Family); err != nil {
				continue
			}
			f.Platforms = append(f.Platforms, p)
		}
		prows.Close()
	}
	crows, _ := s.DB.QueryContext(ctx, `SELECT name FROM game_companies GROUP BY name ORDER BY COUNT(*) DESC LIMIT 200`)
	if crows != nil {
		for crows.Next() {
			var n string
			crows.Scan(&n)
			f.Companies = append(f.Companies, n)
		}
		crows.Close()
	}
	frows, _ := s.DB.QueryContext(ctx, `SELECT franchise FROM games WHERE franchise<>'' GROUP BY franchise ORDER BY COUNT(*) DESC`)
	if frows != nil {
		for frows.Next() {
			var n string
			frows.Scan(&n)
			f.Franchises = append(f.Franchises, n)
		}
		frows.Close()
	}
	yrows, _ := s.DB.QueryContext(ctx, `SELECT DISTINCT release_year FROM games WHERE release_year>0 ORDER BY release_year DESC`)
	if yrows != nil {
		for yrows.Next() {
			var y int
			yrows.Scan(&y)
			f.Years = append(f.Years, y)
		}
		yrows.Close()
	}
	return f
}

func (s *Store) Platforms(ctx context.Context) []models.Platform {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,COALESCE(igdb_id,0),name,abbrev,generation,family FROM platforms ORDER BY generation DESC, name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []models.Platform
	for rows.Next() {
		var p models.Platform
		if err := rows.Scan(&p.ID, &p.IGDBID, &p.Name, &p.Abbrev, &p.Generation, &p.Family); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// FeaturedCollections implements the discovery rails on the home page.
func (s *Store) RecentlyTrending(ctx context.Context, limit int) ([]models.Game, error) {
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM games g WHERE g.parent_id IS NULL AND g.popularity>0 ORDER BY g.popularity DESC LIMIT ?`, limit)
}

func (s *Store) SleeperHits(ctx context.Context, limit int) ([]models.Game, error) {
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM games g WHERE g.avg_rating>=4.0 AND g.rating_count>=2 AND g.plays<100 AND g.parent_id IS NULL ORDER BY g.weighted DESC, g.plays ASC LIMIT ?`, limit)
}

func (s *Store) RecentlyAnticipated(ctx context.Context, limit int) ([]models.Game, error) {
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM games g WHERE g.wishlists>0 AND g.parent_id IS NULL ORDER BY g.wishlists DESC, g.release_date LIMIT ?`, limit)
}

func (s *Store) ComingSoon(ctx context.Context, days int, wishlistOf int64) ([]models.Game, error) {
	start := "date('now')"
	end := fmt.Sprintf("date('now','+%d day')", days)
	extra := ""
	args := []any{}
	if wishlistOf > 0 {
		extra = " AND EXISTS(SELECT 1 FROM statuses st WHERE st.game_id=g.id AND st.user_id=? AND st.wishlist=1)"
		args = append(args, wishlistOf)
	}
	args = append(args, days)
	_ = start
	_ = end
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM games g WHERE g.release_date>date('now') AND g.release_date<=date('now','+'||?||' day') AND g.parent_id IS NULL`+extra+` ORDER BY g.release_date, g.title`, args...)
}

func (s *Store) Top100(ctx context.Context, limit int) ([]models.Game, error) {
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM games g WHERE g.parent_id IS NULL AND g.category='main_game' AND g.rating_count>0 ORDER BY g.weighted DESC LIMIT ?`, limit)
}

// RandomGames returns a deterministic random selection for discovery.
func (s *Store) RandomGames(ctx context.Context, n int, excludeAdult bool, seed int64) ([]models.Game, error) {
	if seed == 0 {
		seed = rand.New(rand.NewSource(1)).Int63()%100000 + 1
	}
	adult := ""
	if excludeAdult {
		adult = " AND adult=0"
	}
	return s.relatedGames(ctx, `SELECT `+gameCols+` FROM games g WHERE parent_id IS NULL`+adult+` ORDER BY ((g.id * ?) % 100003) LIMIT ?`, seed, n)
}

func (s *Store) GamesInList(ctx context.Context, listID int64) ([]models.ListEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT le.list_id,le.game_id,le.position,le.note,le.added_at FROM list_entries le WHERE le.list_id=? ORDER BY le.position`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ListEntry
	for rows.Next() {
		var e models.ListEntry
		var added string
		rows.Scan(&e.ListID, &e.GameID, &e.Position, &e.Note, &added)
		e.AddedAt = parseTime(added)
		if g, err := s.GameByID(ctx, e.GameID); err == nil {
			e.Game = g
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AllGenres/Themes are used by settings and discovery.
func (s *Store) DistinctGameColumn(ctx context.Context, table, col string) []string {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT `+col+` FROM `+table+` ORDER BY 1`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		rows.Scan(&v)
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
