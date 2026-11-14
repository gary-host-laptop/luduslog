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
