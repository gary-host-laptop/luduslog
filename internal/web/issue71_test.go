package web

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func TestCanonicalListCardCSS(t *testing.T) {
	css, err := os.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--cover-w: 120px; --cover-h: 160px; --cover-stack: calc(var(--cover-w) * 3)",
		"display: flex; flex-direction: column; gap: .55rem;",
		"aspect-ratio: 3 / 4;",
		".list-covers { position: relative;",
		"width: var(--cover-stack); min-height: var(--cover-h);",
		".list-covers-empty {",
		".list-card-title { margin: 0; font-size: 1.15rem; line-height: 1.25;",
		".list-card-tags { flex-wrap: nowrap; overflow: hidden; margin: auto 0 0; min-height: 1.6rem; }",
		".list-card-widget .list-covers { width: auto; height: auto; min-height: 0; }",
	} {
		if !strings.Contains(string(css), want) {
			t.Errorf("list card CSS missing %q", want)
		}
	}
}

// cardFor returns the markup of the list card whose title is title.
func cardFor(t *testing.T, body, title string) string {
	t.Helper()
	titleAt := strings.Index(body, ">"+title+"<")
	if titleAt < 0 {
		t.Fatalf("card %q not found", title)
	}
	start := strings.LastIndex(body[:titleAt], `<div class="list-card"`)
	if start < 0 {
		t.Fatalf("card %q has no list-card wrapper", title)
	}
	tagsAt := strings.Index(body[start:], `class="list-card-tags`)
	if tagsAt < 0 {
		t.Fatalf("card %q has no tag row", title)
	}
	closeAt := strings.Index(body[start+tagsAt:], "</div>")
	if closeAt < 0 {
		t.Fatalf("card %q is not closed", title)
	}
	return body[start : start+tagsAt+closeAt+len("</div>")]
}

func TestCanonicalListCardSlots(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	owner, err := st.CreateUser(ctx, "cardowner", "cardowner@example.test", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	rich, err := st.CreateList(ctx, store.ListInput{
		UserID:      owner.ID,
		Title:       "Slotted Discovery",
		Description: "A description long enough to fill the reserved description slot on a canonical list card.",
		Tags:        []string{"coop", "retro", "puzzle", "indie", "long"},
		Privacy:     "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		slug := "slot-game-" + itoa(int64(i))
		g, err := st.SaveGame(ctx, &models.Game{Slug: slug, Title: "Slot Game " + itoa(int64(i))})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddListEntry(ctx, rich.ID, g.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateList(ctx, store.ListInput{UserID: owner.ID, Title: "Uncovered Discovery", Privacy: "public"}); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}

	pages := []string{
		"/lists",
		"/",
		"/search?q=Discovery&tab=lists",
		"/users/cardowner?tab=lists",
	}
	for _, path := range pages {
		code, body := getBody(t, http.DefaultClient, ts.URL+path)
		if code != http.StatusOK {
			t.Fatalf("%s = %d", path, code)
		}
		for _, title := range []string{"Slotted Discovery", "Uncovered Discovery"} {
			card := cardFor(t, body, title)
			for _, want := range []string{
				`class="list-card-title"`,
				`class="list-card-meta muted"`,
				`class="list-card-tags pill-row"`,
			} {
				if !strings.Contains(card, want) {
					t.Errorf("%s card %q missing %s", path, title, want)
				}
			}
			if strings.Contains(card, "list-card-desc") {
				t.Errorf("%s card %q still renders a description slot", path, title)
			}
			if path != "/users/cardowner?tab=lists" {
				for _, want := range []string{
					`class="list-card-author" href="/users/cardowner"`,
					`class="list-card-avatar"`,
				} {
					if !strings.Contains(card, want) {
						t.Errorf("%s card %q missing %s", path, title, want)
					}
				}
			}
			titleAt := strings.Index(card, `class="list-card-title"`)
			coversAt := strings.Index(card, `class="list-covers`)
			metaAt := strings.Index(card, `class="list-card-meta muted"`)
			if titleAt < 0 || coversAt < titleAt || metaAt < coversAt {
				t.Errorf("%s card %q is not ordered title, covers, then author", path, title)
			}
		}

		richCard := cardFor(t, body, "Slotted Discovery")
		if strings.Contains(richCard, "list-covers-empty") {
			t.Errorf("%s rendered an empty cover slot for a list with covers", path)
		}
		coverPart := strings.Split(richCard, `class="list-card-meta`)[0]
		if got := strings.Count(coverPart, "<img "); got != 5 {
			t.Errorf("%s rendered %d covers, want 5", path, got)
		}
		t.Logf("RICH CARD: %s", richCard)
		if !strings.Contains(richCard, `<span class="pill">&#43;2</span>`) {
			t.Errorf("%s did not clamp tags to three plus a +N pill", path)
		}
		if strings.Contains(richCard, "likes") {
			t.Errorf("%s card still renders the like counter", path)
		}

		emptyCard := cardFor(t, body, "Uncovered Discovery")
		if !strings.Contains(emptyCard, `class="list-covers list-covers-empty"`) {
			t.Errorf("%s missing the empty cover placeholder for a list without covers", path)
		}
		if !strings.Contains(emptyCard, "list-cover-fallback") {
			t.Errorf("%s missing the fallback icon for an empty cover area", path)
		}
	}
}

func TestTagPreviewHelper(t *testing.T) {
	if got := tagPreview(nil); len(got) != 0 {
		t.Fatalf("tagPreview(nil) = %v", got)
	}
	if got := tagPreview([]string{"a", "b", "c"}); len(got) != 3 {
		t.Fatalf("tagPreview with three tags = %v", got)
	}
	got := tagPreview([]string{"a", "b", "c", "d", "e"})
	want := []string{"a", "b", "c", "+2"}
	if len(got) != len(want) {
		t.Fatalf("tagPreview with five tags = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tagPreview with five tags = %v, want %v", got, want)
		}
	}
}
