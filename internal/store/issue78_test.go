package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"

	"luduslog/internal/models"

	_ "modernc.org/sqlite"
)

func TestCentralRatingMigrationBackfill(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "test.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "migrations/022_central_ratings.sql" {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}

	for _, name := range []string{"alice", "bob"} {
		if _, err := db.Exec(`INSERT INTO users(id,username,username_lower,email,email_lower,password_hash,created_at)
			VALUES(?,?,?,?,?,'x','2024-01-01T00:00:00Z')`, map[string]any{"alice": 1, "bob": 2}[name], name, name, name+"@example.com", name+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	for i, slug := range []string{"primary", "fallback", "primary-wins"} {
		if _, err := db.Exec(`INSERT INTO games(id,igdb_id,slug,title,sort_title,created_at,updated_at)
			VALUES(?,?,?,?,?,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`, i+1, i+1, slug, slug, slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO logs(id,user_id,game_id,rating,status,primary_rating,draft,created_at,updated_at) VALUES
		(1,1,1,6,'played',0,0,'2024-02-01T00:00:00Z','2024-02-03T00:00:00Z'),
		(2,1,1,8,'completed',1,0,'2024-02-02T00:00:00Z','2024-02-02T00:00:00Z'),
		(3,1,2,4,'played',0,0,'2024-03-02T00:00:00Z','2024-03-02T00:00:00Z'),
		(4,1,2,2,'replayed',0,0,'2024-03-01T00:00:00Z','2024-03-01T00:00:00Z'),
		(5,1,3,9,'played',1,0,'2024-04-01T00:00:00Z','2024-04-01T00:00:00Z'),
		(6,1,3,3,'replayed',0,0,'2024-04-02T00:00:00Z','2024-04-02T00:00:00Z'),
		(7,2,1,6,'played',1,0,'2024-05-01T00:00:00Z','2024-05-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	body, err := migrationFS.ReadFile("migrations/022_central_ratings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatal(err)
	}

	want := map[[2]int64]float64{
		{1, 1}: 8, // the primary log wins over a newer unrated-primary log
		{1, 2}: 4, // no primary; the newest rated log wins
		{1, 3}: 9, // primary wins even when a newer log exists
		{2, 1}: 6,
	}
	for key, expected := range want {
		var rating float64
		if err := db.QueryRow(`SELECT rating FROM game_ratings WHERE user_id=? AND game_id=?`, key[0], key[1]).Scan(&rating); err != nil {
			t.Fatal(err)
		}
		if rating != expected {
			t.Errorf("user %d game %d rating = %v, want %v", key[0], key[1], rating, expected)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_ratings`).Scan(&n); err != nil || n != len(want) {
		t.Fatalf("game_ratings rows = %d, want %d (%v)", n, len(want), err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs WHERE primary_rating<>0`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy primary logs = %d, want 0 (%v)", n, err)
	}

	var avg float64
	var count int
	if err := db.QueryRow(`SELECT avg_rating,rating_count FROM games WHERE id=1`).Scan(&avg, &count); err != nil {
		t.Fatal(err)
	}
	if count != 2 || avg != 7 {
		t.Fatalf("game 1 aggregate = avg %v, count %d; want avg 7, count 2", avg, count)
	}
	var weighted float64
	if err := db.QueryRow(`SELECT weighted FROM games WHERE id=1`).Scan(&weighted); err != nil {
		t.Fatal(err)
	}
	if wantWeighted := WeightedRating(7, 2); weighted != wantWeighted {
		t.Fatalf("weighted = %v, want %v", weighted, wantWeighted)
	}
}

func TestCentralRatingIsIndependentFromLogs(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	user, _ := st.CreateUser(ctx, "ratinguser", "ratinguser@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{Slug: "rating-independent", Title: "Rating Independent"})

	if err := st.SetRating(ctx, user.ID, game.ID, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LatestLogFor(ctx, user.ID, game.ID); err == nil {
		t.Fatal("SetRating created a log")
	}
	status, _ := st.StatusFor(ctx, user.ID, game.ID)
	if status.Played != 0 || status.Shelf != "" {
		t.Fatalf("SetRating changed the game status: %+v", status)
	}

	log, err := st.CreateLog(ctx, LogInput{UserID: user.ID, GameID: game.ID, Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	if log.Rating != 7 {
		t.Fatalf("saved log mirrored rating %v, want 7", log.Rating)
	}
	if err := st.DeleteLog(ctx, log.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if got := st.RatingForUser(ctx, user.ID, game.ID); got != 7 {
		t.Fatalf("deleting a log changed the rating to %v", got)
	}
	if err := st.ClearRating(ctx, user.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	if got := st.RatingForUser(ctx, user.ID, game.ID); got != 0 {
		t.Fatalf("cleared rating = %v, want 0", got)
	}
}
