package web

import (
	"net/http"
	"strconv"
	"strings"

	"luduslog/internal/models"
)

type postPageData struct {
	Page
	Post        *models.Post
	Comments    []*models.Comment
	CanReply    bool
	SubjectType string
	SubjectID   int64
	Path        string
}

func (s *Server) postPage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	var viewerID int64
	if u != nil {
		viewerID = u.ID
	}
	p, err := s.st.PostByID(r.Context(), id, viewerID)
	if err != nil || !s.canViewPost(r, p, u) {
		s.notFound(w, r)
		return
	}
	if p.APID == "" {
		p.APID = s.absolute("/posts/" + itoa(p.ID))
		s.st.SetPostAPID(r.Context(), p.ID, p.APID)
	}
	if wantsActivityJSON(r) {
		s.writePostActivity(w, r, p)
		return
	}
	comments, _ := s.st.CommentsFor(r.Context(), "post", p.ID)
	data := postPageData{
		Page: s.page(r, postTitle(p)), Post: p, Comments: comments,
		SubjectType: "post", SubjectID: p.ID, Path: r.URL.RequestURI(),
	}
	data.CSRF, _ = r.Context().Value(ctxCSRF).(string)
	if u != nil {
		data.CanReply = s.st.CommentAllowed(r.Context(), u.ID, p.UserID)
	}
	s.render(w, r, "post", data)
}

func (s *Server) canViewPost(r *http.Request, p *models.Post, viewer *models.User) bool {
	if p == nil {
		return false
	}
	if p.Game != nil && p.Game.Adult && (viewer == nil || !viewer.AdultOptIn) {
		return false
	}
	switch p.Visibility {
	case "public":
		return true
	case "followers":
		if viewer == nil {
			return false
		}
		if viewer.ID == p.UserID {
			return true
		}
		ok, approved := s.st.IsFollowing(r.Context(), viewer.ID, p.UserID)
		return ok && approved
	default:
		return viewer != nil && viewer.ID == p.UserID
	}
}

func postTitle(p *models.Post) string {
	if p.Log != nil && p.Log.Name != "" {
		return p.Log.Name
	}
	if p.Game != nil {
		return p.Game.Title
	}
	return "Post"
}

func (s *Server) postLike(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	p, err := s.st.PostByID(r.Context(), id, u.ID)
	if err != nil {
		s.notFound(w, r)
		return
	}
	liked, err := s.st.TogglePostLike(r.Context(), p.ID, u.ID)
	if err == nil && liked && p.UserID != u.ID {
		s.notifyContext(r, p.UserID, u.ID, "like", "post", p.ID, "/posts/"+itoa(p.ID))
	}
	if err == nil {
		s.federatePostLike(r, u, p, liked)
	}
	s.redirectBack(w, r, "/posts/"+itoa(id))
}

func (s *Server) postBoost(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	p, err := s.st.PostByID(r.Context(), id, u.ID)
	if err != nil {
		s.notFound(w, r)
		return
	}
	boosted, err := s.st.TogglePostBoost(r.Context(), p.ID, u.ID)
	if err == nil && boosted && p.UserID != u.ID {
		s.notifyContext(r, p.UserID, u.ID, "boost", "post", p.ID, "/posts/"+itoa(p.ID))
	}
	if err == nil {
		s.federatePostBoost(r, u, p, boosted)
	}
	s.redirectBack(w, r, "/posts/"+itoa(id))
}

func (s *Server) postDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := currentUser(r)
	p, err := s.st.PostByID(r.Context(), id, u.ID)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if p.UserID != u.ID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := s.st.DeletePost(r.Context(), id, u.ID); err == nil {
		s.federatePostDelete(r, u, p)
	}
	s.redirectBack(w, r, "/feed")
}

func (s *Server) postLikesCollection(w http.ResponseWriter, r *http.Request) {
	s.writePostCollection(w, r, "post_likes")
}

func (s *Server) postSharesCollection(w http.ResponseWriter, r *http.Request) {
	s.writePostCollection(w, r, "post_boosts")
}

func (s *Server) postRepliesCollection(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ids := s.st.PostReplies(r.Context(), id)
	items := make([]string, 0, len(ids))
	for _, cid := range ids {
		items = append(items, s.absolute("/posts/"+itoa(id)+"#c"+itoa(cid)))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           s.absolute("/posts/" + itoa(id) + "/replies"),
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}, activityJSON)
}

func (s *Server) writePostCollection(w http.ResponseWriter, r *http.Request, table string) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	actors := s.st.PostActors(r.Context(), id, table)
	items := make([]string, 0, len(actors))
	for _, a := range actors {
		if strings.HasPrefix(a, "/") {
			a = s.absolute(a)
		}
		items = append(items, a)
	}
	name := "likes"
	if table == "post_boosts" {
		name = "shares"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           s.absolute("/posts/" + itoa(id) + "/" + name),
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}, activityJSON)
}

func (s *Server) writePostActivity(w http.ResponseWriter, r *http.Request, p *models.Post) {
	obj := map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           p.APID,
		"type":         "Note",
		"attributedTo": s.absolute("/users/" + p.User.Username),
		"content":      markdownHTML(p.Body),
		"published":    p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"url":          s.absolute("/posts/" + itoa(p.ID)),
		"to":           []string{"https://www.w3.org/ns/activitystreams#Public"},
		"likes":        s.absolute("/posts/" + itoa(p.ID) + "/likes"),
		"shares":       s.absolute("/posts/" + itoa(p.ID) + "/shares"),
		"replies":      s.absolute("/posts/" + itoa(p.ID) + "/replies"),
	}
	if p.Game != nil {
		obj["attachment"] = []map[string]any{{
			"type": "Document", "name": p.Game.Title, "url": s.absolute("/games/" + p.Game.Slug),
		}}
	}
	writeJSON(w, http.StatusOK, obj, activityJSON)
}

// deleteCanonicalPost soft-deletes the post attached to content the owner just
// removed, so the permalink and its interactions disappear too.
func (s *Server) deleteCanonicalPost(r *http.Request, u *models.User, subjectType string, subjectID int64) {
	p, err := s.st.PostForSubject(r.Context(), subjectType, subjectID, u.ID)
	if err != nil || p == nil {
		return
	}
	if err := s.st.DeletePost(r.Context(), p.ID, p.UserID); err == nil {
		s.federatePostDelete(r, u, p)
	}
}

func (s *Server) redirectBack(w http.ResponseWriter, r *http.Request, fallback string) {
	if to := safeReturnTo(r.FormValue("return_to")); to != "" {
		s.redirect(w, r, to)
		return
	}
	s.redirect(w, r, fallback)
}

func wantsActivityJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/activity+json") || strings.Contains(accept, "application/ld+json")
}
