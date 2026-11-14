package main

import (
	"context"
	"fmt"

	"luduslog/internal/catalog"
	"luduslog/internal/config"
	"luduslog/internal/igdb"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

// igdbSync imports games from IGDB in API-sized pages, updating existing rows
// by IGDB id. offset makes long imports resumable.
func igdbSync(ctx context.Context, st *store.Store, cfg config.Config, limit, offset int) error {
	client := igdb.New(cfg.IgdbClientID, cfg.IgdbSecret)
	games, err := client.GamesRange(ctx, limit, offset, func(done, total int) {
		fmt.Printf("  fetched %d/%d\n", done, total)
	})
	if err != nil {
		if len(games) > 0 {
			return fmt.Errorf("%w (resume with --offset %d)", err, offset+len(games))
		}
		return err
	}
	added, updated := 0, 0
	for i := range games {
		res, err := catalog.Save(ctx, st, &games[i])
		if err != nil {
			return err
		}
		if res.Created {
			added++
		} else {
			updated++
		}
	}
	fmt.Printf("IGDB sync complete: %d added, %d updated (next offset %d)\n", added, updated, offset+len(games))
	return nil
}

func igdbFetch(ctx context.Context, st *store.Store, cfg config.Config, id int64, slug, search string) (*models.Game, error) {
	client := igdb.New(cfg.IgdbClientID, cfg.IgdbSecret)
	var game *models.Game
	switch {
	case id > 0:
		games, err := client.GamesByIDs(ctx, []int64{id})
		if err != nil {
			return nil, err
		}
		if len(games) == 0 {
			return nil, fmt.Errorf("IGDB has no game with id %d", id)
		}
		game = &games[0]
	case slug != "":
		g, err := client.GameBySlug(ctx, slug)
		if err != nil {
			return nil, err
		}
		game = g
	case search != "":
		games, err := client.Search(ctx, search, 10)
		if err != nil {
			return nil, err
		}
		if len(games) == 0 {
			return nil, fmt.Errorf("IGDB found no game matching %q", search)
		}
		for i, g := range games {
			if i >= 10 {
				break
			}
			fmt.Printf("  %d  %s (%s)\n", g.IGDBID, g.Title, g.Slug)
		}
		game = &games[0]
		fmt.Printf("fetching best match: %s (%d)\n", game.Title, game.IGDBID)
	default:
		return nil, fmt.Errorf("one of --id, --slug or --search is required")
	}
	res, err := catalog.Save(ctx, st, game)
	if err != nil {
		return nil, err
	}
	action := "updated"
	if res.Created {
		action = "added"
	}
	fmt.Printf("%s %s (%s)\n", action, res.Game.Title, res.Game.Slug)
	return res.Game, nil
}
