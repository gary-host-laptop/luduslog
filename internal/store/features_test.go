package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"luduslog/internal/models"
)

func TestCanceledQueryDoesNotPoisonPool(t *testing.T) {
	st := testStore(t)
	st.DB.SetMaxOpenConns(1)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	rows, err := st.DB.QueryContext(ctx, `WITH RECURSIVE cnt(x) AS (
		SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x<100000000
	) SELECT sum(x) FROM cnt`)
	if err == nil {
		for rows.Next() {
		}
		rows.Close()
	}
	cancel()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	var n int
	if err := st.DB.QueryRowContext(ctx2, `SELECT 1`).Scan(&n); err != nil {
		t.Fatalf("query after cancellation failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("query after cancellation = %d, want 1", n)
	}
}

func TestStoreHydrationWithOneConnection(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	st.DB.SetMaxOpenConns(1)
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -301, Slug: "pool-test", Title: "Pool Test", ReleaseStatus: "released"})
	log, err := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: game.ID, Rating: 8, Review: "Solid", Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateComment(ctx, &bob.ID, "log", log.ID, nil, "Nice", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Notify(ctx, alice.ID, "reply", &bob.ID, "log", log.ID, "Bob replied", "/logs/"+itoa(int(log.ID))); err != nil {
		t.Fatal(err)
	}
	if err := st.AddActivity(ctx, alice.ID, "log", &game.ID, &log.ID, nil, game.Title, "public"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateReport(ctx, &bob.ID, "log", log.ID, "/logs/"+itoa(int(log.ID)), "spam", "test"); err != nil {
		t.Fatal(err)
	}
	list, err := st.CreateList(ctx, ListInput{UserID: alice.ID, Title: "Pool List", Privacy: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddListEntry(ctx, list.ID, game.ID, ""); err != nil {
		t.Fatal(err)
	}
	folder, err := st.CreateFolder(ctx, alice.ID, "Pool Folder", "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddListToFolder(ctx, folder, list.ID); err != nil {
		t.Fatal(err)
	}

	checks := map[string]func(context.Context) error{
		"CommentsFor":   func(c context.Context) error { _, err := st.CommentsFor(c, "log", log.ID); return err },
		"Notifications": func(c context.Context) error { _, _ = st.Notifications(c, alice.ID, 10, 0); return nil },
		"ActivityFeed": func(c context.Context) error {
			_, err := st.ActivityFeed(c, alice.ID, alice.ID, 10, 0, false)
			return err
		},
		"Reports": func(c context.Context) error { _, err := st.Reports(c, ""); return err },
		"YearRecap": func(c context.Context) error {
			_, _, err := st.YearRecap(c, alice.ID, time.Now().UTC().Year())
			return err
		},
		"ListsForUser": func(c context.Context) error { _, _ = st.ListsForUser(c, alice.ID, true, 10, 0); return nil },
		"PopularLists": func(c context.Context) error { _, err := st.PopularLists(c, 10); return err },
		"ListsContainingGame": func(c context.Context) error {
			_, err := st.ListsContainingGame(c, game.ID, 10)
			return err
		},
		"FolderLists": func(c context.Context) error { _, err := st.FolderLists(c, folder); return err },
	}
	for name, fn := range checks {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		started := time.Now()
		err := fn(callCtx)
		elapsed := time.Since(started)
		cancel()
		if err != nil {
			t.Fatalf("%s with one connection: %v", name, err)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("%s with one connection took %v", name, elapsed)
		}
	}
}

func TestFavoriteGamesLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, err := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	var games []*models.Game
	for i := 1; i <= 6; i++ {
		g, err := st.SaveGame(ctx, &models.Game{IGDBID: int64(-100 - i), Slug: "fav-" + itoa(i), Title: "Fav " + itoa(i), ReleaseStatus: "released"})
		if err != nil {
			t.Fatal(err)
		}
		games = append(games, g)
	}

	if err := st.AddFavoriteGame(ctx, alice.ID, games[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddFavoriteGame(ctx, alice.ID, games[0].ID); err != nil {
		t.Fatalf("duplicate add should be a no-op: %v", err)
	}
	for i := 1; i < 5; i++ {
		if err := st.AddFavoriteGame(ctx, alice.ID, games[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.FavoriteGames(ctx, alice.ID, 10); len(got) != 5 {
		t.Fatalf("favourites = %d, want 5", len(got))
	}
	if err := st.AddFavoriteGame(ctx, alice.ID, games[5].ID); !errors.Is(err, ErrFavoriteLimit) {
		t.Fatalf("sixth favourite = %v, want ErrFavoriteLimit", err)
	}

	if err := st.RemoveFavoriteGame(ctx, alice.ID, games[1].ID); err != nil {
		t.Fatal(err)
	}
	got := st.FavoriteGames(ctx, alice.ID, 5)
	if len(got) != 4 || got[1].ID != games[2].ID {
		t.Fatalf("positions did not compact: %+v", got)
	}
	var positions []int
	rows, err := st.DB.QueryContext(ctx, `SELECT position FROM favorite_games WHERE user_id=? ORDER BY position`, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p int
		rows.Scan(&p)
		positions = append(positions, p)
	}
	rows.Close()
	for i, p := range positions {
		if p != i+1 {
			t.Fatalf("positions = %v, want 1..n", positions)
		}
	}

	if err := st.ReorderFavoriteGames(ctx, alice.ID, []int64{games[0].ID}); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("short reorder = %v, want ErrInvalidOrder", err)
	}
	if err := st.ReorderFavoriteGames(ctx, alice.ID, []int64{games[3].ID, games[2].ID, games[0].ID, games[4].ID}); err != nil {
		t.Fatal(err)
	}
	got = st.FavoriteGames(ctx, alice.ID, 5)
	want := []int64{games[3].ID, games[2].ID, games[0].ID, games[4].ID}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("reordered favourites = %+v, want %v", got, want)
		}
	}
}

func TestRateGameCreatesAndPreservesLogs(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -201, Slug: "rated", Title: "Rated", ReleaseStatus: "released"})

	if err := st.RateGame(ctx, alice.ID, game.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LatestLogFor(ctx, alice.ID, game.ID); err == nil {
		t.Fatal("clearing a missing rating should not create a log")
	}

	if err := st.RateGame(ctx, alice.ID, game.ID, 7); err != nil {
		t.Fatal(err)
	}
	log, err := st.LatestLogFor(ctx, alice.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if log.Rating != 7 || log.Status != "played" {
		t.Fatalf("quick rating created %+v, want rating 7 and played", log)
	}
	stats, _ := st.GameByID(ctx, game.ID)
	if stats.RatingCount != 1 {
		t.Fatalf("rating count = %d, want 1", stats.RatingCount)
	}

	if _, err := st.UpdateLog(ctx, log.ID, LogInput{
		UserID: alice.ID, GameID: game.ID, Rating: 7, Review: "A precise climb", Status: "backlog", Replay: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RateGame(ctx, alice.ID, game.ID, 4); err != nil {
		t.Fatal(err)
	}
	log, _ = st.LatestLogFor(ctx, alice.ID, game.ID)
	if log.Rating != 4 || log.Review != "A precise climb" || log.Status != "backlog" || !log.Replay {
		t.Fatalf("rating update overwrote the log: %+v", log)
	}

	if err := st.RateGame(ctx, alice.ID, game.ID, 0); err != nil {
		t.Fatal(err)
	}
	log, _ = st.LatestLogFor(ctx, alice.ID, game.ID)
	if log.Rating != 0 || log.Review != "A precise climb" {
		t.Fatalf("clearing rating damaged the log: %+v", log)
	}
	stats, _ = st.GameByID(ctx, game.ID)
	if stats.RatingCount != 0 {
		t.Fatalf("rating count after clear = %d", stats.RatingCount)
	}
}

func TestRatingDistributionSplitsHalfStars(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -202, Slug: "half-stars", Title: "Half Stars", ReleaseStatus: "released"})
	for i, name := range []string{"alice", "bob", "carol"} {
		u, _ := st.CreateUser(ctx, name, name+"@example.test", "hash", false)
		rating := float64([]int{8, 9, 8}[i])
		if err := st.RateGame(ctx, u.ID, game.ID, rating); err != nil {
			t.Fatal(err)
		}
	}
	dist := st.RatingDistribution(ctx, game.ID)
	if dist[8] != 2 || dist[9] != 1 || len(dist) != 2 {
		t.Fatalf("rating distribution = %v, want two 4-star and one 4.5-star rating", dist)
	}
}

func TestDeleteUserRemovesOwnedContentAndGuardsLastAdmin(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	admin, _ := st.CreateUser(ctx, "admin", "admin@example.test", "hash", true)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -301, Slug: "deleted-owner", Title: "Deleted Owner", ReleaseStatus: "released"})

	log, err := st.CreateLog(ctx, LogInput{UserID: bob.ID, GameID: game.ID, Rating: 8, Review: "Goodbye", Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := st.CreateList(ctx, ListInput{UserID: bob.ID, Title: "Bob's list", Privacy: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatusFlag(ctx, bob.ID, game.ID, "wishlist", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ToggleGameLike(ctx, bob.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Follow(ctx, bob.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	profileComment, _ := st.CreateComment(ctx, &admin.ID, "profile", bob.ID, nil, "on the profile", "", "", "")
	logComment, _ := st.CreateComment(ctx, &admin.ID, "log", log.ID, nil, "on the review", "", "", "")
	listComment, _ := st.CreateComment(ctx, &admin.ID, "list", list.ID, nil, "on the list", "", "", "")
	if _, err := st.CreateComment(ctx, &bob.ID, "log", log.ID, nil, "my own reply", "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		typ string
		id  int64
	}{{"log", log.ID}, {"list", list.ID}, {"comment", profileComment}, {"comment", logComment}, {"comment", listComment}} {
		if _, err := st.DB.ExecContext(ctx, `INSERT INTO notifications(user_id,type,actor_id,object_type,object_id,created_at)
			VALUES(?,?,?,?,?,?)`, admin.ID, "reply", bob.ID, c.typ, c.id, Now()); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.DeleteUser(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserByUsername(ctx, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted user still resolves: %v", err)
	}
	unameTaken, emailTaken := st.UserExists(ctx, "bob", "bob@example.test")
	if unameTaken || emailTaken {
		t.Fatal("deletion did not free the username and email")
	}
	var comments, notifications int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE subject_type='profile' AND subject_id=?`, bob.ID).Scan(&comments)
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE object_type IN ('log','list','comment')`).Scan(&notifications)
	if comments != 0 || notifications != 0 {
		t.Fatalf("orphaned comments=%d notifications=%d", comments, notifications)
	}
	updated, _ := st.GameByID(ctx, game.ID)
	if updated.Plays != 0 || updated.Wishlists != 0 || updated.Likes != 0 || updated.RatingCount != 0 || updated.Reviews != 0 {
		t.Fatalf("game stats not refreshed: %+v", updated)
	}

	if err := st.DeleteUser(ctx, admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("deleting last admin = %v, want ErrLastAdmin", err)
	}
	second, _ := st.CreateUser(ctx, "secondadmin", "second@example.test", "hash", true)
	if err := st.DeleteUser(ctx, second.ID); err != nil {
		t.Fatalf("deleting an extra admin = %v", err)
	}
}
