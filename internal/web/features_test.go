package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"luduslog/internal/auth"
	"luduslog/internal/config"
	"luduslog/internal/models"
	"luduslog/internal/queue"
	"luduslog/internal/store"
)

func testServerWithDataDir(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := config.Config{
		Name: "LudusLog Test", Software: "luduslog",
		BaseURL: "http://example.test", SessionKey: "test-session-key-0123456789", Registration: "open",
		MaxCommentChars: 5000, DataDir: dir,
	}
	srv, err := New(cfg, st, queue.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); st.DB.Close() })
	return ts, st, dir
}

func noRedirectClient(client *http.Client) *http.Client {
	return &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func postStatus(t *testing.T, client *http.Client, path string, values url.Values) int {
	t.Helper()
	resp, err := client.PostForm(path, values)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestQuickRatingWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -401, Slug: "quick-rate", Title: "Quick Rate", ReleaseStatus: "released"})
	client, sess := authedClient(t, ts, st, alice)
	plain := noRedirectClient(client)

	code, body := getBody(t, client, ts.URL+"/games/quick-rate")
	if code != 200 || !strings.Contains(body, `action="/games/quick-rate/rate"`) || !strings.Contains(body, `class="quick-rate"`) || !strings.Contains(body, "ph-light ph-star") {
		t.Fatalf("quick rating control missing (%d)", code)
	}
	if got := postStatus(t, plain, ts.URL+"/games/quick-rate/rate", url.Values{"_csrf": {sess.CSRF}, "rating": {"11"}}); got != http.StatusSeeOther {
		t.Fatalf("invalid rating = %d", got)
	}
	if _, err := st.LatestLogFor(ctx, alice.ID, game.ID); err == nil {
		t.Fatal("invalid rating created a log")
	}

	if got := postStatus(t, plain, ts.URL+"/games/quick-rate/rate", url.Values{"_csrf": {sess.CSRF}, "rating": {"6"}}); got != http.StatusSeeOther {
		t.Fatalf("rate = %d", got)
	}
	log, err := st.LatestLogFor(ctx, alice.ID, game.ID)
	if err != nil || log.Rating != 6 || log.Status != "played" {
		t.Fatalf("quick rating log = %+v, %v", log, err)
	}
	status, _ := st.StatusFor(ctx, alice.ID, game.ID)
	if status.Played < 1 {
		t.Fatalf("rating did not mark played: %+v", status)
	}
	_, body = getBody(t, client, ts.URL+"/games/quick-rate")
	if !strings.Contains(body, `value="6" checked`) || !strings.Contains(body, "6.0/10") || !strings.Contains(body, "quick-rate-clear") {
		t.Fatal("game page did not reflect the saved rating")
	}

	if got := postStatus(t, plain, ts.URL+"/games/quick-rate/rate", url.Values{"_csrf": {sess.CSRF}, "clear": {"1"}}); got != http.StatusSeeOther {
		t.Fatalf("clear rating = %d", got)
	}
	log, _ = st.LatestLogFor(ctx, alice.ID, game.ID)
	status, _ = st.StatusFor(ctx, alice.ID, game.ID)
	if log.Rating != 0 || status.Played < 1 {
		t.Fatalf("clearing rating damaged rating/play count: rating=%v played=%d", log.Rating, status.Played)
	}
	_, body = getBody(t, client, ts.URL+"/games/quick-rate")
	if strings.Contains(body, "quick-rate-clear") {
		t.Fatal("clear action still offered without a rating")
	}
}

func TestFavouriteGamesWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	var games []*models.Game
	for i := 1; i <= 6; i++ {
		g, _ := st.SaveGame(ctx, &models.Game{IGDBID: int64(-500 - i), Slug: "fave-" + strconv.Itoa(i), Title: "Fave " + strconv.Itoa(i), ReleaseStatus: "released"})
		games = append(games, g)
	}
	client, sess := authedClient(t, ts, st, alice)
	plain := noRedirectClient(client)

	signedOut := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _ := getBody(t, signedOut, ts.URL+"/settings?tab=favourites"); code != http.StatusSeeOther {
		t.Fatalf("signed-out favourites settings = %d", code)
	}
	code, body := getBody(t, client, ts.URL+"/settings?tab=favourites")
	if code != 200 || !strings.Contains(body, "Favourite games") || !strings.Contains(body, `action="/settings"`) {
		t.Fatalf("favourites tab missing (%d)", code)
	}
	for i := 0; i < 5; i++ {
		if got := postStatus(t, plain, ts.URL+"/settings/favourites/add", url.Values{"_csrf": {sess.CSRF}, "game_id": {strconv.FormatInt(games[i].ID, 10)}}); got != http.StatusSeeOther {
			t.Fatalf("add favourite = %d", got)
		}
	}
	if got := postStatus(t, plain, ts.URL+"/settings/favourites/add", url.Values{"_csrf": {sess.CSRF}, "game_id": {strconv.FormatInt(games[5].ID, 10)}}); got != http.StatusSeeOther {
		t.Fatalf("sixth favourite = %d", got)
	}
	if got := st.FavoriteGames(ctx, alice.ID, 10); len(got) != 5 {
		t.Fatalf("favourites = %d, want 5", len(got))
	}

	_, profile := getBody(t, client, ts.URL+"/users/alice")
	if !strings.Contains(profile, "Favourite games") || !strings.Contains(profile, "Fave 1") {
		t.Fatal("profile overview did not show favourites")
	}
	_, reviews := getBody(t, client, ts.URL+"/users/alice?tab=reviews")
	if strings.Contains(reviews, "Favourite games") {
		t.Fatal("favourites leaked onto the reviews tab")
	}

	if got := postStatus(t, plain, ts.URL+"/settings/favourites/reorder", url.Values{"_csrf": {sess.CSRF}, "game_id": {strconv.FormatInt(games[0].ID, 10)}, "direction": {"down"}}); got != http.StatusSeeOther {
		t.Fatalf("reorder = %d", got)
	}
	got := st.FavoriteGames(ctx, alice.ID, 5)
	if got[0].ID != games[1].ID || got[1].ID != games[0].ID {
		t.Fatalf("move down did not swap: %+v", got)
	}
	if got := postStatus(t, plain, ts.URL+"/settings/favourites/remove", url.Values{"_csrf": {sess.CSRF}, "game_id": {strconv.FormatInt(games[0].ID, 10)}}); got != http.StatusSeeOther {
		t.Fatalf("remove favourite = %d", got)
	}
	if got := st.FavoriteGames(ctx, alice.ID, 5); len(got) != 4 {
		t.Fatalf("favourites after remove = %d", len(got))
	}
}

func TestReviewLikePlacementAndEmptyComments(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -601, Slug: "review-heart", Title: "Review Heart", ReleaseStatus: "released"})
	log, _ := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Rating: 9, Review: "Lovely", Status: "played"})
	reviewURL := ts.URL + "/users/alice/reviews/" + itoa(log.ID)

	client, sess := authedClient(t, ts, st, bob)
	plain := noRedirectClient(client)
	_, body := getBody(t, client, reviewURL)
	for _, want := range []string{`class="review-like"`, `ph-light ph-heart`, "Like · 0", `<span class="muted">0</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("review page missing %q", want)
		}
	}
	reviewCard := strings.SplitN(body, "<h2>Comments</h2>", 2)[0]
	if strings.Contains(body, "No comments yet.") {
		t.Error("empty comments card still rendered")
	}
	if strings.Contains(reviewCard, `class="log-actions"`) {
		t.Error("non-owner review card has an empty action row")
	}

	if got := postStatus(t, plain, ts.URL+"/logs/"+itoa(log.ID)+"/like", url.Values{"_csrf": {sess.CSRF}}); got != http.StatusSeeOther {
		t.Fatalf("like = %d", got)
	}
	_, body = getBody(t, client, reviewURL)
	if !strings.Contains(body, "ph-fill ph-heart") || !strings.Contains(body, "Liked · 1") {
		t.Fatal("liked review did not use the filled heart")
	}
	if strings.Contains(body, "ph-heart-fill") {
		t.Fatal("liked review used an icon class with no glyph")
	}

	ownerClient, _ := authedClient(t, ts, st, alice)
	_, ownerBody := getBody(t, ownerClient, reviewURL)
	if !strings.Contains(ownerBody, `class="log-actions"`) {
		t.Fatal("owner actions disappeared")
	}
}

func TestHomeFeedRendersMissingUser(t *testing.T) {
	ts, _ := testServer(t)
	srv, ok := ts.Config.Handler.(*Server)
	if !ok {
		t.Fatal("test server handler is not *Server")
	}
	data := homeData{
		Feed:  []models.Activity{{Type: "log"}},
		Lists: []*models.List{{Slug: "orphan", Title: "Orphan list"}},
	}
	var buf bytes.Buffer
	if err := srv.pages["home"].ExecuteTemplate(&buf, "content", data); err != nil {
		t.Fatalf("home feed with a missing user: %v", err)
	}
	if !strings.Contains(buf.String(), "Someone") {
		t.Fatal("missing-user fallback was not rendered")
	}
}

func TestAccountDeletionWeb(t *testing.T) {
	t.Run("self delete confirmations and uploads", func(t *testing.T) {
		ts, st, dir := testServerWithDataDir(t)
		ctx := context.Background()
		alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
		client, sess := authedClient(t, ts, st, alice)
		plain := noRedirectClient(client)
		avatarDir := filepath.Join(dir, "uploads", "avatars", strconv.FormatInt(alice.ID, 10))
		if err := os.MkdirAll(avatarDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(avatarDir, "a.png"), []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}

		if got := postStatus(t, plain, ts.URL+"/settings/delete", url.Values{"_csrf": {sess.CSRF}, "password": {"wrong"}, "username": {"alice"}}); got != http.StatusSeeOther {
			t.Fatalf("wrong password = %d", got)
		}
		if _, err := st.UserByUsername(ctx, "alice"); err != nil {
			t.Fatal("wrong password deleted the account")
		}
		if got := postStatus(t, plain, ts.URL+"/settings/delete", url.Values{"_csrf": {sess.CSRF}, "password": {"password123"}, "username": {"nope"}}); got != http.StatusSeeOther {
			t.Fatalf("wrong username = %d", got)
		}
		if _, err := st.UserByUsername(ctx, "alice"); err != nil {
			t.Fatal("wrong username deleted the account")
		}

		// The test helper hashes with "hash" for new accounts.
		hash, _ := auth.HashPassword("password123")
		if err := st.SetUserPassword(ctx, alice.ID, hash); err != nil {
			t.Fatal(err)
		}
		if got := postStatus(t, plain, ts.URL+"/settings/delete", url.Values{"_csrf": {sess.CSRF}, "password": {"password123"}, "username": {"ALICE"}}); got != http.StatusSeeOther {
			t.Fatalf("self delete = %d", got)
		}
		if _, err := st.UserByUsername(ctx, "alice"); err == nil {
			t.Fatal("account still exists after deletion")
		}
		if _, err := os.Stat(avatarDir); !os.IsNotExist(err) {
			t.Fatalf("avatar directory still exists: %v", err)
		}
	})

	t.Run("admin deletion and last admin guard", func(t *testing.T) {
		ts, st, _ := testServerWithDataDir(t)
		ctx := context.Background()
		admin, _ := st.CreateUser(ctx, "admin", "admin@example.test", "hash", true)
		mod, _ := st.CreateUser(ctx, "mod", "mod@example.test", "hash", false)
		st.SetRole(ctx, mod.ID, "mod")
		target, _ := st.CreateUser(ctx, "target", "target@example.test", "hash", false)
		adminClient, adminSess := authedClient(t, ts, st, admin)
		plain := noRedirectClient(adminClient)

		modClient, modSess := authedClient(t, ts, st, mod)
		modPlain := noRedirectClient(modClient)
		if got := postStatus(t, modPlain, ts.URL+"/admin/users/"+strconv.FormatInt(target.ID, 10)+"/delete", url.Values{"_csrf": {modSess.CSRF}}); got != http.StatusNotFound {
			t.Fatalf("moderator delete = %d, want 404", got)
		}
		if _, err := st.UserByID(ctx, target.ID); err != nil {
			t.Fatal("moderator deleted the account")
		}

		if got := postStatus(t, plain, ts.URL+"/admin/users/"+strconv.FormatInt(target.ID, 10)+"/delete", url.Values{"_csrf": {adminSess.CSRF}}); got != http.StatusSeeOther {
			t.Fatalf("admin delete = %d", got)
		}
		if _, err := st.UserByID(ctx, target.ID); err == nil {
			t.Fatal("admin delete did not remove the account")
		}
		entries, _ := st.AuditEntries(ctx, 10)
		found := false
		for _, e := range entries {
			if e.Action == "delete_user" && e.Detail == "target" {
				found = true
			}
		}
		if !found {
			t.Fatal("admin delete was not audited")
		}

		if got := postStatus(t, plain, ts.URL+"/admin/users/"+strconv.FormatInt(admin.ID, 10)+"/delete", url.Values{"_csrf": {adminSess.CSRF}}); got != http.StatusSeeOther {
			t.Fatalf("admin self delete = %d", got)
		}
		if _, err := st.UserByID(ctx, admin.ID); err != nil {
			t.Fatal("admin deleted themselves from the admin page")
		}
	})
}
