package web

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func TestIssue75PostInteractionDelivery(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	author, _ := st.CreateUser(ctx, "fedauthor", "fedauthor@example.test", "hash", false)
	reader, _ := st.CreateUser(ctx, "fedreader", "fedreader@example.test", "hash", false)
	if _, err := st.DB.ExecContext(ctx, `UPDATE users SET federation=1 WHERE id=?`, reader.ID); err != nil {
		t.Fatal(err)
	}
	remote := &store.RemoteActor{
		ActorURI: "https://remote.test/users/alice", Domain: "remote.test", Username: "alice", Name: "Alice",
		Inbox: "https://remote.test/users/alice/inbox", SharedInbox: "https://remote.test/inbox",
	}
	if err := st.SaveRemoteActor(ctx, remote); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRemoteFollow(ctx, remote.ActorURI, reader.ID); err != nil {
		t.Fatal(err)
	}
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7501, Slug: "federated-game", Title: "Federated Game", Category: "main_game", ReleaseStatus: "released"})
	log, _ := st.CreateLog(ctx, store.LogInput{UserID: author.ID, GameID: game.ID, Name: "Federated post", Status: "completed", Review: "Deliver this interaction"})
	if err := st.Follow(ctx, reader.ID, author.ID); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, reader)
	getBody(t, client, ts.URL+"/feed")
	post, err := st.PostForSubject(ctx, "log", log.ID, reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	postPath := "/posts/" + itoa(post.ID)

	postStatus(t, noRedirectClient(client), ts.URL+postPath+"/like", url.Values{"_csrf": {sess.CSRF}})
	postStatus(t, noRedirectClient(client), ts.URL+postPath+"/boost", url.Values{"_csrf": {sess.CSRF}})
	postStatus(t, noRedirectClient(client), ts.URL+"/comments", url.Values{
		"_csrf": {sess.CSRF}, "subject_type": {"post"}, "subject_id": {itoa(post.ID)},
		"body": {"Federated reply"}, "return_to": {postPath},
	})

	payloads := jobPayloads(t, st)
	for _, want := range []string{`Like`, `Announce`, `Create`, `inReplyTo`, `https://remote.test/inbox`} {
		if !strings.Contains(payloads, want) {
			t.Errorf("outbound jobs missing %q in %s", want, payloads)
		}
	}

	postStatus(t, noRedirectClient(client), ts.URL+postPath+"/like", url.Values{"_csrf": {sess.CSRF}})
	postStatus(t, noRedirectClient(client), ts.URL+postPath+"/boost", url.Values{"_csrf": {sess.CSRF}})
	payloads = jobPayloads(t, st)
	if !strings.Contains(payloads, `Undo`) || !strings.Contains(payloads, `Like`) {
		t.Errorf("undo not delivered: %s", payloads)
	}

	comments, _ := st.CommentsFor(ctx, "post", post.ID)
	if len(comments) != 1 {
		t.Fatalf("comments = %d", len(comments))
	}
	cid := comments[0].ID
	postStatus(t, noRedirectClient(client), ts.URL+"/comments/"+itoa(cid)+"/delete", url.Values{"_csrf": {sess.CSRF}, "return_to": {postPath}})
	payloads = jobPayloads(t, st)
	if !strings.Contains(payloads, `Delete`) || !strings.Contains(payloads, "Tombstone") {
		t.Errorf("comment delete not delivered: %s", payloads)
	}
}

func jobPayloads(t *testing.T, st *store.Store) string {
	t.Helper()
	rows, err := st.DB.Query(`SELECT payload FROM jobs WHERE kind='deliver' AND done_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var payload string
		rows.Scan(&payload)
		out.WriteString(payload)
		out.WriteString("\n")
	}
	return out.String()
}
