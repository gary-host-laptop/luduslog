package store

import (
	"context"
	"testing"
	"time"

	"luduslog/internal/models"
)

func TestTimelineMergesSourcesAndPaginates(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -6801, Slug: "timeline-game", Title: "Timeline Game", ReleaseStatus: "released"})

	review, err := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: game.ID, Review: "A review", Rating: 8, Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	bare, err := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: game.ID, Status: "playing"})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := st.CreateSession(ctx, SessionInput{LogID: bare.ID, Note: "A session note", NotePrivacy: "everyone", Playtime: 2, Days: []string{"2026-03-01"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Follow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddActivity(ctx, alice.ID, "status", &game.ID, nil, nil, "backlog:1", "public"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddActivity(ctx, alice.ID, "liked", &game.ID, nil, nil, "", "public"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddActivity(ctx, alice.ID, "rated", &game.ID, nil, nil, "8", "public"); err != nil {
		t.Fatal(err)
	}
	// Content and joined rows must never surface as activity cards.
	if err := st.AddActivity(ctx, alice.ID, "log", &game.ID, &bare.ID, nil, "", "public"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddActivity(ctx, alice.ID, "joined", nil, nil, nil, "", "public"); err != nil {
		t.Fatal(err)
	}

	base := time.Now().UTC().Add(-12 * time.Hour)
	setCreated := func(table string, id int64, at time.Time) {
		t.Helper()
		if _, err := st.DB.ExecContext(ctx, `UPDATE `+table+` SET created_at=? WHERE id=?`, at.Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
	}
	setCreated("logs", review.ID, base.Add(1*time.Hour))
	setCreated("logs", bare.ID, base.Add(2*time.Hour))
	setCreated("play_sessions", sessionID, base.Add(3*time.Hour))
	activityRows, _ := st.DB.QueryContext(ctx, `SELECT id,type FROM activities WHERE user_id=? ORDER BY id`, alice.ID)
	type activityRow struct {
		id   int64
		kind string
	}
	var rows []activityRow
	for activityRows.Next() {
		var row activityRow
		activityRows.Scan(&row.id, &row.kind)
		rows = append(rows, row)
	}
	activityRows.Close()
	for _, row := range rows {
		switch row.kind {
		case "status":
			setCreated("activities", row.id, base.Add(4*time.Hour))
		case "liked":
			setCreated("activities", row.id, base.Add(5*time.Hour))
		case "rated":
			setCreated("activities", row.id, base.Add(6*time.Hour))
		case "follow":
			setCreated("activities", row.id, base.Add(7*time.Hour))
		}
	}

	items, next, err := st.Timeline(ctx, alice.ID, []int64{alice.ID}, "all", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("unexpected next cursor with all items: %q", next)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Kind] = true
		if item.Kind == "log" && item.Visibility == "log" {
			t.Fatal("timeline included a content log activity duplicate")
		}
	}
	for _, kind := range []string{"review", "log", "session", "shelf", "like", "rating", "follow"} {
		if !seen[kind] {
			t.Errorf("timeline missing %s: %+v", kind, seen)
		}
	}
	if len(items) != 7 {
		t.Fatalf("timeline items = %d, want 7", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i].At.After(items[i-1].At) {
			t.Fatalf("timeline is not newest first: %+v", items)
		}
	}

	first, cursor, err := st.Timeline(ctx, alice.ID, []int64{alice.ID}, "all", "", 2)
	if err != nil || cursor == "" || len(first) != 2 {
		t.Fatalf("first timeline page = %d %q %v", len(first), cursor, err)
	}
	second, cursor2, err := st.Timeline(ctx, alice.ID, []int64{alice.ID}, "all", cursor, 2)
	if err != nil || len(second) != 2 || cursor2 == "" {
		t.Fatalf("second timeline page = %d %q %v", len(second), cursor2, err)
	}
	for _, a := range first {
		for _, b := range second {
			if a.Key == b.Key {
				t.Fatalf("pagination repeated key %s", a.Key)
			}
		}
	}
}

func TestTimelineVisibilityAndMasterSwitch(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -6802, Slug: "visibility-game", Title: "Visibility Game", ReleaseStatus: "released"})
	if _, err := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: game.ID, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLog(ctx, LogInput{UserID: alice.ID, GameID: game.ID, Review: "Visible review", Status: "played"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddActivity(ctx, alice.ID, "liked", &game.ID, nil, nil, "", "public"); err != nil {
		t.Fatal(err)
	}

	// A stranger sees everyone-visible rows.
	items, _, err := st.Timeline(ctx, 0, []int64{alice.ID}, "all", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("stranger timeline = %+v", items)
	}

	if err := st.SetActivityVisibility(ctx, alice.ID, map[string]string{"log": "private", "review": "followers", "like": "private"}); err != nil {
		t.Fatal(err)
	}
	items, _, err = st.Timeline(ctx, bob.ID, []int64{alice.ID}, "all", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("private/followers rows leaked to a stranger: %+v", items)
	}
	if err := st.Follow(ctx, bob.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	items, _, err = st.Timeline(ctx, bob.ID, []int64{alice.ID}, "all", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "review" {
		t.Fatalf("follower timeline = %+v", items)
	}
	ownerItems, _, _ := st.Timeline(ctx, alice.ID, []int64{alice.ID}, "all", "", 20)
	if len(ownerItems) != 3 {
		t.Fatalf("owner should always see their stream: %+v", ownerItems)
	}

	if err := st.UpdateProfile(ctx, alice.ID, ProfileUpdate{HideActivity: true}); err != nil {
		t.Fatal(err)
	}
	items, _, err = st.Timeline(ctx, bob.ID, []int64{alice.ID}, "all", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("hide_activity did not override visibility: %+v", items)
	}
}

func TestSessionAutoLogAndEventHygiene(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -6803, Slug: "auto-game", Title: "Auto Game", ReleaseStatus: "released"})

	l, err := st.SessionLogFor(ctx, alice.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !l.AutoCreated {
		t.Fatal("session container log was not marked auto_created")
	}
	if _, err := st.CreateSession(ctx, SessionInput{LogID: l.ID, Note: "Session without a log", NotePrivacy: "everyone", Playtime: 1}); err != nil {
		t.Fatal(err)
	}
	items, _, err := st.Timeline(ctx, alice.ID, []int64{alice.ID}, "all", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "session" {
		t.Fatalf("auto-created log should be hidden behind its session: %+v", items)
	}
	items, _, _ = st.Timeline(ctx, alice.ID, []int64{alice.ID}, "logs", "", 20)
	if len(items) != 0 {
		t.Fatalf("auto-created log rendered in the log filter: %+v", items)
	}

	// One rating event survives repeated changes.
	st.AddActivity(ctx, alice.ID, "rated", &game.ID, nil, nil, "5", "public")
	st.AddActivity(ctx, alice.ID, "rated", &game.ID, nil, nil, "8", "public")
	var ratings int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM activities WHERE user_id=? AND type='rated' AND game_id=?`, alice.ID, game.ID).Scan(&ratings)
	if ratings != 1 {
		t.Fatalf("rating events = %d, want 1", ratings)
	}

	// Deleting a log removes its activities.
	if err := st.AddActivity(ctx, alice.ID, "log", &game.ID, &l.ID, nil, "", "public"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLog(ctx, l.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	var logActivities int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM activities WHERE log_id=?`, l.ID).Scan(&logActivities)
	if logActivities != 0 {
		t.Fatalf("deleted log left %d activities", logActivities)
	}

	// Approved follows produce exactly one follow event.
	if err := st.Follow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.ApproveFollow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	var follows int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM activities WHERE user_id=? AND type='follow' AND target_user=?`, alice.ID, bob.ID).Scan(&follows)
	if follows != 1 {
		t.Fatalf("follow events = %d, want 1", follows)
	}
}
