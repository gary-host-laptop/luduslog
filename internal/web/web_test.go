package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
		MaxCommentChars: 5000,
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
