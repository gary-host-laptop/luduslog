package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

func TestIssue73EditorPage(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "editoruser", "editoruser@example.test", "hash", false)
	platform := mustSaveGamePlatform(t, st, "PC")
	mustSaveGamePlatform(t, st, "Nintendo 3DS")
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7302, Slug: "editor-game", Title: "Editor Game", Category: "main_game", ReleaseStatus: "released", Platforms: []models.Platform{{Name: "PC"}}})
	first, _ := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Name: "First run", Status: "completed", Rating: 8, Review: "A full review", PlatformID: &platform})
	second, _ := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Name: "Second run", Status: "replayed", Rating: 6, Mastered: true})
	client, sess := authedClient(t, ts, st, user)

	code, body := getBody(t, client, ts.URL+"/games/editor-game/log")
	if code != http.StatusOK {
		t.Fatalf("log editor = %d", code)
	}
	for _, want := range []string{
		`class="log-editor-head"`, `data-log-tabs`,
		`data-log-tab="play"`, `data-log-panel="play"`, `data-log-tab="journal"`, `data-log-panel="journal"`,
		`data-log-tab="library"`, `data-log-panel="library"`, `data-log-tab="review"`, `data-log-panel="review"`,
		`name="rating"`, `name="status"`,
		`name="platform_id"`, `name="played_on_id"`, `name="edition_game_id"`, `name="language"`,
		`name="playtime_hours"`, `name="playtime_minutes"`,
		`name="time_to_finish_hours"`, `name="time_to_finish_minutes"`,
		`name="time_to_master_hours"`, `name="time_to_master_minutes"`, `name="sync_sessions"`,
		`name="difficulty"`, `name="replay"`, `name="mastered"`, `name="draft"`,
		`name="game_shelf_form"`, `name="game_shelf" value="playing"`, `name="game_shelf" value="played"`,
		`name="game_shelf" value="backlog"`, `name="game_shelf" value="wishlist"`,
		`name="cover_url"`, `action="/logs/` + itoa(first.ID) + `/delete-all"`,
		"Journal", "Add a session", "Custom cover", "Delete all data", ">Nintendo 3DS</option>",
		`action="/logs/` + itoa(first.ID) + `/edit"`,
		`href="/logs/` + itoa(first.ID) + `/edit?tab=play" aria-current="page"`,
		`href="/logs/` + itoa(second.ID) + `/edit?tab=play"`,
		`class="log-chip log-chip-add"`, "Add log",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("editor missing %q", want)
		}
	}
	for _, unwanted := range []string{"No logs yet", `name="log"`, `class="log-bar`, `<option value="playing"`, `<option value="backlog"`, `<option value="wishlist"`, `name="primary_rating"`, "primary rating"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("editor still renders old log bar/status: %q", unwanted)
		}
	}
	if strings.Index(body, `name="rating"`) > strings.Index(body, `data-log-panel="journal"`) {
		t.Error("rating is not in the Play panel")
	}
	if !strings.Contains(body, `value="6" checked`) {
		t.Error("editor is not showing the central game rating")
	}
	if strings.Index(body, `href="/logs/`+itoa(first.ID)+`/edit?tab=play"`) > strings.Index(body, `href="/logs/`+itoa(second.ID)+`/edit?tab=play"`) {
		t.Error("log chips are not oldest first")
	}
	if strings.Contains(body, `style="max-width:720px"`) {
		t.Error("editor still renders as a max-width card")
	}

	// Log-bar rename changes only the title.
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/logs/"+itoa(second.ID)+"/rename", url.Values{
		"_csrf": {sess.CSRF}, "name": {"  Renamed run  "},
	}); got != http.StatusSeeOther {
		t.Fatalf("rename = %d", got)
	}
	renamed, _ := st.LogByID(ctx, second.ID)
	if renamed.Name != "Renamed run" {
		t.Fatalf("renamed log = %q", renamed.Name)
	}
	if renamed.Status != "replayed" || renamed.Rating != 6 {
		t.Fatalf("rename changed other fields: %+v", renamed)
	}

	// The default played status fills in when a create omits the field.
	if err := st.UpdateProfile(ctx, user.ID, store.ProfileUpdate{DefaultPlayedStatus: "shelved"}); err != nil {
		t.Fatal(err)
	}
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/games/editor-game/log", url.Values{
		"_csrf": {sess.CSRF}, "name": {"Default status run"}, "review": {""},
	}); got != http.StatusSeeOther {
		t.Fatalf("create with default status = %d", got)
	}
	latest, _ := st.LatestLogFor(ctx, user.ID, game.ID)
	if latest.Status != "shelved" {
		t.Fatalf("default status = %q, want shelved", latest.Status)
	}

	// Session tags are capped at eight.
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/logs/"+itoa(second.ID)+"/sessions", url.Values{
		"_csrf": {sess.CSRF}, "note": {"Ten tags"}, "tags": {"a,b,c,d,e,f,g,h,i,j"},
	}); got != http.StatusSeeOther {
		t.Fatalf("session create = %d", got)
	}
	sessions, _ := st.SessionsFor(ctx, second.ID)
	if len(sessions) != 1 || len(sessions[0].Tags) != 8 {
		t.Fatalf("session tags = %+v, want 8", sessions)
	}

	// Delete all data clears logs, sessions, library and status.
	if err := st.SaveLibraryEntry(ctx, user.ID, game.ID, &platform, "owned", "Steam", "Deluxe"); err != nil {
		t.Fatal(err)
	}
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/logs/"+itoa(second.ID)+"/delete-all", url.Values{
		"_csrf": {sess.CSRF},
	}); got != http.StatusSeeOther {
		t.Fatalf("delete all = %d", got)
	}
	if logs, _ := st.UserGameLogs(ctx, user.ID, game.ID, true); len(logs) != 0 {
		t.Fatalf("delete all left %d logs", len(logs))
	}
	if _, err := st.LibraryEntry(ctx, user.ID, game.ID); err == nil {
		t.Fatal("delete all left a library entry")
	}
	if status, _ := st.StatusFor(ctx, user.ID, game.ID); status.Played != 0 || status.Playing || status.Backlog || status.Wishlist {
		t.Fatalf("delete all left status %+v", status)
	}
}

func divBlockContaining(s, marker string) string {
	markerAt := strings.Index(s, marker)
	if markerAt < 0 {
		return ""
	}
	start := strings.LastIndex(s[:markerAt], "<div")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "<div"):
			depth++
			i += len("<div")
		case strings.HasPrefix(s[i:], "</div>"):
			depth--
			i += len("</div>")
			if depth == 0 {
				return s[start:i]
			}
		default:
			i++
		}
	}
	return ""
}

func TestIssue73EditorSheetLayout(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "sheetuser", "sheetuser@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7308, Slug: "sheet-game", Title: "Sheet Game", Category: "main_game", ReleaseStatus: "released"})
	client, _ := authedClient(t, ts, st, user)

	code, body := getBody(t, client, ts.URL+"/games/"+game.Slug+"/log")
	if code != http.StatusOK {
		t.Fatalf("log editor = %d", code)
	}
	playStart := strings.Index(body, `data-log-panel="play"`)
	playEnd := strings.Index(body, `data-log-panel="journal"`)
	libraryStart := strings.Index(body, `data-log-panel="library"`)
	libraryEnd := strings.Index(body, `data-log-panel="review"`)
	if playStart < 0 || playEnd < playStart || libraryStart < 0 || libraryEnd < libraryStart {
		t.Fatal("editor panels were not rendered in the expected order")
	}
	play, library := body[playStart:playEnd], body[libraryStart:libraryEnd]
	for _, panel := range []struct {
		name string
		html string
	}{{"play", play}, {"library", library}} {
		for _, want := range []string{`class="log-sheet"`, `class="log-band"`, `class="log-fields"`, `class="log-band-actions"`} {
			if !strings.Contains(panel.html, want) {
				t.Errorf("%s panel missing %q", panel.name, want)
			}
		}
		for _, unwanted := range []string{`class="log-panel-grid"`, `class="log-section"`} {
			if strings.Contains(panel.html, unwanted) {
				t.Errorf("%s panel still uses old card layout %q", panel.name, unwanted)
			}
		}
	}
	for _, want := range []string{`class="log-rating-row"`, `class="log-toggle-row"`, `class="log-duration-pair"`} {
		if !strings.Contains(play, want) {
			t.Errorf("play panel missing %q", want)
		}
	}
	for _, want := range []string{`class="log-field log-field-wide"`, `class="log-band log-danger"`} {
		if !strings.Contains(library, want) {
			t.Errorf("library panel missing %q", want)
		}
	}

	var rows []string
	for rest := play; ; {
		row := divBlockContaining(rest, `class="log-band-row"`)
		if row == "" {
			break
		}
		rows = append(rows, row)
		rest = rest[strings.Index(rest, row)+len(row):]
	}
	if len(rows) != 2 {
		t.Fatalf("play band rows = %d, want 2", len(rows))
	}
	for _, want := range []string{`>Rating</h2>`, `>This game</h2>`} {
		if !strings.Contains(rows[0], want) {
			t.Errorf("first play row missing %q", want)
		}
	}
	for _, want := range []string{`>This playthrough</h2>`, `>Time</h2>`} {
		if !strings.Contains(rows[1], want) {
			t.Errorf("second play row missing %q", want)
		}
	}
	if !strings.Contains(play, `>Play details</h2>`) || strings.Contains(rows[1], `>Play details</h2>`) {
		t.Error("Play details should be a full-width row outside the paired bands")
	}
	if !strings.Contains(rows[0], `name="played"`) || !strings.Contains(rows[0], `name="game_shelf"`) {
		t.Error("the game shelf and times played should be in the This game row")
	}
	if !strings.Contains(rows[1], `name="status"`) || strings.Contains(rows[0], `name="status"`) {
		t.Error("played status belongs under This playthrough, not This game")
	}
	if !strings.Contains(rows[1], `Separate from your game shelf.`) {
		t.Error("played status is missing its shelf hint")
	}
	started := strings.Index(rows[1], `id="started_at"`)
	finished := strings.Index(rows[1], `id="finished_at"`)
	playtime := strings.Index(rows[1], `id="playtime-hours"`)
	if started < 0 || finished <= started || playtime <= finished {
		t.Error("Time should start with Started on and Finished on before the duration fields")
	}
	for _, want := range []string{`name="playtime_minutes"`, `name="time_to_finish_hours"`, `name="time_to_finish_minutes"`, `name="time_to_master_hours"`, `name="time_to_master_minutes"`} {
		if !strings.Contains(rows[1], want) {
			t.Errorf("time row missing %q", want)
		}
	}
}

func TestIssue73MinutePrecisionDurations(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "minutesuser", "minutesuser@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7309, Slug: "minutes-game", Title: "Minutes Game", Category: "main_game", ReleaseStatus: "released"})
	client, sess := authedClient(t, ts, st, user)

	values := url.Values{
		"_csrf":                  {sess.CSRF},
		"status":                 {"completed"},
		"playtime_hours":         {"1"},
		"playtime_minutes":       {"30"},
		"time_to_finish_hours":   {"12"},
		"time_to_finish_minutes": {"15"},
		"time_to_master_hours":   {"30"},
		"time_to_master_minutes": {"45"},
	}
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/games/"+game.Slug+"/log", values); got != http.StatusSeeOther {
		t.Fatalf("submit with minute durations = %d", got)
	}
	log, err := st.LatestLogFor(ctx, user.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if log.Playtime != 1.5 || log.TimeToFinish != 12.25 || log.TimeToMaster != 30.75 {
		t.Fatalf("durations = %v / %v / %v, want 1.5 / 12.25 / 30.75", log.Playtime, log.TimeToFinish, log.TimeToMaster)
	}

	_, body := getBody(t, client, ts.URL+"/logs/"+itoa(log.ID)+"/edit?tab=play")
	for _, want := range []string{
		`name="playtime_hours" value="1"`, `name="playtime_minutes" value="30"`,
		`name="time_to_finish_hours" value="12"`, `name="time_to_finish_minutes" value="15"`,
		`name="time_to_master_hours" value="30"`, `name="time_to_master_minutes" value="45"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("edit form missing %q", want)
		}
	}
}

func TestIssue73LogChipsEmptyAndAddStates(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "chipuser", "chipuser@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7306, Slug: "chip-game", Title: "Chip Game", Category: "main_game", ReleaseStatus: "released"})
	client, _ := authedClient(t, ts, st, user)

	code, body := getBody(t, client, ts.URL+"/games/chip-game/log")
	if code != http.StatusOK {
		t.Fatalf("empty editor = %d", code)
	}
	for _, want := range []string{
		`class="log-chip active" aria-current="page"`,
		"Log 1", `aria-disabled="true"`, "Save this log before adding another",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("empty editor missing %q", want)
		}
	}
	if strings.Contains(body, "No logs yet") {
		t.Error("empty editor still offers No logs yet")
	}

	if _, err := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Name: "Saved", Status: "played"}); err != nil {
		t.Fatal(err)
	}
	_, body = getBody(t, client, ts.URL+"/games/chip-game/log?new=1&tab=journal")
	if !strings.Contains(body, `>New log</a>`) || !strings.Contains(body, "Save this log before adding another") {
		t.Error("new-log editor does not show an active unsaved chip with a disabled add button")
	}
	_, body = getBody(t, client, ts.URL+"/games/chip-game/log")
	if !strings.Contains(body, `class="log-chip log-chip-add"`) {
		t.Error("saved-log editor does not offer Add log")
	}
	if strings.Contains(body, "Save this log before adding another") {
		t.Error("saved-log editor still disables Add log")
	}
}

func TestIssue73GameStatusFromPlayTab(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "gamestatususer", "gamestatususer@example.test", "hash", false)
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7307, Slug: "gamestatus-game", Title: "Game Status Game", Category: "main_game", ReleaseStatus: "released"})
	log, err := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Status: "played"})
	if err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, user)
	target := ts.URL + "/logs/" + itoa(log.ID) + "/edit"
	values := url.Values{
		"_csrf":           {sess.CSRF},
		"game_shelf_form": {"1"},
		"game_shelf":      {"playing"},
	}
	if got := postStatus(t, noRedirectClient(client), target, values); got != http.StatusSeeOther {
		t.Fatalf("save with game shelf = %d", got)
	}
	status, _ := st.StatusFor(ctx, user.ID, game.ID)
	if status.Shelf != "playing" || status.Played < 1 || status.Backlog || status.Wishlist {
		t.Fatalf("game shelf not saved: %+v", status)
	}
	values.Del("game_shelf")
	values.Set("game_shelf_remove", "1")
	if got := postStatus(t, noRedirectClient(client), target, values); got != http.StatusSeeOther {
		t.Fatalf("clear game shelf = %d", got)
	}
	status, _ = st.StatusFor(ctx, user.ID, game.ID)
	if status.Shelf != "" || status.Played != 1 {
		t.Fatalf("game shelf was not cleared without changing the play count: %+v", status)
	}
}

func TestIssue73LogPageFiltersAndProfileGames(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "filteruser", "filteruser@example.test", "hash", false)
	platform := mustSaveGamePlatform(t, st, "Switch")
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7303, Slug: "filter-game", Title: "Filter Game", Category: "main_game", ReleaseStatus: "released", Platforms: []models.Platform{{Name: "Switch"}}})
	if _, err := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Name: "Completed run", Status: "completed", Rating: 9, Review: "great", PlatformID: &platform}); err != nil {
		t.Fatal(err)
	}
	replay, _ := st.CreateLog(ctx, store.LogInput{UserID: user.ID, GameID: game.ID, Name: "Replay run", Status: "replayed", Rating: 4})
	client, _ := authedClient(t, ts, st, user)

	_, body := getBody(t, client, ts.URL+"/users/filteruser/logs/filter-game?status=replayed")
	if !strings.Contains(body, "Replay run") || strings.Contains(body, "Completed run") {
		t.Fatal("status filter did not isolate the replayed log")
	}
	_, listed := getBody(t, client, ts.URL+"/users/filteruser/logs/filter-game")
	if strings.Count(listed, "4.0 / 10") != 1 || strings.Contains(listed, "9.0 / 10") {
		t.Fatal("log page should show the central game rating once, not a per-log rating")
	}

	// The profile Games tab exposes status/rating/platform filters and rows.
	code, gamesTab := getBody(t, client, ts.URL+"/users/filteruser?tab=games")
	if code != http.StatusOK {
		t.Fatalf("games tab = %d", code)
	}
	for _, want := range []string{"Filter Game", `name="platform"`, `name="year"`, `name="tag"`, `name="ownership"`, `name="mastered"`, `name="review"`, "Open log"} {
		if !strings.Contains(gamesTab, want) {
			t.Errorf("games tab missing %q", want)
		}
	}
	_, filtered := getBody(t, client, ts.URL+"/users/filteruser?tab=games&status=played")
	if strings.Contains(filtered, "Filter Game") {
		t.Fatal("games shelf filter ignored played status")
	}
	_ = replay
}

func TestIssue73ExportImportRoundTrip(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "exportuser", "exportuser@example.test", "hash", false)
	platform := mustSaveGamePlatform(t, st, "PC")
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7304, Slug: "export-game", Title: "Export Game", Category: "main_game", ReleaseStatus: "released", Platforms: []models.Platform{{Name: "PC"}}})
	log, err := st.CreateLog(ctx, store.LogInput{
		UserID: user.ID, GameID: game.ID, Name: "Export title", Status: "completed", Rating: 7,
		Review: "Full body", Spoiler: true, Replay: true, Mastered: true, Difficulty: "hard",
		PlatformID: &platform, PlayedOnID: &platform, Ownership: "owned", Storefront: "Steam",
		StartedAt: "2024-02-01", FinishedAt: "2024-02-09", TimeToFinish: 12, TimeToMaster: 30,
		Playtime: 14, SyncSessions: true, Language: "Japanese",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, store.SessionInput{LogID: log.ID, Note: "Boss down", NotePrivacy: "everyone", Playtime: 2, Days: []string{"2024-02-02"}, Tags: []string{"boss", "coop"}}); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, user)
	code, exported := getBody(t, client, ts.URL+"/settings/export")
	if code != http.StatusOK {
		t.Fatalf("export = %d", code)
	}
	for _, want := range []string{`"format":"luduslog"`, `"played_on"`, `"sync_sessions"`, `"sessions"`, `"note_privacy"`, `"edition"`} {
		if !strings.Contains(exported, want) {
			t.Errorf("export missing %q", want)
		}
	}
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/logs/"+itoa(log.ID)+"/delete-all", url.Values{"_csrf": {sess.CSRF}}); got != http.StatusSeeOther {
		t.Fatalf("clear before import = %d", got)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("_csrf", sess.CSRF); err != nil {
		t.Fatal(err)
	}
	part, _ := mw.CreateFormFile("file", "luduslog-export.json")
	part.Write([]byte(exported))
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/settings/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := noRedirectClient(client).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import = %d", resp.StatusCode)
	}
	restored, err := st.LatestLogFor(ctx, user.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name != "Export title" || restored.Rating != 7 || restored.Status != "completed" ||
		restored.PlayedOnID == nil || *restored.PlayedOnID != platform || restored.Ownership != "owned" ||
		restored.Storefront != "Steam" || restored.Language != "Japanese" || !restored.SyncSessions ||
		restored.StartedAt != "2024-02-01" || restored.FinishedAt != "2024-02-09" || restored.TimeToFinish != 12 {
		t.Fatalf("restored log lost fields: %+v", restored)
	}
	sessions, _ := st.SessionsFor(ctx, restored.ID)
	if len(sessions) != 1 || sessions[0].Note != "Boss down" || sessions[0].NotePrivacy != "everyone" ||
		len(sessions[0].Days) != 1 || len(sessions[0].Tags) != 2 {
		t.Fatalf("restored sessions = %+v", sessions)
	}
}

func TestIssue73FederatesStatusChanges(t *testing.T) {
	ts, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "feduser", "feduser@example.test", "hash", false)
	if err := st.UpdateProfile(ctx, user.ID, store.ProfileUpdate{Federation: true, FedStatuses: true}); err != nil {
		t.Fatal(err)
	}
	game := mustSaveGame(t, st, &models.Game{IGDBID: -7305, Slug: "fed-status-game", Title: "Fed Status Game", Category: "main_game", ReleaseStatus: "released"})
	if err := st.SaveRemoteActor(ctx, &store.RemoteActor{
		ActorURI: "https://remote.example/users/bob", Domain: "remote.example", Username: "bob",
		Name: "Bob", Inbox: "https://remote.example/users/bob/inbox",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRemoteFollow(ctx, "https://remote.example/users/bob", user.ID); err != nil {
		t.Fatal(err)
	}
	client, sess := authedClient(t, ts, st, user)
	if got := postStatus(t, noRedirectClient(client), ts.URL+"/games/"+game.Slug+"/status", url.Values{
		"_csrf": {sess.CSRF}, "shelf": {"playing"},
	}); got != http.StatusSeeOther {
		t.Fatalf("status = %d", got)
	}
	var jobs int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='deliver'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("delivery jobs = %d, want 1", jobs)
	}
}

func TestIssue73ProfileDefaultStatusScanned(t *testing.T) {
	_, st := testServer(t)
	ctx := context.Background()
	user, _ := st.CreateUser(ctx, "statususer", "statususer@example.test", "hash", false)
	if user.DefaultPlayedStatus != "played" {
		t.Fatalf("new user default = %q", user.DefaultPlayedStatus)
	}
	if err := st.UpdateProfile(ctx, user.ID, store.ProfileUpdate{DefaultPlayedStatus: "retired"}); err != nil {
		t.Fatal(err)
	}
	updated, _ := st.UserByID(ctx, user.ID)
	if updated.DefaultPlayedStatus != "retired" {
		t.Fatalf("stored default = %q", updated.DefaultPlayedStatus)
	}
}
