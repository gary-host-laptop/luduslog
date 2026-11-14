package backloggd

import (
	"context"
	"fmt"
	"strings"

	"luduslog/internal/igdb"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

// ImportOptions controls how an export is applied. Fetch requires an IGDB
// client and asks the API for games that are not in the local catalog yet.
type ImportOptions struct {
	UserID   int64
	Fetch    bool
	Client   *igdb.Client
	Progress func(done, total int)
}

// ImportResult reports what happened. Missing lists entries that could not be
// resolved even after an IGDB lookup, usually because the game was removed
// from IGDB or the export refers to a non-game entry.
type ImportResult struct {
	Total      int
	Added      int
	Updated    int
	StatusOnly int
	Fetched    int
	Missing    []string
	Errors     []string
}

// Import resolves export entries against the catalog and writes statuses and
// logs for one user. It is idempotent: re-importing updates the existing log
// for a game instead of creating duplicates.
func Import(ctx context.Context, st *store.Store, entries []Entry, opt ImportOptions) (ImportResult, error) {
	res := ImportResult{Total: len(entries)}
	byIGDB := make(map[int64]*models.Game, len(entries))
	var missingIDs []int64

	for i := range entries {
		id := entries[i].IGDBID
		if id <= 0 {
			continue
		}
		if _, ok := byIGDB[id]; ok {
			continue
		}
		if g, err := st.GameByIGDB(ctx, id); err == nil {
			byIGDB[id] = g
			continue
		}
		missingIDs = append(missingIDs, id)
	}

	if opt.Fetch && opt.Client != nil && len(missingIDs) > 0 {
		fetched, err := opt.Client.GamesByIDs(ctx, missingIDs)
		if err != nil {
			res.Errors = append(res.Errors, "IGDB fetch: "+err.Error())
		}
		for i := range fetched {
			g := &fetched[i]
			if existing, err := st.GameByIGDB(ctx, g.IGDBID); err == nil {
				g.ID = existing.ID
				g.Slug = existing.Slug
			} else if existing, err := st.GameBySlug(ctx, g.Slug); err == nil && existing.IGDBID <= 0 {
				// Adopt a manually seeded row with the same slug.
				if _, err := st.DB.ExecContext(ctx, `UPDATE games SET igdb_id=? WHERE id=?`, g.IGDBID, existing.ID); err == nil {
					g.ID = existing.ID
				}
			}
			saved, err := st.SaveGame(ctx, g)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("IGDB %d %s: %v", g.IGDBID, g.Title, err))
				continue
			}
			byIGDB[g.IGDBID] = saved
			res.Fetched++
		}
	}

	for i := range entries {
		e := &entries[i]
		g := resolve(ctx, st, e, byIGDB)
		if g == nil {
			res.Missing = append(res.Missing, e.Title)
			continue
		}

		// A rating or review still needs a log row even if Backloggd did not
		// mark the game as played.
		hasLog := e.Played || e.Rating > 0 || e.Review != ""
		if hasLog {
			status := "played"
			if e.Played && (e.FinishedAt != "" || e.Mastered) {
				status = "completed"
			}
			in := store.LogInput{
				UserID:     opt.UserID,
				GameID:     g.ID,
				Rating:     e.Rating,
				Review:     e.Review,
				Spoiler:    e.Spoiler,
				Status:     status,
				Replay:     e.Replay,
				Mastered:   e.Mastered,
				StartedAt:  e.StartedAt,
				FinishedAt: e.FinishedAt,
				Playtime:   float64(e.Minutes) / 60,
			}
			if existing, err := st.LatestLogFor(ctx, opt.UserID, g.ID); err == nil {
				if _, err := st.UpdateLog(ctx, existing.ID, in); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", g.Title, err))
					continue
				}
				res.Updated++
			} else {
				if _, err := st.CreateLog(ctx, in); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", g.Title, err))
					continue
				}
				res.Added++
			}
		} else {
			res.StatusOnly++
		}

		if err := st.SetStatuses(ctx, opt.UserID, g.ID, e.Played, e.Playing, e.Backlog, e.Wishlist, e.Liked); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s status: %v", g.Title, err))
		}
		if opt.Progress != nil && (res.Added+res.Updated+res.StatusOnly+len(res.Missing))%100 == 0 {
			opt.Progress(res.Added+res.Updated+res.StatusOnly+len(res.Missing), res.Total)
		}
	}

	if opt.Progress != nil {
		opt.Progress(res.Total, res.Total)
	}
	return res, nil
}

func resolve(ctx context.Context, st *store.Store, e *Entry, byIGDB map[int64]*models.Game) *models.Game {
	if g := byIGDB[e.IGDBID]; g != nil {
		return g
	}
	if e.Slug != "" {
		if g, err := st.GameBySlug(ctx, e.Slug); err == nil {
			if e.IGDBID > 0 && g.IGDBID <= 0 {
				if _, err := st.DB.ExecContext(ctx, `UPDATE games SET igdb_id=? WHERE id=?`, e.IGDBID, g.ID); err == nil {
					g.IGDBID = e.IGDBID
				}
			}
			byIGDB[e.IGDBID] = g
			return g
		}
	}
	// Generic CSV files have no ids. Match only an exact normalized title so
	// a title search cannot silently attach a review to the wrong game.
	games, _, err := st.SearchGames(ctx, e.Title, 10, 0, true)
	if err != nil {
		return nil
	}
	want := normalizeTitle(e.Title)
	for i := range games {
		if normalizeTitle(games[i].Title) == want {
			g := &games[i]
			byIGDB[e.IGDBID] = g
			return g
		}
	}
	return nil
}

func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer("&", "and", ":", "", "'", "", "’", "", "-", " ", "_", " ")
	s = replacer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// Summary renders a short human-readable result, used by both CLI and web.
func (r ImportResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Imported %d games: %d logs added, %d logs updated, %d shelf entries", r.Total, r.Added, r.Updated, r.StatusOnly)
	if r.Fetched > 0 {
		fmt.Fprintf(&b, ", %d fetched from IGDB", r.Fetched)
	}
	if len(r.Missing) > 0 {
		fmt.Fprintf(&b, "; %d unmatched", len(r.Missing))
		if len(r.Missing) <= 5 {
			b.WriteString(" (")
			b.WriteString(strings.Join(r.Missing, ", "))
			b.WriteString(")")
		}
	}
	if len(r.Errors) > 0 {
		fmt.Fprintf(&b, "; %d errors", len(r.Errors))
	}
	return b.String()
}
