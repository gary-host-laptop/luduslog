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

func TestUnifiedTimelinePagesAndFilters(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -6811, Slug: "timeline-web", Title: "Timeline Web", ReleaseStatus: "released"})

	review, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Review: "Timeline review body", Rating: 8, Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	bare, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Status: "playing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, store.SessionInput{LogID: bare.ID, Note: "Timeline session body", NotePrivacy: "everyone", Playtime: 2, Days: []string{"2026-03-02"}}); err != nil {
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
	if err := st.Follow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, alice)

	code, body := getBody(t, client, ts.URL+"/users/alice")
	if code != http.StatusOK {
		t.Fatalf("profile overview = %d", code)
	}
	for _, want := range []string{
		"Activity", "wrote a review", "logged a game", "played a session",
		"added <a href=\"/games/timeline-web\">Timeline Web</a> to Backlog",
		"liked <a href=\"/games/timeline-web\">Timeline Web</a>",
		"rated <a href=\"/games/timeline-web\">Timeline Web</a>",
		"started following <a href=\"/users/bob\">@bob</a>",
		"Timeline review body", "Timeline session body",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("activity page missing %q", want)
		}
	}
	if strings.Contains(body, ">Journal<") || strings.Contains(body, ">Activity</a>") || strings.Contains(body, "tab=activity") || strings.Contains(body, "journal_view") {
		t.Error("Journal or Activity tab still rendered")
	}

	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	redirect, err := noRedirect.Get(ts.URL + "/users/alice?tab=activity&kind=sessions")
	if err != nil {
		t.Fatal(err)
	}
	redirect.Body.Close()
	if redirect.StatusCode != http.StatusFound || redirect.Header.Get("Location") != "/users/alice?kind=sessions" {
		t.Fatalf("legacy activity redirect = %d %q", redirect.StatusCode, redirect.Header.Get("Location"))
	}
	redirect, err = noRedirect.Get(ts.URL + "/users/alice?tab=logs")
	if err != nil {
		t.Fatal(err)
	}
	redirect.Body.Close()
	if redirect.StatusCode != http.StatusFound || redirect.Header.Get("Location") != "/users/alice?kind=logs" {
		t.Fatalf("legacy journal redirect = %d %q", redirect.StatusCode, redirect.Header.Get("Location"))
	}

	_, reviews := getBody(t, client, ts.URL+"/users/alice?kind=reviews")
	if !strings.Contains(reviews, "Timeline review body") || strings.Contains(reviews, "Timeline session body") {
		t.Error("review filter included the wrong kinds")
	}
	_, logs := getBody(t, client, ts.URL+"/users/alice?kind=logs")
	if !strings.Contains(logs, "logged a game") || strings.Contains(logs, "Timeline review body") {
		t.Error("log filter included the wrong kinds")
	}
	_, sessions := getBody(t, client, ts.URL+"/users/alice?kind=sessions")
	if !strings.Contains(sessions, "Timeline session body") || strings.Contains(sessions, "Timeline review body") {
		t.Error("session filter included the wrong kinds")
	}
	_, calendar := getBody(t, client, ts.URL+"/users/alice?view=calendar&month=2026-03")
	if !strings.Contains(calendar, "calendar-day played") || strings.Contains(calendar, "Timeline session body") {
		t.Error("calendar toggle did not render only the month grid")
	}

	// Shelf events carry their on/off state and render old rows generically.
	if got := postStatus(t, noRedirect, ts.URL+"/games/timeline-web/status", url.Values{"_csrf": {sess.CSRF}, "flag": {"playing"}, "on": {"1"}}); got != http.StatusSeeOther {
		t.Fatalf("status post = %d", got)
	}
	var target string
	st.DB.QueryRowContext(ctx, `SELECT target_name FROM activities WHERE user_id=? AND type='status' ORDER BY id DESC LIMIT 1`, alice.ID).Scan(&target)
	if target != "playing:1" {
		t.Fatalf("status activity target = %q", target)
	}

	// The review URL stays valid after the fold.
	if _, err := st.LogByID(ctx, review.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineFeedVisibilityAndPrivacySettings(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -6812, Slug: "bob-private-game", Title: "Bob Private Game", ReleaseStatus: "released"})
	if err := st.Follow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: bob.ID, GameID: game.ID, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	aliceClient, aliceSess := authedClient(t, ts, st, alice)

	_, home := getBody(t, aliceClient, ts.URL+"/feed")
	if !strings.Contains(home, "Bob Private Game") {
		t.Fatal("feed did not include an approved follow")
	}
	if err := st.SetActivityVisibility(ctx, bob.ID, map[string]string{"log": "private", "follow": "followers"}); err != nil {
		t.Fatal(err)
	}
	_, home = getBody(t, aliceClient, ts.URL+"/feed")
	if strings.Contains(home, "Bob Private Game") {
		t.Fatal("private log leaked into the feed")
	}

	// Privacy settings save all eight kinds and reject invalid values.
	noRedirect := &http.Client{Jar: aliceClient.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	values := url.Values{"_csrf": {aliceSess.CSRF}, "activity_review": {"everyone"}, "activity_log": {"followers"}, "activity_session": {"private"}, "activity_list": {"everyone"}, "activity_rating": {"private"}, "activity_like": {"followers"}, "activity_shelf": {"everyone"}, "activity_follow": {"everyone"}}
	if got := postStatus(t, noRedirect, ts.URL+"/settings/privacy", values); got != http.StatusSeeOther {
		t.Fatalf("privacy save = %d", got)
	}
	visibility, err := st.ActivityVisibility(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if visibility["log"] != "followers" || visibility["session"] != "private" || visibility["follow"] != "everyone" {
		t.Fatalf("visibility not saved: %+v", visibility)
	}
	_, settings := getBody(t, aliceClient, ts.URL+"/settings?tab=privacy")
	if !strings.Contains(settings, "Activity visibility") || !strings.Contains(settings, `name="activity_follow"`) {
		t.Error("privacy settings matrix missing")
	}
	bad := url.Values{"_csrf": {aliceSess.CSRF}}
	for _, kind := range store.ActivityKinds {
		bad.Set("activity_"+kind, "everyone")
	}
	bad.Set("activity_log", "friends")
	if got := postStatus(t, noRedirect, ts.URL+"/settings/privacy", bad); got != http.StatusSeeOther {
		t.Fatalf("invalid privacy save = %d", got)
	}
	if got, _ := st.ActivityVisibility(ctx, alice.ID); got["log"] != "followers" {
		t.Fatalf("invalid value overwrote visibility: %+v", got)
	}
}

func TestFeedTabShowsFollowedTimeline(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -6814, Slug: "feed-tab-game", Title: "Feed Tab Game", ReleaseStatus: "released"})
	if err := st.Follow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: bob.ID, GameID: game.ID, Status: "played"}); err != nil {
		t.Fatal(err)
	}
	client, _ := authedClient(t, ts, st, alice)

	code, body := getBody(t, client, ts.URL+"/feed")
	if code != http.StatusOK {
		t.Fatalf("feed = %d", code)
	}
	for _, want := range []string{
		`href="/feed" class="active"`, "Activity from you and the people you follow",
		"Feed Tab Game", "logged a game", `href="/feed?kind=sessions"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("feed missing %q", want)
		}
	}

	if err := st.SetActivityVisibility(ctx, bob.ID, map[string]string{"log": "private"}); err != nil {
		t.Fatal(err)
	}
	_, body = getBody(t, client, ts.URL+"/feed")
	if strings.Contains(body, "Feed Tab Game") {
		t.Fatal("private followed activity leaked into the feed")
	}

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(ts.URL + "/feed")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?next=/feed" {
		t.Fatalf("anonymous feed = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestGamePageSessionCreatesAutoLog(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -6813, Slug: "session-entry-game", Title: "Session Entry Game", ReleaseStatus: "released"})
	client, sess := authedClient(t, ts, st, alice)
	_, page := getBody(t, client, ts.URL+"/games/session-entry-game")
	if !strings.Contains(page, "Log a play session") || !strings.Contains(page, `action="/games/session-entry-game/sessions"`) {
		t.Fatal("game page has no session entry point")
	}
	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.PostForm(ts.URL+"/games/session-entry-game/sessions", url.Values{
		"_csrf": {sess.CSRF}, "note": {"Session from the game page"}, "playtime": {"1.5"},
		"privacy": {"everyone"}, "days": {"2026-04-01"}, "tags": {"first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("game session create = %d", resp.StatusCode)
	}
	l, err := st.SessionLogFor(ctx, alice.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !l.AutoCreated {
		t.Fatal("game page session did not create an auto log")
	}
	sessions, _ := st.SessionsFor(ctx, l.ID)
	if len(sessions) != 1 || sessions[0].Note != "Session from the game page" {
		t.Fatalf("sessions = %+v", sessions)
	}
	_, activity := getBody(t, client, ts.URL+"/users/alice?kind=logs")
	if strings.Contains(activity, "logged a game") {
		t.Error("auto-created container log rendered as a log card")
	}
	_, sessionsPage := getBody(t, client, ts.URL+"/users/alice?kind=sessions")
	if !strings.Contains(sessionsPage, "Session from the game page") {
		t.Error("game-page session missing from Activity")
	}
}
