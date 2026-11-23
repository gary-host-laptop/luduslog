package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"luduslog/internal/models"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	return st
}

func TestWeightedRating(t *testing.T) {
	if got := WeightedRating(9, 1000); math.Abs(got-8.868) > 0.01 {
		t.Fatalf("weighted rating with many votes = %v", got)
	}
	low := WeightedRating(10, 2)
	high := WeightedRating(10, 500)
	if !(low < high) {
		t.Fatalf("confidence should raise the weighted score: %v >= %v", low, high)
	}
	if WeightedRating(0, 0) != 3.6 {
		t.Fatal("prior should apply with no ratings")
	}
}

func TestPlatformWithoutIGDBIDRendersName(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	g, err := st.SaveGame(ctx, &models.Game{
		IGDBID: -10, Slug: "celeste-platform-test", Title: "Celeste Platform Test", ReleaseStatus: "released",
		Platforms: []models.Platform{{Name: "PC"}, {Name: "Switch"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := st.GameDetail(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Platforms) != 2 || detail.Platforms[0].Name == "" || detail.Platforms[1].Name == "" {
		t.Fatalf("platform names were not loaded: %+v", detail.Platforms)
	}
	facets := st.GameFacets(ctx)
	if len(facets.Platforms) != 2 || facets.Platforms[0].Name == "" {
		t.Fatalf("facet platform names were not loaded: %+v", facets.Platforms)
	}
}

func TestPlatformNamesAreUniqueAndDeduped(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	g, err := st.SaveGame(ctx, &models.Game{IGDBID: -11, Slug: "platform-once", Title: "Platform Once", ReleaseStatus: "released",
		Platforms: []models.Platform{{Name: "PC"}, {Name: "pc"}}})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM platforms WHERE name=? COLLATE NOCASE`, "PC").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("case-insensitive duplicate platform rows = %d", n)
	}
	if _, err := st.SaveGame(ctx, &models.Game{IGDBID: -12, Slug: "platform-twice", Title: "Platform Twice", ReleaseStatus: "released",
		Platforms: []models.Platform{{Name: "PC"}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM platforms`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("saving PC twice created %d platform rows", n)
	}
	_ = g
}

func TestPlatformDedupeMigration(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "dedupe.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	init, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(init)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO games(id,igdb_id,slug,title,sort_title,created_at,updated_at) VALUES(1,1,'g','G','G','now','now')`); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		name string
		igdb any
	}{{"PC", nil}, {"PC", nil}, {"pc", 6}, {"Switch", nil}} {
		if _, err := db.Exec(`INSERT INTO platforms(igdb_id,name) VALUES(?,?)`, p.igdb, p.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO game_platforms(game_id,platform_id) VALUES(1,1),(1,2),(1,3),(1,4)`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("migrations/002_platform_dedupe.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var platforms, links, igdb int64
	db.QueryRow(`SELECT COUNT(*) FROM platforms`).Scan(&platforms)
	db.QueryRow(`SELECT COUNT(*) FROM game_platforms`).Scan(&links)
	db.QueryRow(`SELECT COALESCE(MAX(igdb_id),0) FROM platforms WHERE name='PC' COLLATE NOCASE`).Scan(&igdb)
	if platforms != 2 || links != 2 {
		t.Fatalf("after dedupe: %d platforms, %d links", platforms, links)
	}
	if igdb != 6 {
		t.Fatalf("dedupe should prefer the IGDB-backed platform, got igdb_id=%d", igdb)
	}
	if _, err := db.Exec(`INSERT INTO platforms(name) VALUES('PC')`); err == nil {
		t.Fatal("unique platform name index was not created")
	}
}

func TestCover2xMigration(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "covers.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	init, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(init)); err != nil {
		t.Fatal(err)
	}
	for i, cover := range []string{
		"https://images.igdb.com/igdb/image/upload/t_cover_big/one.jpg",
		"https://images.igdb.com/igdb/image/upload/t_cover_big_2x/two.jpg",
		"https://example.test/cover.jpg",
		"",
	} {
		if _, err := db.Exec(`INSERT INTO games(id,igdb_id,slug,title,sort_title,cover_url,created_at,updated_at) VALUES(?,?,?,?,?,?,'now','now')`,
			i+1, i+1, "cover-"+strconv.Itoa(i), "Cover", "Cover", cover); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := migrationFS.ReadFile("migrations/008_cover_2x.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://images.igdb.com/igdb/image/upload/t_cover_big_2x/one.jpg",
		"https://images.igdb.com/igdb/image/upload/t_cover_big_2x/two.jpg",
		"https://example.test/cover.jpg",
		"",
	}
	for i, expected := range want {
		var got string
		if err := db.QueryRow(`SELECT cover_url FROM games WHERE id=?`, i+1).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != expected {
			t.Errorf("cover %d = %q, want %q", i, got, expected)
		}
	}
}

func TestRolesAndModerationLog(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	admin, err := st.CreateUser(ctx, "root", "root@example.test", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	if !admin.IsAdmin() || !admin.CanModerate() || admin.Role != "admin" {
		t.Fatalf("admin role not set: %+v", admin)
	}
	user, _ := st.CreateUser(ctx, "member", "member@example.test", "hash", false)
	if user.IsAdmin() || user.CanModerate() {
		t.Fatalf("ordinary user has elevated role: %+v", user)
	}
	if err := st.SetRole(ctx, user.ID, "mod"); err != nil {
		t.Fatal(err)
	}
	user, _ = st.UserByID(ctx, user.ID)
	if !user.CanModerate() || user.IsAdmin() {
		t.Fatalf("moderator role not applied: %+v", user)
	}
	if got := st.AdminCount(ctx); got != 1 {
		t.Fatalf("admin count = %d", got)
	}
	if err := st.SetRole(ctx, user.ID, "owner"); err == nil {
		t.Fatal("invalid role was accepted")
	}
	if err := st.LogModAction(ctx, user.ID, "suspend", &admin.ID, "test reason", nil); err != nil {
		t.Fatalf("LogModAction: %v", err)
	}
	actions, err := st.ModActions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Moderator != "member" || actions[0].TargetName != "root" || actions[0].Action != "suspend" {
		t.Fatalf("unexpected moderation log: %+v", actions)
	}
}

func TestRoleMigrationBackfillsAdmins(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "roles.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	init, _ := migrationFS.ReadFile("migrations/001_init.sql")
	if _, err := db.Exec(string(init)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(username,username_lower,email,email_lower,password_hash,is_admin,created_at) VALUES('oldadmin','oldadmin','a@example.test','a@example.test','hash',1,'now')`); err != nil {
		t.Fatal(err)
	}
	migration, _ := migrationFS.ReadFile("migrations/003_roles.sql")
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE username='oldadmin'`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "admin" {
		t.Fatalf("old admin was backfilled as %q", role)
	}
	var cols int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name IN ('is_admin','is_supporter')`).Scan(&cols)
	if cols != 0 {
		t.Fatalf("legacy role columns remain: %d", cols)
	}
}

func TestSearchGamesUsesDirectFTSJoin(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, err := st.SaveGame(ctx, &models.Game{IGDBID: -1, Slug: "celeste", Title: "Celeste", ReleaseStatus: "released"}); err != nil {
		t.Fatal(err)
	}
	games, total, err := st.SearchGames(ctx, "celeste", 30, 0, false)
	if err != nil {
		t.Fatalf("SearchGames: %v", err)
	}
	if total != 1 || len(games) != 1 || games[0].Slug != "celeste" {
		t.Fatalf("SearchGames = %d/%d, want one Celeste", total, len(games))
	}
}

func TestFTSQueryAllowsOnlyLettersAndNumbers(t *testing.T) {
	if got := ftsQuery(`# @ ~ | [ ] \\ " ' -`); got != "" {
		t.Fatalf("punctuation query = %q, want empty", got)
	}
	if got := ftsQuery("Pokémon #1"); got != "Pokémon* 1*" {
		t.Fatalf("unicode query = %q", got)
	}
	long := strings.Repeat("a", 100)
	got := ftsQuery(long)
	if got != strings.Repeat("a", 64)+"*" {
		t.Fatalf("long token = %q", got)
	}
	many := strings.TrimSpace(strings.Repeat("word ", 20))
	if parts := strings.Fields(ftsQuery(many)); len(parts) != 12 {
		t.Fatalf("token count = %d, want 12", len(parts))
	}
}

func TestSearchPaginationAndReviewFilters(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "alicia", "alicia@example.test", "hash", false)
	private, _ := st.CreateUser(ctx, "private", "private@example.test", "hash", false)
	st.UpdateProfile(ctx, private.ID, ProfileUpdate{PrivateProfile: true})
	deactivated, _ := st.CreateUser(ctx, "gone", "gone@example.test", "hash", false)
	st.SetDeactivated(ctx, deactivated.ID, ptr("2026-01-01T00:00:00Z"))

	celeste, _ := st.SaveGame(ctx, &models.Game{IGDBID: -1, Slug: "celeste", Title: "Celeste", ReleaseStatus: "released"})
	adult, _ := st.SaveGame(ctx, &models.Game{IGDBID: -2, Slug: "adult-game", Title: "Adult Game", Adult: true, ReleaseStatus: "released"})
	review, err := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: celeste.ID, Name: "Summit", Review: "Celeste is a precise platformer", Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	st.CreateLog(ctx, LogInput{UserID: bob.ID, GameID: celeste.ID, Name: "Draft", Review: "Celeste precise draft", Draft: true})
	privateReview, _ := st.CreateLog(ctx, LogInput{UserID: private.ID, GameID: celeste.ID, Name: "Private", Review: "Celeste precise private"})
	deletedReview, _ := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: celeste.ID, Name: "Deleted", Review: "Celeste precise deleted"})
	st.DeleteLog(ctx, deletedReview.ID, alice.ID)
	adultReview, _ := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: adult.ID, Name: "Adult", Review: "Celeste precise adult"})
	st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: celeste.ID, Review: "", Status: "played"})

	reviews, total, err := st.SearchReviews(ctx, "precise", 30, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(reviews) != 1 || reviews[0].ID != review.ID {
		t.Fatalf("SearchReviews precise = %d/%d: %+v", total, len(reviews), reviews)
	}
	for _, hidden := range []int64{privateReview.ID, deletedReview.ID, adultReview.ID} {
		for _, r := range reviews {
			if r.ID == hidden {
				t.Fatalf("hidden review %d leaked", hidden)
			}
		}
	}
	if got, _, _ := st.SearchReviews(ctx, "Celeste", 30, 0, true); got == nil {
		t.Fatal("game-title review search returned nothing")
	}
	if got, _, _ := st.SearchReviews(ctx, "Summit", 30, 0, false); len(got) != 1 {
		t.Fatalf("review-name search = %d", len(got))
	}

	users, total, err := st.SearchUsers(ctx, "ali", 1, 0)
	if err != nil || total != 2 || len(users) != 1 {
		t.Fatalf("SearchUsers first page = %d/%d err=%v", total, len(users), err)
	}
	users2, _, _ := st.SearchUsers(ctx, "ali", 1, 1)
	if len(users2) != 1 || users2[0].ID == users[0].ID {
		t.Fatalf("SearchUsers offset did not advance: %+v %+v", users, users2)
	}

	for i := 0; i < 3; i++ {
		st.CreateList(ctx, ListInput{UserID: alice.ID, Title: "Best Platformers " + itoa(i), Description: "Celeste"})
	}
	st.CreateList(ctx, ListInput{UserID: alice.ID, Title: "Secret Platformers", Privacy: "private"})
	lists, total, err := st.SearchLists(ctx, "platformers", 2, 0)
	if err != nil || total != 3 || len(lists) != 2 {
		t.Fatalf("SearchLists first page = %d/%d err=%v", total, len(lists), err)
	}
	more, _, _ := st.SearchLists(ctx, "platformers", 2, 2)
	if len(more) != 1 || more[0].ID == lists[0].ID {
		t.Fatalf("SearchLists offset = %+v", more)
	}
}

func ptr[T any](v T) *T { return &v }

func TestGameReviewSorting(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	g, _ := st.SaveGame(ctx, &models.Game{IGDBID: -1, Slug: "sort-game", Title: "Sort Game", ReleaseStatus: "released"})
	var logs []*models.Log
	for i, rating := range []float64{2, 5, 3} {
		u, _ := st.CreateUser(ctx, "sortuser"+itoa(i), "sort"+itoa(i)+"@example.test", "hash", false)
		l, err := st.CreateLog(ctx, LogInput{UserID: u.ID, GameID: g.ID, Review: "review " + itoa(i), Rating: rating, Status: "played"})
		if err != nil {
			t.Fatal(err)
		}
		logs = append(logs, l)
	}
	liker, _ := st.CreateUser(ctx, "liker", "liker@example.test", "hash", false)
	st.LikeLog(ctx, logs[0].ID, liker.ID, true)
	st.LikeLog(ctx, logs[0].ID, logs[1].UserID, true)

	rated, _, err := st.ReviewsForGame(ctx, g.ID, "rating", 10, 0)
	if err != nil || len(rated) != 3 || rated[0].Rating != 5 || rated[1].Rating != 3 || rated[2].Rating != 2 {
		t.Fatalf("rating sort = %+v err=%v", rated, err)
	}
	popular, _, err := st.ReviewsForGame(ctx, g.ID, "likes", 10, 0)
	if err != nil || len(popular) != 3 || popular[0].ID != logs[0].ID {
		t.Fatalf("popular sort = %+v err=%v", popular, err)
	}
	newest, _, err := st.ReviewsForGame(ctx, g.ID, "new", 10, 0)
	if err != nil || newest[0].ID != logs[2].ID {
		t.Fatalf("new sort = %+v err=%v", newest, err)
	}
}

func TestN00bBadgeAndFeaturedTrophy(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	fresh, _ := st.CreateUser(ctx, "freshuser", "fresh@example.test", "hash", false)
	st.CheckBadges(ctx, fresh.ID)
	if badges, err := st.BadgesFor(ctx, fresh.ID); err != nil || len(badges) != 0 {
		t.Fatalf("fresh user badges = %+v err=%v", badges, err)
	}

	u, _ := st.CreateUser(ctx, "dayold", "dayold@example.test", "hash", false)
	old := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	if _, err := st.DB.ExecContext(ctx, `UPDATE users SET created_at=? WHERE id=?`, old, u.ID); err != nil {
		t.Fatal(err)
	}
	st.CheckBadges(ctx, u.ID)
	badges, err := st.BadgesFor(ctx, u.ID)
	if err != nil || len(badges) != 1 || badges[0].Key != "n00b" || badges[0].EarnedAt == nil {
		t.Fatalf("day-old badges = %+v err=%v", badges, err)
	}
	all, err := st.BadgeCatalogue(ctx, u.ID)
	if err != nil || len(all) != len(badgeDefs) {
		t.Fatalf("catalogue = %+v err=%v", all, err)
	}
	if all[0].Key != "n00b" || all[0].EarnedAt == nil {
		t.Fatalf("n00b not in catalogue: %+v", all[0])
	}
	if err := st.SetFeaturedBadge(ctx, u.ID, "n00b"); err != nil {
		t.Fatal(err)
	}
	u, err = st.UserByID(ctx, u.ID)
	if err != nil || u.FeaturedBadge != "n00b" {
		t.Fatalf("featured badge = %q err=%v", u.FeaturedBadge, err)
	}
	if err := st.SetFeaturedBadge(ctx, u.ID, ""); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByID(ctx, u.ID)
	if u.FeaturedBadge != "" {
		t.Fatalf("featured badge was not cleared: %q", u.FeaturedBadge)
	}
}

func TestReviewsFeedAudienceAndSort(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "feedalice", "feedalice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "feedbob", "feedbob@example.test", "hash", false)
	carol, _ := st.CreateUser(ctx, "feedcarol", "feedcarol@example.test", "hash", false)
	dave, _ := st.CreateUser(ctx, "feeddave", "feeddave@example.test", "hash", false)
	if err := st.Follow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Follow(ctx, bob.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Follow(ctx, alice.ID, carol.ID); err != nil {
		t.Fatal(err)
	}
	g1, _ := st.SaveGame(ctx, &models.Game{IGDBID: -501, Slug: "feed-one", Title: "Feed One", ReleaseStatus: "released"})
	g2, _ := st.SaveGame(ctx, &models.Game{IGDBID: -502, Slug: "feed-two", Title: "Feed Two", ReleaseStatus: "released"})
	bobReview, _ := st.CreateLog(ctx, LogInput{UserID: bob.ID, GameID: g1.ID, Review: "bob review", Rating: 3, Status: "played"})
	carolReview, _ := st.CreateLog(ctx, LogInput{UserID: carol.ID, GameID: g2.ID, Review: "carol review", Rating: 5, Status: "played"})
	daveReview, _ := st.CreateLog(ctx, LogInput{UserID: dave.ID, GameID: g1.ID, Review: "dave review", Rating: 8, Status: "played"})
	if err := st.LikeLog(ctx, daveReview.ID, alice.ID, true); err != nil {
		t.Fatal(err)
	}

	ids := func(logs []*models.Log) []int64 {
		out := make([]int64, len(logs))
		for i, l := range logs {
			out[i] = l.ID
		}
		return out
	}
	friends, total, err := st.ReviewsFeed(ctx, alice.ID, "friends", 0, "new", 20, 0)
	if err != nil || total != 1 || len(friends) != 1 || friends[0].ID != bobReview.ID {
		t.Fatalf("friends feed = %v/%d err=%v", ids(friends), total, err)
	}
	following, total, err := st.ReviewsFeed(ctx, alice.ID, "following", 0, "new", 20, 0)
	if err != nil || total != 2 || len(following) != 2 || following[0].ID != carolReview.ID || following[1].ID != bobReview.ID {
		t.Fatalf("following feed = %v/%d err=%v", ids(following), total, err)
	}
	if got, total, err := st.ReviewsFeed(ctx, 0, "friends", 0, "new", 20, 0); err != nil || total != 0 || len(got) != 0 {
		t.Fatalf("signed-out friends feed = %v/%d err=%v", ids(got), total, err)
	}
	popular, _, err := st.ReviewsFeed(ctx, alice.ID, "all", 0, "likes", 20, 0)
	if err != nil || len(popular) != 3 || popular[0].ID != daveReview.ID {
		t.Fatalf("popular feed = %v err=%v", ids(popular), err)
	}
	rated, _, err := st.ReviewsFeed(ctx, alice.ID, "all", 0, "rating", 20, 0)
	if err != nil || len(rated) != 3 || rated[0].ID != daveReview.ID || rated[1].ID != carolReview.ID || rated[2].ID != bobReview.ID {
		t.Fatalf("rating feed = %v err=%v", ids(rated), err)
	}
	oldest, _, err := st.ReviewsFeed(ctx, alice.ID, "all", g1.ID, "oldest", 20, 0)
	if err != nil || len(oldest) != 2 || oldest[0].ID != bobReview.ID || oldest[1].ID != daveReview.ID {
		t.Fatalf("oldest game feed = %v err=%v", ids(oldest), err)
	}
}

func TestPlayedCountAndLikeToggle(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -2, Slug: "counter-game", Title: "Counter Game", ReleaseStatus: "released"})

	if err := st.SetPlayedCount(ctx, alice.ID, game.ID, -1); err != nil {
		t.Fatal(err)
	}
	stStatus, _ := st.StatusFor(ctx, alice.ID, game.ID)
	if stStatus.Played != 0 {
		t.Fatalf("played went below zero: %d", stStatus.Played)
	}
	var rows int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM statuses WHERE user_id=? AND game_id=?`, alice.ID, game.ID).Scan(&rows)
	if rows != 0 {
		t.Fatal("empty status row was not deleted")
	}

	if err := st.SetPlayedCount(ctx, alice.ID, game.ID, 2); err != nil {
		t.Fatal(err)
	}
	stStatus, _ = st.StatusFor(ctx, alice.ID, game.ID)
	if stStatus.Played != 2 {
		t.Fatalf("played count = %d, want 2", stStatus.Played)
	}
	if err := st.SetPlayedCount(ctx, bob.ID, game.ID, 4); err != nil {
		t.Fatal(err)
	}
	updated, _ := st.GameByID(ctx, game.ID)
	if updated.Plays != 2 {
		t.Fatalf("public played count = %d, want 2 distinct players", updated.Plays)
	}
	if counts := st.GameStatusCounts(ctx, game.ID); counts["played"] != 2 {
		t.Fatalf("status counts played = %d, want 2", counts["played"])
	}
	stats, _ := st.StatsFor(ctx, alice.ID)
	if stats.GamesPlayed != 1 {
		t.Fatalf("alice games played = %d, want 1", stats.GamesPlayed)
	}
	if err := st.SetStatuses(ctx, alice.ID, game.ID, true, false, false, true, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatuses(ctx, alice.ID, game.ID, false, false, false, true, false); err != nil {
		t.Fatal(err)
	}
	stStatus, _ = st.StatusFor(ctx, alice.ID, game.ID)
	if stStatus.Played != 2 || !stStatus.Wishlist {
		t.Fatalf("import did not retain count and wishlist: %+v", stStatus)
	}
	if err := st.SetPlayedCount(ctx, alice.ID, game.ID, 0); err != nil {
		t.Fatal(err)
	}
	stStatus, _ = st.StatusFor(ctx, alice.ID, game.ID)
	if stStatus.Played != 0 || !stStatus.Wishlist {
		t.Fatalf("wishlist was lost with the play count: %+v", stStatus)
	}
	if err := st.SetPlayedCount(ctx, alice.ID, game.ID, -1); err != nil {
		t.Fatal(err)
	}
	stStatus, _ = st.StatusFor(ctx, alice.ID, game.ID)
	if stStatus.Played != 0 || !stStatus.Wishlist {
		t.Fatalf("negative adjustment changed a wishlist-only row: %+v", stStatus)
	}
	updated, _ = st.GameByID(ctx, game.ID)
	if updated.Plays != 1 || updated.Wishlists != 1 {
		t.Fatalf("counts after decrement: plays=%d wishlists=%d", updated.Plays, updated.Wishlists)
	}
	stats, _ = st.StatsFor(ctx, alice.ID)
	if stats.GamesPlayed != 0 || stats.GamesWishlist != 1 {
		t.Fatalf("stats after decrement: %+v", stats)
	}

	liked, err := st.ToggleGameLike(ctx, alice.ID, game.ID)
	if err != nil || !liked {
		t.Fatalf("like toggle = %v, %v", liked, err)
	}
	stStatus, _ = st.StatusFor(ctx, alice.ID, game.ID)
	updated, _ = st.GameByID(ctx, game.ID)
	if !stStatus.Liked || !stStatus.Wishlist || stStatus.Played != 0 || updated.Likes != 1 || updated.Plays != 1 {
		t.Fatalf("like leaked into status/play counts: status=%+v game likes=%d plays=%d", stStatus, updated.Likes, updated.Plays)
	}
	liked, err = st.ToggleGameLike(ctx, alice.ID, game.ID)
	if err != nil || liked {
		t.Fatalf("second like toggle = %v, %v", liked, err)
	}
	updated, _ = st.GameByID(ctx, game.ID)
	if updated.Likes != 0 {
		t.Fatalf("game likes after unlike = %d", updated.Likes)
	}

	if err := st.SetStatusFlag(ctx, alice.ID, game.ID, "played", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("played should not be a settable flag: %v", err)
	}
	if err := st.EnsurePlayed(ctx, alice.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	stStatus, _ = st.StatusFor(ctx, alice.ID, game.ID)
	if stStatus.Played != 1 || !stStatus.Wishlist {
		t.Fatalf("EnsurePlayed did not preserve the wishlist: %+v", stStatus)
	}

	empty, _ := st.SaveGame(ctx, &models.Game{IGDBID: -3, Slug: "empty-status-game", Title: "Empty Status Game", ReleaseStatus: "released"})
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO statuses(user_id,game_id,played,playing,backlog,wishlist,updated_at) VALUES(?,?,0,0,0,0,?)`, alice.ID, empty.ID, Now()); err != nil {
		t.Fatal(err)
	}
	games, _, err := st.ListGames(ctx, GameFilter{UserID: alice.ID, NoStatus: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range games {
		if g.ID == empty.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("NoStatus filter did not ignore an empty status row")
	}
	if err := st.RefreshGameStats(ctx, empty.ID); err != nil {
		t.Fatal(err)
	}
	empty, _ = st.GameByID(ctx, empty.ID)
	if empty.Popularity != 0 {
		t.Fatalf("empty status row contributed popularity: %d", empty.Popularity)
	}
}

func TestRecentlyPlayedGamesTracksShelfEvents(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	old, _ := st.SaveGame(ctx, &models.Game{IGDBID: -801, Slug: "old-played", Title: "Old Played", ReleaseStatus: "released"})
	playing, _ := st.SaveGame(ctx, &models.Game{IGDBID: -802, Slug: "now-playing", Title: "Now Playing", ReleaseStatus: "released"})
	rated, _ := st.SaveGame(ctx, &models.Game{IGDBID: -803, Slug: "rated-played", Title: "Rated Played", ReleaseStatus: "released"})
	both, _ := st.SaveGame(ctx, &models.Game{IGDBID: -804, Slug: "both-shelves", Title: "Both Shelves", ReleaseStatus: "released"})
	backlog, _ := st.SaveGame(ctx, &models.Game{IGDBID: -805, Slug: "backlog-only", Title: "Backlog Only", ReleaseStatus: "released"})

	if err := st.SetPlayedCount(ctx, alice.ID, old.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatusFlag(ctx, alice.ID, playing.ID, "playing", true); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePlayed(ctx, alice.ID, rated.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: rated.ID, Rating: 9, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatuses(ctx, alice.ID, both.ID, true, true, false, false, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatusFlag(ctx, alice.ID, backlog.ID, "backlog", true); err != nil {
		t.Fatal(err)
	}

	setEventTimes := func(gameID int64, playedAt, playingAt any, updatedAt string) {
		t.Helper()
		if _, err := st.DB.ExecContext(ctx, `UPDATE statuses SET played_at=?,playing_at=?,updated_at=? WHERE user_id=? AND game_id=?`,
			playedAt, playingAt, updatedAt, alice.ID, gameID); err != nil {
			t.Fatal(err)
		}
	}
	setEventTimes(old.ID, "2026-01-02T12:00:00Z", nil, "2026-01-02T12:00:00Z")
	setEventTimes(playing.ID, nil, "2026-02-03T12:00:00Z", "2026-02-03T12:00:00Z")
	setEventTimes(rated.ID, "2026-03-04T12:00:00Z", nil, "2026-03-04T12:00:00Z")
	setEventTimes(both.ID, "2026-04-06T12:00:00Z", "2026-04-05T12:00:00Z", "2026-04-06T12:00:00Z")

	// Changing an unrelated shelf flag must not make an old played game recent.
	if err := st.SetStatusFlag(ctx, alice.ID, old.ID, "backlog", true); err != nil {
		t.Fatal(err)
	}
	oldStatus, _ := st.StatusFor(ctx, alice.ID, old.ID)
	if want := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC); !oldStatus.PlayedAt.Equal(want) {
		t.Fatalf("backlog edit changed played_at to %v", oldStatus.PlayedAt)
	}

	entries, err := st.RecentlyPlayedGames(ctx, alice.ID, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("recently played = %d entries, want 4", len(entries))
	}
	wantOrder := []string{"Both Shelves", "Rated Played", "Now Playing", "Old Played"}
	for i, title := range wantOrder {
		if entries[i].Game.Title != title {
			t.Fatalf("entry %d = %q, want %q", i, entries[i].Game.Title, title)
		}
	}
	if entries[0].Event != "Played" || entries[1].Rating != 9 || entries[2].Event != "Playing" {
		t.Fatalf("unexpected recently played data: %+v", entries)
	}
	for _, entry := range entries {
		if entry.Game.Title == "Backlog Only" {
			t.Fatal("backlog-only game appeared in recently played")
		}
	}

	if err := st.SetStatusFlag(ctx, alice.ID, playing.ID, "playing", false); err != nil {
		t.Fatal(err)
	}
	playingStatus, _ := st.StatusFor(ctx, alice.ID, playing.ID)
	if !playingStatus.PlayingAt.IsZero() {
		t.Fatal("leaving the playing shelf did not clear playing_at")
	}
	if err := st.SetPlayedCount(ctx, alice.ID, old.ID, 0); err != nil {
		t.Fatal(err)
	}
	oldStatus, _ = st.StatusFor(ctx, alice.ID, old.ID)
	if !oldStatus.PlayedAt.IsZero() {
		t.Fatal("removing the last play did not clear played_at")
	}
	if err := st.SetPlayedCount(ctx, alice.ID, old.ID, 1); err != nil {
		t.Fatal(err)
	}
	oldStatus, _ = st.StatusFor(ctx, alice.ID, old.ID)
	if oldStatus.PlayedAt.IsZero() {
		t.Fatal("replaying did not record a new played_at")
	}
}

func TestStatsAndPrivacy(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, err := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	game, err := st.SaveGame(ctx, &models.Game{IGDBID: -1, Slug: "test-game", Title: "Test Game", ReleaseStatus: "released"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsurePlayed(ctx, alice.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: game.ID, Rating: 8, Status: "played", Playtime: 4})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := st.StatsFor(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.GamesPlayed != 1 || stats.TotalHours != 4 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if err := st.RefreshGameStats(ctx, game.ID); err != nil {
		t.Fatal(err)
	}
	updated, _ := st.GameByID(ctx, game.ID)
	if updated.RatingCount != 1 {
		t.Fatalf("rating count = %d", updated.RatingCount)
	}

	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	if err := st.UpdateProfile(ctx, bob.ID, ProfileUpdate{PrivateProfile: true}); err != nil {
		t.Fatal(err)
	}
	viewer, _ := st.UserByUsername(ctx, "alice")
	if viewer.PrivateProfile {
		t.Fatal("alice should not be private")
	}
	b, _ := st.UserByUsername(ctx, "bob")
	if !b.PrivateProfile {
		t.Fatal("bob should be private")
	}
}
