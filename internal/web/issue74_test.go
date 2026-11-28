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

func TestIssue74CanonicalPostInteractions(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	author, _ := st.CreateUser(ctx, "postauthor", "postauthor@example.test", "hash", false)
	reader, _ := st.CreateUser(ctx, "postreader", "postreader@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7401, Slug: "post-game", Title: "Post Game", Category: "main_game", ReleaseStatus: "released"})
	log, err := st.CreateLog(ctx, store.LogInput{UserID: author.ID, GameID: game.ID, Name: "Post title", Status: "completed", Rating: 8, Review: "A canonical review body"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Follow(ctx, reader.ID, author.ID); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, reader)

	code, feed := getBody(t, client, ts.URL+"/feed")
	if code != http.StatusOK {
		t.Fatalf("feed status = %d", code)
	}
	post, err := st.PostForSubject(ctx, "log", log.ID, reader.ID)
	if err != nil || post == nil {
		t.Fatalf("post backfill = %+v, %v", post, err)
	}
	postPath := "/posts/" + itoa(post.ID)
	for _, want := range []string{`class="post-actions"`, `action="` + postPath + `/like"`, `action="` + postPath + `/boost"`, `href="` + postPath + `#comments"`, "/report?target_type=post&target_id=" + itoa(post.ID)} {
		if !strings.Contains(feed, want) {
			t.Errorf("feed missing %q", want)
		}
	}

	if got := postStatus(t, noRedirectClient(client), ts.URL+postPath+"/like", url.Values{"_csrf": {sess.CSRF}, "return_to": {postPath}}); got != http.StatusSeeOther {
		t.Fatalf("like = %d", got)
	}
	liked, _ := st.PostByID(ctx, post.ID, reader.ID)
	if !liked.Liked || liked.Likes != 1 {
		t.Fatalf("like not recorded: %+v", liked)
	}
	if got := postStatus(t, noRedirectClient(client), ts.URL+postPath+"/boost", url.Values{"_csrf": {sess.CSRF}, "return_to": {postPath}}); got != http.StatusSeeOther {
		t.Fatalf("boost = %d", got)
	}
	boosted, _ := st.PostByID(ctx, post.ID, reader.ID)
	if !boosted.Boosted || boosted.Boosts != 1 {
		t.Fatalf("boost not recorded: %+v", boosted)
	}

	if got := postStatus(t, noRedirectClient(client), ts.URL+"/comments", url.Values{
		"_csrf": {sess.CSRF}, "subject_type": {"post"}, "subject_id": {itoa(post.ID)},
		"body": {"A reply on the canonical post"}, "return_to": {postPath},
	}); got != http.StatusSeeOther {
		t.Fatalf("comment = %d", got)
	}
	withComment, _ := st.PostByID(ctx, post.ID, reader.ID)
	if withComment.Comments != 1 {
		t.Fatalf("comments = %d, want 1", withComment.Comments)
	}

	code, page := getBody(t, client, ts.URL+postPath)
	if code != http.StatusOK {
		t.Fatalf("post page = %d", code)
	}
	for _, want := range []string{"A canonical review body", "A reply on the canonical post", `action="` + postPath + `/like"`, "Comments"} {
		if !strings.Contains(page, want) {
			t.Errorf("post page missing %q", want)
		}
	}

	code, boostFeed := getBody(t, client, ts.URL+"/feed")
	if code != http.StatusOK || !strings.Contains(boostFeed, "boosted a post") {
		t.Errorf("boost missing from feed: %d", code)
	}

	// Toggling the same buttons removes the interactions.
	postStatus(t, noRedirectClient(client), ts.URL+postPath+"/like", url.Values{"_csrf": {sess.CSRF}})
	postStatus(t, noRedirectClient(client), ts.URL+postPath+"/boost", url.Values{"_csrf": {sess.CSRF}})
	final, _ := st.PostByID(ctx, post.ID, reader.ID)
	if final.Liked || final.Boosted || final.Likes != 0 || final.Boosts != 0 {
		t.Fatalf("toggles did not undo: %+v", final)
	}
}

func TestIssue76PostReportAndAdminPreview(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	author, _ := st.CreateUser(ctx, "reportauthor", "reportauthor@example.test", "hash", false)
	reporter, _ := st.CreateUser(ctx, "reportreader", "reportreader@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7601, Slug: "report-game", Title: "Report Game", Category: "main_game", ReleaseStatus: "released"})
	log, _ := st.CreateLog(ctx, store.LogInput{UserID: author.ID, GameID: game.ID, Name: "Report me", Status: "completed", Review: "Reported review body"})
	if err := st.Follow(ctx, reporter.ID, author.ID); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, reporter)
	getBody(t, client, ts.URL+"/feed")
	post, err := st.PostForSubject(ctx, "log", log.ID, reporter.ID)
	if err != nil {
		t.Fatal(err)
	}

	code, form := getBody(t, client, ts.URL+"/report?target_type=post&target_id="+itoa(post.ID)+"&return_to=/feed")
	if code != http.StatusOK {
		t.Fatalf("report form = %d", code)
	}
	for _, want := range []string{"Spam or unwanted content", "Harassment or bullying", `name="reason"`, `name="target_type" value="post"`} {
		if !strings.Contains(form, want) {
			short := form
			if len(short) > 800 {
				short = short[:800]
			}
			t.Errorf("report form missing %q; body=%s", want, short)
		}
	}

	if got := postStatus(t, noRedirectClient(client), ts.URL+"/report", url.Values{
		"_csrf": {sess.CSRF}, "target_type": {"post"}, "target_id": {itoa(post.ID)},
		"reason": {"spam"}, "details": {"unsolicited"}, "return_to": {"/feed"},
	}); got != http.StatusSeeOther {
		t.Fatalf("report post = %d", got)
	}
	reports, _ := st.Reports(ctx, "open")
	if len(reports) != 1 || reports[0].TargetType != "post" || reports[0].TargetID != post.ID {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].TargetAuthor != "reportauthor" || !strings.Contains(reports[0].ContentPreview, "Reported review body") {
		t.Fatalf("report context missing: %+v", reports[0])
	}

	admin, _ := st.CreateUser(ctx, "reportmod", "reportmod@example.test", "hash", true)
	st.SetApproved(ctx, admin.ID, true)
	adminClient, _ := authedClient(t, ts, st, admin)
	code, page := getBody(t, adminClient, ts.URL+"/admin/reports")
	if code != http.StatusOK {
		t.Fatalf("admin reports = %d", code)
	}
	for _, want := range []string{"Reported review body", "reportauthor", "spam"} {
		if !strings.Contains(page, want) {
			t.Errorf("admin report missing %q", want)
		}
	}

	// A remote report can optionally forward a Flag and never notifies the
	// reported account.
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/report", url.Values{
		"_csrf": {sess.CSRF}, "target_type": {"remote"}, "target_url": {"https://remote.test/notes/9"},
		"reason": {"harassment"}, "details": {"forwarded context"}, "forward": {"1"}, "return_to": {"/feed"},
	}); got != http.StatusSeeOther {
		t.Fatalf("remote report = %d", got)
	}
	payloads := jobPayloads(t, st)
	for _, want := range []string{"Flag", "https://remote.test/inbox", "forwarded context"} {
		if !strings.Contains(payloads, want) {
			t.Errorf("flag job missing %q in %s", want, payloads)
		}
	}
	reports, _ = st.Reports(ctx, "open")
	forwarded := false
	for _, rep := range reports {
		if rep.TargetURL == "https://remote.test/notes/9" && rep.Forwarded {
			forwarded = true
		}
	}
	if !forwarded {
		t.Errorf("forwarded report not marked: %+v", reports)
	}
}
