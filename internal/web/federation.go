package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"luduslog/internal/federation"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

const activityJSON = "application/activity+json; charset=utf-8"

func writeJSON(w http.ResponseWriter, code int, v any, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := 200
	if err := s.st.DB.PingContext(r.Context()); err != nil {
		status = "degraded"
		code = 503
	}
	writeJSON(w, code, map[string]any{"status": status, "name": s.instanceConfig(r.Context()).Name, "time": time.Now().UTC()}, "application/json")
}

func (s *Server) actorKey(r *http.Request, u *models.User) (string, error) {
	existing, err := s.st.ActorKey(r.Context(), u.ID)
	if err == nil && existing != "" {
		return existing, nil
	}
	pem, err := federation.GenerateKey()
	if err != nil {
		return "", err
	}
	if err := s.st.SetActorKey(r.Context(), u.ID, pem); err != nil {
		return "", err
	}
	return pem, nil
}

func (s *Server) actorURL(u *models.User) string {
	return s.absolute("/users/" + url.PathEscape(u.Username))
}

func (s *Server) actorDocument(r *http.Request, u *models.User) (map[string]any, error) {
	key, err := s.actorKey(r, u)
	if err != nil {
		return nil, err
	}
	actor := s.actorURL(u)
	doc := map[string]any{
		"@context":                  []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":                        actor,
		"type":                      "Person",
		"preferredUsername":         u.Username,
		"name":                      u.Username,
		"summary":                   markdownHTML(u.Bio),
		"url":                       actor,
		"inbox":                     actor + "/inbox",
		"outbox":                    actor + "/outbox",
		"followers":                 actor + "/followers",
		"following":                 actor + "/following",
		"manuallyApprovesFollowers": u.ManuallyApproves,
		"discoverable":              u.Federation,
		"published":                 u.CreatedAt.UTC().Format(time.RFC3339),
		"publicKey": map[string]any{
			"id": actor + "#main-key", "owner": actor, "publicKeyPem": key,
		},
		"attachment": []any{map[string]any{"type": "PropertyValue", "name": "Profile", "value": actor}},
	}
	if u.Avatar != "" {
		doc["icon"] = map[string]any{"type": "Image", "url": absoluteURL(s.cfg.BaseURL, u.Avatar)}
	}
	if u.Header != "" {
		doc["image"] = map[string]any{"type": "Image", "url": absoluteURL(s.cfg.BaseURL, u.Header)}
	}
	for _, l := range s.st.SocialLinksFor(r.Context(), u.ID) {
		doc["attachment"] = append(doc["attachment"].([]any), map[string]any{"type": "PropertyValue", "name": l.Site, "value": l.URL})
	}
	return doc, nil
}

func (s *Server) apActor(w http.ResponseWriter, r *http.Request, u *models.User) {
	doc, err := s.actorDocument(r, u)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, 200, doc, activityJSON)
}

func (s *Server) webfinger(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if !strings.HasPrefix(resource, "acct:") {
		http.Error(w, "bad resource", 400)
		return
	}
	acct := strings.TrimPrefix(resource, "acct:")
	at := strings.LastIndex(acct, "@")
	if at <= 0 {
		http.Error(w, "bad resource", 400)
		return
	}
	name, host := acct[:at], acct[at+1:]
	base, _ := url.Parse(s.cfg.BaseURL)
	if !strings.EqualFold(host, base.Host) {
		http.NotFound(w, r)
		return
	}
	u, err := s.st.UserByUsername(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	actor := s.actorURL(u)
	writeJSON(w, 200, map[string]any{
		"subject": "acct:" + u.Username + "@" + base.Host,
		"aliases": []string{actor},
		"links": []any{
			map[string]any{"rel": "self", "type": "application/activity+json", "href": actor},
			map[string]any{"rel": "http://webfinger.net/rel/profile-page", "type": "text/html", "href": actor},
		},
	}, "application/jrd+json")
}

func (s *Server) nodeinfoDiscovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"links": []any{map[string]any{
		"rel": "http://nodeinfo.diaspora.software/ns/schema/2.1", "href": s.absolute("/nodeinfo/2.1"),
	}}}, "application/json")
}

func (s *Server) nodeinfo(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceConfig(r.Context())
	var users, posts, comments int
	s.st.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE deactivated_at IS NULL`).Scan(&users)
	s.st.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM logs WHERE draft=0`).Scan(&posts)
	s.st.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM comments WHERE deleted_at IS NULL`).Scan(&comments)
	writeJSON(w, 200, map[string]any{
		"version":           "2.1",
		"software":          map[string]any{"name": "luduslog", "version": "0.1.0"},
		"protocols":         []string{"activitypub"},
		"services":          map[string]any{"inbound": []string{}, "outbound": []string{}},
		"openRegistrations": inst.Registration == "open",
		"usage":             map[string]any{"users": map[string]any{"total": users}, "localPosts": posts, "localComments": comments},
		"metadata":          map[string]any{"nodeName": inst.Name, "nodeDescription": inst.InstanceDesc},
	}, "application/json")
}

func (s *Server) outboxCollection(r *http.Request, u *models.User) (map[string]any, error) {
	items := []any{}
	// Journal-only entries, drafts and private profiles stay off the outbox.
	if u.Federation && u.FedReviews && !u.PrivateProfile {
		logs, _, err := s.st.UserLogs(r.Context(), u.ID, false, 40, 0)
		if err != nil {
			return nil, err
		}
		for _, l := range logs {
			if l.Draft || l.Review == "" {
				continue
			}
			items = append(items, s.reviewActivity(u, l))
		}
	}
	return s.collection(s.actorURL(u)+"/outbox", items), nil
}

func (s *Server) apOutbox(w http.ResponseWriter, r *http.Request) {
	u, ok := s.apTarget(w, r)
	if !ok {
		return
	}
	col, err := s.outboxCollection(r, u)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, 200, col, activityJSON)
}

func (s *Server) apFollowers(w http.ResponseWriter, r *http.Request) {
	u, ok := s.apTarget(w, r)
	if !ok {
		return
	}
	items := []any{}
	if !u.HideSocialGraph {
		for _, f := range s.st.LocalFollowers(r.Context(), u.ID) {
			items = append(items, s.actorURL(f))
		}
		for _, f := range s.st.RemoteFollowers(r.Context(), u.ID) {
			items = append(items, f.ActorURI)
		}
	}
	writeJSON(w, 200, s.collection(s.actorURL(u)+"/followers", items), activityJSON)
}

func (s *Server) apFollowing(w http.ResponseWriter, r *http.Request) {
	u, ok := s.apTarget(w, r)
	if !ok {
		return
	}
	users, _ := s.st.Following(r.Context(), u.ID, 1000, 0)
	items := []any{}
	for _, f := range users {
		items = append(items, s.actorURL(f))
	}
	writeJSON(w, 200, s.collection(s.actorURL(u)+"/following", items), activityJSON)
}

func (s *Server) collection(id string, items []any) map[string]any {
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           id,
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}
}

func (s *Server) apTarget(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	u, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	return u, true
}

func (s *Server) reviewActivity(u *models.User, l *models.Log) map[string]any {
	actor := s.actorURL(u)
	page := s.absolute(fmt.Sprintf("/users/%s/reviews/%d", url.PathEscape(u.Username), l.ID))
	note := map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           page,
		"type":         "Note",
		"attributedTo": actor,
		"actor":        actor,
		"content":      markdownHTML(l.Review),
		"summary":      l.Name,
		"published":    l.CreatedAt.UTC().Format(time.RFC3339),
		"to":           []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc":           []string{actor + "/followers"},
		"url":          page,
		"inReplyTo":    nil,
	}
	if l.Game != nil {
		note["attachment"] = []any{map[string]any{
			"type": "Document", "name": l.Game.Title, "url": s.absolute("/games/" + url.PathEscape(l.Game.Slug)),
		}}
		note["tag"] = []any{map[string]any{"type": "Hashtag", "name": "#" + gameTag(l.Game.Title)}}
	}
	if l.Rating > 0 {
		note["rating"] = l.Rating / 2
		note["ratingScale"] = 5
	}
	if l.Spoiler {
		note["sensitive"] = true
	}
	return map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        page + "/activity",
		"type":      "Create",
		"actor":     actor,
		"published": l.CreatedAt.UTC().Format(time.RFC3339),
		"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
		"object":    note,
	}
}

func gameTag(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Server) apGameActivity(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	logs, _, err := s.st.ReviewsForGame(r.Context(), g.ID, "new", 40, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	items := []any{}
	for _, l := range logs {
		if l.User != nil && l.User.Federation && l.User.FedReviews && !l.User.PrivateProfile && !l.Draft && l.Review != "" {
			items = append(items, s.reviewActivity(l.User, l))
		}
	}
	writeJSON(w, 200, s.collection(s.absolute("/games/"+url.PathEscape(g.Slug)+"/activity"), items), activityJSON)
}

func (s *Server) apInbox(w http.ResponseWriter, r *http.Request) {
	target, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if target.DeactivatedAt != nil {
		http.Error(w, "account deactivated", http.StatusGone)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	err = federation.VerifyRequest(r, body, func(keyID string) (string, error) {
		host := hostOf(keyID)
		if host != "" && s.st.IsDomainBlocked(r.Context(), host) {
			return "", fmt.Errorf("domain blocked")
		}
		return federation.FetchPublicKey(client, keyID)
	})
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "signature verification failed"}, "application/json")
		return
	}
	act, err := federation.ParseActivity(body)
	if err != nil {
		http.Error(w, "bad activity", 400)
		return
	}
	if act.ID != "" && s.st.InboxSeen(r.Context(), act.ID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if act.ID != "" {
		s.st.MarkInboxSeen(r.Context(), act.ID, act.Actor, act.Type)
	}
	actor, _ := s.remoteActor(r, client, act.Actor)
	switch act.Type {
	case "Follow":
		object := rawString(act.Object)
		if object == "" {
			object = act.Target
		}
		if object == s.actorURL(target) || strings.HasSuffix(object, "/users/"+target.Username) {
			if actor != nil {
				s.st.SaveRemoteActor(r.Context(), actor)
				if !target.ManuallyApproves {
					s.st.AddRemoteFollow(r.Context(), actor.ActorURI, target.ID)
					s.enqueueDeliveryActivity(r, target, map[string]any{
						"@context": "https://www.w3.org/ns/activitystreams",
						"id":       s.actorURL(target) + "#accepts/" + fmt.Sprint(time.Now().UnixNano()),
						"type":     "Accept",
						"actor":    s.actorURL(target),
						"object":   act.ID,
						"to":       []string{actor.ActorURI},
					}, actor.Inbox)
					s.st.Notify(r.Context(), target.ID, "follow", nil, "user", int64(len(actor.ActorURI)), actor.Name+" followed you", "")
				}
			}
		}
	case "Undo":
		s.handleRemoteUndo(r, act, target, actor)
	case "Create":
		s.handleRemoteCreate(r, act, target)
	case "Like":
		s.handleRemoteLike(r, act, actor)
	case "Announce":
		s.handleRemoteAnnounce(r, act, actor)
	case "Delete":
		s.handleRemoteDelete(r, act)
	case "Update":
		s.handleRemoteUpdate(r, act)
	case "Flag":
		s.handleRemoteFlag(r, act, actor)
	}
	w.WriteHeader(http.StatusAccepted)
}

// objectID accepts either a bare URI or an embedded object with an id.
func objectID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var id string
		json.Unmarshal(raw, &id)
		return id
	}
	var obj struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &obj)
	return obj.ID
}

func (s *Server) localPostByObject(r *http.Request, object string) *models.Post {
	if object == "" {
		return nil
	}
	if p, err := s.st.PostByAPID(r.Context(), object, 0); err == nil {
		return p
	}
	if strings.Contains(object, "/posts/") {
		part := object[strings.LastIndex(object, "/posts/")+len("/posts/"):]
		part = strings.TrimSuffix(strings.SplitN(part, "#", 2)[0], "/")
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			if p, err := s.st.PostByID(r.Context(), id, 0); err == nil {
				return p
			}
		}
	}
	return nil
}

func (s *Server) handleRemoteLike(r *http.Request, act *federation.InboxActivity, actor *store.RemoteActor) {
	p := s.localPostByObject(r, objectID(act.Object))
	if p == nil {
		return
	}
	if err := s.st.AddRemotePostLike(r.Context(), p.ID, act.Actor); err == nil && p.UserID != 0 {
		name := act.Actor
		if actor != nil && actor.Name != "" {
			name = actor.Name
		}
		s.st.Notify(r.Context(), p.UserID, "like", nil, "post", p.ID, name+" liked your post", "/posts/"+itoa(p.ID))
	}
}

func (s *Server) handleRemoteAnnounce(r *http.Request, act *federation.InboxActivity, actor *store.RemoteActor) {
	p := s.localPostByObject(r, objectID(act.Object))
	if p == nil {
		return
	}
	if err := s.st.AddRemotePostBoost(r.Context(), p.ID, act.Actor); err == nil && p.UserID != 0 {
		name := act.Actor
		if actor != nil && actor.Name != "" {
			name = actor.Name
		}
		s.st.Notify(r.Context(), p.UserID, "boost", nil, "post", p.ID, name+" boosted your post", "/posts/"+itoa(p.ID))
	}
}

func (s *Server) handleRemoteUndo(r *http.Request, act *federation.InboxActivity, target *models.User, actor *store.RemoteActor) {
	var obj struct {
		Type   string          `json:"type"`
		Actor  string          `json:"actor"`
		Object json.RawMessage `json:"object"`
	}
	if json.Unmarshal(act.Object, &obj) != nil || obj.Type == "" {
		if actor != nil {
			s.st.RemoveRemoteFollow(r.Context(), actor.ActorURI, target.ID)
		}
		return
	}
	switch obj.Type {
	case "Like":
		if p := s.localPostByObject(r, objectID(obj.Object)); p != nil {
			s.st.RemoveRemotePostLike(r.Context(), p.ID, act.Actor)
		}
	case "Announce":
		if p := s.localPostByObject(r, objectID(obj.Object)); p != nil {
			s.st.RemoveRemotePostBoost(r.Context(), p.ID, act.Actor)
		}
	default:
		if actor != nil {
			s.st.RemoveRemoteFollow(r.Context(), actor.ActorURI, target.ID)
		}
	}
}

func (s *Server) handleRemoteDelete(r *http.Request, act *federation.InboxActivity) {
	id := objectID(act.Object)
	if id == "" {
		return
	}
	if c, err := s.st.CommentByAPID(r.Context(), id); err == nil && c.RemoteActor == act.Actor {
		s.st.DeleteCommentByAPID(r.Context(), id)
	}
}

func (s *Server) handleRemoteUpdate(r *http.Request, act *federation.InboxActivity) {
	var obj struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Content      string `json:"content"`
		AttributedTo string `json:"attributedTo"`
	}
	if json.Unmarshal(act.Object, &obj) != nil || obj.ID == "" || obj.Type != "Note" {
		return
	}
	if obj.AttributedTo != "" && obj.AttributedTo != act.Actor {
		return
	}
	s.st.UpdateRemoteCommentBody(r.Context(), obj.ID, obj.Content)
}

func (s *Server) handleRemoteFlag(r *http.Request, act *federation.InboxActivity, actor *store.RemoteActor) {
	targets := []string{}
	if id := objectID(act.Object); id != "" {
		targets = append(targets, id)
	} else {
		var list []string
		if json.Unmarshal(act.Object, &list) == nil {
			targets = append(targets, list...)
		}
	}
	for _, targetURL := range targets {
		p := s.localPostByObject(r, targetURL)
		targetType, targetID := "remote", int64(0)
		if p != nil {
			targetType, targetID = "post", p.ID
		}
		name, instance := act.Actor, hostOf(act.Actor)
		if actor != nil && actor.Name != "" {
			name = actor.Name
		}
		s.st.CreateReportDetailed(r.Context(), nil, targetType, targetID, targetURL, "forwarded report", "", name, instance, true)
	}
}

func (s *Server) handleRemoteCreate(r *http.Request, act *federation.InboxActivity, target *models.User) {
	var obj struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Content      string `json:"content"`
		InReplyTo    string `json:"inReplyTo"`
		AttributedTo string `json:"attributedTo"`
	}
	raw := act.Object
	if len(raw) > 0 && raw[0] == '"' {
		json.Unmarshal(raw, &obj.ID)
		return
	}
	if json.Unmarshal(raw, &obj) != nil || obj.InReplyTo == "" {
		return
	}
	if obj.Type != "" && obj.Type != "Note" {
		return
	}
	if obj.AttributedTo != "" && obj.AttributedTo != act.Actor {
		return
	}
	actor, _ := s.st.RemoteActorByURI(r.Context(), act.Actor)
	name, avatar := act.Actor, ""
	if actor != nil {
		name, avatar = actor.Name, actor.Avatar
	}
	if p := s.localPostByObject(r, obj.InReplyTo); p != nil {
		if existing, err := s.st.CommentByAPID(r.Context(), obj.ID); err == nil && existing != nil {
			return
		}
		id, err := s.st.CreateComment(r.Context(), nil, "post", p.ID, nil, obj.Content, act.Actor, name, avatar)
		if err == nil {
			s.st.SetCommentAPID(r.Context(), id, obj.ID, obj.InReplyTo)
			s.st.Notify(r.Context(), p.UserID, "reply", nil, "post", p.ID, name+" replied to your post", "/posts/"+itoa(p.ID))
		}
		return
	}
	s.st.InsertRemoteNote(r.Context(), obj.ID, act.Actor, name, avatar, obj.Content, obj.InReplyTo)
}

func (s *Server) remoteActor(r *http.Request, client *http.Client, uri string) (*store.RemoteActor, error) {
	if a, err := s.st.RemoteActorByURI(r.Context(), uri); err == nil && time.Since(a.FetchedAt) < 24*time.Hour {
		return a, nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/activity+json, application/ld+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("actor fetch: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	ra, err := federation.ParseActor(body)
	if err != nil {
		return nil, err
	}
	out := &store.RemoteActor{
		ActorURI: uri, Domain: hostOf(uri), Username: ra.Username, Name: ra.Name,
		Inbox: ra.Inbox, SharedInbox: ra.SharedInbox, Outbox: ra.Outbox,
		KeyID: ra.KeyID, PublicKeyPEM: ra.PublicKeyPEM,
		Avatar: ra.Avatar, Summary: ra.Summary, FetchedAt: time.Now(),
	}
	s.st.SaveRemoteActor(r.Context(), out)
	return out, nil
}

// deliverToFollowers fans one activity out to every remote follower inbox.
// Private profiles never leave the instance.
func (s *Server) deliverToFollowers(r *http.Request, u *models.User, activity map[string]any) {
	if u == nil || !u.Federation || u.PrivateProfile {
		return
	}
	seen := map[string]bool{}
	for _, follower := range s.st.RemoteFollowers(r.Context(), u.ID) {
		inbox := follower.Inbox
		if follower.SharedInbox != "" {
			inbox = follower.SharedInbox
		}
		if inbox == "" || seen[inbox] {
			continue
		}
		seen[inbox] = true
		s.enqueueDeliveryActivity(r, u, activity, inbox)
	}
}

// statusActivity publishes a shelf or played-status change as a short Note so
// followers on other servers see the same status the profile shows.
func (s *Server) statusActivity(u *models.User, g *models.Game, label string) map[string]any {
	actor := s.actorURL(u)
	gameURL := s.absolute("/games")
	content := label
	if g != nil {
		gameURL = s.absolute("/games/" + url.PathEscape(g.Slug))
		content = label + " <a href=\"" + gameURL + "\">" + g.Title + "</a>"
	}
	id := actor + "#statuses/" + fmt.Sprint(time.Now().UnixNano())
	return map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        id,
		"type":      "Create",
		"actor":     actor,
		"published": time.Now().UTC().Format(time.RFC3339),
		"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc":        []string{actor + "/followers"},
		"object": map[string]any{
			"id":           id,
			"type":         "Note",
			"attributedTo": actor,
			"content":      content,
			"url":          gameURL,
			"to":           []string{"https://www.w3.org/ns/activitystreams#Public"},
			"cc":           []string{actor + "/followers"},
		},
	}
}

// deliverStatusActivity covers game shelf toggles and played-status changes.
// It is a no-op unless the user enabled federation and status delivery.
func (s *Server) deliverStatusActivity(r *http.Request, u *models.User, g *models.Game, label string) {
	if u == nil || !u.FedStatuses {
		return
	}
	s.deliverToFollowers(r, u, s.statusActivity(u, g, label))
}

func (s *Server) deliverLogActivity(r *http.Request, u *models.User, l *models.Log) {
	if u == nil || l == nil || l.Draft {
		return
	}
	if u.FedReviews && l.Review != "" {
		s.deliverToFollowers(r, u, s.reviewActivity(u, l))
	}
	if u.FedStatuses {
		s.deliverToFollowers(r, u, s.statusActivity(u, l.Game, "Marked "+store.NormalizePlayedStatus(l.Status)))
	}
}

// deliverListActivity publishes public list creates and updates to followers.
func (s *Server) deliverListActivity(r *http.Request, u *models.User, l *models.List) {
	if u == nil || l == nil || !u.FedLists || l.Privacy == "private" || l.Privacy == "unlisted" {
		return
	}
	actor := s.actorURL(u)
	page := s.absolute("/users/" + url.PathEscape(u.Username) + "/lists/" + url.PathEscape(l.Slug))
	id := actor + "#lists/" + fmt.Sprint(l.ID) + "/" + fmt.Sprint(time.Now().UnixNano())
	s.deliverToFollowers(r, u, map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        id,
		"type":      "Create",
		"actor":     actor,
		"published": time.Now().UTC().Format(time.RFC3339),
		"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc":        []string{actor + "/followers"},
		"object": map[string]any{
			"id":           id,
			"type":         "Note",
			"attributedTo": actor,
			"content":      "Updated list <a href=\"" + page + "\">" + l.Title + "</a>",
			"url":          page,
			"to":           []string{"https://www.w3.org/ns/activitystreams#Public"},
			"cc":           []string{actor + "/followers"},
		},
	})
}

// postInboxes returns the actor's remote follower inboxes plus the post
// author's inbox when the author is remote. It dedupes shared inboxes.
func (s *Server) postInboxes(r *http.Request, u *models.User, p *models.Post) []string {
	seen := map[string]bool{}
	var out []string
	add := func(inbox string) {
		if inbox == "" || seen[inbox] {
			return
		}
		seen[inbox] = true
		out = append(out, inbox)
	}
	for _, follower := range s.st.RemoteFollowers(r.Context(), u.ID) {
		if follower.SharedInbox != "" {
			add(follower.SharedInbox)
		} else {
			add(follower.Inbox)
		}
	}
	if p != nil && p.RemoteActor != "" {
		if ra, err := s.st.RemoteActorByURI(r.Context(), p.RemoteActor); err == nil {
			add(ra.Inbox)
		}
	}
	return out
}

func (s *Server) deliverPostActivity(r *http.Request, u *models.User, p *models.Post, activity map[string]any) {
	if u == nil || !u.Federation || u.PrivateProfile {
		return
	}
	for _, inbox := range s.postInboxes(r, u, p) {
		s.enqueueDeliveryActivity(r, u, activity, inbox)
	}
}

func (s *Server) ensurePostAPID(r *http.Request, p *models.Post) string {
	if p.APID == "" {
		p.APID = s.absolute("/posts/" + itoa(p.ID))
		s.st.SetPostAPID(r.Context(), p.ID, p.APID)
	}
	return p.APID
}

func (s *Server) federatePostLike(r *http.Request, u *models.User, p *models.Post, liked bool) {
	if u == nil || p == nil || !u.Federation || u.PrivateProfile {
		return
	}
	object := s.ensurePostAPID(r, p)
	actor := s.actorURL(u)
	var activity map[string]any
	if liked {
		activity = map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       actor + "#likes/" + fmt.Sprint(p.ID) + "/" + fmt.Sprint(time.Now().UnixNano()),
			"type":     "Like", "actor": actor, "object": object,
		}
	} else {
		activity = map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       actor + "#likes/" + fmt.Sprint(p.ID) + "/undo/" + fmt.Sprint(time.Now().UnixNano()),
			"type":     "Undo", "actor": actor,
			"object": map[string]any{"type": "Like", "actor": actor, "object": object},
		}
	}
	s.deliverPostActivity(r, u, p, activity)
}

func (s *Server) federatePostBoost(r *http.Request, u *models.User, p *models.Post, boosted bool) {
	if u == nil || p == nil || !u.Federation || u.PrivateProfile {
		return
	}
	object := s.ensurePostAPID(r, p)
	actor := s.actorURL(u)
	var activity map[string]any
	if boosted {
		activity = map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       actor + "#announces/" + fmt.Sprint(p.ID) + "/" + fmt.Sprint(time.Now().UnixNano()),
			"type":     "Announce", "actor": actor, "object": object,
		}
	} else {
		activity = map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       actor + "#announces/" + fmt.Sprint(p.ID) + "/undo/" + fmt.Sprint(time.Now().UnixNano()),
			"type":     "Undo", "actor": actor,
			"object": map[string]any{"type": "Announce", "actor": actor, "object": object},
		}
	}
	s.deliverPostActivity(r, u, p, activity)
}

func (s *Server) deliverCommentActivity(r *http.Request, u *models.User, p *models.Post, commentID int64, body, inReplyTo, apID string) {
	if u == nil || p == nil || !u.Federation || u.PrivateProfile {
		return
	}
	actor := s.actorURL(u)
	published := time.Now().UTC().Format(time.RFC3339)
	s.deliverPostActivity(r, u, p, map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       apID + "/activity",
		"type":     "Create", "actor": actor, "published": published,
		"to": []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc": []string{actor + "/followers"},
		"object": map[string]any{
			"id": apID, "type": "Note", "attributedTo": actor,
			"content": body, "inReplyTo": inReplyTo,
			"url":       s.absolute("/posts/" + itoa(p.ID) + "#c" + itoa(commentID)),
			"published": published,
			"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
			"cc":        []string{actor + "/followers"},
		},
	})
}

func (s *Server) deliverCommentDelete(r *http.Request, u *models.User, c *models.Comment) {
	if u == nil || c == nil || c.APID == "" || !u.Federation || u.PrivateProfile {
		return
	}
	actor := s.actorURL(u)
	activity := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       c.APID + "/delete/" + fmt.Sprint(time.Now().UnixNano()),
		"type":     "Delete", "actor": actor,
		"object": map[string]any{"id": c.APID, "type": "Tombstone"},
	}
	if p, err := s.st.PostByID(r.Context(), c.SubjectID, u.ID); err == nil {
		s.deliverPostActivity(r, u, p, activity)
	}
}

func (s *Server) federatePostDelete(r *http.Request, u *models.User, p *models.Post) {
	if u == nil || p == nil || !u.Federation || u.PrivateProfile || p.APID == "" {
		return
	}
	actor := s.actorURL(u)
	s.deliverPostActivity(r, u, p, map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       p.APID + "/delete/" + fmt.Sprint(time.Now().UnixNano()),
		"type":     "Delete", "actor": actor,
		"object": map[string]any{"id": p.APID, "type": "Tombstone"},
	})
}

// forwardFlag sends a report about a remote object to that instance's shared
// inbox. The reported actor is never notified by the local instance.
func (s *Server) forwardFlag(r *http.Request, u *models.User, targetURL, reason, details string, reportID int64) error {
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("bad target")
	}
	inbox := parsed.Scheme + "://" + parsed.Host + "/inbox"
	actor := s.actorURL(u)
	content := "Reported content: " + reason
	if details != "" {
		content += "\n\n" + details
	}
	activity := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       actor + "#flags/" + fmt.Sprint(reportID),
		"type":     "Flag", "actor": actor,
		"content": content, "object": []string{targetURL},
	}
	s.enqueueDeliveryActivity(r, u, activity, inbox)
	return nil
}

func (s *Server) enqueueDeliveryActivity(r *http.Request, u *models.User, activity map[string]any, inbox string) {
	if inbox == "" {
		return
	}
	payload, _ := json.Marshal(activity)
	job, _ := json.Marshal(map[string]any{"user_id": u.ID, "inbox": inbox, "payload": string(payload)})
	s.q.Enqueue(r.Context(), "deliver", string(job), 0)
}

func rawString(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func absoluteURL(base, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
}

func markdownHTML(s string) string { return string(md(s)) }
