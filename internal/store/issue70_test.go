package store

import (
	"context"
	"testing"

	"luduslog/internal/models"
)

func issue70List(t *testing.T, st *Store, userID int64, title, privacy string) *models.List {
	t.Helper()
	l, err := st.CreateList(context.Background(), ListInput{UserID: userID, Title: title, Privacy: privacy})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func issue70Titles(lists []*models.List) []string {
	out := make([]string, len(lists))
	for i, l := range lists {
		out[i] = l.Title
	}
	return out
}

func TestListsFeedAudienceVisibility(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "listalice", "listalice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "listbob", "listbob@example.test", "hash", false)
	carol, _ := st.CreateUser(ctx, "listcarol", "listcarol@example.test", "hash", false)
	dave, _ := st.CreateUser(ctx, "listdave", "listdave@example.test", "hash", false)
	erin, _ := st.CreateUser(ctx, "listerin", "listerin@example.test", "hash", false)
	eve, _ := st.CreateUser(ctx, "listeve", "listeve@example.test", "hash", false)

	for _, pair := range [][2]int64{{alice.ID, bob.ID}, {bob.ID, alice.ID}, {alice.ID, carol.ID}} {
		if err := st.Follow(ctx, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}

	bobPublic := issue70List(t, st, bob.ID, "Bob Public", "public")
	bobPersonal := issue70List(t, st, bob.ID, "Bob Personal", "personal")
	issue70List(t, st, bob.ID, "Bob Private", "private")
	issue70List(t, st, bob.ID, "Bob Unlisted", "unlisted")
	carolPublic := issue70List(t, st, carol.ID, "Carol Public", "public")
	carolPersonal := issue70List(t, st, carol.ID, "Carol Personal", "personal")
	issue70List(t, st, carol.ID, "Carol Private", "private")
	issue70List(t, st, carol.ID, "Carol Unlisted", "unlisted")
	davePublic := issue70List(t, st, dave.ID, "Dave Public", "public")
	issue70List(t, st, erin.ID, "Erin Public", "public")
	deleted := issue70List(t, st, bob.ID, "Bob Deleted", "public")
	if err := st.DeleteList(ctx, deleted.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE users SET deactivated_at=? WHERE id=?`, Now(), erin.ID); err != nil {
		t.Fatal(err)
	}

	all, total, err := st.ListsFeedSorted(ctx, alice.ID, "all", "date", "desc", 30, 0)
	if err != nil || total != 3 {
		t.Fatalf("everyone = %v total=%d err=%v, want 3", issue70Titles(all), total, err)
	}
	if got := issue70Titles(all); len(got) != 3 || got[0] != davePublic.Title {
		// Newest first: dave, carol, bob were created in that order.
		t.Fatalf("everyone order = %v", got)
	}
	for _, l := range all {
		switch l.Title {
		case "Bob Personal", "Bob Private", "Bob Unlisted", "Carol Personal", "Carol Private",
			"Carol Unlisted", "Erin Public", "Bob Deleted":
			t.Fatalf("ineligible list %q in Everyone", l.Title)
		}
	}
	_ = bobPublic
	_ = carolPublic

	friends, total, err := st.ListsFeedSorted(ctx, alice.ID, "friends", "date", "desc", 30, 0)
	if err != nil || total != 2 || len(friends) != 2 {
		t.Fatalf("friends = %v total=%d err=%v", issue70Titles(friends), total, err)
	}
	friendIDs := map[int64]bool{friends[0].ID: true, friends[1].ID: true}
	if !friendIDs[bobPublic.ID] || !friendIDs[bobPersonal.ID] {
		t.Fatalf("friends = %v, want Bob public/personal", issue70Titles(friends))
	}

	following, total, err := st.ListsFeedSorted(ctx, alice.ID, "following", "date", "desc", 30, 0)
	if err != nil || total != 4 || len(following) != 4 {
		t.Fatalf("following = %v total=%d err=%v", issue70Titles(following), total, err)
	}
	followIDs := map[int64]bool{}
	for _, l := range following {
		followIDs[l.ID] = true
	}
	for _, want := range []int64{bobPublic.ID, bobPersonal.ID, carolPublic.ID, carolPersonal.ID} {
		if !followIDs[want] {
			t.Fatalf("following missing list %d: %v", want, issue70Titles(following))
		}
	}

	if got, total, err := st.ListsFeedSorted(ctx, 0, "friends", "popularity", "desc", 30, 0); err != nil || total != 0 || len(got) != 0 {
		t.Fatalf("signed-out friends = %v total=%d err=%v", issue70Titles(got), total, err)
	}
	if got, total, err := st.ListsFeedSorted(ctx, eve.ID, "following", "popularity", "desc", 30, 0); err != nil || total != 0 || len(got) != 0 {
		t.Fatalf("empty follow set = %v total=%d err=%v", issue70Titles(got), total, err)
	}
}

func TestListsFeedSortKeysAndDirections(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	owner, _ := st.CreateUser(ctx, "sortowner", "sortowner@example.test", "hash", false)
	likers := make([]*models.User, 4)
	for i := range likers {
		likers[i], _ = st.CreateUser(ctx, "sortliker"+itoa(i), "sortliker"+itoa(i)+"@example.test", "hash", false)
	}

	a := issue70List(t, st, owner.ID, "A Popular", "public")
	b := issue70List(t, st, owner.ID, "B Entries", "public")
	c := issue70List(t, st, owner.ID, "C Tie", "public")

	for i := 0; i < 3; i++ {
		if err := st.LikeList(ctx, a.ID, likers[i].ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.LikeList(ctx, b.ID, likers[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.LikeList(ctx, c.ID, likers[1].ID, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		g, err := st.SaveGame(ctx, &models.Game{IGDBID: int64(-9100 - i), Slug: "sort-game-" + itoa(i), Title: "Sort Game " + itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddListEntry(ctx, b.ID, g.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		id               int64
		created, updated string
	}{
		{a.ID, "2026-01-01T12:00:00Z", "2026-01-03T12:00:00Z"},
		{b.ID, "2026-01-02T12:00:00Z", "2026-01-02T12:00:00Z"},
		{c.ID, "2026-01-03T12:00:00Z", "2026-01-01T12:00:00Z"},
	} {
		if _, err := st.DB.ExecContext(ctx, `UPDATE lists SET created_at=?,updated_at=? WHERE id=?`, row.created, row.updated, row.id); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		sort, dir string
		want      []string
	}{
		{"popularity", "desc", []string{"A Popular", "B Entries", "C Tie"}},
		{"popularity", "asc", []string{"C Tie", "B Entries", "A Popular"}},
		{"date", "desc", []string{"C Tie", "B Entries", "A Popular"}},
		{"date", "asc", []string{"A Popular", "B Entries", "C Tie"}},
		{"entries", "desc", []string{"B Entries", "A Popular", "C Tie"}},
		{"entries", "asc", []string{"C Tie", "A Popular", "B Entries"}},
	}
	for _, tc := range cases {
		got, total, err := st.ListsFeedSorted(ctx, owner.ID, "all", tc.sort, tc.dir, 30, 0)
		if err != nil || total != 3 {
			t.Fatalf("%s %s total=%d err=%v", tc.sort, tc.dir, total, err)
		}
		titles := issue70Titles(got)
		for i := range tc.want {
			if titles[i] != tc.want[i] {
				t.Fatalf("%s %s = %v, want %v", tc.sort, tc.dir, titles, tc.want)
			}
		}
	}
}

func TestListsFeedPagination(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	owner, _ := st.CreateUser(ctx, "pageowner", "pageowner@example.test", "hash", false)
	for i := 0; i < 35; i++ {
		issue70List(t, st, owner.ID, "Page List "+itoa(i), "public")
	}

	first, total, err := st.ListsFeedSorted(ctx, owner.ID, "all", "popularity", "desc", 30, 0)
	if err != nil || total != 35 || len(first) != 30 {
		t.Fatalf("first page len=%d total=%d err=%v", len(first), total, err)
	}
	second, total, err := st.ListsFeedSorted(ctx, owner.ID, "all", "popularity", "desc", 30, 30)
	if err != nil || total != 35 || len(second) != 5 {
		t.Fatalf("second page len=%d total=%d err=%v", len(second), total, err)
	}
	seen := map[int64]bool{}
	for _, l := range append(first, second...) {
		if seen[l.ID] {
			t.Fatalf("list %d appeared on both pages", l.ID)
		}
		seen[l.ID] = true
	}
}
