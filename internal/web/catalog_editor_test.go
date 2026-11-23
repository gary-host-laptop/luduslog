package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"luduslog/internal/models"
)

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestCatalogEditorPermissionsAndAdminGrant(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	admin, _ := st.CreateUser(ctx, "catalogadmin", "catalogadmin@example.test", "hash", true)
	editor, _ := st.CreateUser(ctx, "catalogeditor", "catalogeditor@example.test", "hash", false)
	regular, _ := st.CreateUser(ctx, "catalogregular", "catalogregular@example.test", "hash", false)
	mod, _ := st.CreateUser(ctx, "catalogmod", "catalogmod@example.test", "hash", false)
	if err := st.SetRole(ctx, mod.ID, "mod"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCatalogEditor(ctx, editor.ID, true); err != nil {
		t.Fatal(err)
	}

	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := anon.Get(ts.URL + "/games/new")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("anonymous /games/new = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	regularClient, _ := authedClient(t, ts, st, regular)
	if code, _ := getBody(t, regularClient, ts.URL+"/games/new"); code != http.StatusNotFound {
		t.Fatalf("regular user /games/new = %d, want 404", code)
	}
	if err := st.SetCatalogEditor(ctx, editor.ID, true); err != nil {
		t.Fatal(err)
	}
	editor, _ = st.UserByID(ctx, editor.ID)
	editorClient, _ := authedClient(t, ts, st, editor)
	code, body := getBody(t, editorClient, ts.URL+"/games/new")
	if code != http.StatusOK || !strings.Contains(body, "New game") {
		t.Fatalf("editor /games/new = %d: %s", code, body)
	}
	modClient, _ := authedClient(t, ts, st, mod)
	if code, _ := getBody(t, modClient, ts.URL+"/games/new"); code != http.StatusOK {
		t.Fatalf("moderator /games/new = %d, want 200", code)
	}

	adminClient, adminSess := authedClient(t, ts, st, admin)
	noRedirect := noRedirectClient(adminClient)
	resp, err = noRedirect.PostForm(ts.URL+"/admin/users/"+itoa(regular.ID)+"/catalog", url.Values{
		"_csrf": {adminSess.CSRF}, "enabled": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("grant catalog editor = %d", resp.StatusCode)
	}
	regular, _ = st.UserByID(ctx, regular.ID)
	if !regular.CatalogEditor || !regular.CanEditCatalog() {
		t.Fatal("catalog editor privilege was not granted")
	}
	entries, err := st.AuditEntries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundGrant := false
	for _, e := range entries {
		if e.Action == "grant_catalog_editor" {
			foundGrant = true
		}
	}
	if !foundGrant {
		t.Fatal("granting the catalog editor privilege was not audited")
	}

	resp, err = noRedirect.PostForm(ts.URL+"/admin/users/"+itoa(regular.ID)+"/catalog", url.Values{
		"_csrf": {adminSess.CSRF}, "enabled": {"0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	regular, _ = st.UserByID(ctx, regular.ID)
	if regular.CatalogEditor {
		t.Fatal("catalog editor privilege was not revoked")
	}
}

func TestCatalogEditorControlsVisibility(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	mustSaveGame(t, st, &models.Game{IGDBID: -810, Slug: "catalog-control", Title: "Catalog Control", Category: "main_game", ReleaseStatus: "released"})
	regular, _ := st.CreateUser(ctx, "controlregular", "controlregular@example.test", "hash", false)
	editor, _ := st.CreateUser(ctx, "controleditor", "controleditor@example.test", "hash", false)
	if err := st.SetCatalogEditor(ctx, editor.ID, true); err != nil {
		t.Fatal(err)
	}
	editor, _ = st.UserByID(ctx, editor.ID)

	anonCode, anonBody := getBody(t, http.DefaultClient, ts.URL+"/games/catalog-control")
	if anonCode != http.StatusOK || strings.Contains(anonBody, "/games/catalog-control/edit") {
		t.Fatalf("anonymous game page exposes edit control: %d", anonCode)
	}
	regularClient, _ := authedClient(t, ts, st, regular)
	_, regularBody := getBody(t, regularClient, ts.URL+"/games/catalog-control")
	if strings.Contains(regularBody, "/games/catalog-control/edit") {
		t.Fatal("regular user sees edit control")
	}
	editorClient, _ := authedClient(t, ts, st, editor)
	code, editorBody := getBody(t, editorClient, ts.URL+"/games/catalog-control")
	if code != http.StatusOK || !strings.Contains(editorBody, `href="/games/catalog-control/edit"`) {
		t.Fatal("editor does not see the edit control")
	}
	_, index := getBody(t, editorClient, ts.URL+"/games")
	if !strings.Contains(index, `href="/games/new"`) {
		t.Fatal("editor does not see the New game control")
	}
	if code, _ := getBody(t, regularClient, ts.URL+"/games/catalog-control/edit"); code != http.StatusNotFound {
		t.Fatalf("regular user edit page = %d, want 404", code)
	}
}

func TestCatalogEditorCreateEditAndValidation(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	target := mustSaveGame(t, st, &models.Game{IGDBID: -820, Slug: "catalog-target", Title: "Catalog Target", Category: "main_game", ReleaseStatus: "released"})
	igdbGame := mustSaveGame(t, st, &models.Game{
		IGDBID: -821, Slug: "catalog-igdb", Title: "Catalog IGDB", Category: "main_game", ReleaseStatus: "released",
	})
	if _, err := st.DB.ExecContext(ctx, `UPDATE games SET avg_rating=8.2,rating_count=4,plays=7 WHERE id=?`, igdbGame.ID); err != nil {
		t.Fatal(err)
	}
	editor, _ := st.CreateUser(ctx, "catalogwriter", "catalogwriter@example.test", "hash", false)
	if err := st.SetCatalogEditor(ctx, editor.ID, true); err != nil {
		t.Fatal(err)
	}
	editor, _ = st.UserByID(ctx, editor.ID)
	client, sess := authedClient(t, ts, st, editor)
	noRedirect := noRedirectClient(client)

	form := url.Values{
		"_csrf":           {sess.CSRF},
		"title":           {"Manual Quest"},
		"summary":         {"A hand-written catalog entry."},
		"release_date":    {"2024-02-03"},
		"release_status":  {"released"},
		"category":        {"main_game"},
		"franchise":       {"Manual Saga"},
		"platforms":       {"PC\nSwitch"},
		"genres":          {"Adventure\nRPG"},
		"themes":          {"Fantasy"},
		"modes":           {"Single player"},
		"alt_names":       {"MQ"},
		"companies":       {"Example Studio | developer"},
		"releases":        {"PC | Europe | 2024-02-03 | released"},
		"time_hastily":    {"300"},
		"related_remakes": {target.Slug},
	}
	resp, err := noRedirect.PostForm(ts.URL+"/games/new", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/games/manual-quest" {
		t.Fatalf("create game = %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	created, err := st.GameBySlug(ctx, "manual-quest")
	if err != nil {
		t.Fatal(err)
	}
	if created.IGDBID != 0 || created.ReleaseYear != 2024 {
		t.Fatalf("created game identity = igdb %d, year %d", created.IGDBID, created.ReleaseYear)
	}
	detail, err := st.GameDetail(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Platforms) != 2 || len(detail.Genres) != 2 || len(detail.Companies) != 1 || detail.TimeToBeat == nil || detail.TimeToBeat.Hastily != 18000 {
		t.Fatalf("created associations = %+v", detail)
	}
	rel, err := st.CatalogRelations(ctx, created.ID)
	if err != nil || len(rel["remake"]) != 1 || rel["remake"][0].Slug != target.Slug {
		t.Fatalf("created relations = %+v, %v", rel, err)
	}
	if _, total, err := st.SearchGames(ctx, "Manual Quest", 10, 0, false); err != nil || total == 0 {
		t.Fatalf("manual game is not searchable: total=%d err=%v", total, err)
	}

	// Editing preserves the stable slug, IGDB identity, and derived stats.
	edit := url.Values{
		"_csrf":          {sess.CSRF},
		"title":          {"Catalog IGDB Revised"},
		"summary":        {"Corrected summary."},
		"release_status": {"released"},
		"category":       {"main_game"},
		"platforms":      {"PC"},
		"genres":         {"Puzzle"},
		"related_ports":  {target.Slug},
	}
	resp, err = noRedirect.PostForm(ts.URL+"/games/"+igdbGame.Slug+"/edit", edit)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit game = %d", resp.StatusCode)
	}
	edited, err := st.GameBySlug(ctx, igdbGame.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if edited.IGDBID != igdbGame.IGDBID || edited.Plays != 7 || edited.AvgRating != 8.2 || edited.Title != "Catalog IGDB Revised" {
		t.Fatalf("edit changed protected fields: %+v", edited)
	}

	// Parent cycles and duplicate relations are rejected.
	parent := mustSaveGame(t, st, &models.Game{IGDBID: -822, Slug: "catalog-parent", Title: "Catalog Parent", Category: "main_game", ReleaseStatus: "released"})
	resp, err = noRedirect.PostForm(ts.URL+"/games/"+created.Slug+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "title": {"Manual Quest"}, "release_status": {"released"}, "category": {"main_game"},
		"parent_slug": {parent.Slug},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = noRedirect.PostForm(ts.URL+"/games/"+parent.Slug+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "title": {"Catalog Parent"}, "release_status": {"released"}, "category": {"main_game"},
		"parent_slug": {created.Slug},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "cycle") {
		t.Fatalf("cycle edit = %d: %s", resp.StatusCode, body)
	}
	resp, err = noRedirect.PostForm(ts.URL+"/games/"+created.Slug+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "title": {"Manual Quest"}, "release_status": {"released"}, "category": {"main_game"},
		"parent_slug": {created.Slug},
	})
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "own parent") {
		t.Fatalf("self parent edit = %d: %s", resp.StatusCode, body)
	}
	resp, err = noRedirect.PostForm(ts.URL+"/games/"+created.Slug+"/edit", url.Values{
		"_csrf": {sess.CSRF}, "title": {"Manual Quest"}, "release_status": {"released"}, "category": {"main_game"},
		"related_remakes": {target.Slug + "\n" + target.Slug},
	})
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "more than once") {
		t.Fatalf("duplicate relation edit = %d: %s", resp.StatusCode, body)
	}
	if after, _ := st.GameBySlug(ctx, created.Slug); after.Title != "Manual Quest" || after.ParentID == nil {
		t.Fatalf("failed edits changed the game: %+v", after)
	}

	entries, err := st.AuditEntries(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
	}
	if !seen["create_game"] || !seen["edit_game"] {
		t.Fatalf("catalog edits not audited: %+v", entries)
	}
}
