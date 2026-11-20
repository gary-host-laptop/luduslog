package web

import (
	"net/http"
	"strconv"
	"strings"

	"luduslog/internal/catalog"
	"luduslog/internal/models"
)

func (s *Server) adminFetchGame(w http.ResponseWriter, r *http.Request) {
	if s.igdb == nil {
		s.flash(w, r, "error", "IGDB is not configured on this instance.")
		s.redirect(w, r, "/search?q="+r.FormValue("q"))
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("igdb_id"), 10, 64)
	if id <= 0 {
		s.flash(w, r, "error", "Enter a valid IGDB game ID.")
		s.redirect(w, r, "/search?q="+r.FormValue("q"))
		return
	}
	games, err := s.igdb.GamesByIDs(r.Context(), []int64{id})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(games) == 0 {
		s.flash(w, r, "error", "IGDB has no game with that ID.")
		s.redirect(w, r, "/search?q="+r.FormValue("q"))
		return
	}
	res, err := catalog.Save(r.Context(), s.st, &games[0])
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.st.ResolveGameParents(r.Context())
	u := currentUser(r)
	if u != nil {
		s.st.Audit(r.Context(), u.ID, "fetch_game", res.Game.Title+" (IGDB "+strconv.FormatInt(res.Game.IGDBID, 10)+")")
	}
	action := "added"
	if !res.Created {
		action = "updated"
	}
	s.flash(w, r, "success", action+" "+res.Game.Title+" from IGDB.")
	s.redirect(w, r, "/games/"+res.Game.Slug)
}

func (s *Server) adminRefreshGame(w http.ResponseWriter, r *http.Request) {
	if s.igdb == nil {
		s.flash(w, r, "error", "IGDB is not configured on this instance.")
		s.redirect(w, r, "/games/"+r.PathValue("slug"))
		return
	}
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	var fetched *models.Game
	if g.IGDBID > 0 {
		games, err := s.igdb.GamesByIDs(r.Context(), []int64{g.IGDBID})
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(games) > 0 {
			fetched = &games[0]
		}
	} else {
		games, err := s.igdb.Search(r.Context(), g.Title, 10)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		for i := range games {
			if strings.EqualFold(games[i].Slug, g.Slug) {
				fetched = &games[i]
				break
			}
		}
		if fetched == nil && len(games) > 0 {
			fetched = &games[0]
		}
	}
	if fetched == nil {
		s.flash(w, r, "error", "IGDB has no matching game for "+g.Title+".")
		s.redirect(w, r, "/games/"+g.Slug)
		return
	}
	res, err := catalog.Save(r.Context(), s.st, fetched)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.st.ResolveGameParents(r.Context())
	u := currentUser(r)
	if u != nil {
		s.st.Audit(r.Context(), u.ID, "refresh_game", res.Game.Title+" (IGDB "+strconv.FormatInt(res.Game.IGDBID, 10)+")")
	}
	s.flash(w, r, "success", "Refreshed "+res.Game.Title+" from IGDB.")
	s.redirect(w, r, "/games/"+res.Game.Slug)
}
