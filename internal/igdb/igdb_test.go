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
	if len(g.Videos) != 1 || g.Videos[0] != "abc123" || g.CoverURL != "https://images.igdb.com/t_cover_big_2x/cover.jpg" {
		t.Fatalf("media mapping failed: %+v", g)
	}
	if len(g.AltNames) != 1 || g.AltNames[0] != "Celeste Classic" {
		t.Fatalf("alternative names: %+v", g.AltNames)
	}
}

func TestMapGameVariantsAndRelations(t *testing.T) {
	r := apiGame{
		ID: 2001, Name: "Base Game - Deluxe Edition", Slug: "base-game-deluxe",
		VersionParent: 2000, ParentGame: 2000, Category: 10,
		GameType:      &apiGameType{ID: 10, Type: "Expanded Game"},
		Remakes:       []apiNamed{{ID: 3001, Name: "Base Game Remake", Slug: "base-game-remake"}},
		Ports:         []apiNamed{{ID: 3002, Name: "Base Game Portable", Slug: "base-game-portable"}},
		Bundles:       []apiNamed{{ID: 3003, Name: "Base Bundle", Slug: "base-bundle"}},
		ExpandedGames: []apiNamed{{ID: 3004, Name: "Base Game Update", Slug: "base-game-update"}},
	}
	g := mapGame(r)
	if g.ParentIGDBID != 2000 {
		t.Fatalf("parent id = %d, want 2000", g.ParentIGDBID)
	}
	if g.EditionName != "Deluxe Edition" {
		t.Fatalf("edition name = %q", g.EditionName)
	}
	if len(g.Remakes) != 1 || g.Remakes[0].IGDBID != 3001 || g.Remakes[0].Title != "Base Game Remake" || g.Remakes[0].Slug != "base-game-remake" {
		t.Fatalf("remakes = %+v", g.Remakes)
	}
	if len(g.Ports) != 1 || g.Ports[0].IGDBID != 3002 || len(g.Bundles) != 1 || g.Bundles[0].IGDBID != 3003 {
		t.Fatalf("ports/bundles = %+v / %+v", g.Ports, g.Bundles)
	}
	if g.Category != "expanded_game" {
		t.Fatalf("game_type should win over deprecated category, got %q", g.Category)
	}
	if len(g.Updates) != 1 || g.Updates[0].IGDBID != 3004 {
		t.Fatalf("updates = %+v", g.Updates)
	}
	// The deprecated category is still honoured when game_type is absent.
	if g := mapGame(apiGame{ID: 2004, Category: 11}); g.Category != "port" {
		t.Fatalf("legacy category fallback = %q", g.Category)
	}
	// version_parent is the fallback when parent_game is absent.
	g = mapGame(apiGame{ID: 2002, Name: "Portable Edition", VersionParent: 2000})
	if g.ParentIGDBID != 2000 || g.EditionName != "Portable Edition" {
		t.Fatalf("version parent fallback = %d / %q", g.ParentIGDBID, g.EditionName)
	}
	// Base games carry no parent.
	if g := mapGame(apiGame{ID: 2003, Name: "Plain", Category: 0}); g.ParentIGDBID != 0 || g.EditionName != "" {
		t.Fatalf("base game got edition data: %+v", g)
	}
}

func TestTimeToBeatModel(t *testing.T) {
	got := timeToBeatModel(apiTimeToBeat{Hastily: 19800, Normally: 36000, Completely: 54000})
	if got == nil || got.Hastily != 19800 || got.Normally != 36000 || got.Completely != 54000 {
		t.Fatalf("time to beat not mapped: %+v", got)
	}
	if timeToBeatModel(apiTimeToBeat{}) != nil {
		t.Fatal("empty time to beat should not create a record")
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
