package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"luduslog/internal/federation"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

func TestIssue75InboundPostActivities(t *testing.T) {
	ts, st := testServer(t)
	srv := ts.Config.Handler.(*Server)
	ctx := context.Background()
	author, _ := st.CreateUser(ctx, "inboxauthor", "inboxauthor@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7502, Slug: "inbox-game", Title: "Inbox Game", Category: "main_game", ReleaseStatus: "released"})
	log, _ := st.CreateLog(ctx, store.LogInput{UserID: author.ID, GameID: game.ID, Name: "Inbox post", Status: "completed", Review: "Inbox body"})
	if err := st.EnsurePosts(ctx, []int64{author.ID}); err != nil {
		t.Fatal(err)
	}
	p, err := st.PostForSubject(ctx, "log", log.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	apID := "http://example.test/posts/" + itoa(p.ID)
	st.SetPostAPID(ctx, p.ID, apID)
	r := httptest.NewRequest("POST", "/users/inboxauthor/inbox", nil)
	actor := &store.RemoteActor{ActorURI: "https://remote.test/users/bob", Domain: "remote.test", Username: "bob", Name: "Bob", Inbox: "https://remote.test/users/bob/inbox"}
	obj, _ := json.Marshal(apID)

	srv.handleRemoteLike(r, &federation.InboxActivity{ID: "like-1", Type: "Like", Actor: actor.ActorURI, Object: obj}, actor)
	liked, _ := st.PostByID(ctx, p.ID, 0)
	if liked.Likes != 1 {
		t.Fatalf("remote like = %d", liked.Likes)
	}
	srv.handleRemoteAnnounce(r, &federation.InboxActivity{ID: "announce-1", Type: "Announce", Actor: actor.ActorURI, Object: obj}, actor)
	boosted, _ := st.PostByID(ctx, p.ID, 0)
	if boosted.Boosts != 1 {
		t.Fatalf("remote boost = %d", boosted.Boosts)
	}

	undoLike, _ := json.Marshal(map[string]any{"type": "Like", "actor": actor.ActorURI, "object": apID})
	srv.handleRemoteUndo(r, &federation.InboxActivity{ID: "undo-1", Type: "Undo", Actor: actor.ActorURI, Object: undoLike}, nil, actor)
	unliked, _ := st.PostByID(ctx, p.ID, 0)
	if unliked.Likes != 0 {
		t.Fatalf("remote undo like = %d", unliked.Likes)
	}

	replyID := "https://remote.test/users/bob/statuses/1"
	reply, _ := json.Marshal(map[string]any{
		"id": replyID, "type": "Note", "content": "remote reply", "inReplyTo": apID, "attributedTo": actor.ActorURI,
	})
	srv.handleRemoteCreate(r, &federation.InboxActivity{ID: "create-1", Type: "Create", Actor: actor.ActorURI, Object: reply}, author)
	comments, _ := st.CommentsFor(ctx, "post", p.ID)
	if len(comments) != 1 || comments[0].Body != "remote reply" || comments[0].APID != replyID {
		t.Fatalf("remote reply = %+v", comments)
	}

	updated, _ := json.Marshal(map[string]any{
		"id": replyID, "type": "Note", "content": "edited reply", "inReplyTo": apID, "attributedTo": actor.ActorURI,
	})
	srv.handleRemoteUpdate(r, &federation.InboxActivity{ID: "update-1", Type: "Update", Actor: actor.ActorURI, Object: updated})
	comments, _ = st.CommentsFor(ctx, "post", p.ID)
	if len(comments) != 1 || comments[0].Body != "edited reply" {
		t.Fatalf("remote update = %+v", comments)
	}

	deleted, _ := json.Marshal(replyID)
	srv.handleRemoteDelete(r, &federation.InboxActivity{ID: "delete-1", Type: "Delete", Actor: actor.ActorURI, Object: deleted})
	c, _ := st.CommentByAPID(ctx, replyID)
	if c == nil || !c.Deleted {
		t.Fatalf("remote delete = %+v", c)
	}

	srv.handleRemoteFlag(r, &federation.InboxActivity{ID: "flag-1", Type: "Flag", Actor: actor.ActorURI, Object: obj}, actor)
	reports, _ := st.Reports(ctx, "open")
	if len(reports) != 1 || reports[0].TargetType != "post" || reports[0].RemoteReporter != "Bob" || reports[0].SourceInstance != "remote.test" {
		t.Fatalf("inbound flag report = %+v", reports)
	}
}
