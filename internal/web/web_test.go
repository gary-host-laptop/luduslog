package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"luduslog/internal/auth"
	"luduslog/internal/config"
	"luduslog/internal/models"
	"luduslog/internal/queue"
	"luduslog/internal/store"
)

func testServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Name: "LudusLog Test", Software: "luduslog",
		BaseURL: "http://example.test", SessionKey: "test-session-key-0123456789", Registration: "open",
		MaxCommentChars: 5000, DataDir: t.TempDir(),
	}
	srv, err := New(cfg, st, queue.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); st.DB.Close() })
	return ts, st
}

func TestPublicPages(t *testing.T) {
	ts, st := testServer(t)
	if _, err := st.SaveGame(context.Background(), &models.Game{IGDBID: -1, Slug: "celeste", Title: "Celeste", ReleaseStatus: "released"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/games", "/games/celeste", "/reviews", "/lists", "/login", "/healthz", "/nodeinfo/2.1"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s = %d: %s", path, resp.StatusCode, body)
		}
	}
}

func TestWebfingerAndActor(t *testing.T) {
	ts, st := testServer(t)
	u, err := st.CreateUser(context.Background(), "alice", "alice@example.test", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateProfile(context.Background(), u.ID, store.ProfileUpdate{Federation: true}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/.well-known/webfinger?resource=acct:alice@example.test")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Subject string `json:"subject"`
		Links   []struct {
			Href string `json:"href"`
		} `json:"links"`
	}
	json.NewDecoder(resp.Body).Decode(&wf)
	resp.Body.Close()
	if wf.Subject != "acct:alice@example.test" || len(wf.Links) == 0 {
		t.Fatalf("unexpected webfinger: %+v", wf)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/users/alice", nil)
	req.Header.Set("Accept", "application/activity+json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var actor map[string]any
	json.NewDecoder(resp.Body).Decode(&actor)
	resp.Body.Close()
	if actor["type"] != "Person" || actor["inbox"] == "" {
		t.Fatalf("unexpected actor: %+v", actor)
	}
	if _, ok := actor["publicKey"]; !ok {
		t.Fatal("actor has no public key")
	}
	out, err := http.Get(ts.URL + "/users/alice/outbox")
	if err != nil {
		t.Fatal(err)
	}
	var outbox map[string]any
	json.NewDecoder(out.Body).Decode(&outbox)
	out.Body.Close()
	if outbox["type"] != "OrderedCollection" {
		t.Fatalf("unexpected outbox: %+v", outbox)
	}
}

func TestAdminSettingsAreValidatedAndSecretsStayHidden(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	cfg := config.Config{
		Name: "Secret Test", BaseURL: "http://example.test", SessionKey: "session-super-secret",
		Registration: "open", MaxCommentChars: 5000, DataDir: t.TempDir(),
		SMTPPass: "smtp-super-secret", IgdbSecret: "igdb-super-secret", TLSKey: "/tls/super-secret.pem",
	}
	srv, err := New(cfg, st, queue.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	admin, err := st.CreateUser(context.Background(), "root", "root@example.test", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.auth.CreateSession(context.Background(), admin.ID, "test", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(ts.URL)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(base, []*http.Cookie{
		{Name: sessionCookie, Value: sess.Token, Path: "/"},
		{Name: csrfCookie, Value: sess.CSRF, Path: "/"},
	})
	client := &http.Client{Jar: jar}
	resp, err := client.Get(ts.URL + "/admin/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("settings page = %d: %s", resp.StatusCode, body)
	}
	for _, secret := range []string{"session-super-secret", "smtp-super-secret", "igdb-super-secret", "/tls/super-secret.pem"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("secret %q appeared in admin HTML", secret)
		}
	}
	form := url.Values{
		"_csrf": {sess.CSRF}, "name": {"Renamed Instance"}, "description": {"New description"},
		"registration": {"open"}, "invite_code": {""}, "contact_email": {"hello@example.test"},
		"admin_email": {"admin@example.test"}, "max_comment_chars": {"2500"}, "federate_remote_ratings": {"true"},
	}
	resp, err = client.PostForm(ts.URL+"/admin/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("valid settings save = %d", resp.StatusCode)
	}
	if got, _ := st.LookupSetting(context.Background(), "name"); got != "Renamed Instance" {
		t.Fatalf("name setting = %q", got)
	}
	if got := srv.instanceConfig(context.Background()).Name; got != "Renamed Instance" {
		t.Fatalf("runtime instance name = %q", got)
	}

	bad := url.Values{"name": {"Renamed Instance"}, "registration": {"nonsense"}, "max_comment_chars": {"2500"}, "_csrf": {sess.CSRF}}
	resp, err = client.PostForm(ts.URL+"/admin/settings", bad)
	if err != nil {
		t.Fatal(err)
	}
	badBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(badBody), "registration must be") {
		t.Fatalf("invalid settings = %d: %s", resp.StatusCode, badBody)
	}
}

func TestCSRFRejectsLoginWithoutToken(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Post(ts.URL+"/login", "application/x-www-form-urlencoded", strings.NewReader("identifier=alice&password=x"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("login without CSRF = %d", resp.StatusCode)
	}
}

func TestModeratorPermissionsAndContentRemoval(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	cfg := config.Config{Name: "Mod Test", BaseURL: "http://example.test", SessionKey: "test-session-key-0123456789", Registration: "open", MaxCommentChars: 5000, DataDir: t.TempDir()}
	srv, err := New(cfg, st, queue.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	ctx := context.Background()
	mod, _ := st.CreateUser(ctx, "moderator", "mod@example.test", "hash", false)
	author, _ := st.CreateUser(ctx, "author", "author@example.test", "hash", false)
	if err := st.SetRole(ctx, mod.ID, "mod"); err != nil {
		t.Fatal(err)
	}
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -50, Slug: "mod-game", Title: "Mod Game", ReleaseStatus: "released"})
	log, err := st.CreateLog(ctx, store.LogInput{UserID: author.ID, GameID: game.ID, Review: "Remove me", Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := srv.auth.CreateSession(ctx, mod.ID, "test", "127.0.0.1")
	base, _ := url.Parse(ts.URL)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(base, []*http.Cookie{
		{Name: sessionCookie, Value: sess.Token, Path: "/"},
		{Name: csrfCookie, Value: sess.CSRF, Path: "/"},
	})
	client := &http.Client{Jar: jar}
	for path, want := range map[string]int{"/admin": 200, "/admin/users": 200, "/admin/reports": 200, "/admin/modlog": 200, "/admin/settings": 404, "/admin/news": 404} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}
	resp, err := client.PostForm(ts.URL+"/admin/content/log/"+itoa(log.ID)+"/remove", url.Values{"_csrf": {sess.CSRF}, "reason": {"reported"}})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove content = %d", resp.StatusCode)
	}
	if _, err := st.LogByID(ctx, log.ID); err == nil {
		t.Fatal("moderator removal did not hide the log")
	}
	actions, _ := st.ModActions(ctx, 10)
	if len(actions) != 1 || actions[0].Action != "remove_review" || actions[0].Moderator != "moderator" {
		t.Fatalf("moderation action not recorded: %+v", actions)
	}
}

func authedClient(t *testing.T, ts *httptest.Server, st *store.Store, user *models.User) (*http.Client, *auth.Session) {
	t.Helper()
	sess, err := (&auth.Service{Store: st}).CreateSession(context.Background(), user.ID, "test", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(ts.URL)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(base, []*http.Cookie{
		{Name: sessionCookie, Value: sess.Token, Path: "/"},
		{Name: csrfCookie, Value: sess.CSRF, Path: "/"},
	})
	return &http.Client{Jar: jar}, sess
}

func getBody(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestScreenshotSizeHelpers(t *testing.T) {
	raw := "https://images.igdb.com/igdb/image/upload/t_screenshot_big/abc.jpg"
	if got := screenshotThumb(raw); got != "https://images.igdb.com/igdb/image/upload/t_screenshot_med/abc.jpg" {
		t.Fatalf("thumb = %q", got)
	}
	if got := screenshotFull(raw); got != "https://images.igdb.com/igdb/image/upload/t_1080p/abc.jpg" {
		t.Fatalf("full = %q", got)
	}
}

func TestAccountMenuAndLogout(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	client, sess := authedClient(t, ts, st, alice)
	status, body := getBody(t, client, ts.URL+"/")
	if status != 200 {
		t.Fatalf("home = %d", status)
	}
	for _, want := range []string{
		`class="user-menu"`, `href="/users/alice"`, `href="/library"`,
		`href="/users/alice?tab=lists"`, `href="/users/alice/stats"`,
		`href="/settings"`, `action="/logout"`, `name="_csrf"`, "Log out",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("account menu missing %q", want)
		}
	}
	if strings.Contains(body, ">Admin</a>") || strings.Contains(body, ">Moderation</a>") {
		t.Fatal("regular user saw moderation link")
	}

	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.PostForm(ts.URL+"/logout", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF = %d", resp.StatusCode)
	}
	resp, err = noRedirect.PostForm(ts.URL+"/logout", url.Values{"_csrf": {sess.CSRF}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	var n int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE token_hash=?`, sess.TokenHash).Scan(&n)
	if n != 0 {
		t.Fatal("logout did not destroy the session")
	}
	status, body = getBody(t, client, ts.URL+"/")
	if status != 200 || strings.Contains(body, `class="user-menu"`) {
		t.Fatal("logout did not clear the browser session")
	}

	mod, _ := st.CreateUser(ctx, "mod", "mod@example.test", "hash", false)
	st.SetRole(ctx, mod.ID, "mod")
	modClient, _ := authedClient(t, ts, st, mod)
	_, modBody := getBody(t, modClient, ts.URL+"/")
	if !strings.Contains(modBody, `>Moderation</a>`) {
		t.Fatal("moderator account menu missing Moderation")
	}
}

func TestGameMediaAndReviewsRoute(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game, err := st.SaveGame(ctx, &models.Game{
		IGDBID: -1, Slug: "celeste", Title: "Celeste", ReleaseStatus: "released",
		Screenshots: []string{"https://images.igdb.com/igdb/image/upload/t_screenshot_big/a.jpg"},
		Videos:      []string{"abc123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Review: "A tight platformer, all the way up the mountain.", Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	status, body := getBody(t, http.DefaultClient, ts.URL+"/games/celeste")
	if status != 200 {
		t.Fatalf("game = %d", status)
	}
	for _, want := range []string{
		"t_screenshot_med/a.jpg", "t_1080p/a.jpg", "data-lightbox", "alt=\"Screenshot 1 of Celeste\"",
		"youtube.com/watch?v=abc123", `data-video-id="abc123"`, "Video 1",
		"Read more →", "/users/alice/reviews/", "All 1 →",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("game page missing %q", want)
		}
	}
	if strings.Contains(body, "Permalink") {
		t.Fatal("game page still renders Permalink")
	}
	resp, err := http.Get(ts.URL + "/games/celeste")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src https://www.youtube-nocookie.com") {
		t.Fatalf("CSP missing video frame source: %s", csp)
	}

	status, reviewsBody := getBody(t, http.DefaultClient, ts.URL+"/games/celeste/reviews?sort=rating")
	if status != 200 || !strings.Contains(reviewsBody, "Highest rated") || !strings.Contains(reviewsBody, "?sort=rating") {
		t.Fatalf("game reviews = %d: %s", status, reviewsBody[:min(len(reviewsBody), 500)])
	}
	if !strings.Contains(reviewsBody, "Back to Celeste") {
		t.Fatal("game reviews page missing back link")
	}
	if status, _ := getBody(t, http.DefaultClient, ts.URL+"/games/missing/reviews"); status != 404 {
		t.Fatalf("missing game reviews = %d", status)
	}

	empty, _ := st.SaveGame(ctx, &models.Game{IGDBID: -2, Slug: "empty", Title: "Empty Media", ReleaseStatus: "released"})
	_, emptyBody := getBody(t, http.DefaultClient, ts.URL+"/games/"+empty.Slug)
	if strings.Contains(emptyBody, "screenshot-grid") || strings.Contains(emptyBody, "video-grid") {
		t.Fatal("media section rendered without media")
	}

	adult, _ := st.SaveGame(ctx, &models.Game{IGDBID: -3, Slug: "adult", Title: "Adult Game", Adult: true, ReleaseStatus: "released"})
	st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: adult.ID, Review: "secret adult review", Status: "played"})
	_, adultBody := getBody(t, http.DefaultClient, ts.URL+"/games/adult/reviews")
	if !strings.Contains(adultBody, "Age-restricted entry") || strings.Contains(adultBody, "secret adult review") {
		t.Fatal("adult reviews route bypassed the gate")
	}
	_ = review
}

func TestReviewPreviewsAndSearchTabs(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	journalOnly, _ := st.CreateUser(ctx, "journalonly", "journal@example.test", "hash", false)
	game, _ := st.SaveGame(ctx, &models.Game{IGDBID: -1, Slug: "celeste", Title: "Celeste", ReleaseStatus: "released"})
	tail := "TAIL_MARKER_NOT_IN_PREVIEW"
	longReview := strings.Repeat("word ", 120) + tail
	review, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Name: "The climb", Review: longReview, Spoiler: true, Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	st.CreateLog(ctx, store.LogInput{UserID: journalOnly.ID, GameID: game.ID, Name: "Journal only", Status: "played"})

	_, home := getBody(t, http.DefaultClient, ts.URL+"/")
	if !strings.Contains(home, "Read more →") || !strings.Contains(home, "/users/alice/reviews/") {
		t.Fatal("home review lacks Read more link")
	}
	if !strings.Contains(home, "This review contains spoilers") || strings.Contains(home, tail) {
		t.Fatal("home review spoiler or truncation handling is wrong")
	}

	_, profile := getBody(t, http.DefaultClient, ts.URL+"/users/alice?tab=reviews")
	if strings.Contains(profile, "Permalink") || strings.Contains(profile, tail) {
		t.Fatal("profile still shows full review or Permalink")
	}
	if !strings.Contains(profile, "Read more →") || !strings.Contains(profile, "/users/alice/reviews/") {
		t.Fatal("profile review lacks Read more")
	}
	_, journal := getBody(t, http.DefaultClient, ts.URL+"/users/journalonly?tab=logs")
	if strings.Contains(journal, "/users/journalonly/reviews/") || strings.Contains(journal, "Read more →") {
		t.Fatal("bare journal entry renders a broken review link")
	}

	status, search := getBody(t, http.DefaultClient, ts.URL+"/search?q=celeste")
	if status != 200 || !strings.Contains(search, "Celeste") || !strings.Contains(search, `tab=reviews`) {
		t.Fatalf("default search = %d: %s", status, search[:min(len(search), 400)])
	}
	if strings.Count(search, `type="search"`) != 1 {
		t.Fatal("search page should contain only the top-bar search input")
	}
	if strings.Contains(search, ">All</a>") || !strings.Contains(search, `class="on">Games</a>`) {
		t.Fatal("search tabs are wrong")
	}
	_, reviewSearch := getBody(t, http.DefaultClient, ts.URL+"/search?q=celeste&tab=reviews")
	if !strings.Contains(reviewSearch, "The climb") || !strings.Contains(reviewSearch, "Spoiler review") || strings.Contains(reviewSearch, tail) {
		t.Fatal("review search did not hide spoiler text or return the review")
	}
	if !strings.Contains(reviewSearch, `name="tab" value="reviews"`) {
		t.Fatal("top-bar search did not preserve the active tab")
	}
	if status, _ := getBody(t, http.DefaultClient, ts.URL+"/search?q=%23%40~%7C%5B%5D%5C%22&tab=games"); status != 200 {
		t.Fatalf("punctuation search = %d", status)
	}
	_ = review
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
