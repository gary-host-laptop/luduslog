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
	"time"

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
	if got := st.RatingForUser(ctx, alice.ID, game.ID); got != 6 {
		t.Fatalf("central rating = %v, want 6", got)
	}
	if _, err := st.LatestLogFor(ctx, alice.ID, game.ID); err == nil {
		t.Fatal("quick rating created a log")
	}
	status, _ := st.StatusFor(ctx, alice.ID, game.ID)
	if status.Played != 0 || status.Shelf != "" {
		t.Fatalf("rating changed the game status: %+v", status)
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
	if got := st.RatingForUser(ctx, alice.ID, game.ID); got != 0 {
		t.Fatalf("rating after clear = %v, want 0", got)
	}
	if _, err := st.LatestLogFor(ctx, alice.ID, game.ID); err == nil {
		t.Fatal("clearing a rating created a log")
	}
	status, _ = st.StatusFor(ctx, alice.ID, game.ID)
	if status.Played != 0 || status.Shelf != "" {
		t.Fatalf("clearing a rating changed the game status: %+v", status)
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
	if err := st.SetShelf(ctx, alice.ID, game.ID, "played"); err != nil {
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
		`name="shelf" value="playing"`,
		`name="shelf" value="played"`,
		`name="shelf" value="backlog"`,
		`name="shelf" value="wishlist"`,
		`title="Remove from shelf"`,
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
	ratingStart := strings.Index(body, `class="game-user-rating"`)
	logStart := strings.Index(body, `class="game-log-actions"`)
	heroStatusStart := strings.Index(body, `class="status-bar"`)
	if libStart < 0 || likeStart < libStart || ratingStart < likeStart || logStart < ratingStart || heroStatusStart < logStart {
		t.Fatal("library card should order like, stars, log actions, then play status")
	}
	if strings.Contains(body, "In your library") {
		t.Fatal("library card still shows the 'In your library' label")
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
	if strings.Contains(body, `class="time-blocks"`) {
		t.Fatal("game without time-to-beat data rendered time cards")
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
		`class="time-blocks"`,
		`class="time-part"`, `>5H</strong>`, `>30M</strong>`,
		`Main story`, `>5h 30m<`,
		`Main + extras`, `>8h<`,
		`Completionist`, `>15h<`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("game page missing %q", want)
		}
	}
	if strings.Contains(body, `class="card game-times"`) || strings.Contains(body, `<h2>Time to beat</h2>`) {
		t.Fatal("time blocks still render inside a wrapper card")
	}
	if got := strings.Count(body, `class="time-block"`); got != 3 {
		t.Fatalf("game page has %d time blocks, want 3", got)
	}
	metaStart := strings.Index(body, `class="game-meta-cards"`)
	ratingsStart := strings.Index(body, `class="card game-ratings"`)
	timesStart := strings.Index(body, `class="time-blocks"`)
	if ratingsStart < metaStart || timesStart < ratingsStart {
		t.Fatal("ratings and time blocks are not in the expected order")
	}
}

func TestGameReleaseDisplayWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	_, err := st.SaveGame(ctx, &models.Game{
		IGDBID: -406, Slug: "release-display", Title: "Release Display", ReleaseStatus: "released",
		ReleaseDate: "2024-05-02", Franchise: "release display", Category: "main_game",
		ReleaseDates: []models.ReleaseDate{{Platform: "PC", Date: "2024-05-02", Region: "Europe", Status: "released"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, body := getBody(t, http.DefaultClient, ts.URL+"/games/release-display")
	if code != http.StatusOK {
		t.Fatalf("game page = %d", code)
	}
	if !strings.Contains(body, "Released on 2024/05/02") {
		t.Fatal("release line does not lead with Released on")
	}
	if strings.Contains(body, "main_game") {
		t.Fatal("release line still renders the game category")
	}
	if strings.Contains(body, "2024-05-02") {
		t.Fatal("release dates still use dashes")
	}
	if strings.Contains(body, "franchise=") {
		t.Fatal("release line repeats the game title as a franchise")
	}
	if !strings.Contains(body, "PC · 2024/05/02 · Europe · released") {
		t.Fatal("details release row is missing platform, date, region or status")
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
	if err := st.SetOverviewWidgets(ctx, alice.ID, "favourites", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	_, profile := getBody(t, client, ts.URL+"/users/alice")
	if !strings.Contains(profile, "Favourite games") || !strings.Contains(profile, "Fave 1") {
		t.Fatal("profile overview did not show favourites")
	}
	favPos := strings.Index(profile, "<h2>Favourite games</h2>")
	activityPos := strings.Index(profile, "<h2>Activity</h2>")
	if favPos < 0 || activityPos < 0 || favPos > activityPos {
		t.Fatal("favourite games are not the first profile overview section")
	}
	for _, removed := range []string{"<h2>Recent reviews</h2>", "<h2>Journal</h2>", "<h2>Lists</h2>", "<h2>Friends</h2>"} {
		if strings.Contains(profile, removed) {
			t.Fatalf("profile overview still contains %s", removed)
		}
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

func TestProfileOverviewShelves(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "shelfweb", "shelfweb@example.test", "hash", false)
	shelves := []struct {
		label, key string
	}{
		{"Played", "played"},
		{"Playing", "playing"},
		{"Backlog", "backlog"},
	}
	for sIndex, shelf := range shelves {
		for i := 1; i <= 4; i++ {
			game, _ := st.SaveGame(ctx, &models.Game{
				IGDBID: int64(-700 - sIndex*10 - i),
				Slug:   strings.ToLower(shelf.label) + "-shelf-" + strconv.Itoa(i),
				Title:  shelf.label + " " + strconv.Itoa(i), ReleaseStatus: "released",
			})
			if err := st.SetShelf(ctx, alice.ID, game.ID, shelf.key); err != nil {
				t.Fatal(err)
			}
			eventAt := "2026-0" + strconv.Itoa(sIndex+1) + "-0" + strconv.Itoa(i) + "T12:00:00Z"
			if _, err := st.DB.ExecContext(ctx, `UPDATE statuses SET shelf_at=?,updated_at=? WHERE user_id=? AND game_id=?`,
				eventAt, eventAt, alice.ID, game.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	wishlist, _ := st.SaveGame(ctx, &models.Game{IGDBID: -799, Slug: "shelf-wishlist", Title: "Shelf Wishlist", ReleaseStatus: "released"})
	if err := st.SetShelf(ctx, alice.ID, wishlist.ID, "wishlist"); err != nil {
		t.Fatal(err)
	}

	_, body := getBody(t, http.DefaultClient, ts.URL+"/users/shelfweb")
	for _, shelf := range shelves {
		start := strings.Index(body, "<h2>"+shelf.label+"</h2>")
		if start < 0 {
			t.Fatalf("profile overview is missing the %s shelf", shelf.label)
		}
		end := strings.Index(body[start:], "</section>")
		if end < 0 {
			t.Fatalf("%s shelf section was not closed", shelf.label)
		}
		section := body[start : start+end]
		if !strings.Contains(section, "4 games") {
			t.Errorf("%s shelf missing its total count", shelf.label)
		}
		for i := 4; i >= 2; i-- {
			if !strings.Contains(section, shelf.label+" "+strconv.Itoa(i)) {
				t.Errorf("%s shelf missing its latest game %d", shelf.label, i)
			}
		}
		if strings.Contains(section, shelf.label+" 1") {
			t.Errorf("%s shelf included a fourth game", shelf.label)
		}
		if i4, i3 := strings.Index(section, shelf.label+" 4"), strings.Index(section, shelf.label+" 3"); i4 < 0 || i4 > i3 {
			t.Errorf("%s shelf games are not ordered newest first", shelf.label)
		}
	}
	headerStart := strings.Index(body, `class="profile-stats`)
	if headerStart < 0 {
		t.Fatal("profile stats block missing")
	}
	headerEnd := strings.Index(body[headerStart:], "</div>")
	if headerEnd < 0 {
		t.Fatal("profile stats block was not closed")
	}
	header := body[headerStart : headerStart+headerEnd]
	for _, removed := range []string{"played", "backlog", "wishlist", "this year", "reviews"} {
		if strings.Contains(header, removed) {
			t.Errorf("profile header still contains the %s counter", removed)
		}
	}
	for _, want := range []string{"profile-playtime", "playtime", "followers", "following"} {
		if !strings.Contains(header, want) {
			t.Errorf("profile header missing %q", want)
		}
	}
	for _, removed := range []string{"<h2>Journal</h2>", "<h2>Lists</h2>", "<h2>Friends</h2>"} {
		if strings.Contains(body, removed) {
			t.Fatalf("profile overview still contains %s", removed)
		}
	}
}

func TestProfileStatsWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "statsalice", "statsalice@example.test", "hash", false)
	played, _ := st.SaveGame(ctx, &models.Game{IGDBID: -741, Slug: "stats-played", Title: "Stats Played", ReleaseStatus: "released"})
	old, _ := st.SaveGame(ctx, &models.Game{IGDBID: -742, Slug: "stats-old", Title: "Stats Old", ReleaseStatus: "released"})
	backlog, _ := st.SaveGame(ctx, &models.Game{IGDBID: -743, Slug: "stats-backlog-web", Title: "Stats Backlog Web", ReleaseStatus: "released"})
	wishlist, _ := st.SaveGame(ctx, &models.Game{IGDBID: -744, Slug: "stats-wishlist-web", Title: "Stats Wishlist Web", ReleaseStatus: "released"})
	st.SetPlayedCount(ctx, alice.ID, played.ID, 1)
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: played.ID, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: played.ID, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	oldLog, _ := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: old.ID, Status: "played"})
	st.DB.ExecContext(ctx, `UPDATE logs SET created_at=? WHERE id=?`, strconv.Itoa(time.Now().UTC().Year()-1)+"-04-01T12:00:00Z", oldLog.ID)
	st.SetStatusFlag(ctx, alice.ID, backlog.ID, "backlog", true)
	st.SetStatusFlag(ctx, alice.ID, wishlist.ID, "wishlist", true)

	code, body := getBody(t, http.DefaultClient, ts.URL+"/users/statsalice")
	if code != http.StatusOK {
		t.Fatalf("profile = %d", code)
	}
	for _, want := range []string{"playtime", "<h2>Played</h2>", "<h2>Backlog</h2>"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile stats missing %q", want)
		}
	}
	if strings.Contains(body, "wishlist") || strings.Contains(body, "<strong>1</strong> backlog") {
		t.Error("profile still shows removed shelf counters")
	}
	_, stats := getBody(t, http.DefaultClient, ts.URL+"/users/statsalice/stats")
	if !strings.Contains(stats, "1 games played this year") {
		t.Error("stats page did not use the distinct year count")
	}

	st.UpdateProfile(ctx, alice.ID, store.ProfileUpdate{PrivateProfile: true})
	_, private := getBody(t, http.DefaultClient, ts.URL+"/users/statsalice")
	if strings.Contains(private, "this year") || strings.Contains(private, "playtime") {
		t.Error("private profile leaked stats")
	}
	if code, _ := getBody(t, http.DefaultClient, ts.URL+"/users/statsalice/stats"); code != http.StatusNotFound {
		t.Fatalf("private stats page = %d, want 404", code)
	}
}

func TestProfileYearlyGoalWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "goalalice", "goalalice@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -745, Slug: "yearly-goal-game", Title: "Yearly Goal Game", ReleaseStatus: "released"})
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, alice)
	plain := noRedirectClient(client)

	if got := postStatus(t, plain, ts.URL+"/settings/preferences", url.Values{"_csrf": {sess.CSRF}, "yearly_goal": {"4"}}); got != http.StatusSeeOther {
		t.Fatalf("save yearly goal = %d", got)
	}
	if updated, _ := st.UserByID(ctx, alice.ID); updated.YearlyGoal != 4 {
		t.Fatalf("yearly goal = %d, want 4", updated.YearlyGoal)
	}
	_, settings := getBody(t, client, ts.URL+"/settings?tab=preferences")
	if !strings.Contains(settings, `id="yearly_goal" type="number" name="yearly_goal" min="0" max="10000" value="4"`) {
		t.Error("preferences did not render the saved yearly goal")
	}

	code, body := getBody(t, http.DefaultClient, ts.URL+"/users/goalalice")
	if code != http.StatusOK {
		t.Fatalf("profile = %d", code)
	}
	for _, want := range []string{
		`<h2 id="yearly-goal-title">Yearly goal</h2>`,
		"1 of 4 games",
		`<strong class="yearly-goal-percent">25%</strong>`,
		`style="width:25%"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("profile goal missing %q", want)
		}
	}
	goalAt, shelvesAt := strings.Index(body, `class="yearly-goal"`), strings.Index(body, `class="shelf-groups"`)
	if goalAt < 0 || shelvesAt < 0 || goalAt > shelvesAt {
		t.Error("yearly goal is not rendered above the shelf sections")
	}

	if got := postStatus(t, plain, ts.URL+"/settings/preferences", url.Values{"_csrf": {sess.CSRF}, "yearly_goal": {"many"}}); got != http.StatusSeeOther {
		t.Fatalf("invalid yearly goal = %d", got)
	}
	if updated, _ := st.UserByID(ctx, alice.ID); updated.YearlyGoal != 4 {
		t.Fatalf("invalid yearly goal overwrote the saved value: %d", updated.YearlyGoal)
	}
}

func TestProfilePolishAndInlineTrophies(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "polishalice", "polishalice@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -711, Slug: "polish-game", Title: "Polish Game", ReleaseStatus: "released"})
	log, _ := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Rating: 9, Review: "A polished review", Status: "played"})
	oldTime := time.Now().UTC().Add(-25 * time.Hour)
	if _, err := st.DB.ExecContext(ctx, `UPDATE users SET created_at=?,timezone='Europe/London' WHERE id=?`, oldTime.Format(time.RFC3339), alice.ID); err != nil {
		t.Fatal(err)
	}
	st.CheckBadges(ctx, alice.ID)
	if err := st.SetFeaturedBadge(ctx, alice.ID, "n00b"); err != nil {
		t.Fatal(err)
	}
	client, _ := authedClient(t, ts, st, alice)

	_, body := getBody(t, client, ts.URL+"/users/polishalice")
	for _, want := range []string{"Joined " + oldTime.Format("2006/01/02"), `class="profile-trophy-card"`, `class="timeline-item timeline-review"`} {
		if !strings.Contains(body, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	for _, unwanted := range []string{"Europe/London", "Edit profile", `class="profile-actions"`, `<span class="tabs-label">Trophy</span>`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("profile still contains %q", unwanted)
		}
	}
	_, trophyTab := getBody(t, client, ts.URL+"/users/polishalice?tab=trophies")
	if !strings.Contains(trophyTab, `href="/users/polishalice?tab=trophies" class="on"`) {
		t.Error("trophies tab link is not active on the trophies tab")
	}

	code, inline := getBody(t, client, ts.URL+"/users/polishalice/trophies")
	if code != http.StatusOK || !strings.Contains(inline, "3 of 11 earned") || !strings.Contains(inline, `class="trophy-card`) {
		t.Fatalf("legacy trophies route did not render inline (%d)", code)
	}
	if strings.Contains(inline, "Trophies of polishalice") {
		t.Error("legacy trophies page still renders separately")
	}
	_, review := getBody(t, client, ts.URL+"/users/polishalice/reviews/"+itoa(log.ID))
	if !strings.Contains(review, `review-author byline-author`) {
		t.Error("review page byline lacks shared alignment class")
	}
}

func TestOverviewWidgetsWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "widgetalice", "widgetalice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "widgetbob", "widgetbob@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -721, Slug: "widget-game", Title: "Widget Game", ReleaseStatus: "released"})
	if err := st.AddFavoriteGame(ctx, alice.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	review, _ := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Name: "Featured thoughts", Rating: 9, Review: "Featured body text shown as a compact excerpt.", Playtime: 24.5, Status: "played"})
	bobReview, _ := st.CreateLog(ctx, store.LogInput{UserID: bob.ID, GameID: game.ID, Name: "Bob thoughts", Rating: 7, Review: "Bob body", Status: "played"})
	list, _ := st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "Widget List", Privacy: "public"})
	st.AddListEntry(ctx, list.ID, game.ID, "")
	private, _ := st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "Private Widget List", Description: "Games saved for a rainy weekend.", Privacy: "private"})

	client, sess := authedClient(t, ts, st, alice)
	plain := noRedirectClient(client)
	_, body := getBody(t, client, ts.URL+"/users/widgetalice")
	if strings.Contains(body, `class="overview-widgets"`) || strings.Contains(body, "<h2>Favourite games</h2>") {
		t.Error("default Overview rendered a widget row")
	}
	_, settings := getBody(t, client, ts.URL+"/settings")
	for _, want := range []string{`id="overview-widgets"`, `action="/settings/overview-widgets"`, `name="overview_widget_1"`, `name="featured_review_id"`, "Widget List"} {
		if !strings.Contains(settings, want) {
			t.Errorf("widget settings missing %q", want)
		}
	}

	if got := postStatus(t, plain, ts.URL+"/settings/overview-widgets", url.Values{
		"_csrf": {sess.CSRF}, "overview_widget_1": {"favourites"}, "overview_widget_2": {"review"}, "featured_review_id": {itoa(review.ID)},
	}); got != http.StatusSeeOther {
		t.Fatalf("save widgets = %d", got)
	}
	_, body = getBody(t, client, ts.URL+"/users/widgetalice")
	favPos := strings.Index(body, "<h2>Favourite games</h2>")
	reviewPos := strings.Index(body, "<h2>Highlighted review</h2>")
	widgetsBody := body
	for _, marker := range []string{"<h2>Played</h2>", "<h2>Playing</h2>", "<h2>Backlog</h2>", "<h2>Recent reviews</h2>"} {
		if i := strings.Index(widgetsBody, marker); i >= 0 {
			widgetsBody = widgetsBody[:i]
		}
	}
	if favPos < 0 || reviewPos < 0 || favPos > reviewPos || !strings.Contains(widgetsBody, `class="overview-widgets"`) {
		t.Error("ordered favourite/review widgets did not render")
	}
	if !strings.Contains(widgetsBody, "Featured thoughts") || !strings.Contains(widgetsBody, "24.5h") || !strings.Contains(widgetsBody, "played") {
		t.Error("highlighted review is missing its title or playtime")
	}
	if !strings.Contains(widgetsBody, "Featured body text shown as a compact excerpt.") {
		t.Error("highlighted review is missing its body excerpt")
	}
	if strings.Contains(widgetsBody, "Read review") {
		t.Error("highlighted review still renders an action row")
	}

	// Duplicate and foreign content are rejected without changing the stored config.
	for _, tc := range []struct{ w1, w2, reviewID string }{
		{"favourites", "favourites", ""},
		{"review", "", itoa(bobReview.ID)},
		{"nonsense", "", ""},
	} {
		if got := postStatus(t, plain, ts.URL+"/settings/overview-widgets", url.Values{
			"_csrf": {sess.CSRF}, "overview_widget_1": {tc.w1}, "overview_widget_2": {tc.w2}, "featured_review_id": {tc.reviewID},
		}); got != http.StatusSeeOther {
			t.Fatalf("invalid widget post = %d", got)
		}
	}
	updated, _ := st.UserByID(ctx, alice.ID)
	if updated.OverviewWidget1 != "favourites" || updated.OverviewWidget2 != "review" {
		t.Fatalf("invalid widget post changed config: %+v", updated)
	}

	// Adult highlighted reviews stay hidden until the viewer opts in.
	adultGame, _ := st.SaveGame(ctx, &models.Game{IGDBID: -722, Slug: "widget-adult", Title: "Widget Adult", Adult: true, ReleaseStatus: "released"})
	adultReview, _ := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: adultGame.ID, Name: "Adult thoughts", Review: "Adult body", Status: "played"})
	if got := postStatus(t, plain, ts.URL+"/settings/overview-widgets", url.Values{
		"_csrf": {sess.CSRF}, "overview_widget_1": {"review"}, "featured_review_id": {itoa(adultReview.ID)},
	}); got != http.StatusSeeOther {
		t.Fatalf("save adult review widget = %d", got)
	}
	_, adultHidden := getBody(t, client, ts.URL+"/users/widgetalice")
	if strings.Contains(adultHidden, "<h2>Highlighted review</h2>") {
		t.Error("adult highlighted review bypassed the age gate")
	}
	st.DB.ExecContext(ctx, `UPDATE users SET adult_optin=1 WHERE id=?`, alice.ID)
	_, adultVisible := getBody(t, client, ts.URL+"/users/widgetalice")
	if !strings.Contains(adultVisible, "<h2>Highlighted review</h2>") || !strings.Contains(adultVisible, "Adult thoughts") {
		t.Error("opted-in owner cannot see their adult highlighted review")
	}
	st.DB.ExecContext(ctx, `UPDATE users SET adult_optin=0 WHERE id=?`, alice.ID)

	adultList, _ := st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "Adult Widget List", Privacy: "public"})
	st.AddListEntry(ctx, adultList.ID, adultGame.ID, "")
	if got := postStatus(t, plain, ts.URL+"/settings/overview-widgets", url.Values{
		"_csrf": {sess.CSRF}, "overview_widget_1": {"list"}, "featured_list_id": {itoa(adultList.ID)},
	}); got != http.StatusSeeOther {
		t.Fatalf("save adult list widget = %d", got)
	}
	_, adultListHidden := getBody(t, client, ts.URL+"/users/widgetalice")
	if strings.Contains(adultListHidden, "Adult Widget List") || strings.Contains(adultListHidden, `class="overview-widgets"`) {
		t.Error("adult highlighted list bypassed the age gate")
	}
	st.DB.ExecContext(ctx, `UPDATE users SET adult_optin=1 WHERE id=?`, alice.ID)
	_, adultListVisible := getBody(t, client, ts.URL+"/users/widgetalice")
	if !strings.Contains(adultListVisible, "Adult Widget List") {
		t.Error("opted-in owner cannot see their adult highlighted list")
	}
	if !strings.Contains(adultListVisible, `class="list-card list-card-widget"`) {
		t.Error("highlighted list widget did not use the enlarged list card")
	}
	st.DB.ExecContext(ctx, `UPDATE users SET adult_optin=0 WHERE id=?`, alice.ID)

	// A private list is visible to its owner but omitted for a stranger.
	if got := postStatus(t, plain, ts.URL+"/settings/overview-widgets", url.Values{
		"_csrf": {sess.CSRF}, "overview_widget_1": {"list"}, "featured_list_id": {itoa(private.ID)},
	}); got != http.StatusSeeOther {
		t.Fatalf("save private list widget = %d", got)
	}
	_, ownerBody := getBody(t, client, ts.URL+"/users/widgetalice")
	if !strings.Contains(ownerBody, "Private Widget List") || !strings.Contains(ownerBody, `class="list-widget-stats"`) {
		t.Error("owner cannot see a selected private list and its counters")
	}
	if strings.Contains(ownerBody, "Games saved for a rainy weekend.") {
		t.Error("highlighted list widget still renders the list description")
	}
	bobClient, _ := authedClient(t, ts, st, bob)
	_, strangerBody := getBody(t, bobClient, ts.URL+"/users/widgetalice")
	if strings.Contains(strangerBody, `class="overview-widgets"`) || strings.Contains(strangerBody, "Private Widget List") {
		t.Error("private list widget leaked to a stranger")
	}

	// Deleted selections disappear cleanly.
	if err := st.DeleteList(ctx, private.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	_, deletedBody := getBody(t, client, ts.URL+"/users/widgetalice")
	if strings.Contains(deletedBody, `class="overview-widgets"`) || strings.Contains(deletedBody, "Private Widget List") {
		t.Error("deleted list widget still rendered")
	}

	if got := postStatus(t, plain, ts.URL+"/settings/overview-widgets", url.Values{
		"_csrf": {sess.CSRF}, "overview_widget_1": {""}, "overview_widget_2": {""},
	}); got != http.StatusSeeOther {
		t.Fatalf("clear widgets = %d", got)
	}
	updated, _ = st.UserByID(ctx, alice.ID)
	if updated.OverviewWidget1 != "" || updated.OverviewWidget2 != "" || updated.FeaturedReviewID != nil || updated.FeaturedListID != nil {
		t.Fatalf("widgets not cleared: %+v", updated)
	}
}

func TestListCoverCardsWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "coverweb", "coverweb@example.test", "hash", false)
	list, _ := st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "Stack Web", Privacy: "public"})
	for i := 0; i < 6; i++ {
		game, _ := st.SaveGame(ctx, &models.Game{IGDBID: int64(-731 - i), Slug: "stack-game-" + strconv.Itoa(i), Title: "Stack Game " + strconv.Itoa(i), ReleaseStatus: "released"})
		st.AddListEntry(ctx, list.ID, game.ID, "")
		st.DB.ExecContext(ctx, `UPDATE list_entries SET added_at=? WHERE list_id=? AND game_id=?`, "2026-02-0"+strconv.Itoa(i+1)+"T12:00:00Z", list.ID, game.ID)
	}
	for _, path := range []string{
		"/users/coverweb?tab=lists",
		"/lists",
		"/",
		"/search?q=Stack&tab=lists",
		"/games/stack-game-0",
	} {
		code, body := getBody(t, http.DefaultClient, ts.URL+path)
		if code != http.StatusOK || !strings.Contains(body, "Stack Web") {
			t.Fatalf("%s missing list card (%d)", path, code)
		}
		linkAt := strings.Index(body, `href="/users/coverweb/lists/stack-web"`)
		if linkAt < 0 {
			t.Fatalf("%s missing the list card link", path)
		}
		var card string
		if path == "/games/stack-game-0" {
			start := strings.LastIndex(body[:linkAt], "<a ")
			if start < 0 {
				t.Fatalf("%s missing the list card anchor", path)
			}
			card = body[start:]
			if end := strings.Index(card, "</a>"); end >= 0 {
				card = card[:end]
			}
		} else {
			start := strings.LastIndex(body[:linkAt], `<div class="list-card"`)
			if start < 0 {
				t.Fatalf("%s missing the list card wrapper", path)
			}
			card = body[start:]
			tagsAt := strings.Index(card, `class="list-card-tags`)
			if tagsAt < 0 {
				t.Fatalf("%s missing the list card tag row", path)
			}
			if end := strings.Index(card[tagsAt:], "</div>"); end >= 0 {
				card = card[:tagsAt+end]
			}
		}
		if !strings.Contains(card, `class="list-covers"`) {
			t.Errorf("%s missing overlapping list covers", path)
		}
		coverPart := strings.Split(card, `class="list-card-meta`)[0]
		if got := strings.Count(coverPart, "<img "); got != 5 {
			t.Errorf("%s rendered %d covers, want 5", path, got)
		}
		if !strings.Contains(card, "6 games") {
			t.Errorf("%s missing correct list count", path)
		}
		if path == "/games/stack-game-0" && !strings.Contains(card, `class="list-card list-card-compact"`) {
			t.Error("game-page list card did not use the compact cover size")
		}
	}
}

func TestListCoverStackCSS(t *testing.T) {
	css, err := os.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".list-card { --cover-w: 120px; --cover-h: 160px;",
		".list-card-compact { --cover-w: 48px; --cover-h: 64px;",
		".list-card-widget { --cover-w: calc((100cqi - 4 * .7rem) / 5);",
		".list-card-widget .list-covers img { height: auto; aspect-ratio: 3 / 4; }",
		".overview-widget .fav-grid { grid-template-columns: repeat(5, minmax(0, 1fr)); }",
		".overview-widget .list-card { display: flex; flex: 1; width: 100%; padding: 0; border: 0; background: none; align-items: center; }",
		".overview-review-head { display: flex; flex: 1; align-items: flex-start; gap: .75rem; }",
		".overview-review-head .game-cover { width: calc((100cqi - 4 * .7rem) / 5); flex: none; }",
		".overview-review-copy { display: flex; flex: 1; flex-direction: column; align-self: stretch; min-width: 0; }",
		".overview-review-excerpt { flex: 1; min-width: 0; overflow: hidden; color: var(--muted); font-size: .85rem; line-height: 1.4; overflow-wrap: anywhere; display: -webkit-box; -webkit-line-clamp: 6; -webkit-box-orient: vertical; }",
		".overview-review-stat { flex: none; align-self: flex-end; margin-top: auto;",
		".overview-review-stat strong { color: var(--text); font-size: 1.5rem; }",
		"margin-left: calc(-0.5 * var(--cover-w))",
		".list-covers img:nth-child(1) { z-index: 5; }",
		".list-covers img:nth-child(5) { z-index: 1; }",
	} {
		if !strings.Contains(string(css), want) {
			t.Errorf("list cover CSS missing %q", want)
		}
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

func TestTimelineItemRendersMissingUser(t *testing.T) {
	ts, _ := testServer(t)
	srv, ok := ts.Config.Handler.(*Server)
	if !ok {
		t.Fatal("test server handler is not *Server")
	}
	var buf bytes.Buffer
	if err := srv.pages["home"].ExecuteTemplate(&buf, "timeline_item", models.TimelineItem{Kind: "log"}); err != nil {
		t.Fatalf("timeline item with a missing user: %v", err)
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

func TestReviewListingControls(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "reviewalice", "reviewalice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "reviewbob", "reviewbob@example.test", "hash", false)
	carol, _ := st.CreateUser(ctx, "reviewcarol", "reviewcarol@example.test", "hash", false)
	st.Follow(ctx, alice.ID, bob.ID)
	st.Follow(ctx, bob.ID, alice.ID)
	st.Follow(ctx, alice.ID, carol.ID)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -503, Slug: "review-feed", Title: "Review Feed", ReleaseStatus: "released"})
	for i := 0; i < 21; i++ {
		if _, err := st.CreateLog(ctx, store.LogInput{UserID: bob.ID, GameID: game.ID, Review: "bob review " + strconv.Itoa(i), Rating: float64(i%10 + 1), Status: "played"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: carol.ID, GameID: game.ID, Review: "carol review", Rating: 7, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	client, _ := authedClient(t, ts, st, alice)

	code, body := getBody(t, client, ts.URL+"/reviews?tab=friends&sort=rating")
	if code != http.StatusOK {
		t.Fatalf("global reviews = %d", code)
	}
	for _, want := range []string{
		`href="/reviews?tab=all&amp;sort=rating&amp;dir=desc"`,
		`href="/reviews?tab=friends&amp;sort=rating&amp;dir=desc"`,
		`href="/reviews?tab=following&amp;sort=rating&amp;dir=desc"`,
		`href="/reviews?tab=friends&amp;sort=popularity&amp;dir=desc"`,
		`href="/reviews?tab=friends&amp;sort=rating&amp;dir=asc"`,
		`class="sort-btn on"`,
		"reviewbob",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("global reviews missing %q", want)
		}
	}
	if strings.Contains(body, "reviewcarol") {
		t.Error("friends tab included a non-mutual follow")
	}

	code, body = getBody(t, client, ts.URL+"/games/review-feed/reviews?tab=following&sort=oldest")
	if code != http.StatusOK {
		t.Fatalf("game reviews = %d", code)
	}
	for _, want := range []string{
		`href="/games/review-feed/reviews?tab=all&amp;sort=date&amp;dir=asc"`,
		`href="/games/review-feed/reviews?tab=following&amp;sort=date&amp;dir=asc"`,
		`href="/games/review-feed/reviews?tab=following&amp;sort=date&amp;dir=desc"`,
		"reviewbob",
		"page=2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("game reviews missing %q", want)
		}
	}

	code, body = getBody(t, client, ts.URL+"/games/review-feed/reviews?tab=following&sort=oldest&page=2")
	if code != http.StatusOK || !strings.Contains(body, "reviewcarol") {
		t.Fatalf("game reviews page 2 = %d: %s", code, body[:min(len(body), 500)])
	}
	if strings.Contains(body, "tabs-sort") || !strings.Contains(body, `tabs tabs-reviews`) || !strings.Contains(body, `class="review-sort"`) {
		t.Fatal("review sorting is not merged into the tab row")
	}

	// Defaults: global date descending, per-game popularity descending.
	_, globalDefault := getBody(t, client, ts.URL+"/reviews")
	if !strings.Contains(globalDefault, "sort=date&amp;dir=desc") {
		t.Error("global reviews did not default to date descending")
	}
	_, gameDefault := getBody(t, client, ts.URL+"/games/review-feed/reviews")
	if !strings.Contains(gameDefault, "sort=popularity&amp;dir=desc") {
		t.Error("game reviews did not default to popularity descending")
	}

	_, profile := getBody(t, client, ts.URL+"/users/reviewalice?tab=reviews")
	if strings.Contains(profile, "tabs-sort") || strings.Contains(profile, "sort=rating") {
		t.Error("profile reviews gained listing controls")
	}
}

func TestTrophyHighlightWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "trophyalice", "trophyalice@example.test", "hash", false)
	old := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	if _, err := st.DB.ExecContext(ctx, `UPDATE users SET created_at=? WHERE id=?`, old, alice.ID); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, alice)
	plain := noRedirectClient(client)

	// Settings is the only place with a trophy picker.
	code, settings := getBody(t, client, ts.URL+"/settings")
	if code != http.StatusOK {
		t.Fatalf("settings = %d", code)
	}
	for _, want := range []string{
		`id="trophy"`, `action="/settings/trophy"`, `<option value="n00b"`, "No trophy",
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings missing %q", want)
		}
	}
	if strings.Contains(settings, `action="/users/trophyalice/trophy"`) {
		t.Error("settings still uses the old trophy endpoint")
	}

	// Unearned trophies are rejected.
	if got := postStatus(t, plain, ts.URL+"/settings/trophy", url.Values{"_csrf": {sess.CSRF}, "badge": {"ten_games"}}); got != http.StatusSeeOther {
		t.Fatalf("invalid trophy = %d", got)
	}
	updated, _ := st.UserByID(ctx, alice.ID)
	if updated.FeaturedBadge != "" {
		t.Fatalf("invalid trophy stored as %q", updated.FeaturedBadge)
	}

	// Selecting an earned trophy highlights it on the read-only profile.
	if got := postStatus(t, plain, ts.URL+"/settings/trophy", url.Values{"_csrf": {sess.CSRF}, "badge": {"n00b"}}); got != http.StatusSeeOther {
		t.Fatalf("highlight trophy = %d", got)
	}
	updated, _ = st.UserByID(ctx, alice.ID)
	if updated.FeaturedBadge != "n00b" {
		t.Fatalf("featured trophy = %q, want n00b", updated.FeaturedBadge)
	}
	_, body := getBody(t, client, ts.URL+"/users/trophyalice")
	for _, want := range []string{`href="/users/trophyalice?tab=trophies"`, `class="profile-trophy-card"`, "<strong>n00b</strong>", "Earned"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	if strings.Contains(body, `action="/users/trophyalice/trophy"`) || strings.Contains(body, "profile-trophy-form") {
		t.Error("profile still contains a trophy form")
	}

	// The trophies page is read-only, including for the owner.
	_, trophies := getBody(t, client, ts.URL+"/users/trophyalice/trophies")
	for _, want := range []string{"1 of 11 earned", "Highlighted"} {
		if !strings.Contains(trophies, want) {
			t.Errorf("trophies page missing %q", want)
		}
	}
	for _, old := range []string{">Highlight</button>", "Clear highlighted trophy", `action="/users/trophyalice/trophy"`} {
		if strings.Contains(trophies, old) {
			t.Errorf("trophies page is not read-only: %q", old)
		}
	}
	if got := postStatus(t, plain, ts.URL+"/users/trophyalice/trophy", url.Values{"_csrf": {sess.CSRF}, "badge": {""}}); got != http.StatusNotFound {
		t.Fatalf("old trophy endpoint = %d, want 404", got)
	}

	// Clearing is done from settings too.
	if got := postStatus(t, plain, ts.URL+"/settings/trophy", url.Values{"_csrf": {sess.CSRF}, "badge": {""}}); got != http.StatusSeeOther {
		t.Fatalf("clear trophy = %d", got)
	}
	updated, _ = st.UserByID(ctx, alice.ID)
	if updated.FeaturedBadge != "" {
		t.Fatalf("featured trophy not cleared: %q", updated.FeaturedBadge)
	}
	_, body = getBody(t, client, ts.URL+"/users/trophyalice")
	if !strings.Contains(body, "No trophy highlighted yet.") {
		t.Error("cleared profile still shows a trophy")
	}

	// Viewing your own year recap awards the Recap trophy, and stats link to it.
	year := strconv.Itoa(time.Now().UTC().Year())
	if code, _ := getBody(t, client, ts.URL+"/users/trophyalice/year/"+year); code != http.StatusOK {
		t.Fatalf("year recap = %d", code)
	}
	badges, _ := st.BadgesFor(ctx, alice.ID)
	found := false
	for _, b := range badges {
		if b.Key == "year_in_review" && b.EarnedAt != nil {
			found = true
		}
	}
	if !found {
		t.Error("viewing the year recap did not award year_in_review")
	}
	code, stats := getBody(t, client, ts.URL+"/users/trophyalice/stats")
	if code != http.StatusOK || !strings.Contains(stats, "Recap") || !strings.Contains(stats, "n00b") {
		t.Fatalf("stats page does not render earned trophies (%d)", code)
	}
}
