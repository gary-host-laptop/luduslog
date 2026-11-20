package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

func mustSaveGame(t *testing.T, st *store.Store, g *models.Game) *models.Game {
	t.Helper()
	saved, err := st.SaveGame(context.Background(), g)
	if err != nil {
		t.Fatalf("save game %q: %v", g.Title, err)
	}
	return saved
}

func TestEditionImportAndRelatedGames(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()

	base := mustSaveGame(t, st, &models.Game{
		IGDBID: -901, Slug: "base-game", Title: "Base Game", Franchise: "Base Game",
		Category: "main_game", ReleaseStatus: "released", ReleaseYear: 2020,
	})
	// The parent row exists, so SaveGame can resolve the link immediately.
	edition := mustSaveGame(t, st, &models.Game{
		IGDBID: -902, Slug: "base-game-deluxe", Title: "Base Game - Deluxe Edition",
		ParentIGDBID: -901, EditionName: "Deluxe Edition", Category: "main_game",
		ReleaseStatus: "released",
	})
	if edition.ParentID == nil || *edition.ParentID != base.ID {
		t.Fatalf("edition parent = %v, want %d", edition.ParentID, base.ID)
	}
	// A sibling with the same franchise becomes the Series entry.
	other := mustSaveGame(t, st, &models.Game{
		IGDBID: -903, Slug: "base-game-2", Title: "Base Game 2", Franchise: "Base Game",
		Category: "main_game", ReleaseStatus: "released",
	})
	// Remake reference points at a game that is not imported yet.
	mustSaveGame(t, st, &models.Game{
		IGDBID: -901, Slug: "base-game", Title: "Base Game", Franchise: "Base Game",
		Category: "main_game", ReleaseStatus: "released", ReleaseYear: 2020,
		Remakes: []models.Game{{IGDBID: -904, Slug: "base-game-remake", Title: "Base Game Remake"}},
	})
	remakeStub, err := st.GameByIGDB(ctx, -904)
	if err != nil || remakeStub.Title != "Base Game Remake" {
		t.Fatalf("remake stub = %+v, %v", remakeStub, err)
	}
	// Saving the real row reuses the stub row instead of duplicating it.
	real := mustSaveGame(t, st, &models.Game{IGDBID: -904, Slug: "base-game-remake", Title: "Base Game Remake", Category: "remake", ReleaseStatus: "released"})
	if real.ID != remakeStub.ID {
		t.Fatalf("stub was not reused: %d vs %d", real.ID, remakeStub.ID)
	}
	var stubs int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM games WHERE igdb_id=-904`).Scan(&stubs)
	if stubs != 1 {
		t.Fatalf("duplicate stub rows: %d", stubs)
	}

	detail, err := st.GameDetail(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Editions) != 1 || detail.Editions[0].ID != edition.ID {
		t.Fatalf("editions = %+v", detail.Editions)
	}
	if len(detail.Remakes) != 1 || detail.Remakes[0].ID != real.ID {
		t.Fatalf("remakes = %+v", detail.Remakes)
	}
	if len(detail.Series) != 1 || detail.Series[0].ID != other.ID {
		t.Fatalf("series = %+v", detail.Series)
	}

	code, body := getBody(t, http.DefaultClient, ts.URL+"/games/base-game")
	if code != 200 {
		t.Fatalf("game page = %d", code)
	}
	for _, want := range []string{"Related games", "Remakes", "Series", "Deluxe Edition", "Editions & extras"} {
		if !strings.Contains(body, want) {
			t.Errorf("game page missing %q", want)
		}
	}
	if strings.Contains(body, "<h3>Ports</h3>") || strings.Contains(body, "<h3>Bundles</h3>") {
		t.Error("empty related groups should be hidden")
	}
	if strings.Index(body, "Related games") > strings.Index(body, "<h2>Details</h2>") {
		t.Error("Related games should render above Details")
	}
	if strings.Index(body, "<h3>Series</h3>") > strings.Index(body, "<h3>Remakes</h3>") {
		t.Error("Series should come before Remakes")
	}
	rel := relatedSection(body)
	if !strings.Contains(rel, "<h3>Editions & extras</h3>") {
		t.Error("editions should render inside the related section")
	}
	if !strings.Contains(rel, `role="tablist"`) || strings.Count(rel, `role="tab"`) != 3 {
		t.Errorf("expected a tab per related group:\n%s", rel)
	}
	if strings.Count(rel, `role="tabpanel"`) != 3 || !strings.Contains(rel, `id="related-series"`) {
		t.Error("related groups should be tab panels")
	}

	// The games index keeps editions out.
	_, index := getBody(t, http.DefaultClient, ts.URL+"/games")
	if strings.Contains(index, "Base Game - Deluxe Edition") {
		t.Error("games index listed an edition as a base game")
	}
	// Empty related data renders no section.
	mustSaveGame(t, st, &models.Game{IGDBID: -905, Slug: "lonely", Title: "Lonely", Category: "main_game", ReleaseStatus: "released"})
	_, lonely := getBody(t, http.DefaultClient, ts.URL+"/games/lonely")
	if strings.Contains(lonely, "Related games") {
		t.Error("related section rendered without relations")
	}

	// Parent resolution also works when the base game arrived later.
	orphan := mustSaveGame(t, st, &models.Game{IGDBID: -906, Slug: "orphan-edition", Title: "Orphan - Edition", ParentIGDBID: -907, EditionName: "Edition", Category: "expanded_game", ReleaseStatus: "released"})
	if orphan.ParentID != nil {
		t.Fatal("orphan unexpectedly linked")
	}
	mustSaveGame(t, st, &models.Game{IGDBID: -907, Slug: "late-parent", Title: "Late Parent", Category: "main_game", ReleaseStatus: "released"})
	if err := st.ResolveGameParents(ctx); err != nil {
		t.Fatal(err)
	}
	orphan, _ = st.GameByID(ctx, orphan.ID)
	if orphan.ParentID == nil {
		t.Fatal("ResolveGameParents did not link the edition")
	}
}

// relatedSection returns the HTML of the related-games section, or "" if the
// page has none.
func relatedSection(body string) string {
	i := strings.Index(body, `id="related"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</section>")
	if j < 0 {
		return body[i:]
	}
	return body[i : i+j]
}

// TestUpdatesAndChainedPorts covers IGDB's base -> expanded game -> port
// chain: the update is its own group and its ports still reach the base page.
func TestUpdatesAndChainedPorts(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()

	base := mustSaveGame(t, st, &models.Game{
		IGDBID: -930, Slug: "update-base", Title: "Update Base", Category: "main_game", ReleaseStatus: "released",
	})
	update := mustSaveGame(t, st, &models.Game{
		IGDBID: -931, Slug: "update-base-final-cut", Title: "Update Base: Final Cut",
		ParentIGDBID: -930, EditionName: "Final Cut", Category: "expanded_game", ReleaseStatus: "released",
	})
	port := mustSaveGame(t, st, &models.Game{
		IGDBID: -932, Slug: "update-base-mobile", Title: "Update Base",
		ParentIGDBID: -931, EditionName: "Update Base", Category: "port", ReleaseStatus: "released",
	})
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO game_relations(game_id,related_id,kind) VALUES(?,?,?)`, update.ID, port.ID, "port"); err != nil {
		t.Fatal(err)
	}

	detail, err := st.GameDetail(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Updates) != 1 || detail.Updates[0].ID != update.ID {
		t.Fatalf("updates = %+v", detail.Updates)
	}
	if len(detail.Ports) != 1 || detail.Ports[0].ID != port.ID {
		t.Fatalf("ports = %+v", detail.Ports)
	}
	if len(detail.Editions) != 0 {
		t.Fatalf("classified variants leaked into editions: %+v", detail.Editions)
	}

	_, body := getBody(t, http.DefaultClient, ts.URL+"/games/update-base")
	related := relatedSection(body)
	for _, want := range []string{"Updates", `href="/games/update-base-final-cut"`, `href="/games/update-base-mobile"`} {
		if !strings.Contains(related, want) {
			t.Errorf("related section missing %q:\n%s", want, related)
		}
	}
	if strings.Contains(related, "Editions & extras") {
		t.Error("classified variants should not render as editions")
	}
	if len(detail.Parents) != 0 {
		t.Errorf("base game should have no ancestors: %+v", detail.Parents)
	}
	if strings.Contains(related, "related-tab-parents") {
		t.Error("base game should not render an ancestor tab")
	}

	// The port page walks the whole chain, original first.
	portDetail, err := st.GameDetail(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	if len(portDetail.Parents) != 2 || portDetail.Parents[0].ID != base.ID || portDetail.Parents[1].ID != update.ID {
		t.Fatalf("port ancestors = %+v, want [%d %d]", portDetail.Parents, base.ID, update.ID)
	}
	_, portBody := getBody(t, http.DefaultClient, ts.URL+"/games/update-base-mobile")
	portRelated := relatedSection(portBody)
	for _, want := range []string{">Port of<", ">Main game<", ">Update<", `href="/games/update-base"`, `href="/games/update-base-final-cut"`} {
		if !strings.Contains(portRelated, want) {
			t.Errorf("port page missing %q:\n%s", want, portRelated)
		}
	}
	if strings.Index(portRelated, `href="/games/update-base"`) > strings.Index(portRelated, `href="/games/update-base-final-cut"`) {
		t.Error("ancestors should list the main game before the update")
	}

	// The update page names its own relation and links back to the base game.
	_, updateBody := getBody(t, http.DefaultClient, ts.URL+"/games/update-base-final-cut")
	updateRelated := relatedSection(updateBody)
	if !strings.Contains(updateRelated, ">Update of<") || !strings.Contains(updateRelated, `href="/games/update-base"`) {
		t.Errorf("update page should link to its base game:\n%s", updateRelated)
	}
}

func TestAncestorCycleTerminates(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()

	a := mustSaveGame(t, st, &models.Game{IGDBID: -940, Slug: "cycle-a", Title: "Cycle A", Category: "main_game", ReleaseStatus: "released"})
	b := mustSaveGame(t, st, &models.Game{IGDBID: -941, Slug: "cycle-b", Title: "Cycle B", Category: "port", ReleaseStatus: "released"})
	if _, err := st.DB.ExecContext(ctx, `UPDATE games SET parent_id=? WHERE id=?`, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE games SET parent_id=? WHERE id=?`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	detail, err := st.GameDetail(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range detail.Parents {
		if p.ID == a.ID {
			t.Fatalf("ancestor chain included the current game: %+v", detail.Parents)
		}
	}
	if len(detail.Parents) > 1 {
		t.Fatalf("cycle should stop after one hop: %+v", detail.Parents)
	}

	code, body := getBody(t, http.DefaultClient, ts.URL+"/games/cycle-a")
	if code != 200 {
		t.Fatalf("cycle page = %d", code)
	}
	if strings.Contains(body, "internal server error") {
		t.Error("cycle page failed to render")
	}
}

func TestRelatedGroupsExcludeSelfAndDuplicates(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()

	base := mustSaveGame(t, st, &models.Game{
		IGDBID: -920, Slug: "dupe-base", Title: "Dupe Base", Category: "main_game", ReleaseStatus: "released",
	})
	child := mustSaveGame(t, st, &models.Game{
		IGDBID: -921, Slug: "dupe-child", Title: "Dupe Base - Android", ParentIGDBID: -920,
		EditionName: "Android", Category: "port", ReleaseStatus: "released",
	})
	// IGDB can report a platform release both as a port and as a version child.
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO game_relations(game_id,related_id,kind) VALUES(?,?,?)`, base.ID, child.ID, "port"); err != nil {
		t.Fatal(err)
	}

	detail, err := st.GameDetail(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Ports) != 1 || detail.Ports[0].ID != child.ID {
		t.Fatalf("ports = %+v", detail.Ports)
	}
	if len(detail.Editions) != 0 {
		t.Fatalf("duplicate edition left in group: %+v", detail.Editions)
	}

	_, body := getBody(t, http.DefaultClient, ts.URL+"/games/dupe-base")
	related := relatedSection(body)
	if strings.Count(related, `href="/games/dupe-child"`) != 1 {
		t.Errorf("related section should list the child once:\n%s", related)
	}

	// The child is a variant of the base; its own page must not list itself as
	// an edition, and it has no outgoing relations or franchise.
	_, childBody := getBody(t, http.DefaultClient, ts.URL+"/games/dupe-child")
	if strings.Contains(childBody, "Editions & extras") {
		t.Error("edition page listed its own editions")
	}
	if strings.Contains(relatedSection(childBody), `href="/games/dupe-child"`) {
		t.Error("edition page linked to itself in related games")
	}
}

func TestGamePageMetadataAndFranchiseFilter(t *testing.T) {
	ts, st := testServer(t)
	mustSaveGame(t, st, &models.Game{
		IGDBID: -911, Slug: "franchise-one", Title: "Franchise One", Franchise: "Saga",
		Category: "main_game", ReleaseStatus: "released", AltNames: []string{"F1", "First One"},
		ReleaseDates: []models.ReleaseDate{{Platform: "PC", Date: "2021-03-04", Human: "March 4, 2021", Region: "Europe", Status: "released"}},
	})
	mustSaveGame(t, st, &models.Game{IGDBID: -912, Slug: "franchise-two", Title: "Franchise Two", Franchise: "Franchise Two", Category: "main_game", ReleaseStatus: "released"})

	_, body := getBody(t, http.DefaultClient, ts.URL+"/games/franchise-one")
	if !strings.Contains(body, "Also known as: F1, First One") {
		t.Error("alternate names are not rendered")
	}
	if !strings.Contains(body, "PC · March 4, 2021 · Europe · released") {
		t.Error("release region/status/human date are not rendered")
	}
	if !strings.Contains(body, `href="/games?franchise=Saga"`) {
		t.Error("franchise link missing")
	}

	_, filtered := getBody(t, http.DefaultClient, ts.URL+"/games?franchise=Saga")
	if strings.Count(filtered, `class="game-card"`) != 1 || !strings.Contains(filtered, "Franchise One") {
		t.Error("franchise query did not filter the games index")
	}
	if !strings.Contains(filtered, `name="franchise"`) || !strings.Contains(filtered, "Franchise: Saga") {
		t.Error("franchise facet or active filter chip missing")
	}
}

