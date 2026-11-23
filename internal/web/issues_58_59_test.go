package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func TestGamePagePolish(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	if err := st.SetCatalogEditor(ctx, alice.ID, true); err != nil {
		t.Fatal(err)
	}
	game := mustSaveGame(t, st, &models.Game{
		IGDBID: -1101, Slug: "polished-game", Title: "Polished Game", Franchise: "Polish Saga",
		Category: "main_game", ReleaseStatus: "released",
		AltNames:    []string{"BlackOps.exe", "Black Ops", "black ops", " F1 ", "F1"},
		TimeToBeat:  &models.TimeToBeat{Hastily: 19800, Normally: 28800, Completely: 54000},
		Platforms:   []models.Platform{{Name: "PC"}},
		ReleaseDate: "2024-01-02",
	})
	mustSaveGame(t, st, &models.Game{
		IGDBID: -1102, Slug: "polished-game-2", Title: "Polished Game 2", Franchise: "Polish Saga",
		Category: "main_game", ReleaseStatus: "released",
	})

	client, _ := authedClient(t, ts, st, alice)
	code, body := getBody(t, client, ts.URL+"/games/"+game.Slug)
	if code != http.StatusOK {
		t.Fatalf("game page = %d", code)
	}

	// Executable artifacts and case-insensitive duplicates never reach the
	// public metadata card.
	if !strings.Contains(body, `<dd style="margin:0">`) || !strings.Contains(body, "Black Ops") || !strings.Contains(body, "F1") {
		t.Error("filtered alternate names are missing")
	}
	if strings.Contains(body, "Black Ops, black ops") || strings.Contains(body, "F1, F1") {
		t.Error("duplicate alternate names are not filtered")
	}
	if strings.Contains(body, "BlackOps.exe") || strings.Contains(body, "black ops") {
		t.Error("executable or duplicate alternate name leaked")
	}

	// The edit control moved from Details to the title row and is permission
	// gated.
	if !strings.Contains(body, `class="icon-btn game-edit-link"`) || !strings.Contains(body, `href="/games/polished-game/edit"`) {
		t.Error("edit icon missing from the title row")
	}
	titleRow := body[strings.Index(body, `class="game-title-row"`):strings.Index(body, `class="game-hero"`)]
	if !strings.Contains(titleRow, "game-edit-link") {
		t.Error("edit icon is outside the title row")
	}
	details := body[strings.Index(body, "<h2>Details</h2>"):]
	if strings.Contains(details, `href="/games/polished-game/edit"`) {
		t.Error("duplicate edit control remains in Details")
	}
	if strings.Contains(body, "Report an issue") || strings.Contains(body, "/report?target_type=game") {
		t.Error("game report link should be removed")
	}
	_, anonymous := getBody(t, http.DefaultClient, ts.URL+"/games/"+game.Slug)
	if strings.Contains(anonymous, "game-edit-link") {
		t.Error("edit icon visible without catalog permission")
	}

	// Like control is a circular icon above the stars and its count lives in
	// the stat strip next to wishlist.
	top := strings.Index(body, `class="library-card-top"`)
	rating := strings.Index(body, `class="game-user-rating"`)
	if top < 0 || rating < 0 || top > rating {
		t.Error("like control is not above the rating row")
	}
	if !strings.Contains(body, `id="game-like-count">0</strong>`) {
		t.Error("like count is missing from the stat strip")
	}
	if strings.Contains(body, "heart-count") {
		t.Error("like button still renders a visible count")
	}
	wishlist := strings.Index(body, ">wishlist<")
	likes := strings.Index(body, `id="game-like-count"`)
	if wishlist < 0 || likes < wishlist {
		t.Error("like count is not next to wishlist")
	}

	// Related See all lives in the tab bar with the active tab's target, while
	// the group-head link remains for no-JS users.
	related := relatedSection(body)
	tabbarStart := strings.Index(related, `class="related-tabbar"`)
	if tabbarStart < 0 {
		t.Fatal("related tab bar missing")
	}
	seeAll := strings.Index(related[tabbarStart:], "data-related-see-all")
	groupHead := strings.Index(related[tabbarStart:], `class="related-group-head"`)
	if seeAll < 0 || groupHead < 0 || seeAll > groupHead {
		t.Error("tab bar See all link is missing or outside the tab bar")
	}
	if !strings.Contains(related, `data-related-see-all href="/games/polished-game/related/series"`) {
		t.Error("tab bar See all target is wrong")
	}
	if strings.Count(related, ">See all</a>") != 2 {
		t.Errorf("expected tab-bar and no-JS See all links, got %d", strings.Count(related, ">See all</a>"))
	}
	if !strings.Contains(related, `data-see-all="/games/polished-game/related/series"`) {
		t.Error("active tab has no See all target")
	}

	// Larger time-to-beat layout hooks remain present.
	for _, want := range []string{`class="time-block"`, "ph-flag-checkered", "ph-compass", "ph-trophy", ">5h 30m<", ">15h<"} {
		if !strings.Contains(body, want) {
			t.Errorf("time-to-beat layout missing %q", want)
		}
	}
}

func TestUserGameLogPage(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	owner, _ := st.CreateUser(ctx, "owner", "owner@example.test", "hash", false)
	follower, _ := st.CreateUser(ctx, "follower", "follower@example.test", "hash", false)
	pending, _ := st.CreateUser(ctx, "pending", "pending@example.test", "hash", false)
	stranger, _ := st.CreateUser(ctx, "stranger", "stranger@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{
		IGDBID: -1111, Slug: "journal-game", Title: "Journal Game", Category: "main_game", ReleaseStatus: "released",
	})
	platformID := mustSaveGamePlatform(t, st, "PC")

	// Mutual friends make the friend-activity row visible.
	for _, pair := range [][2]*models.User{{owner, follower}} {
		st.Follow(ctx, pair[0].ID, pair[1].ID)
		st.ApproveFollow(ctx, pair[0].ID, pair[1].ID)
		st.Follow(ctx, pair[1].ID, pair[0].ID)
		st.ApproveFollow(ctx, pair[1].ID, pair[0].ID)
	}
	st.SetStatuses(ctx, owner.ID, game.ID, true, false, false, false, false)

	reviewLog, err := st.CreateLog(ctx, store.LogInput{
		UserID: owner.ID, GameID: game.ID, Name: "First run", Review: "A full review", Spoiler: false,
		Status: "completed", Rating: 8, PlatformID: &platformID, Playtime: 10,
		StartedAt: "2024-01-01", FinishedAt: "2024-01-05", Replay: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	journalLog, err := st.CreateLog(ctx, store.LogInput{
		UserID: owner.ID, GameID: game.ID, Name: "Second run", Status: "playing", Rating: 6,
		PlatformID: &platformID, Playtime: 2.5, Mastered: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	draftLog, err := st.CreateLog(ctx, store.LogInput{
		UserID: owner.ID, GameID: game.ID, Name: "Secret draft", Review: "draft body", Status: "played", Draft: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Friend activity links to the user's log for this game.
	_, gamePage := getBody(t, http.DefaultClient, ts.URL+"/games/"+game.Slug)
	if !strings.Contains(gamePage, `href="/users/owner/logs/journal-game"`) {
		// Anonymous visitors do not see friends activity; check as follower.
		client, _ := authedClient(t, ts, st, follower)
		_, gamePage = getBody(t, client, ts.URL+"/games/"+game.Slug)
		if !strings.Contains(gamePage, `href="/users/owner/logs/journal-game"`) {
			t.Error("friend activity row does not link to the user-game log page")
		}
	}

	code, body := getBody(t, http.DefaultClient, ts.URL+"/users/owner/logs/journal-game")
	if code != http.StatusOK {
		t.Fatalf("user game log = %d", code)
	}
	for _, want := range []string{
		"owner", "Journal Game", "Played 1×", "completed", "8.0 / 10",
		"Started 2024-01-01", "Finished 2024-01-05", "Platform PC", "Replay",
		"A full review", "Read the full review →", "Second run", "playing", "Mastered",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("log page missing %q", want)
		}
	}
	if strings.Contains(body, "Secret draft") || strings.Contains(body, "draft body") {
		t.Error("draft log visible to the public")
	}
	first := strings.Index(body, "Second run")
	second := strings.Index(body, "First run")
	if first < 0 || second < 0 || first > second {
		t.Error("logs are not newest first")
	}
	if !strings.Contains(body, `href="/users/owner/reviews/`+itoa(reviewLog.ID)+`"`) {
		t.Error("review entry does not link to the review page")
	}

	// Journal rows link to the new page; review rows keep the review target.
	_, journal := getBody(t, http.DefaultClient, ts.URL+"/users/owner?tab=logs")
	if !strings.Contains(journal, `href="/users/owner/logs/journal-game"`) || !strings.Contains(journal, "View log →") {
		t.Error("journal-only row does not link to the user-game log page")
	}
	if !strings.Contains(journal, `href="/users/owner/reviews/`+itoa(reviewLog.ID)+`"`) || !strings.Contains(journal, "Read more →") {
		t.Error("review row no longer links to the review page")
	}
	if strings.Contains(journal, `/users/owner/reviews/`+itoa(journalLog.ID)) {
		t.Error("journal-only row links to a review page")
	}

	// Sessions return to the right destination for both kinds of log.
	ownerClient, _ := authedClient(t, ts, st, owner)
	_, reviewSessions := getBody(t, ownerClient, ts.URL+"/logs/"+itoa(reviewLog.ID)+"/sessions")
	if !strings.Contains(reviewSessions, `href="/users/owner/reviews/`+itoa(reviewLog.ID)+`"`) {
		t.Error("review sessions back link is wrong")
	}
	_, journalSessions := getBody(t, ownerClient, ts.URL+"/logs/"+itoa(journalLog.ID)+"/sessions")
	if !strings.Contains(journalSessions, `href="/users/owner/logs/journal-game"`) {
		t.Error("journal-only sessions back link is wrong")
	}

	// Blocked viewers cannot open the page.
	if err := st.Block(ctx, owner.ID, stranger.ID); err != nil {
		t.Fatal(err)
	}
	strangerClient, _ := authedClient(t, ts, st, stranger)
	_, blocked := getBody(t, strangerClient, ts.URL+"/users/owner/logs/journal-game")
	if !strings.Contains(blocked, "This profile is unavailable.") || strings.Contains(blocked, "First run") {
		t.Error("blocked viewer can read the log page")
	}
	if err := st.Unblock(ctx, owner.ID, stranger.ID); err != nil {
		t.Fatal(err)
	}

	// Private profiles are visible only to the owner and approved followers;
	// drafts stay owner-only even for those followers.
	if err := st.UpdateProfile(ctx, owner.ID, store.ProfileUpdate{PrivateProfile: true, ManuallyApproves: true}); err != nil {
		t.Fatal(err)
	}
	st.Follow(ctx, pending.ID, owner.ID)
	st.Follow(ctx, follower.ID, owner.ID)
	strangerClient, _ = authedClient(t, ts, st, stranger)
	_, anonymousPrivate := getBody(t, strangerClient, ts.URL+"/users/owner/logs/journal-game")
	if !strings.Contains(anonymousPrivate, "This profile is private.") || strings.Contains(anonymousPrivate, "First run") {
		t.Error("private profile leaked to a stranger")
	}
	pendingClient, _ := authedClient(t, ts, st, pending)
	_, pendingBody := getBody(t, pendingClient, ts.URL+"/users/owner/logs/journal-game")
	if !strings.Contains(pendingBody, "This profile is private.") {
		t.Error("pending follower can read a private profile")
	}
	approvedClient, _ := authedClient(t, ts, st, follower)
	_, approvedBody := getBody(t, approvedClient, ts.URL+"/users/owner/logs/journal-game")
	if !strings.Contains(approvedBody, "First run") || !strings.Contains(approvedBody, "Second run") {
		t.Error("approved follower cannot read the log page")
	}
	if strings.Contains(approvedBody, "Secret draft") {
		t.Error("draft visible to an approved follower")
	}
	_, ownerBody := getBody(t, ownerClient, ts.URL+"/users/owner/logs/journal-game")
	if !strings.Contains(ownerBody, "Secret draft") || !strings.Contains(ownerBody, "Draft") {
		t.Error("owner cannot see their draft")
	}

	// Adult games keep the existing gate.
	adult := mustSaveGame(t, st, &models.Game{IGDBID: -1112, Slug: "adult-log", Title: "Adult Log", Category: "main_game", ReleaseStatus: "released", Adult: true})
	st.CreateLog(ctx, store.LogInput{UserID: owner.ID, GameID: adult.ID, Review: "adult body", Status: "played"})
	_, gated := getBody(t, ownerClient, ts.URL+"/users/owner/logs/adult-log")
	if !strings.Contains(gated, "Age-restricted entry") || strings.Contains(gated, "adult body") {
		t.Error("adult log bypassed the age gate")
	}

	_ = draftLog
}
