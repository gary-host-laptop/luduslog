package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"luduslog/internal/models"
)

func utcReleaseDate(days int) string {
	return time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02")
}

func TestComingSoonRailWindowAndAdultGate(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	main, err := st.SaveGame(ctx, &models.Game{
		Slug: "edition-parent", Title: "Edition Parent", ReleaseDate: utcReleaseDate(200),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []*models.Game{
		{Slug: "today", Title: "Today Game", ReleaseDate: utcReleaseDate(0)},
		{Slug: "tomorrow", Title: "Tomorrow Game", ReleaseDate: utcReleaseDate(1)},
		{Slug: "day60", Title: "Day 60 Game", ReleaseDate: utcReleaseDate(60)},
		{Slug: "day61", Title: "Day 61 Game", ReleaseDate: utcReleaseDate(61)},
		{Slug: "past", Title: "Past Game", ReleaseDate: utcReleaseDate(-1)},
		{Slug: "undated", Title: "Undated Game"},
		{Slug: "adult", Title: "Adult Upcoming", ReleaseDate: utcReleaseDate(2), Adult: true},
		{Slug: "edition", Title: "Edition Child", ReleaseDate: utcReleaseDate(1), ParentID: &main.ID},
	} {
		if _, err := st.SaveGame(ctx, g); err != nil {
			t.Fatalf("save %s: %v", g.Slug, err)
		}
	}

	got, err := st.ComingSoon(ctx, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Today Game", "Tomorrow Game", "Day 60 Game"}
	if len(got) != len(want) {
		t.Fatalf("got %d games: %+v", len(got), titles(got))
	}
	for i := range want {
		if got[i].Title != want[i] {
			t.Fatalf("order = %v, want %v", titles(got), want)
		}
	}

	all, err := st.ComingSoon(ctx, 60, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 || all[1].Title != "Tomorrow Game" || all[2].Title != "Adult Upcoming" {
		t.Fatalf("adult-enabled order = %v", titles(all))
	}
}

func titles(games []models.Game) []string {
	out := make([]string, len(games))
	for i, g := range games {
		out[i] = g.Title
	}
	return out
}

func TestRecentlyAnticipatedRecentRankingAndFallback(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	users := make([]models.User, 10)
	for i := range users {
		u, err := st.CreateUser(ctx, "wisher"+string(rune('a'+i)), "wisher"+string(rune('a'+i))+"@example.com", "password123", false)
		if err != nil {
			t.Fatal(err)
		}
		users[i] = *u
	}

	save := func(slug, title string, days int, adult bool) *models.Game {
		g, err := st.SaveGame(ctx, &models.Game{Slug: slug, Title: title, ReleaseDate: utcReleaseDate(days), Adult: adult})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	alpha := save("alpha", "Alpha", 10, false)
	buzz := save("buzz", "Buzz", 20, false)
	classic := save("classic", "Classic", 30, false)
	runner := save("runner", "Runner", 40, false)
	released := save("released", "Released", -1, false)
	adult := save("adult", "Adult", 15, true)
	parent := save("parent", "Parent", 50, false)
	edition, err := st.SaveGame(ctx, &models.Game{Slug: "edition", Title: "Edition", ReleaseDate: utcReleaseDate(20), ParentID: &parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	today := save("today", "Today", 0, false)

	wish := func(u models.User, g *models.Game) {
		if err := st.SetStatusFlag(ctx, u.ID, g.ID, "wishlist", true); err != nil {
			t.Fatal(err)
		}
	}
	wish(users[0], alpha)
	wish(users[0], buzz)
	wish(users[1], buzz)
	for i := range users {
		wish(users[i], classic)
	}
	wish(users[0], runner)
	wish(users[1], runner)
	wish(users[0], released)
	for i := 0; i < 3; i++ {
		wish(users[i], adult)
	}
	wish(users[0], edition)
	wish(users[0], today)

	old := time.Now().UTC().AddDate(0, 0, -90).Format(time.RFC3339)
	older := time.Now().UTC().AddDate(0, 0, -60).Format(time.RFC3339)
	if _, err := st.DB.ExecContext(ctx, `UPDATE statuses SET wishlist_at=? WHERE game_id=?`, old, classic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE statuses SET wishlist_at=? WHERE game_id=?`, older, runner.ID); err != nil {
		t.Fatal(err)
	}

	got, err := st.RecentlyAnticipated(ctx, 2, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Buzz", "Alpha"}; len(got) != 2 || got[0].Title != want[0] || got[1].Title != want[1] {
		t.Fatalf("recent ranking = %v, want %v", titles(got), want)
	}

	filled, err := st.RecentlyAnticipated(ctx, 4, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Buzz", "Alpha", "Classic", "Runner"}; len(filled) != 4 ||
		filled[0].Title != want[0] || filled[1].Title != want[1] || filled[2].Title != want[2] || filled[3].Title != want[3] {
		t.Fatalf("fallback = %v, want %v", titles(filled), want)
	}

	withAdult, err := st.RecentlyAnticipated(ctx, 10, 30, true)
	if err != nil {
		t.Fatal(err)
	}
	if withAdult[0].Title != "Adult" {
		t.Fatalf("adult recent should lead when enabled: %v", titles(withAdult))
	}
	for _, g := range withAdult {
		switch g.Title {
		case "Released", "Edition", "Today":
			t.Fatalf("ineligible game %q returned: %v", g.Title, titles(withAdult))
		}
	}
}

func TestWishlistTimestampSetPreserveClear(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "timekeeper", "timekeeper@example.com", "password123", false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := st.SaveGame(ctx, &models.Game{Slug: "timed", Title: "Timed"})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SetStatusFlag(ctx, u.ID, g.ID, "wishlist", true); err != nil {
		t.Fatal(err)
	}
	status, err := st.StatusFor(ctx, u.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.WishlistAt.IsZero() {
		t.Fatal("wishlist_at not set")
	}

	old := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := st.DB.ExecContext(ctx, `UPDATE statuses SET wishlist_at=? WHERE user_id=? AND game_id=?`, old.Format(time.RFC3339), u.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatusFlag(ctx, u.ID, g.ID, "backlog", true); err != nil {
		t.Fatal(err)
	}
	status, err = st.StatusFor(ctx, u.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.WishlistAt.Equal(old) {
		t.Fatalf("wishlist_at = %v, want preserved %v", status.WishlistAt, old)
	}

	if err := st.SetStatusFlag(ctx, u.ID, g.ID, "wishlist", false); err != nil {
		t.Fatal(err)
	}
	status, err = st.StatusFor(ctx, u.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.WishlistAt.IsZero() {
		t.Fatalf("wishlist_at not cleared: %v", status.WishlistAt)
	}

	if err := st.SetStatuses(ctx, u.ID, g.ID, false, false, true, true, false); err != nil {
		t.Fatal(err)
	}
	status, err = st.StatusFor(ctx, u.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.WishlistAt.IsZero() {
		t.Fatal("SetStatuses did not set wishlist_at")
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE statuses SET wishlist_at=? WHERE user_id=? AND game_id=?`, old.Format(time.RFC3339), u.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatuses(ctx, u.ID, g.ID, false, false, true, true, false); err != nil {
		t.Fatal(err)
	}
	status, err = st.StatusFor(ctx, u.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.WishlistAt.Equal(old) {
		t.Fatalf("SetStatuses did not preserve wishlist_at: %v", status.WishlistAt)
	}
	if err := st.SetStatuses(ctx, u.ID, g.ID, false, false, true, false, false); err != nil {
		t.Fatal(err)
	}
	status, err = st.StatusFor(ctx, u.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.WishlistAt.IsZero() {
		t.Fatalf("SetStatuses did not clear wishlist_at: %v", status.WishlistAt)
	}
}

func TestWishlistMigrationBackfillsFromUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "test.DB")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	initSQL, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(initSQL)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(username,username_lower,email,email_lower,password_hash,created_at) VALUES('u','u','u@example.com','u@example.com','x','2024-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for i, slug := range []string{"one", "two"} {
		if _, err := db.Exec(`INSERT INTO games(id,igdb_id,slug,title,sort_title,created_at,updated_at) VALUES(?,?,?,?,?,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`, i+1, i+1, slug, slug, slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO statuses(user_id,game_id,wishlist,updated_at) VALUES(1,1,1,'2024-05-01T12:00:00Z'),(1,2,0,'2024-06-01T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("migrations/018_wishlist_event_time.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var wishlistAt sql.NullString
	if err := db.QueryRow(`SELECT wishlist_at FROM statuses WHERE game_id=1`).Scan(&wishlistAt); err != nil {
		t.Fatal(err)
	}
	if !wishlistAt.Valid || wishlistAt.String != "2024-05-01T12:00:00Z" {
		t.Fatalf("backfilled wishlist_at = %+v", wishlistAt)
	}
	if err := db.QueryRow(`SELECT wishlist_at FROM statuses WHERE game_id=2`).Scan(&wishlistAt); err != nil {
		t.Fatal(err)
	}
	if wishlistAt.Valid {
		t.Fatalf("non-wishlist row backfilled: %+v", wishlistAt)
	}
}
