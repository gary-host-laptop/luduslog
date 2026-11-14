// Package catalog contains the shared "save an IGDB game locally" logic used
// by the CLI and the admin web handlers.
package catalog

import (
	"context"
	"fmt"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

// SaveResult reports whether Save inserted a new row.
type SaveResult struct {
	Game    *models.Game
	Created bool
}

// Save adopts an existing row by IGDB id or slug before upserting, so manual
// seed rows never block an IGDB record with the same slug.
func Save(ctx context.Context, st *store.Store, g *models.Game) (SaveResult, error) {
	if g == nil || g.IGDBID <= 0 {
		return SaveResult{}, fmt.Errorf("catalog: missing IGDB id")
	}
	created := true
	if existing, err := st.GameByIGDB(ctx, g.IGDBID); err == nil {
		g.ID = existing.ID
		g.Slug = existing.Slug
		created = false
	} else if existing, err := st.GameBySlug(ctx, g.Slug); err == nil {
		if err := st.SetGameIGDBID(ctx, existing.ID, g.IGDBID); err != nil {
			return SaveResult{}, fmt.Errorf("%s: adopt slug: %w", g.Title, err)
		}
		g.ID = existing.ID
		g.Slug = existing.Slug
		created = false
	}
	saved, err := st.SaveGame(ctx, g)
	if err != nil {
		return SaveResult{}, fmt.Errorf("%s: %w", g.Title, err)
	}
	return SaveResult{Game: saved, Created: created}, nil
}
