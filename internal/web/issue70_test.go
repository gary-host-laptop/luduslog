package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func issue70WebList(t *testing.T, st *store.Store, userID int64, title, privacy string) *models.List {
	t.Helper()
	l, err := st.CreateList(context.Background(), store.ListInput{UserID: userID, Title: title, Privacy: privacy})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestListsDiscoverAudienceSortAndPreservation(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "discoveralice", "discoveralice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "discoverbob", "discoverbob@example.test", "hash", false)
	carol, _ := st.CreateUser(ctx, "discovercarol", "discovercarol@example.test", "hash", false)
	dave, _ := st.CreateUser(ctx, "discoverdave", "discoverdave@example.test", "hash", false)
	eve, _ := st.CreateUser(ctx, "discovereve", "discovereve@example.test", "hash", false)
	for _, pair := range [][2]int64{{alice.ID, bob.ID}, {bob.ID, alice.ID}, {alice.ID, carol.ID}} {
		if err := st.Follow(ctx, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	issue70WebList(t, st, bob.ID, "Bob Public", "public")
	issue70WebList(t, st, bob.ID, "Bob Personal", "personal")
	issue70WebList(t, st, bob.ID, "Bob Secret", "private")
	issue70WebList(t, st, bob.ID, "Bob Link Only", "unlisted")
	issue70WebList(t, st, carol.ID, "Carol Public", "public")
	issue70WebList(t, st, carol.ID, "Carol Personal", "personal")
	issue70WebList(t, st, dave.ID, "Dave Public", "public")

	code, body := getBody(t, http.DefaultClient, ts.URL+"/lists")
	if code != http.StatusOK {
		t.Fatalf("/lists = %d", code)
	}
	for _, want := range []string{
		"tabs tabs-reviews", "Everyone", "Friends", "Following",
		"Popularity", "Date", `class="sort-btn sort-dir-btn"`,
		"3 lists",
		`href="/lists?tab=all&amp;sort=popularity&amp;dir=desc"`,
		"Bob Public", "Carol Public", "Dave Public",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/lists missing %q", want)
		}
	}
	for _, hidden := range []string{"Bob Personal", "Bob Secret", "Bob Link Only", "Carol Personal"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("Everyone rendered non-public list %q", hidden)
		}
	}
	if !strings.Contains(body, `href="/lists?tab=all&amp;sort=popularity&amp;dir=desc" class="sort-btn on"`) {
		t.Fatal("default sort is not popularity descending")
	}

	_, anonFriends := getBody(t, http.DefaultClient, ts.URL+"/lists?tab=friends")
	if !strings.Contains(anonFriends, "Log in to see lists") || strings.Contains(anonFriends, "Bob Public") {
		t.Fatalf("signed-out friends empty state wrong: %s", anonFriends)
	}

	client, _ := authedClient(t, ts, st, alice)
	_, friends := getBody(t, client, ts.URL+"/lists?tab=friends")
	for _, want := range []string{"Bob Public", "Bob Personal"} {
		if !strings.Contains(friends, want) {
			t.Fatalf("Friends missing %q", want)
		}
	}
	for _, hidden := range []string{"Carol Public", "Carol Personal", "Bob Secret", "Bob Link Only"} {
		if strings.Contains(friends, hidden) {
			t.Fatalf("Friends rendered %q", hidden)
		}
	}

	_, following := getBody(t, client, ts.URL+"/lists?tab=following")
	for _, want := range []string{"Bob Public", "Bob Personal", "Carol Public", "Carol Personal"} {
		if !strings.Contains(following, want) {
			t.Fatalf("Following missing %q", want)
		}
	}
	if strings.Contains(following, "Dave Public") || strings.Contains(following, "Bob Secret") {
		t.Fatal("Following rendered a non-followee or private list")
	}

	eveClient, _ := authedClient(t, ts, st, eve)
	_, emptyFriends := getBody(t, eveClient, ts.URL+"/lists?tab=friends")
	if !strings.Contains(emptyFriends, "No lists from friends yet") {
		t.Fatalf("logged-in friends empty state wrong: %s", emptyFriends)
	}

	if strings.Contains(body, "sort=entries") {
		t.Fatal("/lists still offers the entries sort")
	}
	_, selected := getBody(t, client, ts.URL+"/lists?tab=following&sort=date&dir=asc")
	for _, want := range []string{
		`href="/lists?tab=following&amp;sort=date&amp;dir=asc"`,
		`href="/lists?tab=all&amp;sort=date&amp;dir=asc"`,
		`href="/lists?tab=following&amp;sort=date&amp;dir=desc"`,
	} {
		if !strings.Contains(selected, want) {
			t.Fatalf("selected query not preserved: missing %q", want)
		}
	}

	_, oldest := getBody(t, client, ts.URL+"/lists?sort=oldest")
	if !strings.Contains(oldest, `href="/lists?tab=all&amp;sort=date&amp;dir=asc" class="sort-btn on"`) {
		t.Fatal("legacy oldest URL did not select date ascending")
	}
	_, likes := getBody(t, client, ts.URL+"/lists?sort=likes")
	if !strings.Contains(likes, `href="/lists?tab=all&amp;sort=popularity&amp;dir=desc" class="sort-btn on"`) {
		t.Fatal("legacy likes URL did not select popularity descending")
	}
}

func TestListsDiscoverPaginationWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "pagealice", "pagealice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "pagebob", "pagebob@example.test", "hash", false)
	if err := st.Follow(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 31; i++ {
		issue70WebList(t, st, bob.ID, "Paged List "+itoa(int64(i)), "public")
	}
	client, _ := authedClient(t, ts, st, alice)

	_, body := getBody(t, client, ts.URL+"/lists?tab=following&sort=date&dir=asc")
	if strings.Count(body, `class="list-card"`) != 30 {
		t.Fatalf("first page rendered %d cards", strings.Count(body, `class="list-card"`))
	}
	for _, want := range []string{
		"31 lists",
		`href="?dir=asc&amp;sort=date&amp;tab=following&page=2"`,
	} {
		if !strings.Contains(body, want) {
			idx := strings.Index(body, "page=2")
			if idx > 200 {
				t.Fatalf("first page missing %q; pager near %q", want, body[idx-200:idx+30])
			}
			t.Fatalf("first page missing %q", want)
		}
	}

	_, page2 := getBody(t, client, ts.URL+"/lists?tab=following&sort=date&dir=asc&page=2")
	if strings.Count(page2, `class="list-card"`) != 1 {
		t.Fatalf("second page rendered %d cards", strings.Count(page2, `class="list-card"`))
	}
	if !strings.Contains(page2, "2 / 2") {
		t.Fatal("second page did not render pager state")
	}
	if !strings.Contains(page2, `href="?dir=asc&amp;sort=date&amp;tab=following&page=1"`) {
		t.Fatal("second page did not preserve query in Prev link")
	}
}
