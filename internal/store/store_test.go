package store

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

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
	if err := st.SetStatusFlag(ctx, alice.ID, game.ID, "played", true); err != nil {
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
