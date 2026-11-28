package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func TestIssue78EditorAndQuickRatingShareOneValue(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "rateuser", "rateuser@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7801, Slug: "rating-sync", Title: "Rating Sync", Category: "main_game", ReleaseStatus: "released"})
	client, sess := authedClient(t, ts, st, user)
	plain := noRedirectClient(client)

	base := url.Values{
		"_csrf":           {sess.CSRF},
		"name":            {"First run"},
		"status":          {"played"},
		"played":          {"1"},
		"game_shelf_form": {"1"},
		"game_shelf":      {"playing"},
	}
	save := url.Values{}
	for key, values := range base {
		save[key] = append([]string{}, values...)
	}
	save.Set("rating", "7")
	if got := postStatus(t, plain, ts.URL+"/games/rating-sync/log", save); got != http.StatusSeeOther {
		t.Fatalf("editor save = %d", got)
	}
	if got := st.RatingForUser(ctx, user.ID, game.ID); got != 7 {
		t.Fatalf("editor rating = %v, want 7", got)
	}

	_, gameBody := getBody(t, client, ts.URL+"/games/rating-sync")
	if !strings.Contains(gameBody, `value="7" checked`) {
		t.Fatal("game page quick rating did not reflect the editor rating")
	}
	_, editorBody := getBody(t, client, ts.URL+"/games/rating-sync/log")
	if !strings.Contains(editorBody, `value="7" checked`) {
		t.Fatal("editor did not show the saved central rating")
	}
	if strings.Contains(editorBody, `name="primary_rating"`) {
		t.Fatal("editor still offers a primary-rating checkbox")
	}

	status, _ := st.StatusFor(ctx, user.ID, game.ID)
	logs, _ := st.UserGameLogs(ctx, user.ID, game.ID, true)
	if len(logs) != 1 {
		t.Fatalf("logs after editor save = %d, want 1", len(logs))
	}
	if got := postStatus(t, plain, ts.URL+"/games/rating-sync/rate", url.Values{"_csrf": {sess.CSRF}, "rating": {"9"}}); got != http.StatusSeeOther {
		t.Fatalf("quick rating = %d", got)
	}
	if got := st.RatingForUser(ctx, user.ID, game.ID); got != 9 {
		t.Fatalf("quick rating = %v, want 9", got)
	}
	after, _ := st.StatusFor(ctx, user.ID, game.ID)
	if after.Shelf != status.Shelf || after.Played != status.Played {
		t.Fatalf("quick rating changed shelf/count: %+v -> %+v", status, after)
	}
	_, gameBody = getBody(t, client, ts.URL+"/games/rating-sync")
	if !strings.Contains(gameBody, `value="9" checked`) {
		t.Fatal("game page did not show the quick rating")
	}
	_, editorBody = getBody(t, client, ts.URL+"/logs/"+itoa(logs[0].ID)+"/edit?tab=play")
	if !strings.Contains(editorBody, `value="9" checked`) {
		t.Fatal("editor did not pick up the quick rating")
	}

	clear := url.Values{}
	for key, values := range base {
		clear[key] = append([]string{}, values...)
	}
	clear.Set("rating", "0")
	if got := postStatus(t, plain, ts.URL+"/logs/"+itoa(logs[0].ID)+"/edit", clear); got != http.StatusSeeOther {
		t.Fatalf("editor clear = %d", got)
	}
	if got := st.RatingForUser(ctx, user.ID, game.ID); got != 0 {
		t.Fatalf("rating after editor clear = %v, want 0", got)
	}
	_, gameBody = getBody(t, client, ts.URL+"/games/rating-sync")
	if strings.Contains(gameBody, "quick-rate-clear") {
		t.Fatal("game page still offers to clear an empty rating")
	}
}

func TestIssue78MultipleLogsShowOneCentralRating(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "multiuser", "multiuser@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7802, Slug: "multi-rating", Title: "Multi Rating", Category: "main_game", ReleaseStatus: "released"})

	review, err := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Name: "First run", Rating: 8, Review: "A full review", Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Name: "Replay run", Rating: 4, Status: "replayed"}); err != nil {
		t.Fatal(err)
	}
	if got := st.RatingForUser(ctx, user.ID, game.ID); got != 4 {
		t.Fatalf("central rating = %v, want 4", got)
	}

	client, _ := authedClient(t, ts, st, user)
	_, listed := getBody(t, client, ts.URL+"/users/multiuser/logs/multi-rating")
	if strings.Count(listed, "4.0 / 10") != 1 || strings.Contains(listed, "8.0 / 10") {
		t.Fatal("user game logs should show the central rating once")
	}
	_, reviewBody := getBody(t, client, ts.URL+"/users/multiuser/reviews/"+itoa(review.ID))
	if !strings.Contains(reviewBody, "4.0/10") || strings.Contains(reviewBody, "8.0/10") {
		t.Fatal("review page is not showing the central rating")
	}
}
