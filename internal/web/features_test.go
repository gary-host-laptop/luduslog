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
	if !strings.Contains(body, "<noscript>") || !strings.Contains(body, "Save rating") {
		t.Fatal("quick rating no-JS fallback missing")
	}
	if strings.Contains(body, "quick-rate-row") || strings.Contains(body, "quick-rate-label") || strings.Contains(body, `title="10.0/10"`) || strings.Contains(body, `title="0.5/10"`) {
		t.Fatal("quick rating still renders the old numeric row or star tooltips")
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
	if !strings.Contains(body, `value="6" checked`) || !strings.Contains(body, "6.0 out of 10") || !strings.Contains(body, `aria-label="Clear rating"`) || !strings.Contains(body, "quick-rate-clear") {
		t.Fatal("game page did not reflect the saved rating")
	}
	if strings.Contains(body, "6.0/10") || strings.Contains(body, "quick-rate-row") {
		t.Fatal("saved rating still renders the old numeric row")
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

func TestGameCoverPanelWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -402, Slug: "cover-panel", Title: "Cover Panel", Summary: "Summary below the hero.", ReleaseStatus: "released",
		CoverURL: "https://images.igdb.com/igdb/image/upload/t_cover_big_2x/cover-panel.jpg"})
	userLog, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Rating: 8, Review: "Solid", Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE games SET avg_rating=8,weighted=3.8,rating_count=1,plays=7,playing=3,backlogs=5,wishlists=2 WHERE id=?`, game.ID); err != nil {
		t.Fatal(err)
	}
	client, _ := authedClient(t, ts, st, alice)

	code, body := getBody(t, client, ts.URL+"/games/cover-panel")
	if code != http.StatusOK {
		t.Fatalf("game page = %d", code)
	}
	for _, want := range []string{
		`class="game-cover-panel"`,
		`class="game-cover-pane"`,
		`class="game-library-card"`,
		`name="flag" value="playing"`,
		`name="flag" value="backlog"`,
		`name="flag" value="wishlist"`,
		`href="/games/cover-panel/log"`,
		`href="/logs/` + strconv.FormatInt(userLog.ID, 10) + `/sessions"`,
		`action="/games/cover-panel/rate"`,
		`value="8" checked`,
		`8.0 out of 10`,
		`aria-label="Clear rating"`,
		`quick-rate-clear`,
		`<noscript>`,
		`Save rating`,
		`class="rating-chart"`,
		`class="card game-ratings"`,
		`src="https://images.igdb.com/igdb/image/upload/t_cover_big_2x/cover-panel.jpg"`,
		`class="cover"`,
		`>avg. rating<`,
		`>playing<`,
		`>backlog<`,
		`>wishlist<`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("game page missing %q", want)
		}
	}
	if strings.Contains(body, `class="card mt1 game-library-card"`) {
		t.Fatal("library panel still uses the old card wrapper")
	}
	libStart := strings.Index(body, `class="game-library-card"`)
	likeStart := strings.Index(body, `class="game-like"`)
	headStart := strings.Index(body, `class="library-card-head"`)
	if libStart < 0 || likeStart < libStart || headStart < likeStart {
		t.Fatal("interactive like control is not at the top of the library card")
	}
	statStart := strings.Index(body, `class="stat-strip"`)
	metaStart := strings.Index(body, `class="game-meta-cards"`)
	if statStart < 0 || metaStart < statStart {
		t.Fatal("stat strip is missing or out of order")
	}
	statHTML := body[statStart:metaStart]
	for _, want := range []string{`<strong>8.0</strong>`, `<strong>7</strong>`, `<strong>3</strong>`, `<strong>5</strong>`, `<strong>2</strong>`} {
		if !strings.Contains(statHTML, want) {
			t.Fatalf("stat strip missing %q", want)
		}
	}
	for _, old := range []string{"weighted rating", ">ratings<", ">reviews<", ">backlogged<", ">lists<", "class=\"game-like"} {
		if strings.Contains(statHTML, old) {
			t.Fatalf("stat strip still contains %q", old)
		}
	}

	summaryStart := strings.Index(body, "Summary below the hero.")
	infoStart := strings.Index(body, `class="game-info"`)
	ratingsStart := strings.Index(body, `class="card game-ratings"`)
	detailsStart := strings.Index(body, "<h2>Details</h2>")
	if !strings.Contains(body, `class="prose game-summary mt2"`) || summaryStart < 0 || infoStart < 0 || ratingsStart < 0 || detailsStart < 0 || ratingsStart < infoStart || summaryStart < ratingsStart || detailsStart < summaryStart {
		t.Fatal("ratings, summary, and details are not in the expected order")
	}
	ratingsEnd := strings.Index(body[ratingsStart:], "</section>")
	if ratingsEnd < 0 {
		t.Fatal("ratings card is not closed")
	}
	ratingsCard := body[ratingsStart : ratingsStart+ratingsEnd]
	if strings.Contains(ratingsCard, `class="pill"`) || strings.Contains(ratingsCard, "Played ") {
		t.Fatal("ratings card still shows shelf counts")
	}
	if got := strings.Count(ratingsCard, `class="rating-col"`); got != 10 {
		t.Fatalf("rating chart has %d bars, want 10 half-star bars", got)
	}
	if strings.Contains(ratingsCard, "rating-label") || strings.Contains(ratingsCard, "rating-count") || !strings.Contains(ratingsCard, `data-tip="4★ · 1 rating"`) || !strings.Contains(ratingsCard, `data-tip="4½★ · 0 ratings"`) {
		t.Fatal("rating chart should put labels and counts in hover tooltips only")
	}
	if strings.Contains(body, `<div class="grid grid-2 mt3">`) {
		t.Fatal("ratings and details still share the old lower grid")
	}
	if strings.Contains(body, `class="card game-times"`) {
		t.Fatal("game without time-to-beat data rendered a time card")
	}
	statusStart := strings.Index(body, `class="status-bar"`)
	if statusStart < 0 {
		t.Fatal("status bar missing")
	}
	statusEnd := strings.Index(body[statusStart:], "</div>")
	if statusEnd < 0 {
		t.Fatal("status bar closing tag missing")
	}
	statusHTML := body[statusStart : statusStart+statusEnd]
	for _, want := range []string{`class="played-pill"`, `value="playing"`, `value="backlog"`, `value="wishlist"`} {
		if !strings.Contains(statusHTML, want) {
			t.Fatalf("status bar missing %q", want)
		}
	}
	if played := strings.Index(body, `class="played-pill"`); played < statusStart {
		t.Fatal("played pill is outside the status bar")
	}

	code, guestBody := getBody(t, http.DefaultClient, ts.URL+"/games/cover-panel")
	if code != http.StatusOK || !strings.Contains(guestBody, `class="game-cover-panel"`) || !strings.Contains(guestBody, "Log in to track this game") {
		t.Fatalf("signed-out game page missing merged login panel (%d)", code)
	}
	guestLib := strings.Index(guestBody, `class="game-library-card"`)
	guestLikes := strings.Index(guestBody, `class="game-like like-count-only"`)
	if guestLib < 0 || guestLikes < guestLib {
		t.Fatal("signed-out like count is not in the library card")
	}
}

func TestCoverHelpers(t *testing.T) {
	one := &models.Game{CoverURL: "https://images.igdb.com/igdb/image/upload/t_cover_big/abc.jpg"}
	if got := coverURL(one); got != "https://images.igdb.com/igdb/image/upload/t_cover_big/abc.jpg" {
		t.Fatalf("1x cover = %q", got)
	}
	if got := cover2xURL(one); got != "https://images.igdb.com/igdb/image/upload/t_cover_big_2x/abc.jpg" {
		t.Fatalf("1x to 2x cover = %q", got)
	}
	two := &models.Game{CoverURL: "https://images.igdb.com/igdb/image/upload/t_cover_big_2x/abc.jpg"}
	if got := coverURL(two); got != "https://images.igdb.com/igdb/image/upload/t_cover_big/abc.jpg" {
		t.Fatalf("2x to 1x cover = %q", got)
	}
	if got := cover2xURL(two); got != two.CoverURL {
		t.Fatalf("2x cover changed = %q", got)
	}
	if got := coverSrcset(two); got != "https://images.igdb.com/igdb/image/upload/t_cover_big/abc.jpg 1x, https://images.igdb.com/igdb/image/upload/t_cover_big_2x/abc.jpg 2x" {
		t.Fatalf("srcset = %q", got)
	}
	placeholder := &models.Game{}
	if got := coverURL(placeholder); got != "/static/cover-placeholder.svg" {
		t.Fatalf("placeholder cover = %q", got)
	}
	if got := coverSrcset(placeholder); got != "" {
		t.Fatalf("placeholder srcset = %q", got)
	}
	if got := coverURL(nil); got != "/static/cover-placeholder.svg" {
		t.Fatalf("nil cover = %q", got)
	}
}

func TestGameTimeToBeatWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	_, err := st.SaveGame(ctx, &models.Game{
		IGDBID: -405, Slug: "time-to-beat", Title: "Time To Beat", ReleaseStatus: "released",
		TimeToBeat: &models.TimeToBeat{Hastily: 19800, Normally: 28800, Completely: 54000},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, body := getBody(t, http.DefaultClient, ts.URL+"/games/time-to-beat")
	if code != http.StatusOK {
		t.Fatalf("game page = %d", code)
	}
	for _, want := range []string{
		`class="game-meta-cards"`,
		`class="card game-times"`,
		`Main story`, `>5h 30m<`,
		`Main + extras`, `>8h<`,
		`Completionist`, `>15h<`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("game page missing %q", want)
		}
	}
	metaStart := strings.Index(body, `class="game-meta-cards"`)
	ratingsStart := strings.Index(body, `class="card game-ratings"`)
	timesStart := strings.Index(body, `class="card game-times"`)
	if ratingsStart < metaStart || timesStart < ratingsStart {
		t.Fatal("ratings and time cards are not in the expected order")
	}
}

func TestGameCreditsWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	_, err := st.SaveGame(ctx, &models.Game{
		IGDBID: -403, Slug: "credits", Title: "Credits", ReleaseStatus: "released",
		ReleaseDate: "2024-05-02",
		Companies: []models.CompanyRole{
			{Name: "North Studio", Role: "developer"},
			{Name: "East Studio", Role: "developer"},
			{Name: "South Publishing", Role: "publisher"},
			{Name: "East Studio", Role: "publisher"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, body := getBody(t, http.DefaultClient, ts.URL+"/games/credits")
	if code != http.StatusOK {
		t.Fatalf("game page = %d", code)
	}
	titleStart := strings.Index(body, "<h1>Credits</h1>")
	heroStart := strings.Index(body, `class="game-hero"`)
	devStart := strings.Index(body, "North Studio")
	pubStart := strings.Index(body, "South Publishing")
	if devStart < 0 || pubStart < 0 || titleStart < 0 || heroStart < 0 || devStart < titleStart || pubStart < titleStart || devStart > heroStart || pubStart > heroStart {
		t.Fatal("company credits are not below the title and above the hero")
	}
	if !strings.Contains(body, `class="game-credits"`) {
		t.Fatal("company credits block missing")
	}
	if strings.Count(body, `class="role">By<`) != 1 {
		t.Fatal("credits should render a single By label")
	}
	if !strings.Contains(body, "East Studio, North Studio, South Publishing") || strings.Count(body, "East Studio") != 1 {
		t.Fatal("credits are not in developer-then-publisher order with duplicates removed")
	}
	if strings.Contains(body, `class="role">developer<`) || strings.Contains(body, `class="role">publisher<`) {
		t.Fatal("credits still render developer/publisher role labels")
	}
	detailsStart := strings.Index(body, "<h2>Details</h2>")
	if detailsStart < 0 {
		t.Fatal("details section missing")
	}
	detailsEnd := strings.Index(body[detailsStart:], "</dl>")
	if detailsEnd < 0 {
		t.Fatal("details list missing")
	}
	detailsHTML := body[detailsStart : detailsStart+detailsEnd]
	if strings.Contains(detailsHTML, "North Studio") || strings.Contains(detailsHTML, "South Publishing") {
		t.Fatal("company credits are still duplicated inside Details")
	}

	_, err = st.SaveGame(ctx, &models.Game{IGDBID: -404, Slug: "no-credits", Title: "No Credits", ReleaseStatus: "released"})
	if err != nil {
		t.Fatal(err)
	}
	_, body = getBody(t, http.DefaultClient, ts.URL+"/games/no-credits")
	if strings.Contains(body, `class="game-credits"`) {
		t.Fatal("game without companies rendered an empty credits block")
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
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: games[0].ID, Rating: 9, Review: "A favourite review", Status: "played"}); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, alice)
	plain := noRedirectClient(client)

	signedOut := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if code, _ := getBody(t, signedOut, ts.URL+"/settings?tab=favourites"); code != http.StatusSeeOther {
		t.Fatalf("signed-out favourites settings = %d", code)
	}
	resp, err := plain.Get(ts.URL + "/settings?tab=favourites")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/settings#favourites" {
		t.Fatalf("legacy favourites redirect = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	code, body := getBody(t, client, ts.URL+"/settings")
	if code != 200 || !strings.Contains(body, `id="favourites"`) || !strings.Contains(body, "Favourite games") || !strings.Contains(body, `action="/settings#favourites"`) {
		t.Fatalf("favourites card missing from profile settings (%d)", code)
	}
	if strings.Contains(body, `href="/settings?tab=favourites"`) {
		t.Fatal("favourites settings nav link still present")
	}
	if _, accountBody := getBody(t, client, ts.URL+"/settings?tab=account"); strings.Contains(accountBody, `id="favourites"`) {
		t.Fatal("favourites card leaked onto another settings tab")
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
	favPos := strings.Index(profile, "<h2>Favourite games</h2>")
	reviewPos := strings.Index(profile, "<h2>Recent reviews</h2>")
	journalPos := strings.Index(profile, "<h2>Journal</h2>")
	if favPos < 0 || reviewPos < 0 || journalPos < 0 || favPos > reviewPos || favPos > journalPos {
		t.Fatal("favourite games are not the first profile overview section")
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
