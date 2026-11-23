package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"luduslog/internal/catalog"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

type catalogFormData struct {
	Page
	Game    *models.Game
	Input   catalog.GameInput
	Errors  []string
	Action  string
	Heading string
}

func (s *Server) catalogEditor(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r); !u.CanEditCatalog() {
			s.notFound(w, r)
			return
		}
		next(w, r)
	})
}

func (s *Server) newGameForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "game_edit", catalogFormData{
		Page:    s.page(r, "New game"),
		Input:   catalog.GameInput{ReleaseStatus: "released", Category: "main_game"},
		Action:  "/games/new",
		Heading: "New game",
	})
}

func (s *Server) newGameSubmit(w http.ResponseWriter, r *http.Request) {
	s.saveCatalogGame(w, r, "", true)
}

func (s *Server) editGameForm(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	g, err = s.st.GameDetail(r.Context(), g)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	relations, err := s.st.CatalogRelations(r.Context(), g.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	in := catalog.InputFromGame(g, relations, s.parentSlug(r, g))
	toMinutes(&in)
	s.render(w, r, "game_edit", catalogFormData{
		Page:    s.page(r, "Edit "+g.Title),
		Game:    g,
		Input:   in,
		Action:  "/games/" + g.Slug + "/edit",
		Heading: "Edit " + g.Title,
	})
}

func (s *Server) editGameSubmit(w http.ResponseWriter, r *http.Request) {
	s.saveCatalogGame(w, r, r.PathValue("slug"), false)
}

func (s *Server) saveCatalogGame(w http.ResponseWriter, r *http.Request, slug string, create bool) {
	cat := catalog.New(s.st)
	in := parseGameInput(r)
	fromSeconds(&in)
	var (
		g   *models.Game
		err error
	)
	if create {
		g, err = cat.Create(r.Context(), in)
	} else {
		g, err = cat.Update(r.Context(), slug, in)
	}
	if catalog.IsValidation(err) {
		toMinutes(&in)
		heading := "New game"
		action := "/games/new"
		if !create {
			heading = "Edit game"
			action = "/games/" + slug + "/edit"
		}
		s.renderStatus(w, r, "game_edit", http.StatusUnprocessableEntity, catalogFormData{
			Page:    s.page(r, heading),
			Input:   in,
			Errors:  err.(*catalog.ValidationError).Problems,
			Action:  action,
			Heading: heading,
		})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	u := currentUser(r)
	action := "edit_game"
	if create {
		action = "create_game"
	}
	detail := g.Title + " (" + g.Slug + ")"
	if err := s.st.Audit(r.Context(), u.ID, action, detail); err != nil {
		s.flash(w, r, "error", "Saved "+g.Title+" but the audit entry failed: "+err.Error())
		s.redirect(w, r, "/games/"+g.Slug)
		return
	}
	if create {
		s.flash(w, r, "success", "Created "+g.Title+".")
	} else {
		s.flash(w, r, "success", "Updated "+g.Title+".")
	}
	s.redirect(w, r, "/games/"+g.Slug)
}

func (s *Server) parentSlug(r *http.Request, g *models.Game) string {
	if g.ParentID == nil {
		return ""
	}
	if parent, err := s.st.GameByID(r.Context(), *g.ParentID); err == nil {
		return parent.Slug
	}
	return ""
}

func parseGameInput(r *http.Request) catalog.GameInput {
	year, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("release_year")))
	in := catalog.GameInput{
		Title:          r.FormValue("title"),
		Summary:        r.FormValue("summary"),
		CoverURL:       r.FormValue("cover_url"),
		BannerURL:      r.FormValue("banner_url"),
		ReleaseDate:    r.FormValue("release_date"),
		ReleaseYear:    year,
		ReleaseStatus:  r.FormValue("release_status"),
		Category:       r.FormValue("category"),
		Franchise:      r.FormValue("franchise"),
		EditionName:    r.FormValue("edition_name"),
		ParentSlug:     r.FormValue("parent_slug"),
		Adult:          r.FormValue("adult") == "1",
		Restricted:     r.FormValue("restricted") == "1",
		Platforms:      formLines(r, "platforms"),
		Genres:         formLines(r, "genres"),
		Themes:         formLines(r, "themes"),
		Modes:          formLines(r, "modes"),
		AltNames:       formLines(r, "alt_names"),
		Screenshots:    formLines(r, "screenshots"),
		Videos:         formLines(r, "videos"),
		TimeHastily:    formMinutes(r, "time_hastily"),
		TimeNormally:   formMinutes(r, "time_normally"),
		TimeCompletely: formMinutes(r, "time_completely"),
		Relations:      map[string][]string{},
	}
	for _, field := range []string{"related", "remakes", "updates", "ports", "bundles", "contents"} {
		in.Relations[field] = formLines(r, "related_"+field)
	}
	for _, line := range formLines(r, "companies") {
		parts := splitLine(line)
		role := "developer"
		if len(parts) > 1 {
			role = parts[1]
		}
		in.Companies = append(in.Companies, models.CompanyRole{Name: parts[0], Role: role})
	}
	for _, line := range formLines(r, "releases") {
		parts := splitLine(line)
		rd := models.ReleaseDate{Platform: parts[0]}
		if len(parts) > 1 {
			rd.Region = parts[1]
		}
		if len(parts) > 2 {
			rd.Date = parts[2]
		}
		if len(parts) > 3 {
			rd.Status = parts[3]
		}
		in.Releases = append(in.Releases, rd)
	}
	return in
}

func formLines(r *http.Request, name string) []string {
	var out []string
	for _, line := range strings.Split(r.FormValue(name), "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func formMinutes(r *http.Request, name string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	if n < 0 {
		return -1
	}
	return n
}

func splitLine(line string) []string {
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// The form edits time-to-beat in minutes; IGDB stores seconds.
func fromSeconds(in *catalog.GameInput) {
	in.TimeHastily *= 60
	in.TimeNormally *= 60
	in.TimeCompletely *= 60
}

func toMinutes(in *catalog.GameInput) {
	in.TimeHastily /= 60
	in.TimeNormally /= 60
	in.TimeCompletely /= 60
}
