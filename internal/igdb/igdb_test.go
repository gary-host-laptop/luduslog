package igdb

import (
	"testing"
	"time"
)

func TestMapGameCompleteMetadata(t *testing.T) {
	past := time.Now().AddDate(-1, 0, 0).Unix()
	r := apiGame{
		ID: 1029, Name: "Celeste", Slug: "celeste", Summary: "A platformer.", FirstReleaseDate: past,
		Category: 0,
		Cover: &struct {
			URL string `json:"url"`
		}{URL: "//images.igdb.com/t_thumb/cover.jpg"},
		Platforms: []apiNamed{{ID: 6, Name: "PC (Microsoft Windows)"}, {ID: 130, Name: "Nintendo Switch"}},
		Genres: []struct {
			Name string `json:"name"`
		}{{Name: "Platformer"}},
		ReleaseDates: []apiReleaseDate{
			{Platform: &apiNamed{ID: 6, Name: "PC (Microsoft Windows)"}, Date: past, Region: 1, Status: 0},
		},
		Themes:      []apiNamed{{Name: "Drama"}},
		GameModes:   []apiNamed{{Name: "Single player"}},
		Screenshots: []apiNamed{{URL: "//images.igdb.com/t_thumb/shot.jpg"}},
		Videos:      []apiNamed{{VideoID: "abc123"}},
		Alternative: []apiNamed{{Name: "Celeste Classic"}},
		Collections: []apiNamed{{Name: "Celeste Collection"}},
		RatingCount: 42,
		InvolvedCompanies: []struct {
			Developer bool `json:"developer"`
			Publisher bool `json:"publisher"`
			Company   struct {
				Name string `json:"name"`
			} `json:"company"`
		}{},
	}
	g := mapGame(r)
	if g.IGDBID != 1029 || g.Slug != "celeste" || g.Category != "main_game" {
		t.Fatalf("basic mapping failed: %+v", g)
	}
	if len(g.Platforms) != 2 || g.Platforms[0].IGDBID != 6 {
		t.Fatalf("platform IDs not mapped: %+v", g.Platforms)
	}
	if len(g.ReleaseDates) != 1 || g.ReleaseDates[0].PlatformID != 6 || g.ReleaseDates[0].Region != "Europe" {
		t.Fatalf("release date not mapped: %+v", g.ReleaseDates)
	}
	if g.ReleaseDates[0].Date == "" || g.ReleaseDates[0].Human == "" {
		t.Fatalf("release date values missing: %+v", g.ReleaseDates[0])
	}
	if g.Franchise != "Celeste Collection" || len(g.Themes) != 1 || len(g.Modes) != 1 {
		t.Fatalf("collection/theme/mode mapping failed: %+v", g)
	}
	if len(g.Screenshots) != 1 || g.Screenshots[0] != "https://images.igdb.com/t_screenshot_big/shot.jpg" {
		t.Fatalf("screenshot URL: %+v", g.Screenshots)
	}
	if len(g.Videos) != 1 || g.Videos[0] != "abc123" || g.CoverURL != "https://images.igdb.com/t_cover_big/cover.jpg" {
		t.Fatalf("media mapping failed: %+v", g)
	}
	if len(g.AltNames) != 1 || g.AltNames[0] != "Celeste Classic" {
		t.Fatalf("alternative names: %+v", g.AltNames)
	}
}

func TestCategoryAndReleaseStatusNames(t *testing.T) {
	for in, want := range map[int]string{0: "main_game", 1: "dlc_addon", 8: "remake", 9: "remaster", 11: "port", 99: "main_game"} {
		if got := categoryName(in); got != want {
			t.Errorf("categoryName(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int]string{0: "released", 3: "early_access", 5: "cancelled", 7: "delisted"} {
		if got := releaseStatusName(in); got != want {
			t.Errorf("releaseStatusName(%d) = %q, want %q", in, got, want)
		}
	}
}
