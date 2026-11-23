package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"luduslog/internal/models"
)

// Catalog relations are editable by trusted catalog editors. IGDB refresh
// continues to manage its own relation kinds through SaveGame; this list is
// the surface the catalog editor may rewrite.
var CatalogRelationKinds = []string{"related", "remake", "update", "port", "bundle", "bundle_content"}

// SaveCatalogGame creates or updates a manually edited catalog row. Unlike
// SaveGame it never touches IGDB identity, the slug, derived stats, synced_at,
// lock or merge fields. Associations and outgoing relations are replaced with
// the supplied values, including clearing kinds that were removed in the form.
func (s *Store) SaveCatalogGame(ctx context.Context, g *models.Game) (*models.Game, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := Now()
	search := buildSearchText(g)
	media, _ := json.Marshal(map[string]any{"screenshots": g.Screenshots, "videos": g.Videos})
	parent := nullableInt(g.ParentID)
	hastily, normally, completely := 0, 0, 0
	if g.TimeToBeat != nil {
		hastily, normally, completely = g.TimeToBeat.Hastily, g.TimeToBeat.Normally, g.TimeToBeat.Completely
	}
	if g.ID == 0 {
		if strings.TrimSpace(g.Slug) == "" {
			return nil, fmt.Errorf("catalog: missing slug")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO games(igdb_id,slug,title,sort_title,summary,cover_url,banner_url,release_date,
			release_year,release_status,category,parent_id,edition_name,franchise,adult,restricted,search_text,media,
			time_hastily,time_normally,time_completely,created_at,updated_at)
			VALUES(NULL,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			g.Slug, g.Title, sortTitle(g.Title), g.Summary, g.CoverURL, g.BannerURL, nullStr(g.ReleaseDate),
			nullInt(g.ReleaseYear), g.ReleaseStatus, g.Category, parent, g.EditionName, g.Franchise,
			b2i(g.Adult), b2i(g.Restricted), search, string(media), hastily, normally, completely, now, now)
		if err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id FROM games WHERE slug=?`, g.Slug).Scan(&g.ID); err != nil {
			return nil, err
		}
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE games SET title=?,sort_title=?,summary=?,cover_url=?,banner_url=?,
			release_date=?,release_year=?,release_status=?,category=?,parent_id=?,edition_name=?,franchise=?,
			adult=?,restricted=?,search_text=?,media=?,time_hastily=?,time_normally=?,time_completely=?,updated_at=?
			WHERE id=?`,
			g.Title, sortTitle(g.Title), g.Summary, g.CoverURL, g.BannerURL, nullStr(g.ReleaseDate),
			nullInt(g.ReleaseYear), g.ReleaseStatus, g.Category, parent, g.EditionName, g.Franchise,
			b2i(g.Adult), b2i(g.Restricted), search, string(media), hastily, normally, completely, now, g.ID)
		if err != nil {
			return nil, err
		}
	}

	if err := replaceCatalogAssociations(ctx, tx, g); err != nil {
		return nil, err
	}
	if err := replaceCatalogRelations(ctx, tx, g); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GameByID(ctx, g.ID)
}

func replaceCatalogAssociations(ctx context.Context, tx execQueryer, g *models.Game) error {
	for _, table := range []string{"game_platforms", "game_genres", "game_themes", "game_modes", "game_companies", "game_alt_names", "game_release_dates"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE game_id=?`, g.ID); err != nil {
			return err
		}
	}
	for _, p := range g.Platforms {
		if p.ID == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_platforms(game_id,platform_id) VALUES(?,?)`, g.ID, p.ID); err != nil {
			return err
		}
	}
	for _, v := range g.Genres {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_genres(game_id,genre) VALUES(?,?)`, g.ID, v); err != nil {
			return err
		}
	}
	for _, v := range g.Themes {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_themes(game_id,theme) VALUES(?,?)`, g.ID, v); err != nil {
			return err
		}
	}
	for _, v := range g.Modes {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_modes(game_id,mode) VALUES(?,?)`, g.ID, v); err != nil {
			return err
		}
	}
	for _, c := range g.Companies {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_companies(game_id,name,role) VALUES(?,?,?)`, g.ID, c.Name, c.Role); err != nil {
			return err
		}
	}
	for _, v := range g.AltNames {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_alt_names(game_id,name) VALUES(?,?)`, g.ID, v); err != nil {
			return err
		}
	}
	for _, rd := range g.ReleaseDates {
		var platformID any
		if rd.PlatformID > 0 {
			platformID = rd.PlatformID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_release_dates(game_id,platform_id,platform,region,date,human,status) VALUES(?,?,?,?,?,?,?)`,
			g.ID, platformID, rd.Platform, rd.Region, nullStr(rd.Date), rd.Human, rd.Status); err != nil {
			return err
		}
	}
	return nil
}

func replaceCatalogRelations(ctx context.Context, tx execQueryer, g *models.Game) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_relations WHERE game_id=? AND kind IN ('related','remake','update','port','bundle','bundle_content')`, g.ID); err != nil {
		return err
	}
	for _, group := range []struct {
		kind  string
		games []models.Game
	}{
		{"related", g.Related},
		{"remake", g.Remakes},
		{"update", g.Updates},
		{"port", g.Ports},
		{"bundle", g.Bundles},
		{"bundle_content", g.Contents},
	} {
		for _, rel := range group.games {
			if rel.ID == 0 || rel.ID == g.ID {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_relations(game_id,related_id,kind) VALUES(?,?,?)`, g.ID, rel.ID, group.kind); err != nil {
				return err
			}
		}
	}
	return nil
}

// CatalogRelations loads a game's outgoing editable relations keyed by kind.
func (s *Store) CatalogRelations(ctx context.Context, id int64) (map[string][]models.Game, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.kind,g.id
		FROM game_relations r JOIN games g ON g.id=r.related_id
		WHERE r.game_id=? AND r.kind IN ('related','remake','update','port','bundle','bundle_content')
		ORDER BY g.title`, id)
	if err != nil {
		return nil, err
	}
	type relation struct {
		kind string
		id   int64
	}
	var pairs []relation
	for rows.Next() {
		var r relation
		if err := rows.Scan(&r.kind, &r.id); err != nil {
			rows.Close()
			return nil, err
		}
		pairs = append(pairs, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := map[string][]models.Game{}
	for _, p := range pairs {
		g, err := s.GameByID(ctx, p.id)
		if err != nil {
			continue
		}
		out[p.kind] = append(out[p.kind], *g)
	}
	return out, nil
}

// EnsurePlatform returns the id of the named platform, creating a minimal
// platform row when the name is new. Matching is case-insensitive.
func (s *Store) EnsurePlatform(ctx context.Context, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("platform name is required")
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM platforms WHERE name=? COLLATE NOCASE`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO platforms(name) VALUES(?)`, name); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM platforms WHERE name=? COLLATE NOCASE`, name).Scan(&id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
