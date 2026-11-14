package store

import (
	"context"
	"math"
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
