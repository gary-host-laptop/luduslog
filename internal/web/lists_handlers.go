package web

import (
	"net/http"
	"strconv"
	"strings"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

type listData struct {
	Page
	List      *models.List
	Entries   []models.ListEntry
	IsOwner   bool
	Liked     bool
	Games     []models.Game
	Note      string
	Error     string
	Action    string
	Folders   []models.Folder
	Query     string
	Results   []models.Game
	PublicURL string
}

func (s *Server) listPage(w http.ResponseWriter, r *http.Request) {
	owner, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	l, err := s.st.ListByUserSlug(r.Context(), owner.ID, r.PathValue("slug"), true)
	if err != nil {
		s.notFound(w, r)
		return
	}
	viewer := currentUser(r)
	isOwner := viewer != nil && viewer.ID == owner.ID
	if l.Privacy == "private" && !isOwner {
		s.notFound(w, r)
		return
	}
	if l.Privacy == "personal" && (viewer == nil || (!isOwner && !(func() bool { ok, _ := s.st.IsFollowing(r.Context(), viewer.ID, owner.ID); return ok })())) {
		s.notFound(w, r)
		return
	}
	entries, _ := s.st.GamesInList(r.Context(), l.ID)
	liked := false
	if viewer != nil {
		liked = s.st.HasLikedList(r.Context(), l.ID, viewer.ID)
	}
	data := listData{
		Page:      s.page(r, l.Title),
		List:      l,
		Entries:   entries,
		IsOwner:   isOwner,
		Liked:     liked,
		PublicURL: s.absolute("/users/" + owner.Username + "/lists/" + l.Slug),
	}
	if isOwner {
		data.Folders, _ = s.st.Folders(r.Context(), owner.ID)
	}
	s.render(w, r, "list", data)
}

type listFormData struct {
	Page
	List   *models.List
	Action string
	Error  string
}

func (s *Server) listForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "list_form", listFormData{Page: s.page(r, "New list"), Action: "/lists/new"})
}

func listInputFrom(r *http.Request, userID int64) store.ListInput {
	tags := []string{}
	for _, t := range strings.Split(r.FormValue("tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return store.ListInput{
		UserID:      userID,
		Title:       strings.TrimSpace(r.FormValue("title")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Tags:        tags,
		Privacy:     r.FormValue("privacy"),
		Ranked:      r.FormValue("ranked") == "1",
		Progress:    r.FormValue("progress") == "1",
		DefaultSort: r.FormValue("default_sort"),
		SortDir:     r.FormValue("sort_dir"),
		FadePlayed:  r.FormValue("fade_played") == "1",
		DisplayMode: r.FormValue("display_mode"),
		Immersive:   r.FormValue("immersive") == "1",
	}
}

func (s *Server) listCreate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	in := listInputFrom(r, u.ID)
	if in.Title == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "list_form", listFormData{Page: s.page(r, "New list"), Action: "/lists/new", Error: "Give your list a title."})
		return
	}
	l, err := s.st.CreateList(r.Context(), in)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, r, "success", "List created.")
	s.redirect(w, r, "/users/"+u.Username+"/lists/"+l.Slug)
}

func (s *Server) listEditForm(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.ListByID(r.Context(), id, false)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	s.render(w, r, "list_form", listFormData{Page: s.page(r, "Edit "+l.Title), List: l, Action: "/lists/" + itoa(id) + "/edit"})
}

func (s *Server) listUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.ListByID(r.Context(), id, false)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	in := listInputFrom(r, u.ID)
	if err := s.st.UpdateList(r.Context(), id, u.ID, in); err != nil {
		s.serverError(w, r, err)
		return
	}
	updated, _ := s.st.ListByID(r.Context(), id, false)
	s.flash(w, r, "success", "List updated.")
	s.redirect(w, r, "/users/"+u.Username+"/lists/"+updated.Slug)
}

func (s *Server) listDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	s.st.DeleteList(r.Context(), id, u.ID)
	s.flash(w, r, "success", "List deleted.")
	s.redirect(w, r, "/users/"+u.Username+"?tab=lists")
}

func (s *Server) listAddGame(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.ListByID(r.Context(), id, false)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	gameID, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
	if gameID == 0 {
		slug := r.FormValue("game_slug")
		if g, err := s.st.GameBySlug(r.Context(), slug); err == nil {
			gameID = g.ID
		}
	}
	if gameID == 0 {
		s.flash(w, r, "error", "Choose a game to add.")
		s.redirect(w, r, "/users/"+u.Username+"/lists/"+l.Slug)
		return
	}
	if err := s.st.AddListEntry(r.Context(), id, gameID, strings.TrimSpace(r.FormValue("note"))); err != nil && !store.IsUnique(err) {
		s.serverError(w, r, err)
		return
	}
	s.st.AddActivity(r.Context(), u.ID, "list_add", &gameID, nil, &l.ID, "", "public")
	s.flash(w, r, "success", "Game added to list.")
	s.redirect(w, r, "/users/"+u.Username+"/lists/"+l.Slug)
}

func (s *Server) listRemoveGame(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.ListByID(r.Context(), id, false)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	gameID, _ := strconv.ParseInt(r.FormValue("game_id"), 10, 64)
	s.st.RemoveListEntry(r.Context(), id, gameID)
	s.flash(w, r, "success", "Game removed.")
	s.redirect(w, r, "/users/"+u.Username+"/lists/"+l.Slug)
}

func (s *Server) listReorder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.ListByID(r.Context(), id, false)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	var ids []int64
	for _, v := range strings.Split(r.FormValue("order"), ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			ids = append(ids, n)
		}
	}
	if err := s.st.ReorderList(r.Context(), id, ids); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) likeList(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.ListByID(r.Context(), id, false)
	if err != nil {
		s.notFound(w, r)
		return
	}
	like := !s.st.HasLikedList(r.Context(), id, u.ID)
	if err := s.st.LikeList(r.Context(), id, u.ID, like); err != nil {
		s.serverError(w, r, err)
		return
	}
	if like && l.UserID != u.ID {
		s.notifyContext(r, l.UserID, u.ID, "like", "list", id, s.absolute("/users/"+l.User.Username+"/lists/"+l.Slug))
	}
	s.redirect(w, r, "/users/"+l.User.Username+"/lists/"+l.Slug)
}

func (s *Server) listFolderAdd(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	l, err := s.st.ListByID(r.Context(), id, false)
	if err != nil || l.UserID != u.ID {
		s.notFound(w, r)
		return
	}
	folderID, _ := strconv.ParseInt(r.FormValue("folder_id"), 10, 64)
	if folderID > 0 {
		s.st.AddListToFolder(r.Context(), folderID, id)
		s.flash(w, r, "success", "List added to folder.")
	}
	s.redirect(w, r, "/users/"+u.Username+"/lists/"+l.Slug)
}

// listPicker is used by the game page to add a game to a list via htmx.
func (s *Server) listPicker(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	lists, _ := s.st.ListsForUser(r.Context(), u.ID, true, 100, 0)
	g, _ := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	s.renderFragment(w, "game", "list_picker", map[string]any{"Lists": lists, "Game": g, "User": u})
}
