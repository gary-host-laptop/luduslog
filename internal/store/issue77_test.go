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

func TestShelfMigrationBackfillsOneValue(t *testing.T) {
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
		if name >= "migrations/021_shelf.sql" {
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

	if _, err := db.Exec(`INSERT INTO users(id,username,username_lower,email,email_lower,password_hash,created_at)
		VALUES(1,'u','u','u@example.com','u@example.com','x','2024-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for i, slug := range []string{"all", "played", "backlog", "wishlist", "empty"} {
		if _, err := db.Exec(`INSERT INTO games(id,igdb_id,slug,title,sort_title,created_at,updated_at)
			VALUES(?,?,?,?,?,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`, i+1, i+1, slug, slug, slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO statuses(user_id,game_id,played,playing,backlog,wishlist,updated_at) VALUES
		(1,1,1,1,1,1,'2024-05-01T12:00:00Z'),
		(1,2,2,0,0,0,'2024-05-02T12:00:00Z'),
		(1,3,0,0,1,0,'2024-05-03T12:00:00Z'),
		(1,4,0,0,0,1,'2024-05-04T12:00:00Z'),
		(1,5,0,0,0,0,'2024-05-05T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE statuses SET playing_at='2024-06-01T12:00:00Z',played_at='2024-06-02T12:00:00Z',
		backlog_at='2024-06-03T12:00:00Z',wishlist_at='2024-06-04T12:00:00Z' WHERE game_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO logs(id,user_id,game_id,status,created_at,updated_at) VALUES
		(1,1,1,'playing','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),
		(2,1,1,'backlog','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),
		(3,1,1,'wishlist','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),
		(4,1,1,'completed','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	body, err := migrationFS.ReadFile("migrations/021_shelf.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatal(err)
	}

	wantShelf := map[int64]string{1: "playing", 2: "played", 3: "backlog", 4: "wishlist", 5: ""}
	for gameID, want := range wantShelf {
		var got string
		if err := db.QueryRow(`SELECT shelf FROM statuses WHERE game_id=?`, gameID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("game %d shelf = %q, want %q", gameID, got, want)
		}
	}
	var shelfAt sql.NullString
	if err := db.QueryRow(`SELECT shelf_at FROM statuses WHERE game_id=1`).Scan(&shelfAt); err != nil {
		t.Fatal(err)
	}
	if !shelfAt.Valid || shelfAt.String != "2024-06-01T12:00:00Z" {
		t.Fatalf("playing shelf time = %+v, want playing_at", shelfAt)
	}

	rows, err := db.Query(`SELECT status FROM logs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantStatus := []string{"played", "played", "played", "completed"}
	for i := 0; rows.Next(); i++ {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != wantStatus[i] {
			t.Errorf("log %d status = %q, want %q", i+1, status, wantStatus[i])
		}
	}
}

func TestSetShelfAndPlayCountAreIndependent(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	user, _ := st.CreateUser(ctx, "shelftest", "shelftest@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{Slug: "shelf-test", Title: "Shelf Test"})

	if err := st.SetShelf(ctx, user.ID, game.ID, "wishlist"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlayedCount(ctx, user.ID, game.ID, 4); err != nil {
		t.Fatal(err)
	}
	if err := st.SetShelf(ctx, user.ID, game.ID, "played"); err != nil {
		t.Fatal(err)
	}
	status, _ := st.StatusFor(ctx, user.ID, game.ID)
	if status.Shelf != "played" || status.Played != 4 {
		t.Fatalf("played shelf did not retain the play count: %+v", status)
	}
	if err := st.SetShelf(ctx, user.ID, game.ID, "backlog"); err != nil {
		t.Fatal(err)
	}
	status, _ = st.StatusFor(ctx, user.ID, game.ID)
	if status.Shelf != "backlog" || status.Played != 4 {
		t.Fatalf("changing shelves changed the play count: %+v", status)
	}
	if err := st.SetShelf(ctx, user.ID, game.ID, ""); err != nil {
		t.Fatal(err)
	}
	status, _ = st.StatusFor(ctx, user.ID, game.ID)
	if status.Shelf != "" || status.Played != 4 {
		t.Fatalf("removing from shelf changed the play count: %+v", status)
	}
}
