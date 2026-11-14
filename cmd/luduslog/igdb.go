package main

import (
	"context"
	"fmt"

	"luduslog/internal/config"
	"luduslog/internal/igdb"
	"luduslog/internal/store"
)

// igdbSync imports games from IGDB, updating existing rows by IGDB id.
func igdbSync(ctx context.Context, st *store.Store, cfg config.Config, limit int) error {
	client := igdb.New(cfg.IgdbClientID, cfg.IgdbSecret)
	games, err := client.Games(ctx, limit)
	if err != nil {
		return err
	}
	added, updated := 0, 0
	for i := range games {
		g := &games[i]
		if existing, err := st.GameByIGDB(ctx, g.IGDBID); err == nil {
			g.ID = existing.ID
			g.Slug = existing.Slug
			updated++
		} else if existing, err := st.GameBySlug(ctx, g.Slug); err == nil {
			// A manually seeded game already owns this slug; adopt it into the
			// IGDB record rather than failing the unique slug constraint.
			if _, err := st.DB.ExecContext(ctx, `UPDATE games SET igdb_id=? WHERE id=?`, g.IGDBID, existing.ID); err != nil {
				return fmt.Errorf("%s: adopt slug: %w", g.Title, err)
			}
			g.ID = existing.ID
			updated++
		} else {
			added++
		}
		if _, err := st.SaveGame(ctx, g); err != nil {
			return fmt.Errorf("%s: %w", g.Title, err)
		}
	}
	fmt.Printf("IGDB sync complete: %d added, %d updated\n", added, updated)
	return nil
}
