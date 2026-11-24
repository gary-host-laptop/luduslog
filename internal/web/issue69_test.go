package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"luduslog/internal/models"
)

func webReleaseDate(days int) string {
	return time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02")
}

func homeSection(t *testing.T, body, heading string) string {
	t.Helper()
	start := strings.Index(body, "<h2>"+heading+"</h2>")
	if start < 0 {
		t.Fatalf("section %q not found", heading)
	}
	end := strings.Index(body[start:], "</section>")
	if end < 0 {
		t.Fatalf("section %q is not closed", heading)
	}
	return body[start : start+end]
}

func TestHomeComingSoonAndRecentlyAnticipatedRails(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()

	save := func(slug, title string, days int, adult bool) *models.Game {
		g, err := st.SaveGame(ctx, &models.Game{Slug: slug, Title: title, ReleaseDate: webReleaseDate(days), Adult: adult})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	save("tomorrow", "Coming Tomorrow", 1, false)
	later := save("later", "Coming Later", 10, false)
	adult := save("adult", "Adult Upcoming", 5, true)
	released := save("released", "Released Already", -1, false)
	buzz := save("buzz", "Hyped Upcoming", 12, false)
	old := save("old", "Old Wishlisted", 14, false)

	alice, err := st.CreateUser(ctx, "alice", "alice@example.test", "password123", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "bob", "bob@example.test", "password123", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*models.User{alice, bob} {
		if err := st.SetStatusFlag(ctx, u.ID, buzz.ID, "wishlist", true); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetStatusFlag(ctx, alice.ID, old.ID, "wishlist", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE statuses SET wishlist_at=? WHERE user_id=? AND game_id=?`,
		time.Now().UTC().AddDate(0, 0, -90).Format(time.RFC3339), alice.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatusFlag(ctx, alice.ID, released.ID, "wishlist", true); err != nil {
		t.Fatal(err)
	}

	_, body := getBody(t, ts.Client(), ts.URL+"/")
	coming := homeSection(t, body, "Coming soon")
	for _, want := range []string{"Coming Tomorrow", "Coming Later", "Tomorrow", "In 10 days"} {
		if !strings.Contains(coming, want) {
			t.Fatalf("Coming soon missing %q: %s", want, coming)
		}
	}
	if strings.Contains(coming, "Adult Upcoming") {
		t.Fatal("adult game leaked into Coming soon for anonymous viewer")
	}
	if strings.Contains(coming, "Released Already") {
		t.Fatal("released game rendered in Coming soon")
	}
	anticipated := homeSection(t, body, "Recently anticipated")
	buzzAt := strings.Index(anticipated, "Hyped Upcoming")
	oldAt := strings.Index(anticipated, "Old Wishlisted")
	if buzzAt < 0 || oldAt < 0 || buzzAt > oldAt {
		t.Fatalf("Recently anticipated not ordered by recent activity: %s", anticipated)
	}
	if strings.Contains(anticipated, "Released Already") || strings.Contains(anticipated, "Adult Upcoming") {
		t.Fatalf("ineligible game in Recently anticipated: %s", anticipated)
	}

	if _, err := st.DB.ExecContext(ctx, `UPDATE users SET adult_optin=1 WHERE id=?`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatusFlag(ctx, alice.ID, later.ID, "wishlist", true); err != nil {
		t.Fatal(err)
	}
	client, _ := authedClient(t, ts, st, alice)
	_, adultBody := getBody(t, client, ts.URL+"/")
	adultComing := homeSection(t, adultBody, "Coming soon")
	if !strings.Contains(adultComing, "Adult Upcoming") {
		t.Fatalf("adult opt-in did not unlock adult Coming soon: %s", adultComing)
	}
	if !strings.Contains(adultComing, "Wishlisted") {
		t.Fatalf("wishlisted Coming soon card missing marker: %s", adultComing)
	}
	_ = adult
}

func TestHomeRailsHiddenWhenEmpty(t *testing.T) {
	ts, _ := testServer(t)
	_, body := getBody(t, ts.Client(), ts.URL+"/")
	if strings.Contains(body, "Coming soon") {
		t.Fatal("empty Coming soon rail rendered")
	}
	if strings.Contains(body, "Recently anticipated") {
		t.Fatal("empty Recently anticipated rail rendered")
	}
}
