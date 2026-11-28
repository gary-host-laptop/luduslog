package web

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
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
	mustSaveGame(t, st, &models.Game{IGDBID: -913, Slug: "franchise-one-2", Title: "Franchise One 2", Franchise: "Saga", Category: "main_game", ReleaseStatus: "released"})

	_, body := getBody(t, http.DefaultClient, ts.URL+"/games/franchise-one")
	if !strings.Contains(body, `<dt class="muted">Also known as</dt>`) || !strings.Contains(body, `<dd style="margin:0">F1, First One</dd>`) {
		t.Error("alternate names are not rendered in the details card")
	}
	if !strings.Contains(body, "PC · March 4, 2021 · Europe · released") {
		t.Error("release region/status/human date are not rendered")
	}
	if !strings.Contains(body, `href="/games/franchise-one/related/series"`) {
		t.Error("series See all link missing")
	}
	if strings.Contains(body, `href="/games?franchise=Saga"`) {
		t.Error("franchise is still linked next to the release date")
	}
	_, series := getBody(t, http.DefaultClient, ts.URL+"/games/franchise-one/related/series")
	if !strings.Contains(series, "<h1>Series</h1>") || !strings.Contains(series, "Franchise One") {
		t.Error("series See all page does not list the franchise games")
	}

	_, filtered := getBody(t, http.DefaultClient, ts.URL+"/games?franchise=Saga")
	if strings.Count(filtered, `class="game-card"`) != 2 || !strings.Contains(filtered, "Franchise One") {
		t.Error("franchise query did not filter the games index")
	}
	if !strings.Contains(filtered, `name="franchise"`) || !strings.Contains(filtered, "Franchise: Saga") {
		t.Error("franchise facet or active filter chip missing")
	}
}

func TestLogFormNewFieldsAndTimePreservation(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -921, Slug: "form-game", Title: "Form Game", Category: "main_game", ReleaseStatus: "released"})
	edition := mustSaveGame(t, st, &models.Game{IGDBID: -922, Slug: "form-game-deluxe", Title: "Form Game - Deluxe", ParentIGDBID: -921, EditionName: "Deluxe", Category: "main_game", ReleaseStatus: "released"})
	platform := mustSaveGamePlatform(t, st, "Switch")
	client, sess := authedClient(t, ts, st, alice)

	code, form := getBody(t, client, ts.URL+"/games/form-game/log")
	if code != 200 {
		t.Fatalf("log form = %d", code)
	}
	for _, want := range []string{`name="language"`, `name="played_on_id"`, `name="edition_game_id"`, `value="completed"`, `name="time_to_finish_hours"`, `name="time_to_finish_minutes"`, `name="time_to_master_hours"`, `name="time_to_master_minutes"`, "Deluxe"} {
		if !strings.Contains(form, want) {
			t.Errorf("log form missing %q", want)
		}
	}
	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.PostForm(ts.URL+"/games/form-game/log", url.Values{
		"_csrf": {sess.CSRF}, "status": {"completed"}, "language": {"Japanese"},
		"platform_id": {itoa(platform)}, "played_on_id": {itoa(platform)}, "edition_game_id": {itoa(edition.ID)},
		"time_to_finish": {"12.5"}, "time_to_master": {"30"}, "ownership": {"owned"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("log submit = %d", resp.StatusCode)
	}
	log, err := st.LatestLogFor(ctx, alice.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if log.Status != "completed" || log.Language != "Japanese" || log.TimeToFinish != 12.5 || log.TimeToMaster != 30 {
		t.Fatalf("log fields = %+v", log)
	}
	if log.PlayedOnID == nil || *log.PlayedOnID != platform || log.EditionGameID == nil || *log.EditionGameID != edition.ID {
		t.Fatalf("played-on/edition not saved: %+v", log)
	}
	// The ownership on the log also created the library entry.
	if entry, err := st.LibraryEntry(ctx, alice.ID, game.ID); err != nil || entry.Ownership != "owned" || entry.Edition != "Deluxe" {
		t.Fatalf("log ownership did not sync the library: %+v, %v", entry, err)
	}
	// Editing without the timing fields must preserve them.
	resp, err = noRedirect.PostForm(ts.URL+"/logs/"+itoa(log.ID)+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "status": {"completed"}, "review": {"still good"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	edited, _ := st.LogByID(ctx, log.ID)
	if edited.TimeToFinish != 12.5 || edited.TimeToMaster != 30 {
		t.Fatalf("editing cleared timing values: %+v", edited)
	}
}

func TestReviewTitlesWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "titlealice", "titlealice@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -940, Slug: "review-title-game", Title: "Review Title Game", ReleaseStatus: "released"})
	client, sess := authedClient(t, ts, st, alice)
	noRedirect := noRedirectClient(client)

	code, form := getBody(t, client, ts.URL+"/games/review-title-game/log")
	if code != http.StatusOK {
		t.Fatalf("log form = %d", code)
	}
	for _, want := range []string{`name="name"`, `maxlength="200"`, "highlighted review widget"} {
		if !strings.Contains(form, want) {
			t.Errorf("log form missing %q", want)
		}
	}

	if got := postStatus(t, noRedirect, ts.URL+"/games/review-title-game/log", url.Values{
		"_csrf": {sess.CSRF}, "name": {"  The First Climb  "}, "review": {"A full review body"},
		"status": {"completed"}, "playtime": {"2.5"},
	}); got != http.StatusSeeOther {
		t.Fatalf("create titled review = %d", got)
	}
	log, err := st.LatestLogFor(ctx, alice.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if log.Name != "The First Climb" {
		t.Fatalf("saved title = %q", log.Name)
	}
	reviewPath := "/users/titlealice/reviews/" + itoa(log.ID)
	for _, path := range []string{
		reviewPath,
		"/reviews",
		"/games/review-title-game",
		"/search?q=The+First+Climb&tab=reviews",
		"/",
		"/users/titlealice",
		"/users/titlealice?tab=reviews",
		"/settings",
	} {
		code, body := getBody(t, client, ts.URL+path)
		if code != http.StatusOK || !strings.Contains(body, "The First Climb") {
			t.Errorf("%s missing review title (%d)", path, code)
		}
	}

	// Editing through older forms that omit the title must preserve it.
	if got := postStatus(t, noRedirect, ts.URL+"/logs/"+itoa(log.ID)+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "status": {"completed"}, "review": {"A full review body"},
	}); got != http.StatusSeeOther {
		t.Fatalf("edit without title field = %d", got)
	}
	edited, _ := st.LogByID(ctx, log.ID)
	if edited.Name != "The First Climb" {
		t.Fatalf("editing cleared the title: %q", edited.Name)
	}

	// The field can be cleared, and the review page falls back to the game name.
	if got := postStatus(t, noRedirect, ts.URL+"/logs/"+itoa(log.ID)+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "name": {"   "}, "status": {"completed"}, "review": {"A full review body"},
	}); got != http.StatusSeeOther {
		t.Fatalf("clear title = %d", got)
	}
	cleared, _ := st.LogByID(ctx, log.ID)
	if cleared.Name != "" {
		t.Fatalf("cleared title = %q", cleared.Name)
	}
	_, reviewBody := getBody(t, client, ts.URL+reviewPath)
	if !strings.Contains(reviewBody, "Review of Review Title Game") {
		t.Error("review page missing the game-name title fallback")
	}
}

func TestSessionEditingDaysAndPrivacy(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -931, Slug: "session-game", Title: "Session Game", Category: "main_game", ReleaseStatus: "released"})
	log, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Status: "playing"})
	if err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, alice)
	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := noRedirect.PostForm(ts.URL+"/logs/"+itoa(log.ID)+"/sessions", url.Values{
		"_csrf": {sess.CSRF}, "note": {"first day"}, "playtime": {"2"},
		"privacy": {"public"}, "days": {"2026-01-02", "2026-01-03"}, "tags": {"boss, co-op"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("session create = %d", resp.StatusCode)
	}
	sessions, _ := st.SessionsFor(ctx, log.ID)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if sessions[0].NotePrivacy != "everyone" {
		t.Fatalf("privacy was not normalized: %q", sessions[0].NotePrivacy)
	}
	if len(sessions[0].Days) != 2 || len(sessions[0].Tags) != 2 {
		t.Fatalf("multi-day/tags not saved: %+v", sessions[0])
	}

	resp, err = noRedirect.PostForm(ts.URL+"/sessions/"+itoa(sessions[0].ID)+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "note": {"edited"}, "privacy": {"private"}, "playtime": {"3"},
		"days": {"2026-02-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("session edit = %d", resp.StatusCode)
	}
	edited, _ := st.SessionByID(ctx, sessions[0].ID)
	if edited.Note != "edited" || edited.NotePrivacy != "none" || len(edited.Days) != 1 || edited.Days[0] != "2026-02-01" {
		t.Fatalf("session edit = %+v", edited)
	}

	// Private notes are hidden from other viewers.
	if got, _ := st.SessionsForViewer(ctx, log.ID, bob.ID); len(got) != 1 || got[0].Note != "" {
		t.Fatalf("private note leaked: %+v", got)
	}
	// Friends notes require an approved mutual follow.
	st.Follow(ctx, alice.ID, bob.ID)
	st.Follow(ctx, bob.ID, alice.ID)
	edited.NotePrivacy = "friends"
	if err := st.UpdateSession(ctx, edited.ID, store.SessionInput{LogID: log.ID, Note: "friend note", NotePrivacy: "friends", Days: []string{"2026-02-01"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.SessionsForViewer(ctx, log.ID, bob.ID); len(got) != 1 || got[0].Note != "friend note" {
		t.Fatalf("friends note hidden: %+v", got)
	}

	// The legacy Journal URL redirects into Activity.
	legacy, err := noRedirect.Get(ts.URL + "/users/alice?tab=logs")
	if err != nil {
		t.Fatal(err)
	}
	legacy.Body.Close()
	if legacy.StatusCode != http.StatusFound || legacy.Header.Get("Location") != "/users/alice?kind=logs" {
		t.Fatalf("legacy journal redirect = %d %q", legacy.StatusCode, legacy.Header.Get("Location"))
	}

	// Overview keeps the calendar as a toggle and still marks play days.
	_, page := getBody(t, http.DefaultClient, ts.URL+"/users/alice?view=calendar&month=2026-02")
	if !strings.Contains(page, "calendar-day played") || !strings.Contains(page, "calendar-grid") {
		t.Error("activity calendar did not mark play days")
	}
	_, activityPage := getBody(t, client, ts.URL+"/users/alice?kind=sessions")
	if !strings.Contains(activityPage, "Session Game") || !strings.Contains(activityPage, "friend note") || strings.Contains(activityPage, "calendar-grid") {
		t.Error("session filter did not render the activity stream")
	}
}

func TestLibraryAddEditRemoveWeb(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -961, Slug: "lib-game", Title: "Lib Game", Category: "main_game", ReleaseStatus: "released"})
	client, sess := authedClient(t, ts, st, alice)
	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	_, page := getBody(t, client, ts.URL+"/games/lib-game")
	if !strings.Contains(page, "Add to library") || !strings.Contains(page, `action="/library"`) {
		t.Fatal("game page has no library entry form")
	}
	post := func(path string, values url.Values) *http.Response {
		t.Helper()
		resp, err := noRedirect.PostForm(ts.URL+path, values)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	resp := post("/library", url.Values{"_csrf": {sess.CSRF}, "game_id": {itoa(game.ID)}, "ownership": {"owned"}, "storefront": {"Steam"}, "edition": {"Deluxe"}, "return_to": {"/games/lib-game"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/games/lib-game" {
		t.Fatalf("library add = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	entry, err := st.LibraryEntry(ctx, alice.ID, game.ID)
	if err != nil || entry.Ownership != "owned" || entry.Storefront != "Steam" || entry.Edition != "Deluxe" {
		t.Fatalf("library entry = %+v, %v", entry, err)
	}
	post("/library", url.Values{"_csrf": {sess.CSRF}, "game_id": {itoa(game.ID)}, "ownership": {"rented"}, "return_to": {"/library"}})
	entry, _ = st.LibraryEntry(ctx, alice.ID, game.ID)
	if entry.Ownership != "rented" {
		t.Fatalf("library edit = %+v", entry)
	}
	_, lib := getBody(t, client, ts.URL+"/library")
	if !strings.Contains(lib, `action="/library"`) || !strings.Contains(lib, "Lib Game") || !strings.Contains(lib, `value="rented" selected`) {
		t.Fatal("library page does not expose editable rows")
	}
	post("/library/remove", url.Values{"_csrf": {sess.CSRF}, "game_id": {itoa(game.ID)}, "return_to": {"/games/lib-game"}})
	if _, err := st.LibraryEntry(ctx, alice.ID, game.ID); err == nil {
		t.Fatal("library entry was not removed")
	}
}

func TestMentionLinksAndPrivacy(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -971, Slug: "mention-game", Title: "Mention Game", Category: "main_game", ReleaseStatus: "released"})
	log, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Review: "Good game", Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateComment(ctx, &bob.ID, "log", log.ID, nil, "thanks @alice", "", "", ""); err != nil {
		t.Fatal(err)
	}
	_, review := getBody(t, http.DefaultClient, ts.URL+"/users/alice/reviews/"+itoa(log.ID))
	if !strings.Contains(review, `class="mention" href="/users/alice"`) {
		t.Fatal("mention link does not point at the profile route")
	}
	if code, _ := getBody(t, http.DefaultClient, ts.URL+"/users/alice"); code != 200 {
		t.Fatal("mention target does not resolve")
	}

	aliceClient, aliceSess := authedClient(t, ts, st, alice)
	noRedirect := &http.Client{Jar: aliceClient.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	comment := func() int {
		t.Helper()
		resp, err := noRedirect.PostForm(ts.URL+"/comments", url.Values{
			"_csrf": {aliceSess.CSRF}, "subject_type": {"profile"}, "subject_id": {itoa(bob.ID)},
			"body": {"hello"}, "return_to": {"/users/bob"},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return st.CountComments(ctx, "profile", bob.ID)
	}
	// Nobody: the comment is refused.
	st.UpdateProfile(ctx, bob.ID, store.ProfileUpdate{CommentPrivacy: "none"})
	if n := comment(); n != 0 {
		t.Fatalf("comment posted despite CommentPrivacy none: %d", n)
	}
	// Friends only: refused until the follow is mutual.
	st.UpdateProfile(ctx, bob.ID, store.ProfileUpdate{CommentPrivacy: "friends"})
	if n := comment(); n != 0 {
		t.Fatalf("friend-only comment accepted without a mutual follow: %d", n)
	}
	st.Follow(ctx, alice.ID, bob.ID)
	st.Follow(ctx, bob.ID, alice.ID)
	if n := comment(); n != 1 {
		t.Fatalf("friend comment refused: %d", n)
	}

	// Hidden activity stays out of the public feed.
	st.UpdateProfile(ctx, bob.ID, store.ProfileUpdate{HideActivity: true})
	st.AddActivity(ctx, bob.ID, "liked", &game.ID, nil, nil, "", "public")
	feed, err := st.ActivityFeed(ctx, alice.ID, 0, 30, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range feed {
		if a.UserID == bob.ID {
			t.Fatal("hidden activity leaked into the feed")
		}
	}
	if got, _ := st.ActivityFeed(ctx, bob.ID, bob.ID, 30, 0, false); len(got) == 0 {
		t.Fatal("hidden activity disappeared for its owner")
	}
}

func TestFoldersAndListPicker(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	client, sess := authedClient(t, ts, st, alice)
	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, values url.Values) int {
		t.Helper()
		resp, err := noRedirect.PostForm(ts.URL+path, values)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("/folders/new", url.Values{"_csrf": {sess.CSRF}, "name": {"Favourites"}}); code != http.StatusSeeOther {
		t.Fatalf("create folder = %d", code)
	}
	folders, _ := st.Folders(ctx, alice.ID)
	if len(folders) != 1 || folders[0].Name != "Favourites" {
		t.Fatalf("folders = %+v", folders)
	}
	folder := folders[0]
	post("/folders/"+itoa(folder.ID)+"/rename", url.Values{"_csrf": {sess.CSRF}, "name": {"Best of"}})
	folders, _ = st.Folders(ctx, alice.ID)
	if folders[0].Name != "Best of" {
		t.Fatalf("rename = %q", folders[0].Name)
	}
	list, err := st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "My List", Privacy: "public"})
	if err != nil {
		t.Fatal(err)
	}
	personal, _ := st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "Personal List", Privacy: "personal"})
	if code := post("/folders/"+itoa(folder.ID)+"/lists", url.Values{"_csrf": {sess.CSRF}, "list_id": {itoa(list.ID)}}); code != http.StatusSeeOther {
		t.Fatalf("add list to folder = %d", code)
	}
	code, settings := getBody(t, client, ts.URL+"/settings?tab=folders")
	if code != 200 || !strings.Contains(settings, "Best of") || !strings.Contains(settings, "Delete folder") || !strings.Contains(settings, "My List") {
		t.Fatalf("folder management UI = %d", code)
	}
	inFolder, _ := st.FolderLists(ctx, folder.ID)
	if len(inFolder) != 1 || inFolder[0].ID != list.ID {
		t.Fatalf("folder lists = %+v", inFolder)
	}
	post("/folders/"+itoa(folder.ID)+"/lists/remove", url.Values{"_csrf": {sess.CSRF}, "list_id": {itoa(list.ID)}})
	if inFolder, _ = st.FolderLists(ctx, folder.ID); len(inFolder) != 0 {
		t.Fatal("list was not removed from the folder")
	}
	post("/folders/"+itoa(folder.ID)+"/delete", url.Values{"_csrf": {sess.CSRF}})
	if folders, _ = st.Folders(ctx, alice.ID); len(folders) != 0 {
		t.Fatal("folder was not deleted")
	}

	game := mustSaveGame(t, st, &models.Game{IGDBID: -981, Slug: "picker-game", Title: "Picker Game", Category: "main_game", ReleaseStatus: "released"})
	_, page := getBody(t, client, ts.URL+"/games/picker-game")
	if !strings.Contains(page, "Add to a list") || !strings.Contains(page, `action="/games/picker-game/lists"`) {
		t.Fatal("game page has no list picker")
	}
	if code := post("/games/picker-game/lists", url.Values{"_csrf": {sess.CSRF}, "list_id": {itoa(list.ID)}}); code != http.StatusSeeOther {
		t.Fatalf("game list add = %d", code)
	}
	entries, _ := st.GamesInList(ctx, list.ID)
	if len(entries) != 1 || entries[0].GameID != game.ID {
		t.Fatalf("list entries = %+v", entries)
	}

	// Personal lists are visible to approved followers, not strangers.
	if lists, _ := st.VisibleListsFor(ctx, alice.ID, bob.ID); len(lists) != 1 {
		t.Fatalf("stranger saw %d lists, want only the public one", len(lists))
	}
	st.Follow(ctx, bob.ID, alice.ID)
	lists, _ := st.VisibleListsFor(ctx, alice.ID, bob.ID)
	seen := false
	for _, l := range lists {
		if l.ID == personal.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatal("follower cannot see the personal list")
	}
}

func mustSaveGamePlatform(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	mustSaveGame(t, st, &models.Game{
		IGDBID: -950 - int64(len(name)), Slug: "platform-" + strings.ToLower(name), Title: "Platform " + name,
		Category: "main_game", ReleaseStatus: "released",
		Platforms: []models.Platform{{Name: name}},
	})
	var id int64
	st.DB.QueryRowContext(context.Background(), `SELECT id FROM platforms WHERE name=?`, name).Scan(&id)
	return id
}

func outboxItems(t *testing.T, ts string, username string) int {
	t.Helper()
	resp, err := http.Get(ts + "/users/" + username + "/outbox")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		TotalItems   int   `json:"totalItems"`
		OrderedItems []any `json:"orderedItems"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return len(out.OrderedItems)
}

func TestFederationToggles(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -991, Slug: "fed-game", Title: "Fed Game", Category: "main_game", ReleaseStatus: "released"})
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Review: "A public review", Status: "played"}); err != nil {
		t.Fatal(err)
	}
	st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Status: "played"})

	st.UpdateProfile(ctx, alice.ID, store.ProfileUpdate{Federation: true})
	if got := outboxItems(t, ts.URL, "alice"); got != 0 {
		t.Fatalf("outbox published %d reviews with FedReviews off", got)
	}
	st.UpdateProfile(ctx, alice.ID, store.ProfileUpdate{Federation: true, FedReviews: true})
	if got := outboxItems(t, ts.URL, "alice"); got != 1 {
		t.Fatalf("outbox published %d reviews, want the one public review", got)
	}
	st.UpdateProfile(ctx, alice.ID, store.ProfileUpdate{Federation: true, FedReviews: true, PrivateProfile: true})
	if got := outboxItems(t, ts.URL, "alice"); got != 0 {
		t.Fatalf("private profile outbox published %d items", got)
	}

	client, _ := authedClient(t, ts, st, alice)
	_, settings := getBody(t, client, ts.URL+"/settings?tab=federation")
	for _, want := range []string{`name="fed_statuses"`, `name="fed_reviews"`, `name="fed_lists"`} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings missing implemented federation toggle %q", want)
		}
	}

	st.UpdateProfile(ctx, alice.ID, store.ProfileUpdate{Federation: true, HideSocialGraph: true})
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/users/alice/followers", nil)
	req.Header.Set("Accept", "application/activity+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var followers struct {
		OrderedItems []any `json:"orderedItems"`
	}
	json.NewDecoder(resp.Body).Decode(&followers)
	if len(followers.OrderedItems) != 0 {
		t.Fatal("followers collection leaked a hidden social graph")
	}
}

func TestShelfViews(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	completed := mustSaveGame(t, st, &models.Game{IGDBID: -1001, Slug: "done-game", Title: "Done Game", Category: "main_game", ReleaseStatus: "released"})
	abandoned := mustSaveGame(t, st, &models.Game{IGDBID: -1002, Slug: "dropped-game", Title: "Dropped Game", Category: "main_game", ReleaseStatus: "released"})
	playing := mustSaveGame(t, st, &models.Game{IGDBID: -1003, Slug: "active-game", Title: "Active Game", Category: "main_game", ReleaseStatus: "released"})
	st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: completed.ID, Status: "completed"})
	st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: abandoned.ID, Status: "abandoned"})
	st.SetStatusFlag(ctx, alice.ID, playing.ID, "playing", true)
	client, _ := authedClient(t, ts, st, alice)

	cases := []struct {
		status string
		want   string
		avoid  string
	}{
		{"completed", "Done Game", "Dropped Game"},
		{"abandoned", "Dropped Game", "Done Game"},
		{"playing", "Active Game", "Done Game"},
	}
	for _, tc := range cases {
		code, body := getBody(t, client, ts.URL+"/games?status="+tc.status)
		if code != 200 || !strings.Contains(body, tc.want) || strings.Contains(body, tc.avoid) {
			t.Errorf("%s shelf view = %d, missing %q or leaking %q", tc.status, code, tc.want, tc.avoid)
		}
		if strings.Count(body, `class="game-card"`) != 1 {
			t.Errorf("%s shelf view returned the wrong number of cards", tc.status)
		}
	}
	_, empty := getBody(t, client, ts.URL+"/games?status=completed&q=zzzznotfound")
	if !strings.Contains(empty, "No games match") {
		t.Fatal("empty shelf state missing")
	}
}

func TestCSVExport(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -1011, Slug: "csv-game", Title: "CSV Game", Category: "main_game", ReleaseStatus: "released"})
	review := `He said "hi", then left`
	st.CreateLog(ctx, store.LogInput{UserID: alice.ID, GameID: game.ID, Review: review, Status: "played", Rating: 8})
	st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "CSV List", Privacy: "public"})
	st.SaveLibraryEntry(ctx, alice.ID, game.ID, nil, "owned", "Steam", "Deluxe")
	client, _ := authedClient(t, ts, st, alice)

	resp, err := client.Get(ts.URL + "/settings/export?format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, ".csv") {
		t.Fatalf("content disposition = %q", cd)
	}
	rows, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("csv rows = %d, want header + log + list + library\n%v", len(rows), rows)
	}
	if rows[0][0] != "kind" || rows[1][0] != "log" || rows[2][0] != "list" || rows[3][0] != "library" {
		t.Fatalf("unexpected csv kinds: %v", rows)
	}
	if rows[1][12] != review {
		t.Fatalf("review escaping round-trip = %q", rows[1][12])
	}
}

func TestGamePageActionDialogs(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	mustSaveGame(t, st, &models.Game{IGDBID: -1021, Slug: "dialog-game", Title: "Dialog Game", Category: "main_game", ReleaseStatus: "released", Platforms: []models.Platform{{Name: "PC"}}})
	mustSaveGamePlatform(t, st, "Switch")
	list, err := st.CreateList(ctx, store.ListInput{UserID: alice.ID, Title: "Shelf", Privacy: "public"})
	if err != nil {
		t.Fatal(err)
	}

	client, _ := authedClient(t, ts, st, alice)
	_, page := getBody(t, client, ts.URL+"/games/dialog-game")
	for _, want := range []string{
		`data-dialog-open="game-list-dialog"`,
		`data-dialog-open="game-library-dialog"`,
		`<dialog class="game-action-dialog" id="game-list-dialog"`,
		`<dialog class="game-action-dialog" id="game-library-dialog"`,
		`name="list_id" value="` + itoa(list.ID) + `"`,
		"Shelf",
		"Public · 0 games",
		`href="/lists/new"`,
		`action="/library"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("game page missing %q", want)
		}
	}
	for _, unwanted := range []string{`class="game-list-add"`, `class="game-library-edit"`, "<summary>Add to a list</summary>"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("game page still has legacy disclosure %q", unwanted)
		}
	}
	if strings.Count(page, `class="btn btn-sm btn-block game-action-open"`) != 2 {
		t.Error("quick actions should be two buttons")
	}
	// The platform picker offers only platforms this game released on.
	start := strings.Index(page, `id="game-library-dialog"`)
	end := strings.Index(page[start:], "</dialog>")
	if start < 0 || end < 0 {
		t.Fatal("could not isolate the library dialog")
	}
	libraryDialog := page[start : start+end]
	if !strings.Contains(libraryDialog, ">PC</option>") {
		t.Error("library dialog is missing the game's PC platform")
	}
	if strings.Contains(libraryDialog, ">Switch</option>") {
		t.Error("library dialog lists a platform the game was not released on")
	}

	// A user without lists sees the empty state and the create link.
	emptyClient, _ := authedClient(t, ts, st, bob)
	_, empty := getBody(t, emptyClient, ts.URL+"/games/dialog-game")
	if !strings.Contains(empty, "You don't have any lists yet.") || !strings.Contains(empty, `href="/lists/new"`) {
		t.Error("empty list dialog is missing its create link")
	}
	if strings.Contains(empty, `name="list_id"`) {
		t.Error("empty list dialog should not render list radios")
	}
}

func TestFriendsActivityCard(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	alice, _ := st.CreateUser(ctx, "alice", "alice@example.test", "hash", false)
	bob, _ := st.CreateUser(ctx, "bob", "bob@example.test", "hash", false)
	carol, _ := st.CreateUser(ctx, "carol", "carol@example.test", "hash", false)
	dave, _ := st.CreateUser(ctx, "dave", "dave@example.test", "hash", false)
	eve, _ := st.CreateUser(ctx, "eve", "eve@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -1031, Slug: "friends-game", Title: "Friends Game", Category: "main_game", ReleaseStatus: "released"})
	friend := func(a, b *models.User) {
		st.Follow(ctx, a.ID, b.ID)
		st.ApproveFollow(ctx, a.ID, b.ID)
		st.Follow(ctx, b.ID, a.ID)
		st.ApproveFollow(ctx, b.ID, a.ID)
	}
	friend(alice, bob)
	friend(alice, carol)
	friend(alice, dave)

	st.SetStatuses(ctx, bob.ID, game.ID, true, true, false, false, false)
	st.CreateLog(ctx, store.LogInput{UserID: bob.ID, GameID: game.ID, Rating: 8, Status: "played"})
	st.SetStatuses(ctx, carol.ID, game.ID, false, false, true, true, false)
	st.CreateLog(ctx, store.LogInput{UserID: dave.ID, GameID: game.ID, Rating: 7, Status: "played"})
	st.DB.ExecContext(ctx, `DELETE FROM statuses WHERE user_id=? AND game_id=?`, dave.ID, game.ID)
	// Eve is not a mutual friend, so her playing status must stay private.
	st.SetStatuses(ctx, eve.ID, game.ID, false, true, false, false, false)

	client, _ := authedClient(t, ts, st, alice)
	_, page := getBody(t, client, ts.URL+"/games/friends-game")
	if !strings.Contains(page, "Friends' activity") {
		t.Fatal("friends activity card missing")
	}
	row := func(name string) string {
		t.Helper()
		i := strings.Index(page, name)
		if i < 0 {
			t.Fatalf("no row for %s", name)
		}
		start := strings.LastIndex(page[:i], `<li class="friend-activity-row">`)
		end := strings.Index(page[i:], "</li>")
		if start < 0 || end < 0 {
			t.Fatalf("could not isolate row for %s", name)
		}
		return page[start : i+end]
	}
	if bobRow := row(">bob<"); !strings.Contains(bobRow, "ph-play") || strings.Contains(bobRow, "ph-check-circle") ||
		!strings.Contains(bobRow, "8.0") || strings.Count(bobRow, "ph-star") != 1 ||
		!strings.Contains(bobRow, `href="/users/bob/logs/friends-game"`) ||
		!strings.Contains(bobRow, `aria-label="Rated 8.0 out of 10"`) ||
		!strings.Contains(bobRow, `data-description="bob rated it 8/10, is playing it"`) {
		t.Errorf("bob row = %s", bobRow)
	}
	if carolRow := row(">carol<"); !strings.Contains(carolRow, "ph-stack") || strings.Contains(carolRow, "ph-heart") || strings.Contains(carolRow, "ph-star") ||
		!strings.Contains(carolRow, `data-description="carol has it in their backlog"`) {
		t.Errorf("carol row = %s", carolRow)
	}
	if daveRow := row(">dave<"); !strings.Contains(daveRow, "7.0") || strings.Count(daveRow, "ph-star") != 1 ||
		strings.Contains(daveRow, "ph-check-circle") || strings.Contains(daveRow, "ph-play") {
		t.Errorf("dave row = %s", daveRow)
	}
	if strings.Contains(page, ">eve<") {
		t.Error("stranger appears in the friends activity card")
	}
	for _, name := range []string{">bob<", ">carol<", ">dave<"} {
		if !strings.Contains(row(name), `class="avatar avatar-sm"`) {
			t.Errorf("row for %s has no avatar", name)
		}
	}

	// Logged-out visitors and non-friends never see the card.
	_, loggedOut := getBody(t, http.DefaultClient, ts.URL+"/games/friends-game")
	if strings.Contains(loggedOut, "Friends' activity") {
		t.Error("logged-out visitor sees the friends activity card")
	}
	eveClient, _ := authedClient(t, ts, st, eve)
	_, stranger := getBody(t, eveClient, ts.URL+"/games/friends-game")
	if strings.Contains(stranger, "Friends' activity") {
		t.Error("non-friend sees the friends activity card")
	}
}
